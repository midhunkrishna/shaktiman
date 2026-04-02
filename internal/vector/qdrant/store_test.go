package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// fakeQdrant simulates a Qdrant server in-memory for testing.
type fakeQdrant struct {
	mu          sync.Mutex
	collections map[string]*fakeCollection
}

type fakeCollection struct {
	dims   int
	points map[int64][]float32
}

func newFakeQdrant() *fakeQdrant {
	return &fakeQdrant{
		collections: make(map[string]*fakeCollection),
	}
}

func (fq *fakeQdrant) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fq.mu.Lock()
		defer fq.mu.Unlock()

		// Route based on method + path
		path := r.URL.Path
		switch {
		case r.Method == "GET" && path == "/":
			w.Write([]byte(`{"title":"qdrant","version":"1.8.0"}`))

		case r.Method == "PUT" && len(path) > len("/collections/") && !contains(path, "/points"):
			name := path[len("/collections/"):]
			var body CollectionConfig
			json.NewDecoder(r.Body).Decode(&body)
			fq.collections[name] = &fakeCollection{
				dims:   body.Vectors.Size,
				points: make(map[int64][]float32),
			}
			writeOK(w, true)

		case r.Method == "GET" && len(path) > len("/collections/") && !contains(path, "/points"):
			name := path[len("/collections/"):]
			col, ok := fq.collections[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"status":{"error":"not found"}}`))
				return
			}
			writeOK(w, CollectionInfo{
				Status:       "green",
				PointsCount:  len(col.points),
				VectorsCount: len(col.points),
				Config: CollectionInfoConfig{
					Params: CollectionInfoParams{
						Vectors: VectorsConfig{Size: col.dims, Distance: "Cosine"},
					},
				},
			})

		case r.Method == "PUT" && contains(path, "/points"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			if col == nil {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"status":{"error":"collection not found"}}`))
				return
			}
			var body struct {
				Points []Point `json:"points"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			for _, p := range body.Points {
				col.points[p.ID] = p.Vector
			}
			writeOK(w, struct{ Status string }{Status: "completed"})

		case r.Method == "POST" && contains(path, "/points/search"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			if col == nil {
				writeOK(w, []SearchResult{})
				return
			}
			var req SearchRequest
			json.NewDecoder(r.Body).Decode(&req)
			results := fq.searchCollection(col, req.Vector, req.Limit)
			writeOK(w, results)

		case r.Method == "POST" && contains(path, "/points/delete"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			if col == nil {
				writeOK(w, struct{ Status string }{Status: "completed"})
				return
			}
			var body struct {
				Points []int64 `json:"points"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			for _, id := range body.Points {
				delete(col.points, id)
			}
			writeOK(w, struct{ Status string }{Status: "completed"})

		case r.Method == "POST" && contains(path, "/points/count"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			count := 0
			if col != nil {
				count = len(col.points)
			}
			writeOK(w, struct{ Count int `json:"count"` }{Count: count})

		case r.Method == "POST" && contains(path, "/points/scroll"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			var resp ScrollResponse
			if col != nil {
				for id := range col.points {
					resp.Points = append(resp.Points, ScrollPoint{ID: id})
				}
			}
			writeOK(w, resp)

		case r.Method == "POST" && contains(path, "/points") && !contains(path, "/points/"):
			// GetPoints
			colName := extractCollection(path)
			col := fq.collections[colName]
			var body struct {
				IDs []int64 `json:"ids"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			var points []ScrollPoint
			if col != nil {
				for _, id := range body.IDs {
					if _, ok := col.points[id]; ok {
						points = append(points, ScrollPoint{ID: id})
					}
				}
			}
			writeOK(w, points)

		default:
			t.Logf("unhandled: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	})
}

func (fq *fakeQdrant) searchCollection(col *fakeCollection, query []float32, limit int) []SearchResult {
	type scored struct {
		id    int64
		score float64
	}
	var all []scored
	for id, vec := range col.points {
		s := cosine(query, vec)
		all = append(all, scored{id, s})
	}
	// Sort descending by score (simple bubble sort for small N)
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].score > all[i].score {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if limit > len(all) {
		limit = len(all)
	}
	results := make([]SearchResult, limit)
	for i := 0; i < limit; i++ {
		results[i] = SearchResult{ID: all[i].id, Score: all[i].score}
	}
	return results
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt(na) * sqrt(nb))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub) >= 0
}

func searchStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func extractCollection(path string) string {
	// /collections/{name}/points... -> name
	const prefix = "/collections/"
	rest := path[len(prefix):]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return rest
}

func writeOK(w http.ResponseWriter, result any) {
	data, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiResponse{Status: "ok", Result: data})
}

// newTestStore creates a QdrantStore backed by a fake Qdrant server.
func newTestStore(t *testing.T, dims int) (*QdrantStore, *httptest.Server) {
	t.Helper()
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	client := NewClient(srv.URL, "")
	store, err := NewQdrantStore(client, "test", dims)
	if err != nil {
		srv.Close()
		t.Fatalf("NewQdrantStore: %v", err)
	}
	return store, srv
}

// ── Store tests ──

func TestQdrantStore_Upsert_And_Count(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	if err := store.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
}

func TestQdrantStore_UpsertBatch(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	err := store.UpsertBatch(ctx,
		[]int64{1, 2, 3},
		[][]float32{
			{0.1, 0.2, 0.3, 0.4},
			{0.5, 0.6, 0.7, 0.8},
			{0.9, 0.8, 0.7, 0.6},
		})
	if err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	count, _ := store.Count(ctx)
	if count != 3 {
		t.Errorf("Count = %d, want 3", count)
	}
}

func TestQdrantStore_UpsertBatch_LengthMismatch(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	err := store.UpsertBatch(context.Background(),
		[]int64{1, 2},
		[][]float32{{0.1, 0.2, 0.3, 0.4}})
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestQdrantStore_UpsertBatch_Chunking(t *testing.T) {
	store, srv := newTestStore(t, 2)
	defer srv.Close()
	defer store.Close()

	// Insert more than maxBatchSize to test chunking
	n := maxBatchSize + 10
	ids := make([]int64, n)
	vecs := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = int64(i + 1)
		vecs[i] = []float32{float32(i) / float32(n), 0.5}
	}

	ctx := context.Background()
	if err := store.UpsertBatch(ctx, ids, vecs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	count, _ := store.Count(ctx)
	if count != n {
		t.Errorf("Count = %d, want %d", count, n)
	}
}

func TestQdrantStore_Search(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	store.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	store.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	store.Upsert(ctx, 3, []float32{0.9, 0.1, 0, 0})

	results, err := store.Search(ctx, []float32{1, 0, 0, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	// Top result should be chunk 1 (identical vector)
	if results[0].ChunkID != 1 {
		t.Errorf("top result = %d, want 1", results[0].ChunkID)
	}
}

func TestQdrantStore_Search_TopKZero(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	results, err := store.Search(context.Background(), []float32{1, 0, 0, 0}, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for topK=0, got %v", results)
	}
}

func TestQdrantStore_Search_EmptyStore(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	results, err := store.Search(context.Background(), []float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQdrantStore_Has(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	store.Upsert(ctx, 42, []float32{0.1, 0.2, 0.3, 0.4})

	has, err := store.Has(ctx, 42)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !has {
		t.Error("expected Has(42) = true")
	}

	has, _ = store.Has(ctx, 999)
	if has {
		t.Error("expected Has(999) = false")
	}
}

func TestQdrantStore_Delete(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	store.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4})
	store.Upsert(ctx, 2, []float32{0.5, 0.6, 0.7, 0.8})

	if err := store.Delete(ctx, []int64{1}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	has, _ := store.Has(ctx, 1)
	if has {
		t.Error("expected Has(1) = false after delete")
	}
	has, _ = store.Has(ctx, 2)
	if !has {
		t.Error("expected Has(2) = true")
	}
}

func TestQdrantStore_Delete_Empty(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	// Deleting with empty slice should be a no-op
	if err := store.Delete(context.Background(), nil); err != nil {
		t.Fatalf("Delete nil: %v", err)
	}
	if err := store.Delete(context.Background(), []int64{}); err != nil {
		t.Fatalf("Delete empty: %v", err)
	}
}

func TestQdrantStore_Delete_Chunking(t *testing.T) {
	store, srv := newTestStore(t, 2)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	// Insert maxBatchSize+5 items, then delete them all
	n := maxBatchSize + 5
	ids := make([]int64, n)
	vecs := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = int64(i + 1)
		vecs[i] = []float32{0.1, 0.2}
	}
	store.UpsertBatch(ctx, ids, vecs)

	if err := store.Delete(ctx, ids); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	count, _ := store.Count(ctx)
	if count != 0 {
		t.Errorf("Count after delete = %d, want 0", count)
	}
}

func TestQdrantStore_Healthy(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()

	if !store.Healthy(context.Background()) {
		t.Error("expected Healthy = true")
	}
}

func TestQdrantStore_Healthy_Down(t *testing.T) {
	store, srv := newTestStore(t, 4)
	srv.Close() // shut down server

	if store.Healthy(context.Background()) {
		t.Error("expected Healthy = false after server shutdown")
	}
}

func TestQdrantStore_Close_Idempotent(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()

	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestQdrantStore_CompileTimeCheck(t *testing.T) {
	// Verify QdrantStore satisfies VectorStore at compile time
	var _ types.VectorStore = (*QdrantStore)(nil)
}

func TestNewQdrantStore_ExistingCollection_DimsMismatch(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	// Create collection with dims=4
	client.CreateCollection(context.Background(), "test", 4)

	// Try to open with dims=8 — should fail
	_, err := NewQdrantStore(client, "test", 8)
	if err == nil {
		t.Fatal("expected error for dims mismatch")
	}
}

func TestNewQdrantStore_CreatesCollection(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	store, err := NewQdrantStore(client, "new_col", 128)
	if err != nil {
		t.Fatalf("NewQdrantStore: %v", err)
	}
	defer store.Close()

	// Verify collection was created
	info, err := client.GetCollection(context.Background(), "new_col")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if info.Config.Params.Vectors.Size != 128 {
		t.Errorf("dims = %d, want 128", info.Config.Params.Vectors.Size)
	}
}

func TestNewQdrantStore_ExistingCollection_DimsMatch(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	client.CreateCollection(context.Background(), "existing", 64)

	store, err := NewQdrantStore(client, "existing", 64)
	if err != nil {
		t.Fatalf("NewQdrantStore: %v", err)
	}
	store.Close()
}

func TestQdrantStore_Upsert_Overwrites(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	store.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4})
	store.Upsert(ctx, 1, []float32{0.5, 0.6, 0.7, 0.8})

	count, _ := store.Count(ctx)
	if count != 1 {
		t.Errorf("Count after overwrite = %d, want 1", count)
	}
}

func TestQdrantStore_Search_ScoreOrder(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	store.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	store.Upsert(ctx, 2, []float32{0.9, 0.1, 0, 0})
	store.Upsert(ctx, 3, []float32{0, 0, 0, 1})

	results, err := store.Search(ctx, []float32{1, 0, 0, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Results should be in descending score order
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score+1e-6 {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}
