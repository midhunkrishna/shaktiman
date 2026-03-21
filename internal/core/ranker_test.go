//go:build sqlite_fts5

package core

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func setupTestStore(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return storage.NewStore(db)
}

func TestHybridRank_EmptyCandidates(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	got := HybridRank(context.Background(), HybridRankInput{
		Candidates: nil,
		Store:      store,
		Weights:    DefaultRankWeights(),
	})

	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d items", len(got))
	}
}

func TestHybridRank_KeywordOnly(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	candidates := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9, Path: "a.go", Kind: "function", Content: "func A()"},
		{ChunkID: 2, Score: 0.5, Path: "b.go", Kind: "function", Content: "func B()"},
		{ChunkID: 3, Score: 0.7, Path: "c.go", Kind: "function", Content: "func C()"},
	}

	got := HybridRank(context.Background(), HybridRankInput{
		Candidates: candidates,
		Store:      store,
		Weights:    RankWeights{Keyword: 1.0, Structural: 0, Change: 0},
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	// With keyword-only weights and no structural/change data, order should
	// match descending original scores: 0.9, 0.7, 0.5.
	wantOrder := []int64{1, 3, 2}
	for i, wantID := range wantOrder {
		if got[i].ChunkID != wantID {
			t.Errorf("position %d: want ChunkID %d, got %d", i, wantID, got[i].ChunkID)
		}
	}
}

func TestHybridRank_PreservesOrder(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	candidates := []types.ScoredResult{
		{ChunkID: 10, Score: 0.8, Path: "x.go", Kind: "function", Content: "func X()"},
		{ChunkID: 20, Score: 0.6, Path: "y.go", Kind: "function", Content: "func Y()"},
		{ChunkID: 30, Score: 0.4, Path: "z.go", Kind: "function", Content: "func Z()"},
	}

	got := HybridRank(context.Background(), HybridRankInput{
		Candidates: candidates,
		Store:      store,
		Weights:    DefaultRankWeights(),
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	// With no edges or diffs in the store, structural and change scores are 0.
	// Final score = 0.5*keyword + 0.3*0 + 0.2*0 = 0.5*keyword.
	// Order should remain descending by original score.
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("results not sorted descending: position %d score %.4f > position %d score %.4f",
				i, got[i].Score, i-1, got[i-1].Score)
		}
	}
}

func TestDefaultRankWeights(t *testing.T) {
	t.Parallel()

	w := DefaultRankWeights()

	if w.Keyword != 0.5 {
		t.Errorf("Keyword: want 0.5, got %v", w.Keyword)
	}
	if w.Structural != 0.3 {
		t.Errorf("Structural: want 0.3, got %v", w.Structural)
	}
	if w.Change != 0.2 {
		t.Errorf("Change: want 0.2, got %v", w.Change)
	}
}
