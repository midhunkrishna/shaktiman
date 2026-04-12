package vector

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

// EmbedJob represents a single chunk to be embedded.
type EmbedJob struct {
	ChunkID int64
	Content string
}

// EmbedWorkerInput configures the EmbedWorker.
type EmbedWorkerInput struct {
	Store          types.VectorStore
	Embedder       *OllamaClient
	BatchSize      int
	OnBatchDone    func(chunkIDs []int64) // optional callback after successful upsert
	DocumentPrefix string                 // prepended to chunk content before embedding (e.g. "search_document: ")
}

// EmbedWorker processes embedding jobs in batches with circuit breaker protection.
type EmbedWorker struct {
	store          types.VectorStore
	embedder       *OllamaClient
	cb             *CircuitBreaker
	queue          chan EmbedJob
	batchSz        int
	logger         *slog.Logger
	onBatchDone    func(chunkIDs []int64)
	dropped        atomic.Int64
	inflight       sync.WaitGroup // tracks in-flight processBatch calls
	documentPrefix string         // prepended to chunk content before embedding
}

// NewEmbedWorker creates an embedding worker.
func NewEmbedWorker(input EmbedWorkerInput) *EmbedWorker {
	batchSz := input.BatchSize
	if batchSz <= 0 {
		batchSz = 128
	}
	return &EmbedWorker{
		store:          input.Store,
		embedder:       input.Embedder,
		cb:             NewCircuitBreaker(),
		queue:          make(chan EmbedJob, 1000),
		batchSz:        batchSz,
		logger:         slog.Default().With("component", "embed-worker"),
		onBatchDone:    input.OnBatchDone,
		documentPrefix: input.DocumentPrefix,
	}
}

// EmbedderHealthy returns true if the underlying Ollama API is reachable.
func (w *EmbedWorker) EmbedderHealthy(ctx context.Context) bool {
	return w.embedder.Healthy(ctx)
}

// Submit enqueues an embedding job. Non-blocking; drops if queue is full.
func (w *EmbedWorker) Submit(job EmbedJob) bool {
	select {
	case w.queue <- job:
		return true
	default:
		total := w.dropped.Add(1)
		if total == 1 || total%100 == 0 {
			w.logger.Warn("embed job dropped, queue full",
				"queue_cap", cap(w.queue),
				"total_dropped", total)
		}
		return false
	}
}

// SubmitBatch enqueues multiple embedding jobs.
func (w *EmbedWorker) SubmitBatch(jobs []EmbedJob) int {
	submitted := 0
	for _, j := range jobs {
		if w.Submit(j) {
			submitted++
		}
	}
	return submitted
}

// CircuitBreaker returns the worker's circuit breaker for status queries.
func (w *EmbedWorker) CircuitBreaker() *CircuitBreaker {
	return w.cb
}

// Pending returns the number of jobs waiting in the queue.
func (w *EmbedWorker) Pending() int {
	return len(w.queue)
}

// EmbedReady returns true if the circuit breaker allows embedding requests.
func (w *EmbedWorker) EmbedReady() bool {
	s := w.cb.State()
	return s == StateClosed || s == StateHalfOpen
}

// Run processes embedding jobs until ctx is cancelled. Blocks.
func (w *EmbedWorker) Run(ctx context.Context) {
	batch := make([]EmbedJob, 0, w.batchSz)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case job, ok := <-w.queue:
			if !ok {
				if len(batch) > 0 {
					w.processBatch(ctx, batch)
				}
				return
			}
			batch = append(batch, job)
			if len(batch) >= w.batchSz {
				w.processBatch(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				w.processBatch(ctx, batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			if len(batch) > 0 {
				drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				w.processBatch(drainCtx, batch)
				cancel()
			}
			return
		}
	}
}

// WaitIdle blocks until the queue is empty and no batch is in flight.
func (w *EmbedWorker) WaitIdle() {
	for w.Pending() > 0 {
		time.Sleep(200 * time.Millisecond)
	}
	w.inflight.Wait()
}

func (w *EmbedWorker) processBatch(ctx context.Context, batch []EmbedJob) {
	w.inflight.Add(1)
	defer w.inflight.Done()
	start := time.Now()
	if !w.cb.Allow() {
		w.logger.Debug("circuit breaker open, skipping batch", "size", len(batch))
		return
	}

	texts := make([]string, len(batch))
	for i, j := range batch {
		texts[i] = w.documentPrefix + j.Content
	}

	vectors, err := w.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		w.cb.RecordFailure()
		w.logger.Warn("embed batch failed", "size", len(batch), "err", err)
		return
	}
	w.cb.RecordSuccess()

	chunkIDs := make([]int64, len(batch))
	for i, j := range batch {
		chunkIDs[i] = j.ChunkID
	}

	if err := w.store.UpsertBatch(ctx, chunkIDs, vectors); err != nil {
		w.logger.Error("upsert batch failed", "err", err)
		return
	}

	w.logger.Info("embed batch completed",
		"size", len(batch),
		"duration_ms", time.Since(start).Milliseconds())

	if w.onBatchDone != nil {
		w.onBatchDone(chunkIDs)
	}
}

// ── RunFromDB and helpers ──

// runState tracks mutable progress during a RunFromDB execution.
type runState struct {
	embedded    int
	skipped     int
	activeBatch int // adaptive batch size; halves on error, restores on success
	deferred    []deferredJob
	total       int
	onProgress  func(types.EmbedProgress)
}

// report sends a progress update if the callback is set.
func (rs *runState) report(warning string) {
	if rs.onProgress != nil {
		rs.onProgress(types.EmbedProgress{
			Embedded: rs.embedded,
			Total:    rs.total,
			Skipped:  rs.skipped,
			Warning:  warning,
		})
	}
}

// RunFromDB pulls chunks from source using cursor-based pagination, embeds them
// in batches, and marks them as embedded. Synchronous and blocking.
//
// Phase 1: pages through unembedded chunks, embedding each batch with adaptive
// sizing and circuit breaker protection. Permanent errors (4xx) are skipped;
// transient errors are deferred for Phase 2.
//
// Phase 2: retries deferred chunks one-at-a-time over multiple passes.
//
// Has() reconciliation: chunks already in the vector store are skipped
// (marked embedded in DB) but not re-embedded.
func (w *EmbedWorker) RunFromDB(ctx context.Context, source types.EmbedSource, onProgress func(types.EmbedProgress)) error {
	const pageSize = 256

	total, err := source.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		return fmt.Errorf("count chunks needing embedding: %w", err)
	}
	if total == 0 {
		if onProgress != nil {
			onProgress(types.EmbedProgress{Embedded: 0, Total: 0})
		}
		return nil
	}

	rs := &runState{
		activeBatch: w.batchSz,
		total:       total,
		onProgress:  onProgress,
	}

	// Phase 1: cursor-based pagination.
	lastID := int64(0)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		page, err := source.GetEmbedPage(ctx, lastID, pageSize)
		if err != nil {
			return fmt.Errorf("get embed page after %d: %w", lastID, err)
		}
		if len(page) == 0 {
			break
		}

		for i := 0; i < len(page); {
			end := i + rs.activeBatch
			if end > len(page) {
				end = len(page)
			}
			err := w.reconcileAndEmbed(ctx, source, page[i:end], rs)
			if errors.Is(err, errBatchShrunk) {
				continue // re-slice from same i with smaller activeBatch
			}
			if err != nil {
				return err
			}
			i = end
		}

		lastID = page[len(page)-1].ChunkID
	}

	// Phase 2: retry deferred chunks.
	if err := w.retryDeferred(ctx, rs); err != nil {
		return err
	}

	if rs.skipped > 0 {
		w.logger.Warn("embedding completed with skipped chunks",
			"embedded", rs.embedded, "skipped", rs.skipped, "total", total)
		rs.report("")
	}

	return nil
}

// reconcileAndEmbed processes a single batch from a page. It performs Has()
// reconciliation, embeds chunks not yet in the store, upserts vectors, and
// marks chunks in the source.
//
// Returns errBatchShrunk if the adaptive batch size was halved (caller should
// re-slice from the same page position). Returns nil on success, including
// cases where chunks were permanently skipped or deferred for Phase 2.
func (w *EmbedWorker) reconcileAndEmbed(ctx context.Context, source types.EmbedSource, batch []types.EmbedJob, rs *runState) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	batchIDs := make([]int64, len(batch))
	for j, job := range batch {
		batchIDs[j] = job.ChunkID
	}

	// Has() reconciliation: skip chunks already in vector store.
	var needEmbed []types.EmbedJob
	for _, job := range batch {
		has, err := w.store.Has(ctx, job.ChunkID)
		if err != nil {
			return fmt.Errorf("check vector store for chunk %d: %w", job.ChunkID, err)
		}
		if !has {
			needEmbed = append(needEmbed, job)
		}
	}

	if len(needEmbed) == 0 {
		if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
			return fmt.Errorf("mark already-embedded chunks: %w", err)
		}
		rs.embedded += len(batch)
		rs.report("")
		return nil
	}

	// Build texts and IDs for embedding.
	texts := make([]string, len(needEmbed))
	needIDs := make([]int64, len(needEmbed))
	for j, job := range needEmbed {
		texts[j] = w.documentPrefix + job.Content
		needIDs[j] = job.ChunkID
	}

	vectors, err := w.embedWithRetry(ctx, texts, rs)

	// Batch size was halved — caller should re-slice.
	if errors.Is(err, errBatchShrunk) {
		return errBatchShrunk
	}

	// Permanent error — skip batch, advance cursor.
	if errors.Is(err, ErrPermanentEmbed) {
		rs.skipped += len(needEmbed)
		if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
			return fmt.Errorf("mark skipped chunks: %w", err)
		}
		rs.embedded += len(batch)
		rs.report("")
		return nil
	}

	// Transient failure after retries — defer for Phase 2.
	if err != nil {
		for _, job := range needEmbed {
			rs.deferred = append(rs.deferred, deferredJob{job: job})
		}
		w.logger.Warn("batch deferred for retry",
			"size", len(needEmbed), "deferred_total", len(rs.deferred))
		rs.report(fmt.Sprintf("Ollama connection issues. %d chunks deferred for retry.", len(rs.deferred)))
		if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
			return fmt.Errorf("mark deferred chunks: %w", err)
		}
		rs.embedded += len(batch)
		rs.report("")
		return nil
	}

	// Success — upsert and mark.
	if err := w.store.UpsertBatch(ctx, needIDs, vectors); err != nil {
		return fmt.Errorf("upsert batch: %w", err)
	}
	if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
		return fmt.Errorf("mark chunks embedded: %w", err)
	}
	rs.embedded += len(batch)
	rs.report("")
	return nil
}

// embedWithRetry attempts to embed texts with quick retries and circuit breaker
// integration. It adapts rs.activeBatch on transient errors.
//
// Returns:
//   - (vectors, nil) on success; rs.activeBatch restored to original
//   - (nil, errBatchShrunk) if batch size was halved; caller should re-slice
//   - (nil, ErrPermanentEmbed-wrapping error) on permanent failure
//   - (nil, other error) on transient exhaustion; caller should defer
func (w *EmbedWorker) embedWithRetry(ctx context.Context, texts []string, rs *runState) ([][]float32, error) {
	const quickRetries = 3
	var lastErr error

	for attempt := 0; attempt < quickRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !w.cb.Allow() {
			select {
			case <-time.After(2 * time.Second):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		vectors, err := w.embedder.EmbedBatch(ctx, texts)
		if err == nil {
			w.cb.RecordSuccess()
			if rs.activeBatch != w.batchSz {
				w.logger.Info("restoring batch size after success",
					"from", rs.activeBatch, "to", w.batchSz)
				rs.activeBatch = w.batchSz
			}
			return vectors, nil
		}

		// Permanent errors (4xx) are not retryable.
		if errors.Is(err, ErrPermanentEmbed) {
			w.logger.Warn("permanent embed error, skipping batch",
				"size", len(texts), "err", err)
			return nil, err
		}

		w.cb.RecordFailure()
		lastErr = err

		// Adaptive: halve batch size and signal re-slice.
		if newSz := rs.activeBatch / 2; newSz >= 1 && newSz < rs.activeBatch {
			w.logger.Info("shrinking batch size after error",
				"from", rs.activeBatch, "to", newSz)
			rs.activeBatch = newSz
			return nil, errBatchShrunk
		}

		w.logger.Warn("embed batch failed, retrying",
			"size", len(texts), "retry", attempt+1, "err", err)
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("embed batch exhausted %d retries: %w", quickRetries, lastErr)
}

// retryDeferred processes Phase 2: re-embeds deferred chunks one at a time
// over multiple passes. Updates rs.skipped for permanently failed or
// exhausted chunks.
func (w *EmbedWorker) retryDeferred(ctx context.Context, rs *runState) error {
	if len(rs.deferred) == 0 {
		return nil
	}

	w.logger.Info("processing deferred chunks", "count", len(rs.deferred))
	rs.report(fmt.Sprintf("Retrying %d deferred chunks...", len(rs.deferred)))

	const maxPasses = 3
	for pass := 0; pass < maxPasses && len(rs.deferred) > 0; pass++ {
		var stillFailing []deferredJob

		for _, dc := range rs.deferred {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if !w.cb.Allow() {
				stillFailing = append(stillFailing, dc)
				continue
			}

			vecs, err := w.embedder.EmbedBatch(ctx, []string{w.documentPrefix + dc.job.Content})
			if err != nil {
				if errors.Is(err, ErrPermanentEmbed) {
					rs.skipped++
					w.logger.Warn("deferred chunk permanently skipped",
						"chunk_id", dc.job.ChunkID, "err", err)
					continue
				}
				w.cb.RecordFailure()
				stillFailing = append(stillFailing, dc)
				continue
			}
			w.cb.RecordSuccess()

			if err := w.store.Upsert(ctx, dc.job.ChunkID, vecs[0]); err != nil {
				return fmt.Errorf("upsert deferred chunk %d: %w", dc.job.ChunkID, err)
			}
		}

		rs.deferred = stillFailing

		if len(rs.deferred) > 0 {
			w.logger.Info("deferred retry pass complete",
				"pass", pass+1, "remaining", len(rs.deferred))
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Remaining deferred chunks after all passes are skipped.
	if len(rs.deferred) > 0 {
		rs.skipped += len(rs.deferred)
		for _, dc := range rs.deferred {
			w.logger.Warn("chunk skipped after all retries",
				"chunk_id", dc.job.ChunkID)
		}
	}

	return nil
}
