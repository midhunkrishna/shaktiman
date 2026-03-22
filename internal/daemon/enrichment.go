package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shaktimanai/shaktiman/internal/parser"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// EnrichmentPipeline orchestrates parsing and indexing of source files
// using a pool of worker goroutines, each with its own parser (IP-2).
type EnrichmentPipeline struct {
	store          *storage.Store
	writer         *WriterManager
	workers        int
	logger         *slog.Logger
	incrParser     *parser.Parser
	incrParserOnce sync.Once
	incrParserMu   sync.Mutex // serialize incremental parses
}

// NewEnrichmentPipeline creates a pipeline with the given worker count.
func NewEnrichmentPipeline(store *storage.Store, writer *WriterManager, workers int) *EnrichmentPipeline {
	return &EnrichmentPipeline{
		store:   store,
		writer:  writer,
		workers: workers,
		logger:  slog.Default().With("component", "enrichment"),
	}
}

// IndexAllInput configures a cold indexing operation.
type IndexAllInput struct {
	ProjectRoot string
	Files       []ScannedFile
}

// IndexAll performs cold indexing: parses all files and writes to the database.
// Skips files whose content_hash hasn't changed. Uses N worker goroutines.
func (ep *EnrichmentPipeline) IndexAll(ctx context.Context, input IndexAllInput) error {
	// Filter to files that need indexing
	needsIndex, err := ep.filterChanged(ctx, input.Files)
	if err != nil {
		return fmt.Errorf("filter changed files: %w", err)
	}

	if len(needsIndex) == 0 {
		ep.logger.Info("all files up to date, nothing to index")
		return nil
	}

	startTime := time.Now()
	ep.logger.Info("starting cold index",
		"total_files", len(input.Files),
		"needs_index", len(needsIndex))

	// Disable FTS triggers for bulk insert performance (A11)
	if err := ep.store.DisableFTSTriggers(ctx); err != nil {
		ep.logger.Warn("failed to disable FTS triggers", "err", err)
	}

	// Distribute work to workers
	jobCh := make(chan ScannedFile, len(needsIndex))
	for _, f := range needsIndex {
		jobCh <- f
	}
	close(jobCh)

	ep.writer.AddProducer()
	var wg sync.WaitGroup
	var indexErr error
	var mu sync.Mutex
	var indexed, errCount int

	for i := 0; i < ep.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker owns its own parser (IP-2: not goroutine-safe)
			p, err := parser.NewParser()
			if err != nil {
				mu.Lock()
				indexErr = fmt.Errorf("worker %d: create parser: %w", workerID, err)
				mu.Unlock()
				return
			}
			defer p.Close()

			for file := range jobCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if err := ep.enrichFile(ctx, p, file); err != nil {
					ep.logger.Warn("enrich failed",
						"file", file.Path,
						"err", err)
					mu.Lock()
					errCount++
					mu.Unlock()
					continue
				}

				mu.Lock()
				indexed++
				if indexed%100 == 0 {
					ep.logger.Info("indexing progress",
						"indexed", indexed,
						"total", len(needsIndex))
				}
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	ep.writer.RemoveProducer()

	// Wait for all writes to complete by submitting a sync job
	done := make(chan error, 1)
	if err := ep.writer.Submit(types.WriteJob{
		Type: types.WriteJobEnrichment,
		File: &types.FileRecord{
			Path:            "__sync_marker__",
			ContentHash:     "sync",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		FilePath:  "__sync_marker__",
		Timestamp: time.Now(),
		Done:      done,
	}); err != nil {
		ep.logger.Warn("submit sync marker failed", "err", err)
	} else {
		<-done
	}

	// Clean up sync marker
	ep.writer.AddProducer()
	if err := ep.writer.Submit(types.WriteJob{
		Type:     types.WriteJobFileDelete,
		FilePath: "__sync_marker__",
	}); err != nil {
		ep.logger.Warn("submit sync marker cleanup failed", "err", err)
	}
	ep.writer.RemoveProducer()

	// Rebuild FTS5 index (A11)
	if err := ep.store.RebuildFTS(ctx); err != nil {
		ep.logger.Warn("failed to rebuild FTS", "err", err)
	}

	// Re-enable FTS triggers
	if err := ep.store.EnableFTSTriggers(ctx); err != nil {
		ep.logger.Warn("failed to enable FTS triggers", "err", err)
	}

	elapsed := time.Since(startTime)
	ep.logger.Info("cold index complete",
		"indexed", indexed,
		"errors", errCount,
		"total", len(needsIndex),
		"duration_ms", elapsed.Milliseconds())

	return indexErr
}

// enrichFile parses a single file and submits the result to the writer.
func (ep *EnrichmentPipeline) enrichFile(ctx context.Context, p *parser.Parser, file ScannedFile) error {
	content, err := readFileContent(file.AbsPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", file.Path, err)
	}

	result, err := p.Parse(ctx, parser.ParseInput{
		FilePath: file.Path,
		Content:  content,
		Language: file.Language,
	})
	if err != nil {
		return fmt.Errorf("parse %s: %w", file.Path, err)
	}

	job := types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: file.Path,
		File: &types.FileRecord{
			Path:            file.Path,
			ContentHash:     file.ContentHash,
			Mtime:           file.Mtime,
			Size:            file.Size,
			Language:         file.Language,
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks:      result.Chunks,
		Symbols:     result.Symbols,
		Edges:       result.Edges,
		ContentHash: file.ContentHash,
		Timestamp:   time.Now(),
	}

	if err := ep.writer.Submit(job); err != nil {
		return fmt.Errorf("submit write job for %s: %w", file.Path, err)
	}
	return nil
}

// filterChanged returns files whose content hash differs from the stored version.
func (ep *EnrichmentPipeline) filterChanged(ctx context.Context, files []ScannedFile) ([]ScannedFile, error) {
	var changed []ScannedFile
	for _, f := range files {
		existing, err := ep.store.GetFileByPath(ctx, f.Path)
		if err != nil {
			return nil, fmt.Errorf("check file %s: %w", f.Path, err)
		}
		if existing == nil || existing.ContentHash != f.ContentHash {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// EnrichFile re-parses a single file and submits a write job.
// Used for incremental indexing from watcher events.
func (ep *EnrichmentPipeline) EnrichFile(ctx context.Context, event FileChangeEvent) error {
	if event.ChangeType == "delete" {
		if err := ep.writer.Submit(types.WriteJob{
			Type:     types.WriteJobFileDelete,
			FilePath: event.Path,
		}); err != nil {
			return fmt.Errorf("submit delete job for %s: %w", event.Path, err)
		}
		return nil
	}

	content, err := readFileContent(event.AbsPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", event.Path, err)
	}

	ext := filepath.Ext(event.Path)
	lang, ok := LanguageForExt(ext)
	if !ok {
		return nil // unsupported file type
	}

	hash := contentHash(content)

	// Check if already indexed with same hash
	existing, err := ep.store.GetFileByPath(ctx, event.Path)
	if err != nil {
		return fmt.Errorf("check file %s: %w", event.Path, err)
	}
	if existing != nil && existing.ContentHash == hash {
		return nil // no change
	}

	ep.incrParserOnce.Do(func() {
		ep.incrParser, err = parser.NewParser()
	})
	if ep.incrParser == nil {
		return fmt.Errorf("create incremental parser: %w", err)
	}

	ep.incrParserMu.Lock()
	result, err := ep.incrParser.Parse(ctx, parser.ParseInput{
		FilePath: event.Path,
		Content:  content,
		Language: lang,
	})
	ep.incrParserMu.Unlock()
	if err != nil {
		return fmt.Errorf("parse %s: %w", event.Path, err)
	}

	info, err := os.Stat(event.AbsPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", event.Path, err)
	}

	if err := ep.writer.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: event.Path,
		File: &types.FileRecord{
			Path:            event.Path,
			ContentHash:     hash,
			Mtime:           float64(info.ModTime().UnixMilli()) / 1000.0,
			Size:            info.Size(),
			Language:        lang,
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks:      result.Chunks,
		Symbols:     result.Symbols,
		Edges:       result.Edges,
		ContentHash: hash,
		Timestamp:   time.Now(),
	}); err != nil {
		return fmt.Errorf("submit enrich job for %s: %w", event.Path, err)
	}
	return nil
}


// contentHash returns the SHA-256 hex hash of content.
func contentHash(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

const maxFileSize = 10 * 1024 * 1024 // 10MB

func readFileContent(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxFileSize)
	}
	return os.ReadFile(path)
}
