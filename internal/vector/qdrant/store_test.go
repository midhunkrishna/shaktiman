package qdrant

import (
	"context"
	"encoding/json"
	"math"
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

type fakePoint struct {
	vector  []float32
	payload map[string]any
}

type fakeCollection struct {
	dims   int
	points map[int64]fakePoint
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

		case r.Method == "PUT" && len(path) > len("/collections/") && !contains(path, "/points") && !contains(path, "/index"):
			name := path[len("/collections/"):]
			var body CollectionConfig
			json.NewDecoder(r.Body).Decode(&body)
			fq.collections[name] = &fakeCollection{
				dims:   body.Vectors.Size,
				points: make(map[int64]fakePoint),
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

		case r.Method == "PUT" && contains(path, "/index"):
			// CreatePayloadIndex — no-op in fake server.
			writeOK(w, struct{ Status string }{Status: "completed"})

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
				col.points[p.ID] = fakePoint{vector: p.Vector, payload: p.Payload}
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
			results := fq.searchCollection(col, req.Vector, req.Limit, req.Filter)
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
				Filter *Filter `json:"filter"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Filter != nil {
				for id, pt := range col.points {
					if matchesFilter(pt, body.Filter) {
						delete(col.points, id)
					}
				}
			} else {
				for _, id := range body.Points {
					delete(col.points, id)
				}
			}
			writeOK(w, struct{ Status string }{Status: "completed"})

		case r.Method == "POST" && contains(path, "/points/count"):
			colName := extractCollection(path)
			col := fq.collections[colName]
			count := 0
			if col != nil {
				var body struct {
					Filter *Filter `json:"filter"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				for _, pt := range col.points {
					if matchesFilter(pt, body.Filter) {
						count++
					}
				}
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

		case r.Method == "DELETE" && len(path) > len("/collections/") && !contains(path, "/points"):
			name := path[len("/collections/"):]
			delete(fq.collections, name)
			writeOK(w, true)

		default:
			t.Logf("unhandled: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	})
}

// matchesFilter returns true if the point satisfies all must conditions.
// A nil filter matches everything.
func matchesFilter(pt fakePoint, filter *Filter) bool {
	if filter == nil {
		return true
	}
	for _, cond := range filter.Must {
		if cond.Key != "" && cond.Match != nil {
			val, ok := pt.payload[cond.Key]
			if !ok {
				return false
			}
			if !valuesEqual(val, cond.Match.Value) {
				return false
			}
		}
	}
	return true
}

// valuesEqual compares values handling JSON number type coercion.
// JSON numbers decode as float64; int64 values from Go code need matching.
func valuesEqual(a, b any) bool {
	af, aIsFloat := toFloat64(a)
	bf, bIsFloat := toFloat64(b)
	if aIsFloat && bIsFloat {
		return math.Abs(af-bf) < 1e-9
	}
	return a == b
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (fq *fakeQdrant) searchCollection(col *fakeCollection, query []float32, limit int, filter *Filter) []SearchResult {
	type scored struct {
		id    int64
		score float64
	}
	var all []scored
	for id, pt := range col.points {
		if !matchesFilter(pt, filter) {
			continue
		}
		s := cosine(query, pt.vector)
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

// newTestStore creates a Store backed by a fake Qdrant server (unscoped).
func newTestStore(t *testing.T, dims int) (*Store, *httptest.Server) {
	return newTestStoreWithProject(t, dims, 0)
}

// newTestStoreWithProject creates a Store with a specific projectID.
func newTestStoreWithProject(t *testing.T, dims int, projectID int64) (*Store, *httptest.Server) {
	t.Helper()
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	client := NewClient(srv.URL, "")
	store, err := NewStore(client, "test", dims, projectID)
	if err != nil {
		srv.Close()
		t.Fatalf("NewStore: %v", err)
	}
	return store, srv
}

// newTestStoreOnServer creates a Store sharing an existing fake server.
func newTestStoreOnServer(t *testing.T, srv *httptest.Server, collection string, dims int, projectID int64) *Store {
	t.Helper()
	client := NewClient(srv.URL, "")
	store, err := NewStore(client, collection, dims, projectID)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// ── Store tests ──

func TestStore_Upsert_And_Count(t *testing.T) {
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

func TestStore_UpsertBatch(t *testing.T) {
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

func TestStore_UpsertBatch_LengthMismatch(t *testing.T) {
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

func TestStore_UpsertBatch_Chunking(t *testing.T) {
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

func TestStore_Search(t *testing.T) {
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

func TestStore_Search_TopKZero(t *testing.T) {
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

func TestStore_Search_EmptyStore(t *testing.T) {
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

func TestStore_Has(t *testing.T) {
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

func TestStore_Delete(t *testing.T) {
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

func TestStore_Delete_Empty(t *testing.T) {
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

func TestStore_Delete_Chunking(t *testing.T) {
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

func TestStore_Healthy(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()

	if !store.Healthy(context.Background()) {
		t.Error("expected Healthy = true")
	}
}

func TestStore_Healthy_Down(t *testing.T) {
	store, srv := newTestStore(t, 4)
	srv.Close() // shut down server

	if store.Healthy(context.Background()) {
		t.Error("expected Healthy = false after server shutdown")
	}
}

func TestStore_Close_Idempotent(t *testing.T) {
	store, srv := newTestStore(t, 4)
	defer srv.Close()

	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStore_CompileTimeCheck(t *testing.T) {
	// Verify Store satisfies VectorStore at compile time
	var _ types.VectorStore = (*Store)(nil)
}

func TestNewStore_ExistingCollection_DimsMismatch(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	// Create collection with dims=4
	client.CreateCollection(context.Background(), "test", 4)

	// Try to open with dims=8 — should fail
	_, err := NewStore(client, "test", 8, 0)
	if err == nil {
		t.Fatal("expected error for dims mismatch")
	}
}

func TestNewStore_CreatesCollection(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	store, err := NewStore(client, "new_col", 128, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
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

func TestNewStore_ExistingCollection_DimsMatch(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	client.CreateCollection(context.Background(), "existing", 64)

	store, err := NewStore(client, "existing", 64, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.Close()
}

func TestStore_Upsert_Overwrites(t *testing.T) {
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

func TestStore_Search_ScoreOrder(t *testing.T) {
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

func TestStore_PurgeAll_Unscoped(t *testing.T) {
	store, srv := newTestStore(t, 4) // projectID=0
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()

	store.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	store.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	store.Upsert(ctx, 3, []float32{0, 0, 1, 0})

	count, _ := store.Count(ctx)
	if count != 3 {
		t.Fatalf("pre-purge count = %d, want 3", count)
	}

	if err := store.PurgeAll(ctx); err != nil {
		t.Fatalf("PurgeAll: %v", err)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count after purge: %v", err)
	}
	if count != 0 {
		t.Errorf("post-purge count = %d, want 0", count)
	}

	// Should still be usable (collection was recreated)
	if err := store.Upsert(ctx, 10, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert after purge: %v", err)
	}
	count, _ = store.Count(ctx)
	if count != 1 {
		t.Errorf("count after re-upsert = %d, want 1", count)
	}
}

// ── Project isolation tests ──

func TestStore_SearchIsolation(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	storeA := newTestStoreOnServer(t, srv, "shared", 4, 1)
	defer storeA.Close()
	storeB := newTestStoreOnServer(t, srv, "shared", 4, 2)
	defer storeB.Close()

	ctx := context.Background()

	// Project A vectors
	storeA.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	storeA.Upsert(ctx, 2, []float32{0.9, 0.1, 0, 0})

	// Project B vectors
	storeB.Upsert(ctx, 3, []float32{0, 1, 0, 0})
	storeB.Upsert(ctx, 4, []float32{0, 0.9, 0.1, 0})

	// Search from A should only return A's vectors
	results, err := storeA.Search(ctx, []float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Search A: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search A returned %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.ChunkID != 1 && r.ChunkID != 2 {
			t.Errorf("Search A returned unexpected chunk %d", r.ChunkID)
		}
	}

	// Search from B should only return B's vectors
	results, err = storeB.Search(ctx, []float32{0, 1, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Search B: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search B returned %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.ChunkID != 3 && r.ChunkID != 4 {
			t.Errorf("Search B returned unexpected chunk %d", r.ChunkID)
		}
	}
}

func TestStore_CountIsolation(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	storeA := newTestStoreOnServer(t, srv, "shared", 4, 1)
	defer storeA.Close()
	storeB := newTestStoreOnServer(t, srv, "shared", 4, 2)
	defer storeB.Close()

	ctx := context.Background()

	storeA.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	storeA.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	storeB.Upsert(ctx, 3, []float32{0, 0, 1, 0})

	countA, _ := storeA.Count(ctx)
	if countA != 2 {
		t.Errorf("Count A = %d, want 2", countA)
	}

	countB, _ := storeB.Count(ctx)
	if countB != 1 {
		t.Errorf("Count B = %d, want 1", countB)
	}
}

func TestStore_PurgeAll_Isolation(t *testing.T) {
	fq := newFakeQdrant()
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()

	storeA := newTestStoreOnServer(t, srv, "shared", 4, 1)
	defer storeA.Close()
	storeB := newTestStoreOnServer(t, srv, "shared", 4, 2)
	defer storeB.Close()

	ctx := context.Background()

	storeA.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	storeA.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	storeB.Upsert(ctx, 3, []float32{0, 0, 1, 0})
	storeB.Upsert(ctx, 4, []float32{0, 0, 0, 1})

	// Purge A
	if err := storeA.PurgeAll(ctx); err != nil {
		t.Fatalf("PurgeAll A: %v", err)
	}

	// A should be empty
	countA, _ := storeA.Count(ctx)
	if countA != 0 {
		t.Errorf("Count A after purge = %d, want 0", countA)
	}

	// B should be untouched
	countB, _ := storeB.Count(ctx)
	if countB != 2 {
		t.Errorf("Count B after purge A = %d, want 2", countB)
	}

	// A should still be usable
	if err := storeA.Upsert(ctx, 10, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert after purge: %v", err)
	}
	countA, _ = storeA.Count(ctx)
	if countA != 1 {
		t.Errorf("Count A after re-upsert = %d, want 1", countA)
	}
}
