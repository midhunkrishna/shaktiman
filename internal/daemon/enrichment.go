package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/shaktimanai/shaktiman/internal/parser"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// EnrichmentPipeline orchestrates parsing and indexing of source files
// using a pool of worker goroutines, each with its own parser (IP-2).
type EnrichmentPipeline struct {
	store   *storage.Store
	writer  *WriterManager
	workers int
	logger  *slog.Logger
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
	var indexed int

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
	ep.writer.Submit(types.WriteJob{
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
	})
	<-done

	// Clean up sync marker
	ep.writer.AddProducer()
	ep.writer.Submit(types.WriteJob{
		Type:     types.WriteJobFileDelete,
		FilePath: "__sync_marker__",
	})
	ep.writer.RemoveProducer()

	// Rebuild FTS5 index (A11)
	if err := ep.store.RebuildFTS(ctx); err != nil {
		ep.logger.Warn("failed to rebuild FTS", "err", err)
	}

	// Re-enable FTS triggers
	if err := ep.store.EnableFTSTriggers(ctx); err != nil {
		ep.logger.Warn("failed to enable FTS triggers", "err", err)
	}

	ep.logger.Info("cold index complete",
		"indexed", indexed,
		"total", len(needsIndex))

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
		ContentHash: file.ContentHash,
		Timestamp:   time.Now(),
	}

	ep.writer.Submit(job)
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

func readFileContent(path string) ([]byte, error) {
	return os.ReadFile(path)
}
