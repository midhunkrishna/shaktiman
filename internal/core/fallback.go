package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// FallbackLevel indicates which retrieval strategy to use.
type FallbackLevel int

const (
	// LevelKeyword uses FTS5 keyword search.
	LevelKeyword FallbackLevel = 2
	// LevelFilesystem reads raw file content.
	LevelFilesystem FallbackLevel = 3
)

// DetermineLevel decides which retrieval strategy to use.
// If the index has chunks → L2 (keyword), otherwise → L3 (filesystem).
func DetermineLevel(ctx context.Context, store *storage.Store) FallbackLevel {
	stats, err := store.GetIndexStats(ctx)
	if err != nil || stats.TotalChunks == 0 {
		return LevelFilesystem
	}
	return LevelKeyword
}

// FilesystemFallback reads raw TypeScript files up to the token budget.
// Used when the index is empty or keyword search returns no results.
func FilesystemFallback(ctx context.Context, projectRoot string, query string, budget int) (*types.ContextPackage, error) {
	// Find TypeScript files
	var tsFiles []string
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".ts" || ext == ".tsx" {
			tsFiles = append(tsFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk for fallback: %w", err)
	}

	// Read files up to budget
	var chunks []types.ScoredResult
	remaining := budget

	for _, path := range tsFiles {
		if remaining <= 0 {
			break
		}

		select {
		case <-ctx.Done():
			break
		default:
		}

		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		relPath, _ := filepath.Rel(projectRoot, path)
		tokenCount := len(content) / 4 // rough estimate

		if tokenCount > remaining {
			// Truncate to fit
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
			Score:      0.1, // low score for filesystem fallback
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
		Strategy:    "filesystem_l3",
	}, nil
}
