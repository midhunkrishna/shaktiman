package vector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOllamaClient_Embed_Success(t *testing.T) {
	t.Parallel()
	const dims = 4

	srv := newMockOllamaServer(t, nil, dims)
	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test",
		Timeout: 5 * time.Second,
	})

	vec, err := client.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != dims {
		t.Fatalf("len(vec) = %d, want %d", len(vec), dims)
	}
}

func TestOllamaClient_Embed_ServerError(t *testing.T) {
	t.Parallel()
	const dims = 4

	failCount := &atomic.Int32{}
	failCount.Store(100) // always fail
	srv := newMockOllamaServer(t, failCount, dims)
	client := NewOllamaClient(OllamaClientInput{
		BaseURL: srv.URL,
		Model:   "test",
		Timeout: 5 * time.Second,
	})

	_, err := client.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from failing server, got nil")
	}
}

func TestOllamaClient_Embed_Unreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	client := NewOllamaClient(OllamaClientInput{
		BaseURL: url,
		Model:   "test",
		Timeout: 1 * time.Second,
	})

	_, err := client.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from unreachable server, got nil")
	}
}

func TestEmbedWorker_Accessors(t *testing.T) {
	t.Parallel()

	srv := newMockOllamaServer(t, nil, 4)
	store := NewBruteForceStore(4)
	w := newTestWorker(t, srv, store, 32)

	if w.CircuitBreaker() == nil {
		t.Fatal("CircuitBreaker() returned nil")
	}
	if w.Pending() != 0 {
		t.Fatalf("Pending() = %d, want 0", w.Pending())
	}
	if !w.EmbedReady() {
		t.Fatal("EmbedReady() = false, want true (CB closed)")
	}
}

func TestEmbedWorker_EmbedReady_CircuitOpen(t *testing.T) {
	t.Parallel()

	srv := newMockOllamaServer(t, nil, 4)
	store := NewBruteForceStore(4)
	w := newTestWorker(t, srv, store, 32)

	// Trip the circuit breaker
	cb := w.CircuitBreaker()
	for i := 0; i < cb.failThreshold; i++ {
		cb.RecordFailure()
	}

	if w.EmbedReady() {
		t.Fatal("EmbedReady() = true, want false (CB open)")
	}

	cb.Reset()
	if !w.EmbedReady() {
		t.Fatal("EmbedReady() = false after reset, want true")
	}
}

func TestEmbedWorker_Submit(t *testing.T) {
	t.Parallel()

	srv := newMockOllamaServer(t, nil, 4)
	store := NewBruteForceStore(4)
	w := newTestWorker(t, srv, store, 32)

	ok := w.Submit(EmbedJob{ChunkID: 1, Content: "hello"})
	if !ok {
		t.Fatal("Submit returned false for empty queue")
	}
	if w.Pending() != 1 {
		t.Fatalf("Pending() = %d, want 1", w.Pending())
	}
}

func TestEmbedWorker_Submit_QueueFull(t *testing.T) {
	t.Parallel()

	srv := newMockOllamaServer(t, nil, 4)
	store := NewBruteForceStore(4)
	w := newTestWorker(t, srv, store, 32)

	// Fill the queue (cap is 1000)
	for i := 0; i < 1000; i++ {
		w.Submit(EmbedJob{ChunkID: int64(i), Content: "x"})
	}

	ok := w.Submit(EmbedJob{ChunkID: 9999, Content: "overflow"})
	if ok {
		t.Fatal("Submit returned true for full queue")
	}
}

func TestEmbedWorker_SubmitBatch(t *testing.T) {
	t.Parallel()

	srv := newMockOllamaServer(t, nil, 4)
	store := NewBruteForceStore(4)
	w := newTestWorker(t, srv, store, 32)

	jobs := []EmbedJob{
		{ChunkID: 1, Content: "a"},
		{ChunkID: 2, Content: "b"},
		{ChunkID: 3, Content: "c"},
	}
	n := w.SubmitBatch(jobs)
	if n != 3 {
		t.Fatalf("SubmitBatch returned %d, want 3", n)
	}
	if w.Pending() != 3 {
		t.Fatalf("Pending() = %d, want 3", w.Pending())
	}
}

func TestProcessBatch_Success(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllamaServer(t, nil, dims)
	store := NewBruteForceStore(dims)
	w := newTestWorker(t, srv, store, 32)

	var doneIDs []int64
	w.onBatchDone = func(ids []int64) {
		doneIDs = append(doneIDs, ids...)
	}

	batch := []EmbedJob{
		{ChunkID: 10, Content: "hello"},
		{ChunkID: 20, Content: "world"},
	}
	w.processBatch(context.Background(), batch)

	// Verify vectors upserted
	ctx := context.Background()
	for _, j := range batch {
		has, err := store.Has(ctx, j.ChunkID)
		if err != nil {
			t.Fatalf("Has(%d): %v", j.ChunkID, err)
		}
		if !has {
			t.Errorf("store missing chunk %d after processBatch", j.ChunkID)
		}
	}

	// Verify onBatchDone callback
	if len(doneIDs) != 2 {
		t.Fatalf("onBatchDone called with %d IDs, want 2", len(doneIDs))
	}
}

func TestProcessBatch_CircuitOpen(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllamaServer(t, nil, dims)
	store := NewBruteForceStore(dims)
	w := newTestWorker(t, srv, store, 32)

	// Trip the circuit breaker
	cb := w.CircuitBreaker()
	for i := 0; i < cb.failThreshold; i++ {
		cb.RecordFailure()
	}

	batch := []EmbedJob{{ChunkID: 1, Content: "test"}}
	w.processBatch(context.Background(), batch)

	// Should not have upserted anything
	count, _ := store.Count(context.Background())
	if count != 0 {
		t.Fatalf("store count = %d, want 0 (CB open should skip batch)", count)
	}
}

func TestEmbedWorker_RunAndWaitIdle(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllamaServer(t, nil, dims)
	store := NewBruteForceStore(dims)
	// Use batchSz=2 so batch fills quickly without waiting for ticker
	w := newTestWorker(t, srv, store, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Run(ctx)

	// Submit jobs in batches of 2 so they process immediately
	w.Submit(EmbedJob{ChunkID: 1, Content: "a"})
	w.Submit(EmbedJob{ChunkID: 2, Content: "b"})
	w.Submit(EmbedJob{ChunkID: 3, Content: "c"})
	w.Submit(EmbedJob{ChunkID: 4, Content: "d"})

	w.WaitIdle()

	// Verify all vectors in store
	for _, id := range []int64{1, 2, 3, 4} {
		has, err := store.Has(context.Background(), id)
		if err != nil {
			t.Fatalf("Has(%d): %v", id, err)
		}
		if !has {
			t.Errorf("store missing chunk %d after Run+WaitIdle", id)
		}
	}

	cancel()
}

func TestEmbedWorker_Run_ContextCancel(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllamaServer(t, nil, dims)
	store := NewBruteForceStore(dims)
	w := newTestWorker(t, srv, store, 100) // large batch so ticker path is used

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Submit one job, then cancel — exercises the context-cancel drain path
	w.Submit(EmbedJob{ChunkID: 1, Content: "drain-test"})
	time.Sleep(10 * time.Millisecond) // let Run pick up the job
	cancel()

	select {
	case <-done:
		// good, Run exited
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}
