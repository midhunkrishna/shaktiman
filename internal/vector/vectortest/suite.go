// Package vectortest provides compliance test suites for VectorStore
// implementations. Any backend (BruteForce, HNSW, Qdrant, pgvector)
// must pass these tests to be considered a valid implementation.
package vectortest

import (
	"context"
	"math"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// StoreFactory creates a fresh VectorStore for each test.
// Dims specifies the vector dimensionality to use.
type StoreFactory func(t *testing.T, dims int) types.VectorStore

// RunVectorStoreTests runs the full compliance suite against a VectorStore.
func RunVectorStoreTests(t *testing.T, factory StoreFactory) {
	const dims = 4

	t.Run("Upsert_And_Count", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		if err := vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		count, err := vs.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if count != 1 {
			t.Errorf("Count = %d, want 1", count)
		}
	})

	t.Run("Upsert_Overwrites", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4})
		vs.Upsert(ctx, 1, []float32{0.5, 0.6, 0.7, 0.8})

		count, _ := vs.Count(ctx)
		if count != 1 {
			t.Errorf("Count after overwrite = %d, want 1", count)
		}
	})

	t.Run("UpsertBatch", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		err := vs.UpsertBatch(ctx,
			[]int64{1, 2, 3},
			[][]float32{
				{0.1, 0.2, 0.3, 0.4},
				{0.5, 0.6, 0.7, 0.8},
				{0.9, 0.8, 0.7, 0.6},
			})
		if err != nil {
			t.Fatalf("UpsertBatch: %v", err)
		}
		count, _ := vs.Count(ctx)
		if count != 3 {
			t.Errorf("Count = %d, want 3", count)
		}
	})

	t.Run("Has", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		vs.Upsert(ctx, 42, []float32{0.1, 0.2, 0.3, 0.4})

		has, err := vs.Has(ctx, 42)
		if err != nil {
			t.Fatalf("Has(42): %v", err)
		}
		if !has {
			t.Error("expected Has(42) = true")
		}

		has, _ = vs.Has(ctx, 999)
		if has {
			t.Error("expected Has(999) = false")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4})
		vs.Upsert(ctx, 2, []float32{0.5, 0.6, 0.7, 0.8})

		if err := vs.Delete(ctx, []int64{1}); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		has, _ := vs.Has(ctx, 1)
		if has {
			t.Error("expected Has(1) = false after delete")
		}
		has, _ = vs.Has(ctx, 2)
		if !has {
			t.Error("expected Has(2) = true (not deleted)")
		}
	})

	t.Run("Delete_Idempotent", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		// Deleting nonexistent IDs should not error
		if err := vs.Delete(ctx, []int64{999}); err != nil {
			t.Fatalf("Delete nonexistent: %v", err)
		}
	})

	t.Run("Search_CosineSimilarity", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		// Insert vectors with known similarity to query
		vs.Upsert(ctx, 1, []float32{1, 0, 0, 0})    // identical to query
		vs.Upsert(ctx, 2, []float32{0, 1, 0, 0})    // orthogonal
		vs.Upsert(ctx, 3, []float32{0.9, 0.1, 0, 0}) // similar to query

		results, err := vs.Search(ctx, []float32{1, 0, 0, 0}, 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}

		// First result should be the most similar (chunk 1 or 3)
		topID := results[0].ChunkID
		if topID != 1 && topID != 3 {
			t.Errorf("top result ChunkID = %d, expected 1 or 3", topID)
		}

		// Scores should be in descending order
		for i := 1; i < len(results); i++ {
			if results[i].Score > results[i-1].Score+1e-6 {
				t.Errorf("results not sorted by score: [%d].Score=%f > [%d].Score=%f",
					i, results[i].Score, i-1, results[i-1].Score)
			}
		}
	})

	t.Run("Search_TopK", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		for i := int64(1); i <= 10; i++ {
			v := make([]float32, dims)
			v[0] = float32(i) / 10.0
			vs.Upsert(ctx, i, v)
		}

		results, err := vs.Search(ctx, []float32{1, 0, 0, 0}, 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) > 3 {
			t.Errorf("Search returned %d results, want <= 3", len(results))
		}
	})

	t.Run("Search_EmptyStore", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()

		results, err := vs.Search(context.Background(), []float32{1, 0, 0, 0}, 5)
		if err != nil {
			t.Fatalf("Search empty: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results from empty store, got %d", len(results))
		}
	})

	t.Run("Search_ScoresNotNaN", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()
		ctx := context.Background()

		vs.Upsert(ctx, 1, []float32{1, 0, 0, 0})
		vs.Upsert(ctx, 2, []float32{0, 1, 0, 0})

		results, err := vs.Search(ctx, []float32{1, 0, 0, 0}, 2)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		for _, r := range results {
			if math.IsNaN(r.Score) {
				t.Errorf("score is NaN for chunk %d", r.ChunkID)
			}
		}
		// Identical vector should have highest score
		if len(results) > 0 && results[0].ChunkID != 1 {
			t.Errorf("expected chunk 1 (identical) as top result, got %d", results[0].ChunkID)
		}
	})

	t.Run("Healthy", func(t *testing.T) {
		vs := factory(t, dims)
		defer vs.Close()

		if !vs.Healthy(context.Background()) {
			t.Error("expected Healthy = true")
		}
	})

	t.Run("Close_Idempotent", func(t *testing.T) {
		vs := factory(t, dims)
		if err := vs.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		// Second close should not panic (may or may not error)
		vs.Close()
	})
}
