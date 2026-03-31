package vector

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Compile-time interface checks.
var _ types.VectorStore = (*BruteForceStore)(nil)
var _ types.VectorPersister = (*BruteForceStore)(nil)

func TestBruteForceStore_UpsertAndCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)

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

func TestBruteForceStore_Search(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)

	// Insert three basis vectors.
	vectors := map[int64][]float32{
		1: {1, 0, 0},
		2: {0, 1, 0},
		3: {0, 0, 1},
	}
	for id, v := range vectors {
		if err := s.Upsert(ctx, id, v); err != nil {
			t.Fatalf("Upsert(%d): %v", id, err)
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
	if math.Abs(results[0].Score-1.0) > 1e-6 {
		t.Errorf("top result Score = %f, want 1.0", results[0].Score)
	}
	// The other two basis vectors are orthogonal, score ~0.
	if math.Abs(results[1].Score) > 1e-6 {
		t.Errorf("second result Score = %f, want ~0", results[1].Score)
	}
}

func TestBruteForceStore_Search_TopKLargerThanStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

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

func TestBruteForceStore_Search_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)

	results, err := s.Search(ctx, []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Search on empty store: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results, got %v", results)
	}
}

func TestBruteForceStore_Search_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)

	_, err := s.Search(ctx, []float32{1, 0}, 1)
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestBruteForceStore_UpsertBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

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

func TestBruteForceStore_UpsertBatch_LenMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

	err := s.UpsertBatch(ctx, []int64{1, 2}, [][]float32{{1, 0}})
	if err == nil {
		t.Fatal("expected error for length mismatch, got nil")
	}
}

func TestBruteForceStore_UpsertBatch_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

	err := s.UpsertBatch(ctx, []int64{1}, [][]float32{{1, 0, 0}})
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestBruteForceStore_Upsert_DimMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)

	err := s.Upsert(ctx, 1, []float32{1, 0})
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

func TestBruteForceStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

	ids := []int64{1, 2, 3}
	for _, id := range ids {
		if err := s.Upsert(ctx, id, []float32{float32(id), 0}); err != nil {
			t.Fatalf("Upsert(%d): %v", id, err)
		}
	}

	if err := s.Delete(ctx, []int64{1, 3}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := s.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 1 {
		t.Fatalf("Count after delete = %d, want 1", got)
	}

	// Verify the remaining vector is searchable and is chunk 2.
	results, err := s.Search(ctx, []float32{2, 0}, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ChunkID != 2 {
		t.Fatalf("expected only chunk 2, got %v", results)
	}
}

func TestBruteForceStore_Delete_NonExistent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

	// Deleting IDs that don't exist should not error.
	if err := s.Delete(ctx, []int64{99, 100}); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestBruteForceStore_Persistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings.bin")

	s1 := NewBruteForceStore(3)
	vectors := map[int64][]float32{
		1: {1, 2, 3},
		2: {4, 5, 6},
		3: {7, 8, 9},
	}
	for id, v := range vectors {
		if err := s1.Upsert(ctx, id, v); err != nil {
			t.Fatalf("Upsert(%d): %v", id, err)
		}
	}

	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	// Load into a fresh store.
	s2 := NewBruteForceStore(0) // dim will be overwritten by LoadFromDisk
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
	if s2.Dim() != 3 {
		t.Fatalf("Dim = %d, want 3", s2.Dim())
	}

	// Verify each vector is preserved.
	for id, want := range vectors {
		results, err := s2.Search(ctx, want, 1)
		if err != nil {
			t.Fatalf("Search for chunk %d: %v", id, err)
		}
		if len(results) == 0 {
			t.Fatalf("no results for chunk %d", id)
		}
		if results[0].ChunkID != id {
			t.Errorf("top result for query %d: ChunkID = %d, want %d", id, results[0].ChunkID, id)
		}
		if math.Abs(results[0].Score-1.0) > 1e-6 {
			t.Errorf("top result for query %d: Score = %f, want 1.0", id, results[0].Score)
		}
	}
}

func TestBruteForceStore_LoadFromDisk_NotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.bin")

	s := NewBruteForceStore(3)
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

func TestBruteForceStore_Has(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

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

	if err := s.Delete(ctx, []int64{1}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if has, err := s.Has(ctx, 1); err != nil {
		t.Fatalf("Has: %v", err)
	} else if has {
		t.Fatal("Has(1) should be false after delete")
	}
}

func TestBruteForceStore_LoadFromDisk_BoundsValidation(t *testing.T) {
	t.Parallel()

	t.Run("rejects oversized dim", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "bad_dim.bin")

		writeBadHeader(t, path, 1, maxDim+1, 0)

		s := NewBruteForceStore(0)
		err := s.LoadFromDisk(path)
		if err == nil {
			t.Fatal("expected error for oversized dim")
		}
	})

	t.Run("rejects oversized count", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "bad_count.bin")

		writeBadHeader(t, path, 1, 3, maxVectorCount+1)

		s := NewBruteForceStore(0)
		err := s.LoadFromDisk(path)
		if err == nil {
			t.Fatal("expected error for oversized count")
		}
	})

	t.Run("rejects dim mismatch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "mismatch.bin")

		writeBadHeader(t, path, 1, 768, 0)

		s := NewBruteForceStore(384) // expects 384, file says 768
		err := s.LoadFromDisk(path)
		if err == nil {
			t.Fatal("expected error for dim mismatch")
		}
	})
}

func TestBruteForceStore_UpsertBatch_NoPartialWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(2)

	// Pre-populate with known value
	if err := s.Upsert(ctx, 10, []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Batch with one valid and one invalid dim — should fail atomically
	err := s.UpsertBatch(ctx, []int64{20, 30}, [][]float32{{0, 1}, {1, 2, 3}})
	if err == nil {
		t.Fatal("expected error for dim mismatch in batch")
	}

	// Verify no partial writes: count should still be 1
	got, _ := s.Count(ctx)
	if got != 1 {
		t.Fatalf("Count = %d after failed batch, want 1 (no partial write)", got)
	}
}

func TestBruteForceStore_Persistence_CRC32(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "embeddings_v2.bin")

	s1 := NewBruteForceStore(2)
	if err := s1.Upsert(ctx, 1, []float32{1, 0}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s1.Upsert(ctx, 2, []float32{0, 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	// Corrupt a byte in the middle of the file
	corruptPath := filepath.Join(dir, "corrupted.bin")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	corrupted := make([]byte, len(data))
	copy(corrupted, data)
	// Flip a byte in the entries area (after 16-byte header)
	if len(corrupted) > 20 {
		corrupted[20] ^= 0xFF
	}
	if err := os.WriteFile(corruptPath, corrupted, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s2 := NewBruteForceStore(0)
	err = s2.LoadFromDisk(corruptPath)
	if err == nil {
		t.Fatal("expected CRC32 mismatch error for corrupted file")
	}
}

// writeBadHeader writes a minimal persistence file with the given header values (v1 format, no entries).
func writeBadHeader(t *testing.T, path string, version, dim, count uint32) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer f.Close()

	// Write magic
	f.Write([]byte{'E', 'M', 'B', 'V'})
	// Write version, dim, count as little-endian uint32
	for _, v := range []uint32{version, dim, count} {
		b := [4]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
		f.Write(b[:])
	}
}

func TestBruteForceStore_Close(t *testing.T) {
	t.Parallel()
	s := NewBruteForceStore(3)
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func TestBruteForceStore_SaveToDisk_EmptyStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")

	s1 := NewBruteForceStore(3)
	if err := s1.SaveToDisk(path); err != nil {
		t.Fatalf("SaveToDisk empty store: %v", err)
	}

	s2 := NewBruteForceStore(3)
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

func TestBruteForceStore_SaveToDisk_BadPath(t *testing.T) {
	t.Parallel()
	s := NewBruteForceStore(3)
	if err := s.Upsert(context.Background(), 1, []float32{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	// /dev/null is a file, not a directory — MkdirAll will fail
	err := s.SaveToDisk("/dev/null/sub/embeddings.bin")
	if err == nil {
		t.Fatal("expected error for bad path, got nil")
	}
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float64
		tol  float64
	}{
		{
			name: "identical",
			a:    []float32{1, 2, 3},
			b:    []float32{1, 2, 3},
			want: 1.0,
			tol:  1e-9,
		},
		{
			name: "opposite",
			a:    []float32{1, 2, 3},
			b:    []float32{-1, -2, -3},
			want: -1.0,
			tol:  1e-9,
		},
		{
			name: "orthogonal",
			a:    []float32{1, 0, 0},
			b:    []float32{0, 1, 0},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "45_degrees",
			a:    []float32{1, 0},
			b:    []float32{1, 1},
			want: 1.0 / math.Sqrt(2),
			tol:  1e-9,
		},
		{
			name: "zero_vector_a",
			a:    []float32{0, 0, 0},
			b:    []float32{1, 2, 3},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "zero_vector_b",
			a:    []float32{1, 2, 3},
			b:    []float32{0, 0, 0},
			want: 0.0,
			tol:  1e-9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > tt.tol {
				t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineSimilarity_HighDim(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))
	a := make([]float32, 768)
	b := make([]float32, 768)
	for i := range a {
		a[i] = rng.Float32()*2 - 1
		b[i] = rng.Float32()*2 - 1
	}

	// Reference: compute in float64 for ground truth
	var dot, nA, nB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		nA += ai * ai
		nB += bi * bi
	}
	ref := dot / (math.Sqrt(nA) * math.Sqrt(nB))

	got := cosineSimilarity(a, b)
	if math.Abs(got-ref) > 1e-4 {
		t.Errorf("cosineSimilarity(768-dim): got %f, ref %f, diff %e", got, ref, math.Abs(got-ref))
	}
}

func TestBruteForceStore_Search_TopKZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)
	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	results, err := s.Search(ctx, []float32{1, 0, 0}, 0)
	if err != nil {
		t.Fatalf("Search(topK=0): %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil, got %d results", len(results))
	}
}

func TestBruteForceStore_Search_TopKNegative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewBruteForceStore(3)
	if err := s.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	results, err := s.Search(ctx, []float32{1, 0, 0}, -1)
	if err != nil {
		t.Fatalf("Search(topK=-1): %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil, got %d results", len(results))
	}
}
