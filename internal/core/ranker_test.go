//go:build sqlite_fts5

package core

import (
	"context"
	"database/sql"
	"fmt"
	"math"
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
	// Session and semantic are also 0 (unavailable), weight redistributed to keyword.
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

	if w.Semantic != 0.40 {
		t.Errorf("Semantic: want 0.40, got %v", w.Semantic)
	}
	if w.Structural != 0.20 {
		t.Errorf("Structural: want 0.20, got %v", w.Structural)
	}
	if w.Change != 0.15 {
		t.Errorf("Change: want 0.15, got %v", w.Change)
	}
	if w.Session != 0.15 {
		t.Errorf("Session: want 0.15, got %v", w.Session)
	}
	if w.Keyword != 0.10 {
		t.Errorf("Keyword: want 0.10, got %v", w.Keyword)
	}
}

func TestRedistributeWeights_SemanticUnavailable(t *testing.T) {
	t.Parallel()

	base := DefaultRankWeights()
	w := redistributeWeights(base, false, false)

	if w.Semantic != 0 {
		t.Errorf("Semantic should be 0 when unavailable, got %v", w.Semantic)
	}
	if w.Session != 0 {
		t.Errorf("Session should be 0 (Phase 4 not implemented), got %v", w.Session)
	}

	// Remaining signals: Keyword, Structural, Change
	// Base remaining = 0.10 + 0.20 + 0.15 = 0.45
	// Unavailable = 0.40 (semantic) + 0.15 (session) = 0.55
	// Factor = (0.45 + 0.55) / 0.45 = 1.0/0.45 ~= 2.2222
	sum := w.Keyword + w.Structural + w.Change + w.Semantic + w.Session
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("weights should sum to ~1.0, got %v", sum)
	}

	// Proportions among remaining signals should be preserved
	// Structural was 2x Keyword, Change was 1.5x Keyword
	if math.Abs(w.Structural/w.Keyword-2.0) > 1e-9 {
		t.Errorf("Structural/Keyword ratio should be 2.0, got %v", w.Structural/w.Keyword)
	}
	if math.Abs(w.Change/w.Keyword-1.5) > 1e-9 {
		t.Errorf("Change/Keyword ratio should be 1.5, got %v", w.Change/w.Keyword)
	}
}

func TestRedistributeWeights_AllAvailable(t *testing.T) {
	t.Parallel()

	base := DefaultRankWeights()
	w := redistributeWeights(base, true, false)

	// Session is still unavailable, so its weight is redistributed
	if w.Session != 0 {
		t.Errorf("Session should be 0 when sessionReady=false, got %v", w.Session)
	}

	// Semantic should remain available and be scaled up
	if w.Semantic == 0 {
		t.Errorf("Semantic should be non-zero when semanticReady=true")
	}

	sum := w.Keyword + w.Structural + w.Change + w.Semantic + w.Session
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("weights should sum to ~1.0, got %v", sum)
	}

	// Base remaining = 0.40 + 0.20 + 0.15 + 0.10 = 0.85
	// Unavailable = 0.15 (session)
	// Factor = (0.85 + 0.15) / 0.85 = 1.0/0.85 ~= 1.17647
	// Proportions among all 4 remaining should be preserved
	if math.Abs(w.Semantic/w.Keyword-4.0) > 1e-9 {
		t.Errorf("Semantic/Keyword ratio should be 4.0, got %v", w.Semantic/w.Keyword)
	}
}

func TestRedistributeWeights_AllSignalsAvailable(t *testing.T) {
	t.Parallel()

	base := DefaultRankWeights()
	w := redistributeWeights(base, true, true)

	// All signals available — weights should be unchanged
	if math.Abs(w.Semantic-base.Semantic) > 1e-9 {
		t.Errorf("Semantic: want %v, got %v", base.Semantic, w.Semantic)
	}
	if math.Abs(w.Session-base.Session) > 1e-9 {
		t.Errorf("Session: want %v, got %v", base.Session, w.Session)
	}

	sum := w.Keyword + w.Structural + w.Change + w.Semantic + w.Session
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("weights should sum to ~1.0, got %v", sum)
	}
}

func TestHybridRank_WithSemanticScores(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	candidates := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9, Path: "a.go", Kind: "function", Content: "func A()"},
		{ChunkID: 2, Score: 0.3, Path: "b.go", Kind: "function", Content: "func B()"},
		{ChunkID: 3, Score: 0.5, Path: "c.go", Kind: "function", Content: "func C()"},
	}

	// ChunkID 2 has a very high semantic score despite low keyword score
	semanticScores := map[int64]float64{
		1: 0.2,
		2: 1.0,
		3: 0.3,
	}

	got := HybridRank(context.Background(), HybridRankInput{
		Candidates:     candidates,
		Store:          store,
		Weights:        DefaultRankWeights(),
		SemanticScores: semanticScores,
		SemanticReady:  true,
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	// ChunkID 2 should be boosted to first due to high semantic score
	// (semantic weight is dominant after redistribution)
	if got[0].ChunkID != 2 {
		t.Errorf("expected ChunkID 2 first (high semantic), got ChunkID %d", got[0].ChunkID)
	}
}

// mockSessionScorer returns fixed scores for testing.
type mockSessionScorer struct {
	scores map[string]float64
}

func (m *mockSessionScorer) Score(filePath string, startLine int) float64 {
	key := fmt.Sprintf("%s:%d", filePath, startLine)
	return m.scores[key]
}

func TestHybridRank_WithSessionScorer(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	candidates := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9, Path: "a.go", StartLine: 1, Kind: "function", Content: "func A()"},
		{ChunkID: 2, Score: 0.3, Path: "b.go", StartLine: 10, Kind: "function", Content: "func B()"},
		{ChunkID: 3, Score: 0.5, Path: "c.go", StartLine: 20, Kind: "function", Content: "func C()"},
	}

	scorer := &mockSessionScorer{scores: map[string]float64{
		"a.go:1":  0.1,
		"b.go:10": 0.95, // high session score despite low keyword
		"c.go:20": 0.2,
	}}

	got := HybridRank(context.Background(), HybridRankInput{
		Candidates:    candidates,
		Store:         store,
		Weights:       RankWeights{Session: 1.0}, // session-only ranking
		SemanticReady: false,
		SessionScorer: scorer,
	})

	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	// With session-only weights, ChunkID 2 should be first (score 0.95)
	if got[0].ChunkID != 2 {
		t.Errorf("expected ChunkID 2 first (high session score), got ChunkID %d", got[0].ChunkID)
	}
}

func TestLookupSymbolForChunk_NoSymbol(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert a file and a chunk with no SymbolName.
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "nosym.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "", Kind: "block",
			StartLine: 1, EndLine: 10,
			Content: "package main", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	id := lookupSymbolForChunk(ctx, store, chunkIDs[0])
	if id != 0 {
		t.Errorf("expected 0 for chunk with no symbol name, got %d", id)
	}
}

func TestLookupChunkIDsForSymbols_UnknownID(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// Unknown symbol IDs should be silently skipped.
	result := lookupChunkIDsForSymbols(context.Background(), store, []int64{99999})
	if len(result) != 0 {
		t.Errorf("expected 0 results for unknown IDs, got %d", len(result))
	}
}

func TestComputeStructuralScores_WithGraphEdges(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Create two files, each with 1 chunk and 1 symbol, connected by an edge.
	fileID1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 1: %v", err)
	}
	chunkIDs1, err := store.InsertChunks(ctx, fileID1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "FuncA", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func A() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks 1: %v", err)
	}
	symIDs1, err := store.InsertSymbols(ctx, fileID1, []types.SymbolRecord{
		{ChunkID: chunkIDs1[0], Name: "FuncA", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols 1: %v", err)
	}

	fileID2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "b.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 2: %v", err)
	}
	chunkIDs2, err := store.InsertChunks(ctx, fileID2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "FuncB", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func B() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks 2: %v", err)
	}
	symIDs2, err := store.InsertSymbols(ctx, fileID2, []types.SymbolRecord{
		{ChunkID: chunkIDs2[0], Name: "FuncB", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols 2: %v", err)
	}

	// Insert a direct edge: FuncA -> FuncB
	err = store.DB().WithWriteTx(func(tx *sql.Tx) error {
		symMap := map[string]int64{
			"FuncA": symIDs1[0],
			"FuncB": symIDs2[0],
		}
		return store.InsertEdges(ctx, storage.SqliteTxHandle{Tx: tx}, fileID1, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, symMap, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	candidates := []types.ScoredResult{
		{ChunkID: chunkIDs1[0], Score: 0.9, Path: "a.go", SymbolName: "FuncA",
			Kind: "function", StartLine: 1, EndLine: 10},
		{ChunkID: chunkIDs2[0], Score: 0.8, Path: "b.go", SymbolName: "FuncB",
			Kind: "function", StartLine: 1, EndLine: 10},
	}

	scores := computeStructuralScores(ctx, store, candidates)

	// Both chunks should have non-zero structural scores because they are
	// BFS-reachable neighbors and both appear in the candidate set.
	if scores[chunkIDs1[0]] == 0 {
		t.Error("expected non-zero structural score for FuncA (neighbor FuncB is a candidate)")
	}
	if scores[chunkIDs2[0]] == 0 {
		t.Error("expected non-zero structural score for FuncB (neighbor FuncA is a candidate)")
	}
}

func TestLookupSymbolForChunk_MatchingSymbol(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "sym.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "MyFunc", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func MyFunc() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}
	symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "MyFunc", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}

	got := lookupSymbolForChunk(ctx, store, chunkIDs[0])
	if got != symIDs[0] {
		t.Errorf("lookupSymbolForChunk = %d, want %d", got, symIDs[0])
	}
}

func TestLookupSymbolForChunk_MatchesSameFile(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Create two files, both with a symbol named "Handler".
	// lookupSymbolForChunk should prefer the same-file symbol.
	fileID1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "f1.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 1: %v", err)
	}
	chunkIDs1, err := store.InsertChunks(ctx, fileID1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Handler", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func Handler() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks 1: %v", err)
	}
	symIDs1, err := store.InsertSymbols(ctx, fileID1, []types.SymbolRecord{
		{ChunkID: chunkIDs1[0], Name: "Handler", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols 1: %v", err)
	}

	fileID2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "f2.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 2: %v", err)
	}
	chunkIDs2, err := store.InsertChunks(ctx, fileID2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Handler", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func Handler() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks 2: %v", err)
	}
	store.InsertSymbols(ctx, fileID2, []types.SymbolRecord{
		{ChunkID: chunkIDs2[0], Name: "Handler", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// Lookup for file1's chunk should return file1's symbol
	got := lookupSymbolForChunk(ctx, store, chunkIDs1[0])
	if got != symIDs1[0] {
		t.Errorf("lookupSymbolForChunk for file1 chunk = %d, want %d (same-file match)", got, symIDs1[0])
	}
}

func TestNormalizeCosineSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"max similarity", 1.0, 1.0},
		{"zero similarity", 0.0, 0.5},
		{"min similarity", -1.0, 0.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeCosineSimilarity(tc.in)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("NormalizeCosineSimilarity(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
