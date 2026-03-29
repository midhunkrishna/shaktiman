// Package daemon provides lifecycle management, background indexing, and the writer goroutine.
package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// ErrWriterClosed is returned by Submit when the writer has been shut down.
var ErrWriterClosed = errors.New("writer is closed")

// WriterManager serializes all SQLite writes through a single goroutine (IP-4).
// Producers register via AddProducer/RemoveProducer for ordered shutdown.
type WriterManager struct {
	ch            chan types.WriteJob
	producers     sync.WaitGroup
	done          chan struct{}
	store         *storage.Store
	logger        *slog.Logger
	closed        atomic.Bool
	started       atomic.Bool         // true after Run() is called
	mu            sync.Mutex          // protects close sequence
	vectorDeleter types.VectorDeleter // nil if embeddings disabled
}

// SetVectorDeleter attaches a vector deleter for cleaning up stale embeddings
// when chunks are replaced or files are deleted.
func (wm *WriterManager) SetVectorDeleter(vd types.VectorDeleter) {
	wm.vectorDeleter = vd
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
	wm.started.Store(true)
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

	wm.mu.Lock()
	wm.closed.Store(true)
	close(wm.ch)
	wm.mu.Unlock()

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
// Returns ErrWriterClosed if the writer has been shut down.
func (wm *WriterManager) Submit(job types.WriteJob) error {
	if wm.closed.Load() {
		return ErrWriterClosed
	}
	wm.mu.Lock()
	if wm.closed.Load() {
		wm.mu.Unlock()
		return ErrWriterClosed
	}
	// Non-blocking attempt under lock
	select {
	case wm.ch <- job:
		wm.mu.Unlock()
		return nil
	default:
		// Channel full — release lock before blocking so drain() can proceed
		wm.mu.Unlock()
	}

	wm.logger.Debug("writer channel full, blocking",
		"queue_len", len(wm.ch),
		"queue_cap", cap(wm.ch),
		"file", job.FilePath)

	// Block outside the lock; also listen for shutdown to avoid deadlock
	select {
	case wm.ch <- job:
		return nil
	case <-wm.done:
		return ErrWriterClosed
	}
}

// AddProducer registers a producer goroutine for shutdown ordering.
func (wm *WriterManager) AddProducer() { wm.producers.Add(1) }

// RemoveProducer unregisters a producer goroutine.
func (wm *WriterManager) RemoveProducer() { wm.producers.Done() }

// Done returns a channel that is closed when the writer has finished.
func (wm *WriterManager) Done() <-chan struct{} { return wm.done }

// Started reports whether Run has been called.
func (wm *WriterManager) Started() bool { return wm.started.Load() }

func (wm *WriterManager) processJob(ctx context.Context, job types.WriteJob) {
	start := time.Now()
	var staleChunkIDs []int64
	err := wm.store.DB().WithWriteTx(func(tx *sql.Tx) error {
		var txErr error
		staleChunkIDs, txErr = processWriteJob(ctx, tx, wm.store, wm.logger, job)
		return txErr
	})
	if err != nil {
		wm.logger.Error("write job failed",
			"type", job.Type,
			"file", job.FilePath,
			"err", err)
	}
	// Clean up stale vectors after successful transaction.
	// INVARIANT (Phase 1): When a file is re-indexed, processEnrichmentJob deletes old
	// chunks and inserts new ones (with embedded=0), resets embedding_status='pending',
	// and returns stale chunk IDs here. The vectorDeleter removes their vectors.
	// This ensures RunFromDB picks up the new chunks (embedded=0) on its next page,
	// and old vectors are cleaned up. No additional synchronization is needed between
	// RunFromDB and the watcher — BruteForceStore is RWMutex-protected, and the
	// cursor-based query naturally skips deleted rows.
	if err == nil && wm.vectorDeleter != nil && len(staleChunkIDs) > 0 {
		if delErr := wm.vectorDeleter.Delete(ctx, staleChunkIDs); delErr != nil {
			wm.logger.Warn("vector cleanup failed", "chunks", len(staleChunkIDs), "err", delErr)
		}
	}
	wm.logger.Debug("job done", "type", job.Type, "file", job.FilePath, "duration_ms", time.Since(start).Milliseconds())
	if job.Done != nil {
		job.Done <- err
	}
}

// processWriteJob executes a single write job within a transaction.
// Returns IDs of chunks that were deleted (for vector store cleanup).
func processWriteJob(ctx context.Context, tx *sql.Tx, store *storage.Store, logger *slog.Logger, job types.WriteJob) ([]int64, error) {
	switch job.Type {
	case types.WriteJobEnrichment:
		return processEnrichmentJob(ctx, tx, store, logger, job)
	case types.WriteJobFileDelete:
		stale := collectChunkIDsByPath(ctx, tx, job.FilePath)
		_, err := tx.ExecContext(ctx, "DELETE FROM files WHERE path = ?", job.FilePath)
		if err != nil {
			return nil, fmt.Errorf("delete file %s: %w", job.FilePath, err)
		}
		return stale, nil
	default:
		return nil, fmt.Errorf("unknown write job type: %d", job.Type)
	}
}

// collectChunkIDsByPath returns all chunk IDs for a file path (before deletion).
func collectChunkIDsByPath(ctx context.Context, tx *sql.Tx, path string) []int64 {
	rows, err := tx.QueryContext(ctx,
		"SELECT c.id FROM chunks c JOIN files f ON c.file_id = f.id WHERE f.path = ?", path)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// processEnrichmentJob handles an enrichment write: upsert file, replace chunks + symbols.
// Returns IDs of old chunks that were replaced (for vector store cleanup).
func processEnrichmentJob(ctx context.Context, tx *sql.Tx, store *storage.Store, logger *slog.Logger, job types.WriteJob) ([]int64, error) {
	if job.File == nil {
		return nil, fmt.Errorf("enrichment job for %s has nil file", job.FilePath)
	}

	// Content hash guard (CA-3): skip if already indexed with same hash
	if job.ContentHash != "" {
		var currentHash string
		var indexedAt sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT content_hash, indexed_at FROM files WHERE path = ?",
			job.FilePath).Scan(&currentHash, &indexedAt)
		if err == nil && currentHash == job.ContentHash {
			logger.Debug("skip same hash", "file", job.FilePath)
			return nil, nil
		}
		// Stale check: if DB was updated after job was created
		if err == nil && indexedAt.Valid {
			dbTime, tErr := time.Parse(time.RFC3339Nano, indexedAt.String)
			if tErr == nil && dbTime.After(job.Timestamp) {
				logger.Debug("skip stale job", "file", job.FilePath)
				return nil, nil
			}
		}
	}

	// Fetch old file record and symbols for diff computation
	var oldHash string
	var oldFileID int64
	var oldSymbols map[string]oldSymbolInfo
	_ = tx.QueryRowContext(ctx, "SELECT id, content_hash FROM files WHERE path = ?",
		job.FilePath).Scan(&oldFileID, &oldHash)
	if oldFileID != 0 {
		oldSymbols = fetchOldSymbols(ctx, tx, oldFileID)
	}

	// Upsert file — reset embedding_status to 'pending' since chunks are changing
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO files (path, content_hash, mtime, size, language, indexed_at, embedding_status, parse_quality)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			content_hash = excluded.content_hash,
			mtime = excluded.mtime,
			size = excluded.size,
			language = excluded.language,
			indexed_at = excluded.indexed_at,
			embedding_status = 'pending',
			parse_quality = excluded.parse_quality`,
		job.File.Path, job.File.ContentHash, job.File.Mtime, job.File.Size,
		job.File.Language, time.Now().UTC().Format(time.RFC3339Nano),
		job.File.EmbeddingStatus, job.File.ParseQuality); err != nil {
		return nil, fmt.Errorf("upsert file %s: %w", job.FilePath, err)
	}

	// Always SELECT the file ID after upsert. LastInsertId() is unreliable
	// here because sqlite3_last_insert_rowid() is connection-scoped: when the
	// ON CONFLICT DO UPDATE path is taken, it returns a stale ID from a
	// previous transaction's INSERT (possibly into a different table).
	var fileID int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM files WHERE path = ?", job.FilePath).Scan(&fileID); err != nil {
		return nil, fmt.Errorf("lookup file id %s: %w", job.FilePath, err)
	}

	// Collect old chunk IDs for vector store cleanup before deleting
	var staleChunkIDs []int64
	{
		rows, qerr := tx.QueryContext(ctx, "SELECT id FROM chunks WHERE file_id = ?", fileID)
		if qerr == nil {
			for rows.Next() {
				var cid int64
				if rows.Scan(&cid) == nil {
					staleChunkIDs = append(staleChunkIDs, cid)
				}
			}
			rows.Close()
		}
	}

	// Delete old chunks and symbols (cascade handles symbols via FK)
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE file_id = ?", fileID); err != nil {
		return nil, fmt.Errorf("delete old chunks for %s: %w", job.FilePath, err)
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
			return nil, fmt.Errorf("insert chunk %d for %s: %w", i, job.FilePath, err)
		}
		chunkIDs[i], _ = res.LastInsertId()
	}

	// Resolve parent chunk IDs (CA-10)
	for i, c := range job.Chunks {
		if c.ParentIndex != nil && *c.ParentIndex < len(chunkIDs) {
			parentID := chunkIDs[*c.ParentIndex]
			if _, err := tx.ExecContext(ctx, "UPDATE chunks SET parent_chunk_id = ? WHERE id = ?",
				parentID, chunkIDs[i]); err != nil {
				return nil, fmt.Errorf("set parent for chunk %d: %w", i, err)
			}
		}
	}

	// Insert symbols with resolved chunk IDs, track name→ID mapping for edges
	symbolIDs := make(map[string]int64, len(job.Symbols))
	var newSymbolNames []string
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
		res, err := tx.ExecContext(ctx, `
			INSERT INTO symbols (chunk_id, file_id, name, qualified_name, kind,
			                     line, signature, visibility, is_exported)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chunkID, fileID, sym.Name, sym.QualifiedName, sym.Kind,
			sym.Line, sym.Signature, sym.Visibility, exported)
		if err != nil {
			return nil, fmt.Errorf("insert symbol %s: %w", sym.Name, err)
		}
		symID, _ := res.LastInsertId()
		symbolIDs[sym.Name] = symID
		newSymbolNames = append(newSymbolNames, sym.Name)
	}

	// Compute and record diff if this is a re-index (oldHash != "")
	if oldHash != "" && oldHash != job.ContentHash {
		computeAndRecordDiff(ctx, tx, store, fileID, oldHash, job, oldSymbols, symbolIDs)
	} else if oldHash == "" {
		// New file — record as "add"
		totalLines := 0
		for _, c := range job.Chunks {
			totalLines += c.EndLine - c.StartLine + 1
		}
		recordAddDiff(ctx, tx, store, fileID, job.ContentHash, totalLines, job.Symbols, symbolIDs)
	}

	// Delete old edges for this file, then insert new edges (CA-1)
	if err := store.DeleteEdgesByFile(ctx, tx, fileID); err != nil {
		return nil, fmt.Errorf("delete old edges for %s: %w", job.FilePath, err)
	}
	if err := store.InsertEdges(ctx, tx, fileID, job.Edges, symbolIDs); err != nil {
		return nil, fmt.Errorf("insert edges for %s: %w", job.FilePath, err)
	}

	// Attempt to resolve pending edges for newly inserted symbol names
	if err := store.ResolvePendingEdges(ctx, tx, newSymbolNames); err != nil {
		return nil, fmt.Errorf("resolve pending edges for %s: %w", job.FilePath, err)
	}

	return staleChunkIDs, nil
}

// oldSymbolInfo holds symbol data from the previous version for diff comparison.
type oldSymbolInfo struct {
	name      string
	kind      string
	signature string
	startLine int
	endLine   int
}

// fetchOldSymbols queries old symbols for a file before they're deleted.
func fetchOldSymbols(ctx context.Context, tx *sql.Tx, fileID int64) map[string]oldSymbolInfo {
	symbols := make(map[string]oldSymbolInfo)
	rows, err := tx.QueryContext(ctx, `
		SELECT s.name, s.kind, s.signature, s.line,
		       COALESCE(c.end_line, s.line) as end_line
		FROM symbols s LEFT JOIN chunks c ON s.chunk_id = c.id
		WHERE s.file_id = ?`, fileID)
	if err != nil {
		return symbols
	}
	defer rows.Close()

	for rows.Next() {
		var s oldSymbolInfo
		var sig sql.NullString
		if err := rows.Scan(&s.name, &s.kind, &sig, &s.startLine, &s.endLine); err != nil {
			continue
		}
		s.signature = sig.String
		symbols[s.name] = s
	}
	return symbols
}

// computeAndRecordDiff computes symbol-level diffs and records them.
func computeAndRecordDiff(ctx context.Context, tx *sql.Tx, store *storage.Store,
	fileID int64, oldHash string, job types.WriteJob,
	oldSymbols map[string]oldSymbolInfo, newSymbolIDs map[string]int64) {

	// Compute lines changed (approximate from chunk content)
	var totalOldLines, totalNewLines int
	for _, s := range oldSymbols {
		totalOldLines += s.endLine - s.startLine + 1
	}
	for _, c := range job.Chunks {
		totalNewLines += c.EndLine - c.StartLine + 1
	}
	linesAdded := 0
	linesRemoved := 0
	if totalNewLines > totalOldLines {
		linesAdded = totalNewLines - totalOldLines
	} else {
		linesRemoved = totalOldLines - totalNewLines
	}

	diffID, err := store.InsertDiffLog(ctx, tx, storage.DiffLogEntry{
		FileID:       fileID,
		ChangeType:   "modify",
		LinesAdded:   linesAdded,
		LinesRemoved: linesRemoved,
		HashBefore:   oldHash,
		HashAfter:    job.ContentHash,
	})
	if err != nil {
		return // non-fatal
	}

	// Compare old vs new symbols
	var diffSymbols []storage.DiffSymbolEntry
	newSymbolSet := make(map[string]types.SymbolRecord)
	for _, sym := range job.Symbols {
		newSymbolSet[sym.Name] = sym
	}

	// Find modified and removed symbols
	for name, oldSym := range oldSymbols {
		if newSym, exists := newSymbolSet[name]; exists {
			if oldSym.signature != newSym.Signature {
				diffSymbols = append(diffSymbols, storage.DiffSymbolEntry{
					SymbolName: name,
					SymbolID:   newSymbolIDs[name],
					ChangeType: "signature_changed",
				})
			} else if oldSym.startLine != newSym.Line {
				diffSymbols = append(diffSymbols, storage.DiffSymbolEntry{
					SymbolName: name,
					SymbolID:   newSymbolIDs[name],
					ChangeType: "modified",
				})
			}
		} else {
			diffSymbols = append(diffSymbols, storage.DiffSymbolEntry{
				SymbolName: name,
				ChangeType: "removed",
			})
		}
	}

	// Find added symbols
	for name := range newSymbolSet {
		if _, existed := oldSymbols[name]; !existed {
			diffSymbols = append(diffSymbols, storage.DiffSymbolEntry{
				SymbolName: name,
				SymbolID:   newSymbolIDs[name],
				ChangeType: "added",
			})
		}
	}

	if len(diffSymbols) > 0 {
		_ = store.InsertDiffSymbols(ctx, tx, diffID, diffSymbols)
	}
}

// recordAddDiff records a diff for a newly added file.
func recordAddDiff(ctx context.Context, tx *sql.Tx, store *storage.Store,
	fileID int64, hash string, totalLines int,
	symbols []types.SymbolRecord, symbolIDs map[string]int64) {

	diffID, err := store.InsertDiffLog(ctx, tx, storage.DiffLogEntry{
		FileID:     fileID,
		ChangeType: "add",
		LinesAdded: totalLines,
		HashAfter:  hash,
	})
	if err != nil {
		return
	}

	var diffSymbols []storage.DiffSymbolEntry
	for _, sym := range symbols {
		diffSymbols = append(diffSymbols, storage.DiffSymbolEntry{
			SymbolName: sym.Name,
			SymbolID:   symbolIDs[sym.Name],
			ChangeType: "added",
		})
	}
	if len(diffSymbols) > 0 {
		_ = store.InsertDiffSymbols(ctx, tx, diffID, diffSymbols)
	}
}

func coalesce(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
