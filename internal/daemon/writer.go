// Package daemon provides lifecycle management, background indexing, and the writer goroutine.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

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
	store         types.WriterStore
	logger        *slog.Logger
	closed        atomic.Bool
	started       atomic.Bool         // true after Run() is called
	draining      atomic.Bool         // true once drain() begins
	mu            sync.Mutex          // protects close sequence, draining flag, and channel sends
	notFull       *sync.Cond          // signaled when a job is processed (channel has space)
	vectorDeleter types.VectorDeleter // nil if embeddings disabled
	testPatterns  []string            // glob patterns for test file detection
}

// SetVectorDeleter attaches a vector deleter for cleaning up stale embeddings
// when chunks are replaced or files are deleted.
func (wm *WriterManager) SetVectorDeleter(vd types.VectorDeleter) {
	wm.vectorDeleter = vd
}

// NewWriterManager creates a writer with the given channel capacity (IP-5: 500).
func NewWriterManager(store types.WriterStore, chanSize int, testPatterns []string) *WriterManager {
	wm := &WriterManager{
		ch:           make(chan types.WriteJob, chanSize),
		done:         make(chan struct{}),
		store:        store,
		logger:       slog.Default().With("component", "writer"),
		testPatterns: testPatterns,
	}
	wm.notFull = sync.NewCond(&wm.mu)
	return wm
}

// Run processes write jobs until ctx is cancelled, then drains remaining jobs.
// This method blocks — run it in a goroutine.
func (wm *WriterManager) Run(ctx context.Context) {
	wm.started.Store(true)

	for {
		select {
		case job := <-wm.ch:
			wm.processJob(ctx, job)
			wm.notFull.Signal() // wake one blocked Submit
		case <-ctx.Done():
			wm.drain()
			close(wm.done) // signal callers waiting on Done()
			return
		}
	}
}

// drain waits for all producers to stop, then processes remaining jobs.
// The channel is never closed — Submit uses the closed flag and notFull
// condition to detect shutdown, eliminating send-on-closed-channel races.
func (wm *WriterManager) drain() {
	wm.mu.Lock()
	wm.draining.Store(true)
	wm.mu.Unlock()

	wm.logger.Info("writer draining: waiting for producers")
	wm.producers.Wait()

	// Set closed and wake all blocked Submits. They will re-check
	// closed under the lock and return ErrWriterClosed.
	wm.mu.Lock()
	wm.closed.Store(true)
	wm.notFull.Broadcast()
	wm.mu.Unlock()

	// Drain remaining buffered jobs via non-blocking receive.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case job := <-wm.ch:
			wm.processJob(context.Background(), job)
		case <-deadline:
			wm.logger.Warn("writer drain timeout, dropping remaining jobs")
			return
		default:
			wm.logger.Info("writer drain complete")
			return
		}
	}
}

// Submit sends a write job to the writer goroutine.
// Blocks if the channel is full (back-pressure).
// Returns ErrWriterClosed if the writer has been shut down.
//
// The closed check and channel send are always under the same lock hold,
// with notFull.Wait() for back-pressure. This eliminates the TOCTOU gap
// that previously allowed send-on-closed-channel panics.
func (wm *WriterManager) Submit(job types.WriteJob) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	logged := false
	for {
		if wm.closed.Load() {
			return ErrWriterClosed
		}

		// Non-blocking send attempt under lock.
		select {
		case wm.ch <- job:
			return nil
		default:
		}

		// Channel full — log once, then wait for space.
		if !logged {
			wm.logger.Debug("writer channel full, blocking",
				"queue_len", len(wm.ch),
				"queue_cap", cap(wm.ch),
				"file", job.FilePath)
			logged = true
		}

		// Wait releases mu, allowing Run to process jobs and signal notFull.
		// On wake, mu is re-acquired and the loop re-checks closed + retries send.
		wm.notFull.Wait()
	}
}

// AddProducer registers a producer goroutine for shutdown ordering.
// Returns false if the writer is draining or closed; callers must not
// call RemoveProducer when AddProducer returns false.
func (wm *WriterManager) AddProducer() bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wm.draining.Load() || wm.closed.Load() {
		return false
	}
	wm.producers.Add(1)
	return true
}

// RemoveProducer unregisters a producer goroutine.
func (wm *WriterManager) RemoveProducer() { wm.producers.Done() }

// Done returns a channel that is closed when the writer has finished.
func (wm *WriterManager) Done() <-chan struct{} { return wm.done }

// Started reports whether Run has been called.
func (wm *WriterManager) Started() bool { return wm.started.Load() }

func (wm *WriterManager) processJob(ctx context.Context, job types.WriteJob) {
	start := time.Now()
	staleChunkIDs, err := processWriteJob(ctx, wm.store, wm.logger, job, wm.testPatterns)
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

// processWriteJob executes a single write job via the WriterStore interface.
// Returns IDs of chunks that were deleted (for vector store cleanup).
func processWriteJob(ctx context.Context, store types.WriterStore, logger *slog.Logger, job types.WriteJob, testPatterns []string) ([]int64, error) {
	switch job.Type {
	case types.WriteJobEnrichment:
		return processEnrichmentJob(ctx, store, logger, job, testPatterns)
	case types.WriteJobFileDelete:
		var staleChunkIDs []int64
		file, err := store.GetFileByPath(ctx, job.FilePath)
		if err != nil {
			return nil, fmt.Errorf("lookup file for delete %s: %w", job.FilePath, err)
		}
		if file != nil {
			staleChunkIDs, _ = store.GetEmbeddedChunkIDsByFile(ctx, file.ID)
		}
		if _, err := store.DeleteFileByPath(ctx, job.FilePath); err != nil {
			return nil, fmt.Errorf("delete file %s: %w", job.FilePath, err)
		}
		return staleChunkIDs, nil
	case types.WriteJobSync:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown write job type: %d", job.Type)
	}
}

// processEnrichmentJob handles an enrichment write: upsert file, replace chunks + symbols.
// Returns IDs of old chunks that were replaced (for vector store cleanup).
func processEnrichmentJob(ctx context.Context, store types.WriterStore, logger *slog.Logger, job types.WriteJob, testPatterns []string) ([]int64, error) {
	if job.File == nil {
		return nil, fmt.Errorf("enrichment job for %s has nil file", job.FilePath)
	}

	// Content hash guard (CA-3): skip if already indexed with same hash
	if job.ContentHash != "" {
		file, err := store.GetFileByPath(ctx, job.FilePath)
		if err == nil && file != nil {
			if file.ContentHash == job.ContentHash {
				logger.Debug("skip same hash", "file", job.FilePath)
				return nil, nil
			}
			// Stale check: if DB was updated after job was created
			if file.IndexedAt != "" {
				dbTime, tErr := time.Parse(time.RFC3339Nano, file.IndexedAt)
				if tErr == nil && dbTime.After(job.Timestamp) {
					logger.Debug("skip stale job", "file", job.FilePath)
					return nil, nil
				}
			}
		}
	}

	// Fetch old file record and symbols for diff computation
	var oldHash string
	var oldFileID int64
	var oldSymbols map[string]oldSymbolInfo
	oldFile, _ := store.GetFileByPath(ctx, job.FilePath)
	if oldFile != nil {
		oldFileID = oldFile.ID
		oldHash = oldFile.ContentHash
		oldSymbols = buildOldSymbols(ctx, store, oldFileID)
	}

	// Upsert file — reset embedding_status to 'pending' since chunks are changing
	isTest := IsTestFile(job.File.Path, testPatterns)
	fileRecord := &types.FileRecord{
		Path:            job.File.Path,
		ContentHash:     job.File.ContentHash,
		Mtime:           job.File.Mtime,
		Size:            job.File.Size,
		Language:        job.File.Language,
		IndexedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		EmbeddingStatus: "pending",
		ParseQuality:    job.File.ParseQuality,
		IsTest:          isTest,
	}
	fileID, err := store.UpsertFile(ctx, fileRecord)
	if err != nil {
		return nil, fmt.Errorf("upsert file %s: %w", job.FilePath, err)
	}

	// Collect old chunk IDs for vector store cleanup before deleting
	staleChunkIDs, _ := store.GetEmbeddedChunkIDsByFile(ctx, fileID)

	// Delete old chunks and symbols (cascade handles symbols via FK)
	if err := store.DeleteChunksByFile(ctx, fileID); err != nil {
		return nil, fmt.Errorf("delete old chunks for %s: %w", job.FilePath, err)
	}

	// Insert new chunks
	chunks := make([]types.ChunkRecord, len(job.Chunks))
	for i, c := range job.Chunks {
		chunks[i] = types.ChunkRecord{
			ChunkIndex:   c.ChunkIndex,
			SymbolName:   c.SymbolName,
			Kind:         c.Kind,
			StartLine:    c.StartLine,
			EndLine:      c.EndLine,
			Content:      c.Content,
			TokenCount:   c.TokenCount,
			Signature:    c.Signature,
			ParseQuality: coalesce(c.ParseQuality, "full"),
		}
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, chunks)
	if err != nil {
		return nil, fmt.Errorf("insert chunks for %s: %w", job.FilePath, err)
	}

	// Resolve parent chunk IDs (CA-10)
	parents := make(map[int64]int64)
	for i, c := range job.Chunks {
		if c.ParentIndex != nil && *c.ParentIndex < len(chunkIDs) {
			parents[chunkIDs[i]] = chunkIDs[*c.ParentIndex]
		}
	}
	if len(parents) > 0 {
		if err := store.UpdateChunkParents(ctx, parents); err != nil {
			return nil, fmt.Errorf("set parent chunks for %s: %w", job.FilePath, err)
		}
	}

	// Insert symbols with resolved chunk IDs, track name->ID mapping for edges
	symRecords := make([]types.SymbolRecord, len(job.Symbols))
	for i, sym := range job.Symbols {
		chunkID := int64(0)
		if int(sym.ChunkID) < len(chunkIDs) {
			chunkID = chunkIDs[sym.ChunkID]
		}
		symRecords[i] = types.SymbolRecord{
			ChunkID:       chunkID,
			Name:          sym.Name,
			QualifiedName: sym.QualifiedName,
			Kind:          sym.Kind,
			Line:          sym.Line,
			Signature:     sym.Signature,
			Visibility:    sym.Visibility,
			IsExported:    sym.IsExported,
		}
	}
	symIDs, err := store.InsertSymbols(ctx, fileID, symRecords)
	if err != nil {
		return nil, fmt.Errorf("insert symbols for %s: %w", job.FilePath, err)
	}

	symbolIDs := make(map[string]int64, len(job.Symbols))
	var newSymbolNames []string
	for i, sym := range job.Symbols {
		// Keep the first symbol ID for each name. Duplicates (e.g. Java
		// method overloads) would overwrite, potentially pointing edges
		// at the wrong overload.
		if _, exists := symbolIDs[sym.Name]; !exists {
			symbolIDs[sym.Name] = symIDs[i]
		}
		newSymbolNames = append(newSymbolNames, sym.Name)
	}

	// Compute and record diff if this is a re-index (oldHash != "")
	if oldHash != "" && oldHash != job.ContentHash {
		computeAndRecordDiff(ctx, store, fileID, oldHash, job, oldSymbols, symbolIDs)
	} else if oldHash == "" {
		// New file — record as "add"
		totalLines := 0
		for _, c := range job.Chunks {
			totalLines += c.EndLine - c.StartLine + 1
		}
		recordAddDiff(ctx, store, fileID, job.ContentHash, totalLines, job.Symbols, symbolIDs)
	}

	// Delete old edges for this file, then insert new edges (CA-1)
	// Use WithWriteTx for edge operations that require a TxHandle.
	if err := store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		if err := store.DeleteEdgesByFile(ctx, txh, fileID); err != nil {
			return fmt.Errorf("delete old edges for %s: %w", job.FilePath, err)
		}
		if err := store.InsertEdges(ctx, txh, fileID, job.Edges, symbolIDs, job.File.Language); err != nil {
			return fmt.Errorf("insert edges for %s: %w", job.FilePath, err)
		}
		return store.ResolvePendingEdges(ctx, txh, newSymbolNames)
	}); err != nil {
		return nil, err
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

// buildOldSymbols constructs old symbol info from store queries for diff comparison.
func buildOldSymbols(ctx context.Context, store types.WriterStore, fileID int64) map[string]oldSymbolInfo {
	symbols, err := store.GetSymbolsByFile(ctx, fileID)
	if err != nil {
		return nil
	}

	// Build chunkID -> endLine mapping from chunks
	chunkEndLines := map[int64]int{}
	chunks, err := store.GetChunksByFile(ctx, fileID)
	if err == nil {
		for _, c := range chunks {
			chunkEndLines[c.ID] = c.EndLine
		}
	}

	result := make(map[string]oldSymbolInfo, len(symbols))
	for _, s := range symbols {
		endLine := s.Line
		if el, ok := chunkEndLines[s.ChunkID]; ok {
			endLine = el
		}
		result[s.Name] = oldSymbolInfo{
			name:      s.Name,
			kind:      s.Kind,
			signature: s.Signature,
			startLine: s.Line,
			endLine:   endLine,
		}
	}
	return result
}

// computeAndRecordDiff computes symbol-level diffs and records them.
func computeAndRecordDiff(ctx context.Context, store types.WriterStore,
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

	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		diffID, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID:       fileID,
			ChangeType:   "modify",
			LinesAdded:   linesAdded,
			LinesRemoved: linesRemoved,
			HashBefore:   oldHash,
			HashAfter:    job.ContentHash,
		})
		if err != nil {
			return err // non-fatal at caller level
		}

		// Compare old vs new symbols
		var diffSymbols []types.DiffSymbolEntry
		newSymbolSet := make(map[string]types.SymbolRecord)
		for _, sym := range job.Symbols {
			newSymbolSet[sym.Name] = sym
		}

		// Find modified and removed symbols
		for name, oldSym := range oldSymbols {
			if newSym, exists := newSymbolSet[name]; exists {
				if oldSym.signature != newSym.Signature {
					diffSymbols = append(diffSymbols, types.DiffSymbolEntry{
						SymbolName: name,
						SymbolID:   newSymbolIDs[name],
						ChangeType: "signature_changed",
					})
				} else if oldSym.startLine != newSym.Line {
					diffSymbols = append(diffSymbols, types.DiffSymbolEntry{
						SymbolName: name,
						SymbolID:   newSymbolIDs[name],
						ChangeType: "modified",
					})
				}
			} else {
				diffSymbols = append(diffSymbols, types.DiffSymbolEntry{
					SymbolName: name,
					ChangeType: "removed",
				})
			}
		}

		// Find added symbols
		for name := range newSymbolSet {
			if _, existed := oldSymbols[name]; !existed {
				diffSymbols = append(diffSymbols, types.DiffSymbolEntry{
					SymbolName: name,
					SymbolID:   newSymbolIDs[name],
					ChangeType: "added",
				})
			}
		}

		if len(diffSymbols) > 0 {
			return store.InsertDiffSymbols(ctx, txh, diffID, diffSymbols)
		}
		return nil
	})
}

// recordAddDiff records a diff for a newly added file.
func recordAddDiff(ctx context.Context, store types.WriterStore,
	fileID int64, hash string, totalLines int,
	symbols []types.SymbolRecord, symbolIDs map[string]int64) {

	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		diffID, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID:     fileID,
			ChangeType: "add",
			LinesAdded: totalLines,
			HashAfter:  hash,
		})
		if err != nil {
			return err
		}

		var diffSymbols []types.DiffSymbolEntry
		for _, sym := range symbols {
			diffSymbols = append(diffSymbols, types.DiffSymbolEntry{
				SymbolName: sym.Name,
				SymbolID:   symbolIDs[sym.Name],
				ChangeType: "added",
			})
		}
		if len(diffSymbols) > 0 {
			return store.InsertDiffSymbols(ctx, txh, diffID, diffSymbols)
		}
		return nil
	})
}

func coalesce(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
