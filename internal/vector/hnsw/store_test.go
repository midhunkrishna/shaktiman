package hnsw

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Compile-time interface checks.
var _ types.VectorStore = (*Store)(nil)
var _ types.VectorPersister = (*Store)(nil)

func newTestStore(t *testing.T, dim int) *Store {
	t.Helper()
	s, err := NewStore(StoreInput{Dim: dim})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_UpsertAndCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	assertCount := func(t *testing.T, want int) {
		t.Helper()
		got, err := s.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if got != want {
			t.Fatalf("Count = %d, want %d", got, want)
		}
	}

	assertCount(t, 0)

	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	assertCount(t, 1)

	if err := s.Upsert(ctx, 2, []float32{0, 1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	assertCount(t, 2)

	// Upsert same ID replaces, count stays the same.
	if err := s.Upsert(ctx, 1, []float32{0, 0, 1}); err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}
	assertCount(t, 2)

	if s.Dim() != 3 {
		t.Fatalf("Dim = %d, want 3", s.Dim())
	}
}

func TestStore_Search(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	// Insert three basis vectors.
	vectors := []struct {
		id  int64
		vec []float32
	}{
		{1, []float32{1, 0, 0}},
		{2, []float32{0, 1, 0}},
		{3, []float32{0, 0, 1}},
	}
	for _, v := range vectors {
		if err := s.Upsert(ctx, v.id, v.vec); err != nil {
			t.Fatalf("Upsert(%d): %v", v.id, err)
		}
	}

	// Query along the x-axis: chunk 1 should be the top result with score ~1.0.
	results, err := s.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].ChunkID != 1 {
		t.Errorf("top result ChunkID = %d, want 1", results[0].ChunkID)
	}
	if math.Abs(results[0].Score-1.0) > 0.01 {
		t.Errorf("top result Score = %f, want ~1.0", results[0].Score)
	}
}

func TestStore_Search_TopKLargerThanStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	if err := s.Upsert(ctx, 1, []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestStore_Search_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	results, err := s.Search(ctx, []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Search on empty store: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results, got %v", results)
	}
}

func TestStore_Search_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	_, err := s.Search(ctx, []float32{1, 0}, 1)
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestStore_UpsertBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	ids := []int64{10, 20, 30}
	vecs := [][]float32{
		{1, 0},
		{0, 1},
		{1, 1},
	}

	if err := s.UpsertBatch(ctx, ids, vecs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	got, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
}

func TestStore_UpsertBatch_LenMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	err := s.UpsertBatch(ctx, []int64{1, 2}, [][]float32{{1, 0}})
	if err == nil {
		t.Fatal("expected error for length mismatch, got nil")
	}
}

func TestStore_UpsertBatch_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	err := s.UpsertBatch(ctx, []int64{1}, [][]float32{{1, 0, 0}})
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestStore_Upsert_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	err := s.Upsert(ctx, 1, []float32{1, 0})
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	ids := []int64{1, 2, 3}
	for _, id := range ids {
		if err := s.Upsert(ctx, id, []float32{float32(id), 0}); err != nil {
			t.Fatalf("Upsert(%d): %v", id, err)
		}
	}

	if err := s.Delete(ctx, []int64{1, 3}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Soft delete: hnswlib excludes deleted elements from search.
	// Use topK=1 since only 1 non-deleted element remains.
	results, err := s.Search(ctx, []float32{2, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != 2 {
		t.Fatalf("expected only chunk 2, got %v", results)
	}
}

func TestStore_Delete_NonExistent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	// Deleting IDs that don't exist should not error.
	if err := s.Delete(ctx, []int64{99, 100}); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestStore_Has(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	if has, err := s.Has(ctx, 1); err != nil {
		t.Fatalf("Has: %v", err)
	} else if has {
		t.Fatal("Has(1) should be false on empty store")
	}

	if err := s.Upsert(ctx, 1, []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if has, err := s.Has(ctx, 1); err != nil {
		t.Fatalf("Has: %v", err)
	} else if !has {
		t.Fatal("Has(1) should be true after upsert")
	}
	if has, err := s.Has(ctx, 2); err != nil {
		t.Fatalf("Has: %v", err)
	} else if has {
		t.Fatal("Has(2) should be false")
	}
}

func TestStore_Close(t *testing.T) {
	t.Parallel()
	s, err := NewStore(StoreInput{Dim: 3})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func TestStore_UpsertBatch_NoPartialWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	// Pre-populate with known value
	if err := s.Upsert(ctx, 10, []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Batch with one valid and one invalid dim — should fail
	err := s.UpsertBatch(ctx, []int64{20, 30}, [][]float32{{0, 1}, {1, 2, 3}})
	if err == nil {
		t.Fatal("expected error for dim mismatch in batch")
	}

	// Verify no additional writes: count should still be 1
	got, _ := s.Count(ctx)
	if got != 1 {
		t.Fatalf("Count = %d after failed batch, want 1 (no partial write)", got)
	}
}

func TestStore_Persistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.hnsw")

	s1, err := NewStore(StoreInput{Dim: 3})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s1.Close()

	vectors := []struct {
		id  int64
		vec []float32
	}{
		{1, []float32{1, 2, 3}},
		{2, []float32{4, 5, 6}},
		{3, []float32{7, 8, 9}},
	}
	for _, v := range vectors {
		if err := s1.Upsert(ctx, v.id, v.vec); err != nil {
			t.Fatalf("Upsert(%d): %v", v.id, err)
		}
	}

	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	// Load into a fresh store.
	s2, err := NewStore(StoreInput{Dim: 3})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s2.Close()

	if err := s2.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	got, err := s2.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != len(vectors) {
		t.Fatalf("Count = %d, want %d", got, len(vectors))
	}

	// Verify each vector is retrievable via search.
	for _, v := range vectors {
		results, err := s2.Search(ctx, v.vec, 1)
		if err != nil {
			t.Fatalf("Search for chunk %d: %v", v.id, err)
		}
		if len(results) == 0 {
			t.Fatalf("no results for chunk %d", v.id)
		}
		if results[0].ChunkID != v.id {
			t.Errorf("top result for query %d: ChunkID = %d, want %d", v.id, results[0].ChunkID, v.id)
		}
	}
}

func TestStore_LoadFromDisk_NotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.hnsw")

	s := newTestStore(t, 3)
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk for non-existent file should return nil, got: %v", err)
	}

	// Store should still be empty and usable.
	got, err := s.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestStore_SaveToDisk_EmptyStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.hnsw")

	s1 := newTestStore(t, 3)
	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk empty store: %v", err)
	}

	s2, err := NewStore(StoreInput{Dim: 3})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s2.Close()

	if err := s2.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	got, err := s2.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestStore_SaveToDisk_BadPath(t *testing.T) {
	t.Parallel()
	s := newTestStore(t, 3)
	if err := s.Upsert(context.Background(), 1, []float32{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	err := s.SaveToDisk("/dev/null/sub/index.hnsw")
	if err == nil {
		t.Fatal("expected error for bad path, got nil")
	}
}

// HNSW-specific tests

func TestStore_UpsertReplacesExisting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	// Insert vector aligned with x-axis
	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Also insert a y-axis vector for contrast
	if err := s.Upsert(ctx, 2, []float32{0, 1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Replace chunk 1 with y-axis vector
	if err := s.Upsert(ctx, 1, []float32{0, 1, 0}); err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}

	// Search for y-axis: both chunk 1 and 2 should match well now
	results, err := s.Search(ctx, []float32{0, 1, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Chunk 1 (now y-axis) should be in top results
	found := false
	for _, r := range results {
		if r.ChunkID == 1 && r.Score > 0.9 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected chunk 1 (replaced to y-axis) in top results with high score, got %v", results)
	}
}

func TestStore_ScoreConversion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Upsert(ctx, 2, []float32{0, 1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Query with exact match
	results, err := s.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Exact match should have similarity ~1.0
	if math.Abs(results[0].Score-1.0) > 0.01 {
		t.Errorf("exact match score = %f, want ~1.0", results[0].Score)
	}

	// Orthogonal vector should have similarity ~0.0
	if math.Abs(results[1].Score) > 0.01 {
		t.Errorf("orthogonal score = %f, want ~0.0", results[1].Score)
	}
}

func TestStore_SearchRecall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 32
	s, err := NewStore(StoreInput{Dim: dim, MaxElements: 1100})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	rng := rand.New(rand.NewSource(42))

	// Insert 1000 random vectors
	for i := int64(1); i <= 1000; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		if err := s.Upsert(ctx, i, vec); err != nil {
			t.Fatalf("Upsert(%d): %v", i, err)
		}
	}

	// Insert a known vector
	known := make([]float32, dim)
	for j := range known {
		known[j] = 1.0
	}
	if err := s.Upsert(ctx, 9999, known); err != nil {
		t.Fatalf("Upsert known: %v", err)
	}

	// Search for the known vector — it should be in top-K
	results, err := s.Search(ctx, known, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	found := false
	for _, r := range results {
		if r.ChunkID == 9999 {
			found = true
			break
		}
	}
	if !found {
		t.Error("known vector not found in top-10 results (recall issue)")
	}
}

func TestStore_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 8
	s, err := NewStore(StoreInput{Dim: dim, MaxElements: 1000})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Seed with some data
	for i := int64(1); i <= 10; i++ {
		vec := make([]float32, dim)
		vec[0] = float32(i)
		if err := s.Upsert(ctx, i, vec); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	// Concurrent writers
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 20; i++ {
				id := base*100 + i + 100
				vec := make([]float32, dim)
				vec[0] = float32(id)
				if err := s.Upsert(ctx, id, vec); err != nil {
					errs <- err
				}
			}
		}(int64(w))
	}

	// Concurrent readers
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := make([]float32, dim)
			query[0] = 1.0
			for i := 0; i < 20; i++ {
				if _, err := s.Search(ctx, query, 5); err != nil {
					errs <- err
				}
				if _, err := s.Has(ctx, 1); err != nil {
					errs <- err
				}
				if _, err := s.Count(ctx); err != nil {
					errs <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestStore_CapacityGrowth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 4
	// Start with very small capacity
	s, err := NewStore(StoreInput{Dim: dim, MaxElements: 10})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Insert beyond initial capacity — ensureCapacity should auto-resize
	for i := int64(1); i <= 50; i++ {
		vec := make([]float32, dim)
		vec[0] = float32(i)
		if err := s.Upsert(ctx, i, vec); err != nil {
			t.Fatalf("Upsert(%d): %v", i, err)
		}
	}

	got, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 50 {
		t.Fatalf("Count = %d, want 50", got)
	}
}

func TestStore_UpsertBatch_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 3)

	// Empty batch should be a no-op
	if err := s.UpsertBatch(ctx, nil, nil); err != nil {
		t.Fatalf("UpsertBatch(nil): %v", err)
	}
	if err := s.UpsertBatch(ctx, []int64{}, [][]float32{}); err != nil {
		t.Fatalf("UpsertBatch(empty): %v", err)
	}

	got, _ := s.Count(ctx)
	if got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestStore_Delete_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	// Delete on empty store should not error
	if err := s.Delete(ctx, []int64{}); err != nil {
		t.Fatalf("Delete empty: %v", err)
	}
	if err := s.Delete(ctx, nil); err != nil {
		t.Fatalf("Delete nil: %v", err)
	}
}

func TestStore_SaveToDisk_CreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "index.hnsw")

	s := newTestStore(t, 3)
	if err := s.Upsert(context.Background(), 1, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	if err := s.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk should create nested dirs: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after save: %v", err)
	}
}

func TestStore_NewStore_CustomParams(t *testing.T) {
	t.Parallel()
	s, err := NewStore(StoreInput{
		Dim:            4,
		M:              32,
		EfConstruction: 400,
		MaxElements:    500,
	})
	if err != nil {
		t.Fatalf("NewStore with custom params: %v", err)
	}
	defer s.Close()

	if s.Dim() != 4 {
		t.Errorf("Dim = %d, want 4", s.Dim())
	}
	if s.max != 500 {
		t.Errorf("max = %d, want 500", s.max)
	}
}

func TestStore_Search_AfterClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := NewStore(StoreInput{Dim: 3})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	s.Close()

	// Operations on a closed store should return errors
	_, err = s.Search(ctx, []float32{1, 0, 0}, 1)
	if err == nil {
		t.Error("expected error for Search on closed store, got nil")
	}

	_, err = s.Count(ctx)
	if err == nil {
		t.Error("expected error for Count on closed store, got nil")
	}
}

func TestStore_Search_SoftDeleteFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t, 2)

	// Insert 3 vectors, delete all 3, then search.
	// This triggers the "Cannot return the results" error path.
	for i := int64(1); i <= 3; i++ {
		if err := s.Upsert(ctx, i, []float32{float32(i), 0}); err != nil {
			t.Fatalf("Upsert(%d): %v", i, err)
		}
	}
	for i := int64(1); i <= 3; i++ {
		if err := s.Delete(ctx, []int64{i}); err != nil {
			t.Fatalf("Delete(%d): %v", i, err)
		}
	}

	// Search should return nil (graceful fallback), not error
	results, err := s.Search(ctx, []float32{1, 0}, 3)
	if err != nil {
		t.Fatalf("Search after all deleted: expected nil error, got %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results after all deleted, got %v", results)
	}
}

func TestStore_LoadFromDisk_CorruptFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.hnsw")

	// Write garbage to simulate a corrupt index file
	if err := os.WriteFile(path, []byte("this is not an hnsw index"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := newTestStore(t, 3)
	err := s.LoadFromDisk(path)
	if err == nil {
		t.Fatal("expected error for corrupt file, got nil")
	}

	// Store should still be usable after failed load (recovery path)
	if err := s.Upsert(context.Background(), 1, []float32{1, 0, 0}); err != nil {
		t.Fatalf("Upsert after failed load should work: %v", err)
	}
	got, _ := s.Count(context.Background())
	if got != 1 {
		t.Fatalf("Count = %d, want 1 after recovery", got)
	}
}

func TestStore_LoadFromDisk_UpdatesMax(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "dense.hnsw")

	// Create store with small capacity, fill it, save
	s1, err := NewStore(StoreInput{Dim: 3, MaxElements: 50})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for i := int64(1); i <= 50; i++ {
		if err := s1.Upsert(ctx, i, []float32{float32(i), 0, 0}); err != nil {
			t.Fatalf("Upsert(%d): %v", i, err)
		}
	}
	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}
	s1.Close()

	// Load into store with smaller initial max — hnswlib will expand to fit
	s2, err := NewStore(StoreInput{Dim: 3, MaxElements: 10})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s2.Close()

	if err := s2.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	// max should be updated to match the loaded index's capacity (>= 50)
	if s2.max < 50 {
		t.Errorf("max after load = %d, expected >= 50", s2.max)
	}

	// Verify all 50 elements are present
	got, _ := s2.Count(ctx)
	if got != 50 {
		t.Errorf("Count = %d after load, want 50", got)
	}
}

func TestStore_EnsureCapacity_LargeNeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dim := 4
	// Capacity = 5, insert 5, then batch insert 20 more.
	// This triggers the newMax < count+needed branch (doubling isn't enough).
	s, err := NewStore(StoreInput{Dim: dim, MaxElements: 5})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	// Fill to capacity
	for i := int64(1); i <= 5; i++ {
		vec := make([]float32, dim)
		vec[0] = float32(i)
		if err := s.Upsert(ctx, i, vec); err != nil {
			t.Fatalf("Upsert(%d): %v", i, err)
		}
	}

	// Batch insert 20 at once — doubling from 5→10 isn't enough, needs 25
	ids := make([]int64, 20)
	vecs := make([][]float32, 20)
	for i := 0; i < 20; i++ {
		ids[i] = int64(100 + i)
		vecs[i] = make([]float32, dim)
		vecs[i][0] = float32(100 + i)
	}

	if err := s.UpsertBatch(ctx, ids, vecs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	got, _ := s.Count(ctx)
	if got != 25 {
		t.Fatalf("Count = %d, want 25", got)
	}
}

func TestStore_SaveToDisk_RenameFail(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, 3)
	if err := s.Upsert(context.Background(), 1, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	// Save to a path where the dir exists but the final rename target is a directory,
	// causing os.Rename to fail.
	dir := t.TempDir()
	target := filepath.Join(dir, "index.hnsw")
	// Create target as a non-empty directory — rename file→dir fails
	if err := os.MkdirAll(filepath.Join(target, "blocker"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := s.SaveToDisk(target)
	if err == nil {
		t.Fatal("expected error when rename target is a directory, got nil")
	}

	// Temp file should be cleaned up
	tmpPath := target + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file should have been cleaned up, stat err: %v", err)
	}
}
