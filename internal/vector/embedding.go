package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Sentinel errors for embedding operations.
var (
	ErrOllamaUnreachable = errors.New("ollama is unreachable")
	ErrCircuitOpen       = errors.New("circuit breaker is open")
)

// ── Ollama HTTP Client ──

// OllamaClient calls the Ollama embedding API over HTTP.
type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
	logger  *slog.Logger
}

// OllamaClientInput configures an OllamaClient.
type OllamaClientInput struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// NewOllamaClient creates an Ollama HTTP client.
func NewOllamaClient(input OllamaClientInput) *OllamaClient {
	timeout := input.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &OllamaClient{
		baseURL: input.BaseURL,
		model:   input.Model,
		client:  &http.Client{Timeout: timeout},
		logger:  slog.Default().With("component", "ollama"),
	}
}

type ollamaEmbedRequest struct {
	Model    string `json:"model"`
	Input    any    `json:"input"`    // string or []string
	Truncate bool   `json:"truncate"` // truncate input to fit model context window
}

// maxSafeEmbedChars is a conservative client-side limit for warning about
// oversized inputs. nomic-embed-text has 8192 token context; ~4 chars/token
// = ~32K chars. We warn at 28K to leave headroom.
const maxSafeEmbedChars = 28000

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed produces a single embedding vector for the given text.
func (c *OllamaClient) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama returned 0 embeddings for 1 input")
	}
	return vecs[0], nil
}

// EmbedBatch produces embedding vectors for multiple texts in one HTTP call.
// Sends truncate=true so Ollama clips oversized inputs to the model's context
// window instead of returning an error.
func (c *OllamaClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	for i, t := range texts {
		if len(t) > maxSafeEmbedChars {
			c.logger.Warn("oversized input will be truncated by Ollama",
				"index", i, "chars", len(t), "limit", maxSafeEmbedChars)
		}
	}

	body, err := json.Marshal(ollamaEmbedRequest{Model: c.model, Input: texts, Truncate: true})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed HTTP call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama embed returned %d: %s", resp.StatusCode, respBody)
	}

	const maxEmbedResponseSize = 50 * 1024 * 1024 // 50MB
	var result ollamaEmbedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEmbedResponseSize)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs",
			len(result.Embeddings), len(texts))
	}
	return result.Embeddings, nil
}

// Healthy returns true if the Ollama API is reachable.
func (c *OllamaClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ── Circuit Breaker ──

// CircuitState represents the state of the circuit breaker.
type CircuitState int32

const (
	StateClosed   CircuitState = iota // normal operation
	StateOpen                         // failing, reject requests with backoff
	StateHalfOpen                     // probing single request
	StateDisabled                     // kept for API compat; functionally same as StateOpen
)

// CircuitBreaker protects Ollama calls with a state machine (IP-15: mutex-based).
// Uses exponential backoff instead of permanent disable: cooldown doubles on each
// open cycle up to backoffMax, then keeps retrying at backoffMax intervals.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	consecutiveFail int
	openCycles      int
	lastOpenTime    time.Time
	baseCooldown    time.Duration
	currentCooldown time.Duration
	backoffMax      time.Duration
	failThreshold   int
	halfOpenProbing bool // true when a probe request is in flight
}

// NewCircuitBreaker creates a circuit breaker with defaults.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:           StateClosed,
		baseCooldown:    5 * time.Minute,
		currentCooldown: 5 * time.Minute,
		backoffMax:      1 * time.Hour,
		failThreshold:   3,
	}
}

// Allow returns true if a request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.lastOpenTime) > cb.currentCooldown {
			cb.state = StateHalfOpen
			cb.halfOpenProbing = true
			return true
		}
		return false
	case StateHalfOpen:
		if cb.halfOpenProbing {
			return false // only one probe at a time
		}
		cb.halfOpenProbing = true
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	prevState := cb.state
	cb.consecutiveFail = 0
	cb.openCycles = 0
	cb.currentCooldown = cb.baseCooldown
	cb.halfOpenProbing = false
	cb.state = StateClosed
	cb.mu.Unlock()

	if prevState != StateClosed {
		slog.Info("circuit breaker recovered",
			"from", stateString(prevState), "to", "closed")
	}
}

// RecordFailure records a failed call. Transitions to OPEN with exponential backoff.
// In HalfOpen, a single failure immediately re-opens the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.halfOpenProbing = false

	// HalfOpen probe failed → immediately re-open with escalated backoff
	if cb.state == StateHalfOpen {
		cb.openCycles++
		cb.state = StateOpen
		cb.lastOpenTime = time.Now()
		cb.currentCooldown = min(cb.currentCooldown*2, cb.backoffMax)
		slog.Warn("circuit breaker opened",
			"from", "half_open", "cooldown", cb.currentCooldown, "cycles", cb.openCycles)
		return
	}

	cb.consecutiveFail++
	if cb.consecutiveFail >= cb.failThreshold {
		prevState := cb.state
		cb.openCycles++
		cb.state = StateOpen
		cb.lastOpenTime = time.Now()
		cb.consecutiveFail = 0
		// Exponential backoff: 5m → 10m → 20m → 40m → 60m (capped)
		cb.currentCooldown = min(cb.currentCooldown*2, cb.backoffMax)
		slog.Warn("circuit breaker opened",
			"from", stateString(prevState), "cooldown", cb.currentCooldown, "cycles", cb.openCycles)
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset forces the circuit breaker back to CLOSED.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.consecutiveFail = 0
	cb.openCycles = 0
	cb.currentCooldown = cb.baseCooldown
	cb.halfOpenProbing = false
}

func stateString(s CircuitState) string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// ── Embed Worker ──

// EmbedJob represents a single chunk to be embedded.
type EmbedJob struct {
	ChunkID int64
	Content string
}

// EmbedWorkerInput configures the EmbedWorker.
type EmbedWorkerInput struct {
	Store       types.VectorStore
	Embedder    *OllamaClient
	BatchSize   int
	OnBatchDone func(chunkIDs []int64) // optional callback after successful upsert
}

// EmbedWorker processes embedding jobs in batches with circuit breaker protection.
type EmbedWorker struct {
	store       types.VectorStore
	embedder    *OllamaClient
	cb          *CircuitBreaker
	queue       chan EmbedJob
	batchSz     int
	logger      *slog.Logger
	onBatchDone func(chunkIDs []int64)
	dropped     atomic.Int64
	inflight    sync.WaitGroup // tracks in-flight processBatch calls
}

// NewEmbedWorker creates an embedding worker.
func NewEmbedWorker(input EmbedWorkerInput) *EmbedWorker {
	batchSz := input.BatchSize
	if batchSz <= 0 {
		batchSz = 128
	}
	return &EmbedWorker{
		store:       input.Store,
		embedder:    input.Embedder,
		cb:          NewCircuitBreaker(),
		queue:       make(chan EmbedJob, 1000),
		batchSz:     batchSz,
		logger:      slog.Default().With("component", "embed-worker"),
		onBatchDone: input.OnBatchDone,
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
			// Drain remaining batch with a short timeout
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

// RunFromDB pulls chunks from source using cursor-based pagination, embeds them
// in batches, and marks them as embedded. It is a synchronous, blocking call that
// replaces the channel-based Submit/Run path for bulk embedding.
//
// The cursor never advances past a failed batch — on circuit breaker rejection or
// embed error, the same batch is retried with exponential backoff.
//
// Has() reconciliation: chunks already present in the vector store are skipped
// (marked embedded in DB) but not re-embedded. This handles crash recovery where
// DB says embedded=0 but the vector store loaded vectors from disk.
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

	embedded := 0
	lastID := int64(0)
	activeBatchSz := w.batchSz // adaptive: halves on error, restores on success

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

		// Process page in adaptive batches. Index is advanced manually so
		// that a batch-size shrink re-slices from the same position.
		for i := 0; i < len(page); {
			end := i + activeBatchSz
			if end > len(page) {
				end = len(page)
			}
			batch := page[i:end]

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
				// All chunks already in store — just mark them in DB.
				if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
					return fmt.Errorf("mark already-embedded chunks: %w", err)
				}
				embedded += len(batch)
				if onProgress != nil {
					onProgress(types.EmbedProgress{Embedded: embedded, Total: total})
				}
				i = end
				continue
			}

			// Embed the chunks that need it, with circuit breaker retry.
			texts := make([]string, len(needEmbed))
			needIDs := make([]int64, len(needEmbed))
			for j, job := range needEmbed {
				texts[j] = job.Content
				needIDs[j] = job.ChunkID
			}

			var vectors [][]float32
			const maxRetries = 30
			retries := 0
			shrunk := false // set if batch size shrunk during retries
			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if retries >= maxRetries {
					return fmt.Errorf("embed batch failed after %d retries: %w", retries, ErrCircuitOpen)
				}
				if !w.cb.Allow() {
					w.logger.Debug("circuit breaker open, waiting to retry batch",
						"batch_size", len(needEmbed), "retry", retries)
					retries++
					if onProgress != nil {
						onProgress(types.EmbedProgress{
							Embedded: embedded, Total: total,
							Warning: fmt.Sprintf("Ollama connection issues. Retrying... (attempt %d/%d)", retries, maxRetries),
						})
					}
					select {
					case <-time.After(2 * time.Second):
						continue
					case <-ctx.Done():
						return ctx.Err()
					}
				}

				vectors, err = w.embedder.EmbedBatch(ctx, texts)
				if err != nil {
					w.cb.RecordFailure()
					retries++

					// Adaptive: halve batch size and re-slice from same position.
					if newSz := activeBatchSz / 2; newSz >= 1 && newSz < activeBatchSz {
						w.logger.Info("shrinking batch size after error",
							"from", activeBatchSz, "to", newSz)
						activeBatchSz = newSz
						shrunk = true
						break // break retry loop to re-slice with smaller batch
					}

					w.logger.Warn("embed batch failed, retrying",
						"size", len(needEmbed), "retry", retries, "err", err)
					if onProgress != nil {
						onProgress(types.EmbedProgress{
							Embedded: embedded, Total: total,
							Warning: fmt.Sprintf("Ollama connection issues. Retrying... (attempt %d/%d)", retries, maxRetries),
						})
					}
					select {
					case <-time.After(1 * time.Second):
						continue
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				w.cb.RecordSuccess()

				// Restore original batch size on success.
				if activeBatchSz != w.batchSz {
					w.logger.Info("restoring batch size after success",
						"from", activeBatchSz, "to", w.batchSz)
					activeBatchSz = w.batchSz
				}
				break
			}

			// If batch size shrunk, retry from the same position with smaller batch.
			if shrunk {
				continue
			}

			if err := w.store.UpsertBatch(ctx, needIDs, vectors); err != nil {
				return fmt.Errorf("upsert batch: %w", err)
			}

			if err := source.MarkChunksEmbedded(ctx, batchIDs); err != nil {
				return fmt.Errorf("mark chunks embedded: %w", err)
			}

			embedded += len(batch)
			if onProgress != nil {
				onProgress(types.EmbedProgress{Embedded: embedded, Total: total})
			}
			i = end
		}

		lastID = page[len(page)-1].ChunkID
	}

	return nil
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
		texts[i] = j.Content
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

// ── Query Embedding Cache (LRU) ──

// EmbedCache is an LRU cache for query embeddings.
type EmbedCache struct {
	mu      sync.Mutex
	entries map[string][]float32
	order   []string
	maxSize int
}

// NewEmbedCache creates a cache with the given maximum entry count.
func NewEmbedCache(maxSize int) *EmbedCache {
	return &EmbedCache{
		entries: make(map[string][]float32, maxSize),
		maxSize: maxSize,
	}
}

// Get returns a cached embedding if present. Returns a copy to prevent mutation.
func (c *EmbedCache) Get(query string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vec, ok := c.entries[query]
	if ok {
		c.moveToEnd(query)
		cp := make([]float32, len(vec))
		copy(cp, vec)
		return cp, true
	}
	return nil, false
}

// Put stores a copy of the query embedding in the cache, evicting the oldest if full.
func (c *EmbedCache) Put(query string, vec []float32) {
	cp := make([]float32, len(vec))
	copy(cp, vec)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[query]; exists {
		c.entries[query] = cp
		c.moveToEnd(query)
		return
	}

	if len(c.order) >= c.maxSize {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}

	c.entries[query] = cp
	c.order = append(c.order, query)
}

func (c *EmbedCache) moveToEnd(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}
