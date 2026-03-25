//go:build sqlite_fts5

package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newMockOllamaServerB is the benchmark variant of newMockOllamaServer.
func newMockOllamaServerB(b *testing.B, failCount *atomic.Int32, dims int) *httptest.Server {
	b.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
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
	b.Cleanup(srv.Close)
	return srv
}

// newBenchWorker builds an EmbedWorker for benchmarks using an httptest.Server.
func newBenchWorker(b *testing.B, srv *httptest.Server, store *BruteForceStore, batchSize int) *EmbedWorker {
	b.Helper()
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

// BenchmarkRunFromDB_Throughput measures end-to-end embedding throughput with a
// mock Ollama server. Target: >=100 batches/sec.
func BenchmarkRunFromDB_Throughput(b *testing.B) {
	const (
		dims      = 4
		totalJobs = 10_000
		batchSize = 32
	)

	jobs := makeJobs(totalJobs)
	srv := newMockOllamaServerB(b, nil, dims)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		source := newMockEmbedSource(jobs, totalJobs)
		store := NewBruteForceStore(dims)
		worker := newBenchWorker(b, srv, store, batchSize)

		if err := worker.RunFromDB(context.Background(), source, nil); err != nil {
			b.Fatalf("RunFromDB: %v", err)
		}
	}

	b.StopTimer()
	totalBatches := float64(b.N) * float64((totalJobs+batchSize-1)/batchSize)
	b.ReportMetric(totalBatches/b.Elapsed().Seconds(), "batches/sec")
}

// BenchmarkRunFromDB_Memory measures memory allocation under a 50K chunk load.
// Target: <10MB peak heap.
func BenchmarkRunFromDB_Memory(b *testing.B) {
	const (
		dims      = 4
		totalJobs = 50_000
		batchSize = 32
	)

	jobs := makeJobs(totalJobs)
	srv := newMockOllamaServerB(b, nil, dims)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		source := newMockEmbedSource(jobs, totalJobs)
		store := NewBruteForceStore(dims)
		worker := newBenchWorker(b, srv, store, batchSize)

		if err := worker.RunFromDB(context.Background(), source, nil); err != nil {
			b.Fatalf("RunFromDB: %v", err)
		}
	}
}
