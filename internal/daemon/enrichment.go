package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shaktimanai/shaktiman/internal/parser"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// EnrichmentPipeline orchestrates parsing and indexing of source files
// using a pool of worker goroutines, each with its own parser (IP-2).
type EnrichmentPipeline struct {
	store          types.WriterStore
	lifecycle      types.StoreLifecycle // nil if backend needs no lifecycle hooks
	writer         *WriterManager
	workers        int
	logger         *slog.Logger
	incrParser     *parser.Parser
	incrParserOnce sync.Once
	incrParserMu   sync.Mutex // serialize incremental parses
}

// NewEnrichmentPipeline creates a pipeline with the given worker count.
func NewEnrichmentPipeline(store types.WriterStore, writer *WriterManager, workers int, lifecycle types.StoreLifecycle) *EnrichmentPipeline {
	return &EnrichmentPipeline{
		store:     store,
		lifecycle: lifecycle,
		writer:    writer,
		workers:   workers,
		logger:    slog.Default().With("component", "enrichment"),
	}
}

// IndexProgress reports cold indexing progress.
type IndexProgress struct {
	Indexed int
	Errors  int
	Total   int
}

// IndexAllInput configures a cold indexing operation.
type IndexAllInput struct {
	ProjectRoot string
	Files       []ScannedFile
	OnProgress  func(IndexProgress) // optional; nil = no-op
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

	// Run backend-specific bulk write optimization (e.g., disable FTS triggers for SQLite)
	if ep.lifecycle != nil {
		if err := ep.lifecycle.OnBulkWriteBegin(ctx); err != nil {
			ep.logger.Warn("lifecycle OnBulkWriteBegin failed", "err", err)
		}
		defer func() {
			if err := ep.lifecycle.OnBulkWriteEnd(ctx); err != nil {
				ep.logger.Warn("lifecycle OnBulkWriteEnd failed", "err", err)
			}
		}()
	}

	// Distribute work to workers
	jobCh := make(chan ScannedFile, len(needsIndex))
	for _, f := range needsIndex {
		jobCh <- f
	}
	close(jobCh)

	if !ep.writer.AddProducer() {
		return fmt.Errorf("writer is shutting down")
	}
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
				if input.OnProgress != nil {
					input.OnProgress(IndexProgress{
						Indexed: indexed,
						Errors:  errCount,
						Total:   len(needsIndex),
					})
				}
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	ep.writer.RemoveProducer()

	// Wait for all writes to complete by submitting a no-op sync job.
	// WriteJobSync touches no database, so no cleanup delete is needed
	// and there is no race with callers that cancel the writer context
	// immediately after IndexAll returns (e.g. IndexProject).
	done := make(chan error, 1)
	if err := ep.writer.Submit(types.WriteJob{
		Type: types.WriteJobSync,
		Done: done,
	}); err != nil {
		ep.logger.Warn("submit sync failed", "err", err)
	} else {
		<-done
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
// Uses file.Content if available (carried from scan), falls back to reading from disk.
func (ep *EnrichmentPipeline) enrichFile(ctx context.Context, p *parser.Parser, file ScannedFile) error {
	content := file.Content
	if content == nil {
		var err error
		content, err = readFileContent(file.AbsPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", file.Path, err)
		}
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
// Uses batch query (1 query instead of N).
func (ep *EnrichmentPipeline) filterChanged(ctx context.Context, files []ScannedFile) ([]ScannedFile, error) {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}

	// Use batch path if available (BatchMetadataStore), fallback to per-file
	var existing map[string]string
	if bs, ok := ep.store.(types.BatchMetadataStore); ok {
		var err2 error
		existing, err2 = bs.BatchGetFileHashes(ctx, paths)
		if err2 != nil {
			return nil, fmt.Errorf("batch get file hashes: %w", err2)
		}
	} else {
		existing = make(map[string]string, len(paths))
		for _, p := range paths {
			f, err2 := ep.store.GetFileByPath(ctx, p)
			if err2 == nil && f != nil {
				existing[p] = f.ContentHash
			}
		}
	}

	var changed []ScannedFile
	for _, f := range files {
		if hash, found := existing[f.Path]; !found || hash != f.ContentHash {
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
		ep.logger.Debug("skip unchanged", "path", event.Path)
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

	ep.logger.Debug("parsed", "path", event.Path, "chunks", len(result.Chunks), "symbols", len(result.Symbols))

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
	ep.logger.Debug("enrich submitted", "path", event.Path)
	return nil
}


// contentHash returns the SHA-256 hex hash of content.
func contentHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
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
