// Package daemon provides lifecycle management, background indexing, and the writer goroutine.
package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// WriterManager serializes all SQLite writes through a single goroutine (IP-4).
// Producers register via AddProducer/RemoveProducer for ordered shutdown.
type WriterManager struct {
	ch        chan types.WriteJob
	producers sync.WaitGroup
	done      chan struct{}
	store     *storage.Store
	logger    *slog.Logger
}

// NewWriterManager creates a writer with the given channel capacity (IP-5: 500).
func NewWriterManager(store *storage.Store, chanSize int) *WriterManager {
	return &WriterManager{
		ch:    make(chan types.WriteJob, chanSize),
		done:  make(chan struct{}),
		store: store,
		logger: slog.Default().With("component", "writer"),
	}
}

// Run processes write jobs until ctx is cancelled, then drains remaining jobs.
// This method blocks — run it in a goroutine.
func (wm *WriterManager) Run(ctx context.Context) {
	defer close(wm.done)

	for {
		select {
		case job := <-wm.ch:
			wm.processJob(ctx, job)
		case <-ctx.Done():
			wm.drain()
			return
		}
	}
}

// drain waits for all producers to stop, then processes remaining jobs.
func (wm *WriterManager) drain() {
	wm.logger.Info("writer draining: waiting for producers")
	wm.producers.Wait()
	close(wm.ch)

	deadline := time.After(10 * time.Second)
	for job := range wm.ch {
		select {
		case <-deadline:
			wm.logger.Warn("writer drain timeout, dropping remaining jobs")
			return
		default:
			wm.processJob(context.Background(), job)
		}
	}
	wm.logger.Info("writer drain complete")
}

// Submit sends a write job to the writer goroutine.
// Blocks if the channel is full (back-pressure).
func (wm *WriterManager) Submit(job types.WriteJob) {
	wm.ch <- job
}

// AddProducer registers a producer goroutine for shutdown ordering.
func (wm *WriterManager) AddProducer() { wm.producers.Add(1) }

// RemoveProducer unregisters a producer goroutine.
func (wm *WriterManager) RemoveProducer() { wm.producers.Done() }

// Done returns a channel that is closed when the writer has finished.
func (wm *WriterManager) Done() <-chan struct{} { return wm.done }

func (wm *WriterManager) processJob(ctx context.Context, job types.WriteJob) {
	err := wm.store.DB().WithWriteTx(func(tx *sql.Tx) error {
		return processWriteJob(ctx, tx, wm.store, job)
	})
	if err != nil {
		wm.logger.Error("write job failed",
			"type", job.Type,
			"file", job.FilePath,
			"err", err)
	}
	if job.Done != nil {
		job.Done <- err
	}
}

// processWriteJob executes a single write job within a transaction.
func processWriteJob(ctx context.Context, tx *sql.Tx, store *storage.Store, job types.WriteJob) error {
	switch job.Type {
	case types.WriteJobEnrichment:
		return processEnrichmentJob(ctx, tx, store, job)
	case types.WriteJobFileDelete:
		_, err := tx.ExecContext(ctx, "DELETE FROM files WHERE path = ?", job.FilePath)
		if err != nil {
			return fmt.Errorf("delete file %s: %w", job.FilePath, err)
		}
		return nil
	default:
		return fmt.Errorf("unknown write job type: %d", job.Type)
	}
}

// processEnrichmentJob handles an enrichment write: upsert file, replace chunks + symbols.
func processEnrichmentJob(ctx context.Context, tx *sql.Tx, store *storage.Store, job types.WriteJob) error {
	if job.File == nil {
		return fmt.Errorf("enrichment job for %s has nil file", job.FilePath)
	}

	// Upsert file
	res, err := tx.ExecContext(ctx, `
		INSERT INTO files (path, content_hash, mtime, size, language, indexed_at, embedding_status, parse_quality)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			content_hash = excluded.content_hash,
			mtime = excluded.mtime,
			size = excluded.size,
			language = excluded.language,
			indexed_at = excluded.indexed_at,
			parse_quality = excluded.parse_quality`,
		job.File.Path, job.File.ContentHash, job.File.Mtime, job.File.Size,
		job.File.Language, time.Now().UTC().Format(time.RFC3339Nano),
		job.File.EmbeddingStatus, job.File.ParseQuality)
	if err != nil {
		return fmt.Errorf("upsert file %s: %w", job.FilePath, err)
	}

	fileID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get file id: %w", err)
	}
	if fileID == 0 {
		err = tx.QueryRowContext(ctx, "SELECT id FROM files WHERE path = ?", job.FilePath).Scan(&fileID)
		if err != nil {
			return fmt.Errorf("lookup file id %s: %w", job.FilePath, err)
		}
	}

	// Delete old chunks and symbols (cascade handles symbols via FK)
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE file_id = ?", fileID); err != nil {
		return fmt.Errorf("delete old chunks for %s: %w", job.FilePath, err)
	}

	// Insert new chunks
	chunkIDs := make([]int64, len(job.Chunks))
	for i, c := range job.Chunks {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO chunks (file_id, chunk_index, symbol_name, kind,
			                    start_line, end_line, content, token_count, signature, parse_quality)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, c.ChunkIndex, c.SymbolName, c.Kind,
			c.StartLine, c.EndLine, c.Content, c.TokenCount, c.Signature,
			coalesce(c.ParseQuality, "full"))
		if err != nil {
			return fmt.Errorf("insert chunk %d for %s: %w", i, job.FilePath, err)
		}
		chunkIDs[i], _ = res.LastInsertId()
	}

	// Resolve parent chunk IDs (CA-10)
	for i, c := range job.Chunks {
		if c.ParentIndex != nil && *c.ParentIndex < len(chunkIDs) {
			parentID := chunkIDs[*c.ParentIndex]
			if _, err := tx.ExecContext(ctx, "UPDATE chunks SET parent_chunk_id = ? WHERE id = ?",
				parentID, chunkIDs[i]); err != nil {
				return fmt.Errorf("set parent for chunk %d: %w", i, err)
			}
		}
	}

	// Insert symbols with resolved chunk IDs
	for _, sym := range job.Symbols {
		chunkID := int64(0)
		// sym.ChunkID is the chunk index (temporary), resolve to actual ID
		if int(sym.ChunkID) < len(chunkIDs) {
			chunkID = chunkIDs[sym.ChunkID]
		}

		exported := 0
		if sym.IsExported {
			exported = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO symbols (chunk_id, file_id, name, qualified_name, kind,
			                     line, signature, visibility, is_exported)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chunkID, fileID, sym.Name, sym.QualifiedName, sym.Kind,
			sym.Line, sym.Signature, sym.Visibility, exported); err != nil {
			return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
		}
	}

	return nil
}

func coalesce(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
