package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── Circuit Breaker Tests ──

func TestCircuitBreaker_InitialState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
		wantAllow bool
	}{
		{"starts closed and allows", StateClosed, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() = %d, want %d", got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() = %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

func TestCircuitBreaker_TransitionToOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failures  int
		wantState CircuitState
		wantAllow bool
	}{
		{"below threshold stays closed", 2, StateClosed, true},
		{"at threshold transitions to open", 3, StateOpen, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			for i := 0; i < tc.failures; i++ {
				cb.RecordFailure()
			}
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after %d failures = %d, want %d", tc.failures, got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after %d failures = %v, want %v", tc.failures, got, tc.wantAllow)
			}
		})
	}
}

func TestCircuitBreaker_HalfOpenProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
		wantAllow bool
	}{
		{"transitions to half-open after cooldown", StateHalfOpen, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			cb.baseCooldown = time.Millisecond
			cb.currentCooldown = time.Millisecond

			// Trip to Open
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if got := cb.State(); got != StateOpen {
				t.Fatalf("expected StateOpen before cooldown, got %d", got)
			}

			time.Sleep(5 * time.Millisecond) // exceed cooldown

			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after cooldown = %v, want %v", got, tc.wantAllow)
			}
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after cooldown Allow() = %d, want %d", got, tc.wantState)
			}
		})
	}
}

func TestCircuitBreaker_RecoveryOnSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
	}{
		{"success in half-open recovers to closed", StateClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			cb.baseCooldown = time.Millisecond
			cb.currentCooldown = time.Millisecond

			// Trip to Open
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}

			time.Sleep(5 * time.Millisecond)
			cb.Allow() // transitions Open -> HalfOpen

			if got := cb.State(); got != StateHalfOpen {
				t.Fatalf("expected StateHalfOpen, got %d", got)
			}

			cb.RecordSuccess()

			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after success in half-open = %d, want %d", got, tc.wantState)
			}
			if !cb.Allow() {
				t.Fatal("Allow() should be true after recovery")
			}
		})
	}
}

func TestCircuitBreaker_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	t.Run("cooldown doubles on each open cycle", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 100 * time.Millisecond

		// Cycle 1: trip to Open → cooldown should double to 2ms
		for i := 0; i < cb.failThreshold; i++ {
			cb.RecordFailure()
		}
		if got := cb.State(); got != StateOpen {
			t.Fatalf("cycle 1: State() = %d, want StateOpen", got)
		}
		if cb.currentCooldown != 2*time.Millisecond {
			t.Fatalf("cycle 1: cooldown = %v, want 2ms", cb.currentCooldown)
		}

		// Wait for cooldown, probe and fail again → cycle 2, cooldown 4ms
		time.Sleep(5 * time.Millisecond)
		cb.Allow() // Open → HalfOpen
		for i := 0; i < cb.failThreshold; i++ {
			cb.RecordFailure()
		}
		if cb.currentCooldown != 4*time.Millisecond {
			t.Fatalf("cycle 2: cooldown = %v, want 4ms", cb.currentCooldown)
		}

		// Still StateOpen, NOT StateDisabled — always recoverable
		if got := cb.State(); got != StateOpen {
			t.Fatalf("cycle 2: State() = %d, want StateOpen (not disabled)", got)
		}
	})

	t.Run("cooldown capped at backoffMax", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 4 * time.Millisecond

		// Trip 5 times — cooldown: 2, 4, 4, 4, 4 (capped)
		for cycle := 0; cycle < 5; cycle++ {
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if cycle < 4 {
				time.Sleep(10 * time.Millisecond)
				cb.Allow()
			}
		}
		if cb.currentCooldown != 4*time.Millisecond {
			t.Fatalf("cooldown = %v, want cap at 4ms", cb.currentCooldown)
		}
	})

	t.Run("still recoverable after many cycles", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 4 * time.Millisecond

		// Trip 5 times
		for cycle := 0; cycle < 5; cycle++ {
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if cycle < 4 {
				time.Sleep(10 * time.Millisecond)
				cb.Allow()
			}
		}

		// Wait for cooldown — should still allow probe
		time.Sleep(10 * time.Millisecond)
		if !cb.Allow() {
			t.Fatal("Allow() should be true after cooldown (never permanently disabled)")
		}
		if got := cb.State(); got != StateHalfOpen {
			t.Fatalf("State() = %d, want StateHalfOpen", got)
		}

		// Success resets everything
		cb.RecordSuccess()
		if got := cb.State(); got != StateClosed {
			t.Fatalf("State() after success = %d, want StateClosed", got)
		}
		if cb.currentCooldown != cb.baseCooldown {
			t.Fatalf("cooldown not reset: %v, want %v", cb.currentCooldown, cb.baseCooldown)
		}
	})
}

func TestCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupState func(cb *CircuitBreaker)
		wantState  CircuitState
		wantAllow  bool
	}{
		{
			"reset from open",
			func(cb *CircuitBreaker) {
				for i := 0; i < cb.failThreshold; i++ {
					cb.RecordFailure()
				}
			},
			StateClosed,
			true,
		},
		{
			"reset from open with escalated backoff",
			func(cb *CircuitBreaker) {
				// Trip multiple cycles to escalate backoff
				for cycle := 0; cycle < 5; cycle++ {
					for i := 0; i < cb.failThreshold; i++ {
						cb.RecordFailure()
					}
				}
			},
			StateClosed,
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			tc.setupState(cb)
			cb.Reset()

			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after Reset() = %d, want %d", got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after Reset() = %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

// ── Embed Cache Tests ──

func TestEmbedCache_PutAndGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		vec     []float32
		wantHit bool
	}{
		{"store and retrieve", "hello", []float32{1.0, 2.0, 3.0}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(8)
			cache.Put(tc.key, tc.vec)

			got, ok := cache.Get(tc.key)
			if ok != tc.wantHit {
				t.Fatalf("Get(%q) hit = %v, want %v", tc.key, ok, tc.wantHit)
			}
			if len(got) != len(tc.vec) {
				t.Fatalf("Get(%q) len = %d, want %d", tc.key, len(got), len(tc.vec))
			}
			for i := range tc.vec {
				if got[i] != tc.vec[i] {
					t.Fatalf("Get(%q)[%d] = %f, want %f", tc.key, i, got[i], tc.vec[i])
				}
			}
		})
	}
}

func TestEmbedCache_Eviction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		maxSize    int
		keys       []string
		evictedKey string
		survivorKey string
	}{
		{
			"oldest evicted when full",
			2,
			[]string{"a", "b", "c"},
			"a",
			"c",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(tc.maxSize)
			for _, k := range tc.keys {
				cache.Put(k, []float32{1.0})
			}

			if _, ok := cache.Get(tc.evictedKey); ok {
				t.Fatalf("Get(%q) should miss after eviction", tc.evictedKey)
			}
			if _, ok := cache.Get(tc.survivorKey); !ok {
				t.Fatalf("Get(%q) should hit, was not evicted", tc.survivorKey)
			}
		})
	}
}

func TestEmbedCache_LRUOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxSize     int
		puts        []string
		accessKey   string // key to access (move to end) before final put
		finalPut    string
		wantEvicted string
		wantPresent string
	}{
		{
			"recently accessed survives eviction",
			2,
			[]string{"a", "b"},
			"a",      // access "a" to make it most recent
			"c",      // insert "c", should evict "b" (now oldest)
			"b",
			"a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(tc.maxSize)
			for _, k := range tc.puts {
				cache.Put(k, []float32{1.0})
			}

			// Access to promote in LRU order
			cache.Get(tc.accessKey)

			// Insert one more to trigger eviction
			cache.Put(tc.finalPut, []float32{2.0})

			if _, ok := cache.Get(tc.wantEvicted); ok {
				t.Fatalf("Get(%q) should miss, expected eviction", tc.wantEvicted)
			}
			if _, ok := cache.Get(tc.wantPresent); !ok {
				t.Fatalf("Get(%q) should hit, recently accessed", tc.wantPresent)
			}
		})
	}
}

func TestEmbedCache_Miss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
	}{
		{"nonexistent key returns miss", "nonexistent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(4)

			vec, ok := cache.Get(tc.key)
			if ok {
				t.Fatalf("Get(%q) should return false for nonexistent key", tc.key)
			}
			if vec != nil {
				t.Fatalf("Get(%q) vec should be nil on miss, got %v", tc.key, vec)
			}
		})
	}
}

func TestEmbedCache_SliceIsolation(t *testing.T) {
	t.Parallel()

	t.Run("Put copies input slice", func(t *testing.T) {
		t.Parallel()
		cache := NewEmbedCache(4)
		vec := []float32{1.0, 2.0, 3.0}
		cache.Put("key", vec)

		// Mutate the original slice
		vec[0] = 999.0

		// Cache should be unaffected
		got, ok := cache.Get("key")
		if !ok {
			t.Fatal("expected cache hit")
		}
		if got[0] != 1.0 {
			t.Fatalf("cache corrupted: got[0] = %f, want 1.0", got[0])
		}
	})

	t.Run("Get returns independent copy", func(t *testing.T) {
		t.Parallel()
		cache := NewEmbedCache(4)
		cache.Put("key", []float32{1.0, 2.0, 3.0})

		got1, _ := cache.Get("key")
		got1[0] = 999.0

		// Second Get should return original value
		got2, _ := cache.Get("key")
		if got2[0] != 1.0 {
			t.Fatalf("cache corrupted via Get mutation: got2[0] = %f, want 1.0", got2[0])
		}
	})
}

func TestCircuitBreaker_HalfOpenSingleProbe(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker()
	cb.baseCooldown = time.Millisecond
	cb.currentCooldown = time.Millisecond

	// Trip to Open
	for i := 0; i < cb.failThreshold; i++ {
		cb.RecordFailure()
	}

	time.Sleep(5 * time.Millisecond)

	// First Allow: transitions to HalfOpen, allows probe
	if !cb.Allow() {
		t.Fatal("first Allow() after cooldown should return true")
	}
	if got := cb.State(); got != StateHalfOpen {
		t.Fatalf("State() = %d, want StateHalfOpen", got)
	}

	// Second Allow while probe in flight: should reject
	if cb.Allow() {
		t.Fatal("second Allow() in HalfOpen should return false (probe in flight)")
	}

	// RecordFailure clears probe flag, trips back to Open
	cb.RecordFailure()
	if got := cb.State(); got != StateOpen {
		t.Fatalf("State() after failure in HalfOpen = %d, want StateOpen", got)
	}
}

// ── RunFromDB Test Helpers ──

// mockEmbedSource implements types.EmbedSource with cursor-based pagination.
type mockEmbedSource struct {
	mu         sync.Mutex
	jobs       []types.EmbedJob // all jobs, sorted by ChunkID ascending
	marked     []int64          // all chunk IDs passed to MarkChunksEmbedded
	totalCount int              // value returned by CountChunksNeedingEmbedding
}

func newMockEmbedSource(jobs []types.EmbedJob, totalCount int) *mockEmbedSource {
	sorted := make([]types.EmbedJob, len(jobs))
	copy(sorted, jobs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ChunkID < sorted[j].ChunkID })
	return &mockEmbedSource{
		jobs:       sorted,
		totalCount: totalCount,
	}
}

func (m *mockEmbedSource) CountChunksNeedingEmbedding(_ context.Context) (int, error) {
	return m.totalCount, nil
}

func (m *mockEmbedSource) GetEmbedPage(_ context.Context, afterID int64, limit int) ([]types.EmbedJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var page []types.EmbedJob
	for _, j := range m.jobs {
		if j.ChunkID > afterID {
			page = append(page, j)
			if len(page) >= limit {
				break
			}
		}
	}
	return page, nil
}

func (m *mockEmbedSource) MarkChunksEmbedded(_ context.Context, chunkIDs []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marked = append(m.marked, chunkIDs...)
	return nil
}

func (m *mockEmbedSource) getMarked() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]int64, len(m.marked))
	copy(cp, m.marked)
	return cp
}

// newMockOllamaServer creates an httptest.Server mimicking the Ollama /api/embed
// endpoint. failCount controls how many initial requests return HTTP 500 before
// responding with valid embeddings. dims sets the dimensionality of returned vectors.
func newMockOllamaServer(t *testing.T, failCount *atomic.Int32, dims int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// Health check endpoint
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		if failCount != nil && failCount.Add(-1) >= 0 {
			http.Error(w, "simulated failure", http.StatusInternalServerError)
			return
		}
		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		switch v := req.Input.(type) {
		case string:
			inputs = []string{v}
		case []interface{}:
			for _, s := range v {
				inputs = append(inputs, fmt.Sprintf("%v", s))
			}
		}
		embeddings := make([][]float32, len(inputs))
		for i := range inputs {
			vec := make([]float32, dims)
			for d := range vec {
				vec[d] = float32(i+1) * 0.1
			}
			embeddings[i] = vec
		}
		resp := ollamaEmbedResponse{Embeddings: embeddings}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makeJobs creates n EmbedJob entries with sequential ChunkIDs starting at 1.
func makeJobs(n int) []types.EmbedJob {
	jobs := make([]types.EmbedJob, n)
	for i := range jobs {
		jobs[i] = types.EmbedJob{
			ChunkID: int64(i + 1),
			Content: fmt.Sprintf("content-%d", i+1),
		}
	}
	return jobs
}

// newTestWorker builds an EmbedWorker pointing at the given mock server.
func newTestWorker(t *testing.T, srv *httptest.Server, store *BruteForceStore, batchSize int) *EmbedWorker {
	t.Helper()
	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test-model",
		Timeout: 5 * time.Second,
	})
	return NewEmbedWorker(EmbedWorkerInput{
		Store:     store,
		Embedder:  client,
		BatchSize: batchSize,
	})
}

// ── RunFromDB Tests ──

func TestRunFromDB_AllChunksEmbedded(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 128 // 2 pages of 64 at pageSize 256, but we use batchSz to control batching

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)
	srv := newMockOllamaServer(t, nil, dims)
	worker := newTestWorker(t, srv, store, 32)

	var progressCalls []types.EmbedProgress
	var mu sync.Mutex
	onProgress := func(p types.EmbedProgress) {
		mu.Lock()
		progressCalls = append(progressCalls, p)
		mu.Unlock()
	}

	err := worker.RunFromDB(context.Background(), source, onProgress)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	// Verify all chunks are in the vector store.
	for _, job := range jobs {
		if has, _ := store.Has(context.Background(), job.ChunkID); !has {
			t.Errorf("store missing chunk %d", job.ChunkID)
		}
	}

	// Verify all chunks were marked embedded.
	marked := source.getMarked()
	markedSet := make(map[int64]bool, len(marked))
	for _, id := range marked {
		markedSet[id] = true
	}
	for _, job := range jobs {
		if !markedSet[job.ChunkID] {
			t.Errorf("chunk %d not marked as embedded", job.ChunkID)
		}
	}

	// Verify progress callback was invoked with correct final counts.
	mu.Lock()
	defer mu.Unlock()
	if len(progressCalls) == 0 {
		t.Fatal("progress callback never called")
	}
	last := progressCalls[len(progressCalls)-1]
	if last.Embedded != totalJobs {
		t.Errorf("final progress Embedded = %d, want %d", last.Embedded, totalJobs)
	}
	if last.Total != totalJobs {
		t.Errorf("final progress Total = %d, want %d", last.Total, totalJobs)
	}
	// Progress should be monotonically increasing.
	for i := 1; i < len(progressCalls); i++ {
		if progressCalls[i].Embedded < progressCalls[i-1].Embedded {
			t.Errorf("progress not monotonic: %d then %d",
				progressCalls[i-1].Embedded, progressCalls[i].Embedded)
		}
	}
}

func TestRunFromDB_CircuitBreakerRetry(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 8 // small to keep test fast

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)

	// Server fails first 3 requests, then succeeds.
	failCount := &atomic.Int32{}
	failCount.Store(3)
	srv := newMockOllamaServer(t, failCount, dims)
	worker := newTestWorker(t, srv, store, 32)

	// Reduce circuit breaker cooldown so retry doesn't take 5 minutes.
	worker.cb.baseCooldown = time.Millisecond
	worker.cb.currentCooldown = time.Millisecond

	err := worker.RunFromDB(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	// Verify all chunks embedded despite initial failures.
	for _, job := range jobs {
		if has, _ := store.Has(context.Background(), job.ChunkID); !has {
			t.Errorf("store missing chunk %d after CB retry", job.ChunkID)
		}
	}

	// Verify cursor did not skip: all chunks marked.
	marked := source.getMarked()
	markedSet := make(map[int64]bool, len(marked))
	for _, id := range marked {
		markedSet[id] = true
	}
	for _, job := range jobs {
		if !markedSet[job.ChunkID] {
			t.Errorf("chunk %d not marked, cursor may have skipped batch", job.ChunkID)
		}
	}
}

func TestRunFromDB_ContextCancellation(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 512 // large enough so cancellation hits mid-run

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)
	srv := newMockOllamaServer(t, nil, dims)
	worker := newTestWorker(t, srv, store, 16) // small batch to increase iterations

	ctx, cancel := context.WithCancel(context.Background())

	var progressMu sync.Mutex
	var progressCalls []types.EmbedProgress
	onProgress := func(p types.EmbedProgress) {
		progressMu.Lock()
		progressCalls = append(progressCalls, p)
		progressMu.Unlock()
		// Cancel after some progress but before completion.
		if p.Embedded >= 64 {
			cancel()
		}
	}

	err := worker.RunFromDB(ctx, source, onProgress)

	// Must return a context error.
	if err == nil {
		t.Fatal("RunFromDB should return error on context cancellation")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	// Verify partial progress: some but not all chunks embedded.
	count, _ := store.Count(context.Background())
	if count == 0 {
		t.Error("expected some chunks embedded before cancellation")
	}
	if count >= totalJobs {
		t.Error("expected partial embedding, but all chunks were embedded")
	}
}

func TestRunFromDB_EmptySource(t *testing.T) {
	t.Parallel()

	const dims = 4

	source := newMockEmbedSource(nil, 0)
	store := NewBruteForceStore(dims)
	srv := newMockOllamaServer(t, nil, dims)
	worker := newTestWorker(t, srv, store, 32)

	var gotProgress types.EmbedProgress
	progressCalled := false
	onProgress := func(p types.EmbedProgress) {
		gotProgress = p
		progressCalled = true
	}

	err := worker.RunFromDB(context.Background(), source, onProgress)
	if err != nil {
		t.Fatalf("RunFromDB returned error for empty source: %v", err)
	}

	if !progressCalled {
		t.Fatal("progress callback not called for empty source")
	}
	if gotProgress.Embedded != 0 || gotProgress.Total != 0 {
		t.Errorf("progress = {Embedded:%d, Total:%d}, want {0, 0}",
			gotProgress.Embedded, gotProgress.Total)
	}

	count, _ := store.Count(context.Background())
	if count != 0 {
		t.Errorf("store count = %d, want 0", count)
	}
}

func TestRunFromDB_HasReconciliation(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 64

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)

	// Pre-populate store with vectors for the first half of chunks.
	// This simulates crash recovery: DB says embedded=0, but vector store
	// already has the vectors from a previous persistence load.
	prePopulated := totalJobs / 2
	for i := 0; i < prePopulated; i++ {
		vec := make([]float32, dims)
		for d := range vec {
			vec[d] = 0.5
		}
		if err := store.Upsert(context.Background(), jobs[i].ChunkID, vec); err != nil {
			t.Fatalf("pre-populate store: %v", err)
		}
	}

	// Track how many texts the embedder actually receives.
	var embedCallTexts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		switch v := req.Input.(type) {
		case string:
			inputs = []string{v}
		case []interface{}:
			for _, s := range v {
				inputs = append(inputs, fmt.Sprintf("%v", s))
			}
		}
		embedCallTexts.Add(int64(len(inputs)))
		embeddings := make([][]float32, len(inputs))
		for i := range inputs {
			vec := make([]float32, dims)
			for d := range vec {
				vec[d] = float32(i+1) * 0.1
			}
			embeddings[i] = vec
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	t.Cleanup(srv.Close)

	worker := newTestWorker(t, srv, store, 32)

	err := worker.RunFromDB(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	// All chunks should now be in the store.
	for _, job := range jobs {
		if has, _ := store.Has(context.Background(), job.ChunkID); !has {
			t.Errorf("store missing chunk %d", job.ChunkID)
		}
	}

	// All chunks should be marked embedded in the source.
	marked := source.getMarked()
	markedSet := make(map[int64]bool, len(marked))
	for _, id := range marked {
		markedSet[id] = true
	}
	for _, job := range jobs {
		if !markedSet[job.ChunkID] {
			t.Errorf("chunk %d not marked as embedded", job.ChunkID)
		}
	}

	// The embedder should have received fewer texts than totalJobs because
	// pre-populated chunks are skipped via Has() reconciliation.
	actualEmbedded := int(embedCallTexts.Load())
	expectedMax := totalJobs - prePopulated
	if actualEmbedded > expectedMax {
		t.Errorf("embedder received %d texts, want <= %d (reconciliation failed)",
			actualEmbedded, expectedMax)
	}
	if actualEmbedded == 0 {
		t.Error("embedder received 0 texts, expected some for non-prepopulated chunks")
	}
}

// ── Adaptive Batch Sizing Tests ──

func TestRunFromDB_AdaptiveBatchShrink(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 128
	const maxAccepted = 32 // server rejects batches larger than this

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)

	// Server returns 500 for batches > maxAccepted, 200 otherwise.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		switch v := req.Input.(type) {
		case string:
			inputs = []string{v}
		case []interface{}:
			for _, s := range v {
				inputs = append(inputs, fmt.Sprintf("%v", s))
			}
		}
		if len(inputs) > maxAccepted {
			http.Error(w, "batch too large", http.StatusInternalServerError)
			return
		}
		embeddings := make([][]float32, len(inputs))
		for i := range embeddings {
			vec := make([]float32, dims)
			for d := range vec {
				vec[d] = float32(i+1) * 0.1
			}
			embeddings[i] = vec
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	t.Cleanup(srv.Close)

	// Worker starts at batchSize=128, should shrink to <= 32.
	worker := newTestWorker(t, srv, store, 128)
	worker.cb.baseCooldown = time.Millisecond
	worker.cb.currentCooldown = time.Millisecond

	err := worker.RunFromDB(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	// All chunks should be embedded despite batch shrinking.
	count, _ := store.Count(context.Background())
	if count != totalJobs {
		t.Errorf("store count = %d, want %d", count, totalJobs)
	}
}

func TestRunFromDB_AdaptiveBatchRestore(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 64

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)

	// Server fails first 3 requests, then succeeds.
	failCount := &atomic.Int32{}
	failCount.Store(3)
	srv := newMockOllamaServer(t, failCount, dims)
	worker := newTestWorker(t, srv, store, 64)
	worker.cb.baseCooldown = time.Millisecond
	worker.cb.currentCooldown = time.Millisecond

	err := worker.RunFromDB(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	count, _ := store.Count(context.Background())
	if count != totalJobs {
		t.Errorf("store count = %d, want %d", count, totalJobs)
	}
}

func TestRunFromDB_LargeBatchSuccess(t *testing.T) {
	t.Parallel()

	const dims = 4
	const totalJobs = 512

	jobs := makeJobs(totalJobs)
	source := newMockEmbedSource(jobs, totalJobs)
	store := NewBruteForceStore(dims)

	var embedCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		embedCalls.Add(1)
		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		switch v := req.Input.(type) {
		case string:
			inputs = []string{v}
		case []interface{}:
			for _, s := range v {
				inputs = append(inputs, fmt.Sprintf("%v", s))
			}
		}
		embeddings := make([][]float32, len(inputs))
		for i := range embeddings {
			vec := make([]float32, dims)
			for d := range vec {
				vec[d] = float32(i+1) * 0.1
			}
			embeddings[i] = vec
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	t.Cleanup(srv.Close)

	worker := newTestWorker(t, srv, store, 128)

	err := worker.RunFromDB(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("RunFromDB returned error: %v", err)
	}

	count, _ := store.Count(context.Background())
	if count != totalJobs {
		t.Errorf("store count = %d, want %d", count, totalJobs)
	}

	// With batch size 128 and 512 jobs, we expect ~4 embed calls (512/128).
	// With batch size 32 it would be ~16. Verify fewer calls.
	calls := embedCalls.Load()
	if calls > 8 {
		t.Errorf("embed API calls = %d, expected <= 8 for batch size 128", calls)
	}
	t.Logf("embed API calls: %d (512 chunks at batch size 128)", calls)
}

// ── Context-Length Safety Tests ──

func TestEmbedBatch_TruncateFieldSent(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		// Parse and respond with correct number of embeddings.
		var req struct {
			Input []string `json:"input"`
		}
		json.Unmarshal(capturedBody, &req)
		embeddings := make([][]float32, len(req.Input))
		for i := range embeddings {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	t.Cleanup(srv.Close)

	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test",
		Timeout: 5 * time.Second,
	})

	_, err := client.EmbedBatch(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	// Verify the request body contains "truncate":true.
	var parsed map[string]any
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	truncate, ok := parsed["truncate"]
	if !ok {
		t.Fatal("request body missing 'truncate' field")
	}
	if truncate != true {
		t.Errorf("truncate = %v, want true", truncate)
	}
}

func TestEmbedBatch_OversizedInputHandled(t *testing.T) {
	t.Parallel()

	const dims = 4

	// Create a text larger than maxSafeEmbedChars.
	oversized := make([]byte, maxSafeEmbedChars+1000)
	for i := range oversized {
		oversized[i] = 'a'
	}

	srv := newMockOllamaServer(t, nil, dims)
	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test",
		Timeout: 5 * time.Second,
	})

	// Should succeed (server truncates) — no error returned.
	vecs, err := client.EmbedBatch(context.Background(), []string{string(oversized)})
	if err != nil {
		t.Fatalf("EmbedBatch with oversized input: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(vecs))
	}
	if len(vecs[0]) != dims {
		t.Fatalf("expected %d dims, got %d", dims, len(vecs[0]))
	}
}

func TestEmbedBatch_NormalInputSucceeds(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllamaServer(t, nil, dims)
	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test",
		Timeout: 5 * time.Second,
	})

	vecs, err := client.EmbedBatch(context.Background(), []string{"short text"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != dims {
		t.Fatalf("unexpected result: %d vecs, %d dims", len(vecs), len(vecs[0]))
	}
}

// ── EmbedderHealthy Tests ──

func TestEmbedderHealthy_Reachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := NewBruteForceStore(4)
	worker := newTestWorker(t, srv, store, 32)

	if !worker.EmbedderHealthy(context.Background()) {
		t.Error("expected EmbedderHealthy to return true for reachable server")
	}
}

func TestEmbedderHealthy_Unreachable(t *testing.T) {
	t.Parallel()

	// Create and immediately close server to get an unreachable URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	client := NewOllamaClient(OllamaClientInput{
		BaseURL: url,
		Model:   "test-model",
		Timeout: 1 * time.Second,
	})
	store := NewBruteForceStore(4)
	worker := NewEmbedWorker(EmbedWorkerInput{
		Store:    store,
		Embedder: client,
	})

	if worker.EmbedderHealthy(context.Background()) {
		t.Error("expected EmbedderHealthy to return false for unreachable server")
	}
}

// ── EmbedProgress Warning Tests ──

func TestEmbedProgress_Warning(t *testing.T) {
	t.Parallel()

	const dims = 4

	// Server that always fails — forces circuit breaker retries.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "fail", http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := NewBruteForceStore(dims)
	worker := newTestWorker(t, srv, store, 32)

	jobs := makeJobs(5)
	source := newMockEmbedSource(jobs, 5)

	var warnings []string
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = worker.RunFromDB(ctx, source, func(p types.EmbedProgress) {
		if p.Warning != "" {
			mu.Lock()
			warnings = append(warnings, p.Warning)
			mu.Unlock()
		}
	})

	mu.Lock()
	defer mu.Unlock()
	if len(warnings) == 0 {
		t.Error("expected at least one warning from progress callback during retries")
	}
	for _, w := range warnings {
		if len(w) == 0 {
			t.Error("warning string should not be empty")
		}
	}
}
