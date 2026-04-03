package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// FallbackLevel indicates which retrieval strategy to use.
type FallbackLevel int

const (
	// LevelHybrid uses all 5 signals (semantic + structural + change + session + keyword).
	LevelHybrid FallbackLevel = 0
	// LevelMixed uses semantic + structural + keyword (no session).
	LevelMixed FallbackLevel = 1
	// LevelKeyword uses FTS5 keyword search + structural + change.
	LevelKeyword FallbackLevel = 2
	// LevelFilesystem reads raw file content.
	LevelFilesystem FallbackLevel = 3
)

// String returns a human-readable name for the fallback level.
func (l FallbackLevel) String() string {
	switch l {
	case LevelHybrid:
		return "hybrid_l0"
	case LevelMixed:
		return "mixed_l0.5"
	case LevelKeyword:
		return "keyword_l2"
	case LevelFilesystem:
		return "filesystem_l3"
	default:
		return fmt.Sprintf("unknown_%d", int(l))
	}
}

// DetermineLevelInput configures fallback level determination.
type DetermineLevelInput struct {
	Store          types.MetadataStore
	VectorCount    int // number of vectors in the store
	EmbeddingReady bool
}

// DetermineLevel decides which retrieval strategy to use based on index state.
func DetermineLevel(ctx context.Context, store types.MetadataStore) FallbackLevel {
	stats, err := store.GetIndexStats(ctx)
	if err != nil || stats.TotalChunks == 0 {
		return LevelFilesystem
	}
	return LevelKeyword
}

// DetermineLevelFull decides the retrieval strategy considering embedding readiness.
func DetermineLevelFull(ctx context.Context, input DetermineLevelInput) FallbackLevel {
	stats, err := input.Store.GetIndexStats(ctx)
	if err != nil || stats.TotalChunks == 0 {
		return LevelFilesystem
	}

	if !input.EmbeddingReady || input.VectorCount == 0 {
		return LevelKeyword
	}

	readiness := float64(input.VectorCount) / float64(stats.TotalChunks)
	if readiness >= 0.8 {
		return LevelHybrid
	}
	if readiness >= 0.2 {
		return LevelMixed
	}
	return LevelKeyword
}

// FilesystemFallback reads raw source files up to the token budget.
// Used when the index is empty or keyword search returns no results.
func FilesystemFallback(ctx context.Context, projectRoot string, query string, budget int) (*types.ContextPackage, error) {
	absRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	var srcFiles []string
	err = filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "dist" || name == "build" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		switch ext {
		case ".ts", ".tsx", ".py", ".go", ".rs", ".java", ".sh", ".bash", ".js", ".jsx", ".mjs", ".cjs":
			// Resolve symlinks and verify path stays within project root
			absPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			if !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) && absPath != absRoot {
				return nil
			}
			srcFiles = append(srcFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk for fallback: %w", err)
	}

	var chunks []types.ScoredResult
	remaining := budget

fileLoop:
	for _, path := range srcFiles {
		if remaining <= 0 {
			break
		}

		select {
		case <-ctx.Done():
			break fileLoop
		default:
		}

		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		relPath, _ := filepath.Rel(projectRoot, path)
		tokenCount := len(content) / 4

		if tokenCount > remaining {
			content = content[:remaining*4]
			tokenCount = remaining
		}

		chunks = append(chunks, types.ScoredResult{
			Path:       relPath,
			Kind:       "block",
			StartLine:  1,
			EndLine:    strings.Count(string(content), "\n") + 1,
			Content:    string(content),
			TokenCount: tokenCount,
			Score:      0.1,
		})

		remaining -= tokenCount
	}

	totalTokens := 0
	for _, c := range chunks {
		totalTokens += c.TokenCount
	}

	return &types.ContextPackage{
		Chunks:      chunks,
		TotalTokens: totalTokens,
		Strategy:    LevelFilesystem.String(),
	}, nil
}
