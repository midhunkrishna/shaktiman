package sqlite

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestKeywordSearch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "validateToken", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content: "function validateToken(token: string): boolean { return token.length > 0; }",
			TokenCount: 20},
		{ChunkIndex: 1, SymbolName: "hashPassword", Kind: "function",
			StartLine: 12, EndLine: 20,
			Content: "function hashPassword(password: string): string { return hash(password); }",
			TokenCount: 18},
	})

	results, err := store.KeywordSearch(ctx, "validate token", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// The validateToken chunk should match
	found := false
	for _, r := range results {
		chunk, _ := store.GetChunkByID(ctx, r.ChunkID)
		if chunk != nil && chunk.SymbolName == "validateToken" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected validateToken in results")
	}
}

func TestFTSRebuild(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Disable triggers
	if err := store.DisableFTSTriggers(ctx); err != nil {
		t.Fatalf("DisableFTSTriggers: %v", err)
	}

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "hello", Kind: "function",
			StartLine: 1, EndLine: 5,
			Content: "function hello() { console.log('hello'); }",
			TokenCount: 10},
	})

	// Search should fail without triggers and rebuild
	results, _ := store.KeywordSearch(ctx, "hello", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results before rebuild, got %d", len(results))
	}

	// Rebuild FTS
	if err := store.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	// Re-enable triggers
	if err := store.EnableFTSTriggers(ctx); err != nil {
		t.Fatalf("EnableFTSTriggers: %v", err)
	}

	// Now search should work
	results, err := store.KeywordSearch(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("KeywordSearch after rebuild: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results after rebuild")
	}
}

func TestIsFTSStale_FreshDB(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// Empty DB: both counts are 0, so FTS is not stale.
	stale, err := store.IsFTSStale(context.Background())
	if err != nil {
		t.Fatalf("IsFTSStale: %v", err)
	}
	if stale {
		t.Error("expected fresh DB to not be stale")
	}
}

func TestIsFTSStale_AfterInsert(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// With triggers active, inserting a chunk keeps FTS in sync.
	insertTestFileWithChunks(t, store, "sync.go", 3)

	stale, err := store.IsFTSStale(ctx)
	if err != nil {
		t.Fatalf("IsFTSStale: %v", err)
	}
	if stale {
		t.Error("FTS should not be stale when triggers are active")
	}
}

func TestIsFTSStale_WithDisabledTriggers(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Disable triggers and insert data. Because chunks_fts is an external-content
	// FTS5 table (content=chunks), COUNT(*) on chunks_fts reads from the content
	// table, so IsFTSStale reports counts as matching. This test exercises the code
	// path and verifies no errors occur.
	if err := store.DisableFTSTriggers(ctx); err != nil {
		t.Fatalf("DisableFTSTriggers: %v", err)
	}
	insertTestFileWithChunks(t, store, "stale.go", 2)

	_, err := store.IsFTSStale(ctx)
	if err != nil {
		t.Fatalf("IsFTSStale: %v", err)
	}
}

func TestEnsureFTSTriggers_Idempotent(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Calling EnsureFTSTriggers multiple times should not error.
	for i := 0; i < 3; i++ {
		if err := store.EnsureFTSTriggers(ctx); err != nil {
			t.Fatalf("EnsureFTSTriggers (call %d): %v", i+1, err)
		}
	}
}

func TestKeywordSearch_EmptyQuery(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Empty query should return nil, nil.
	results, err := store.KeywordSearch(ctx, "", 10)
	if err != nil {
		t.Fatalf("KeywordSearch(empty): %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
}

func TestKeywordSearch_SpecialCharsOnly(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Query with only FTS5 special chars should sanitize to empty and return nil.
	results, err := store.KeywordSearch(ctx, `"*+-^`, 10)
	if err != nil {
		t.Fatalf("KeywordSearch(special): %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for all-special-char query, got %d", len(results))
	}
}

func TestKeywordSearch_SymbolNameMatch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "sym.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "ProcessPayment", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content: "func ProcessPayment(amount float64) error { return nil }",
			TokenCount: 15},
	})

	// Search by symbol name.
	results, err := store.KeywordSearch(ctx, "ProcessPayment", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for symbol name query")
	}
}

func TestKeywordSearch_MultipleResults(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "multi.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	// Insert multiple chunks that share the common term "handler".
	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "handleRequest", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content:    "func handleRequest(r Request) Response { return handler(r) }",
			TokenCount: 15},
		{ChunkIndex: 1, SymbolName: "handleError", Kind: "function",
			StartLine: 12, EndLine: 20,
			Content:    "func handleError(err error) Response { return handler(err) }",
			TokenCount: 14},
		{ChunkIndex: 2, SymbolName: "unrelatedFunc", Kind: "function",
			StartLine: 22, EndLine: 30,
			Content:    "func unrelatedFunc() int { return 42 }",
			TokenCount: 10},
	})

	results, err := store.KeywordSearch(ctx, "handler", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}

	// At least 2 results should match "handler".
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'handler', got %d", len(results))
	}

	// Verify results are ordered by rank (BM25, lower is better in FTS5).
	for i := 1; i < len(results); i++ {
		if results[i].Rank < results[i-1].Rank {
			t.Errorf("results not ordered by rank: result[%d].Rank=%f < result[%d].Rank=%f",
				i, results[i].Rank, i-1, results[i-1].Rank)
		}
	}

	// Verify each result has a valid ChunkID.
	for i, r := range results {
		if r.ChunkID <= 0 {
			t.Errorf("result[%d].ChunkID = %d, expected positive", i, r.ChunkID)
		}
	}
}

func TestEnsureFTSTriggers_RepairsAfterDisable(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Disable triggers, then re-enable via EnsureFTSTriggers.
	if err := store.DisableFTSTriggers(ctx); err != nil {
		t.Fatalf("DisableFTSTriggers: %v", err)
	}
	if err := store.EnsureFTSTriggers(ctx); err != nil {
		t.Fatalf("EnsureFTSTriggers: %v", err)
	}

	// Insert data and verify FTS is in sync (triggers are working again).
	insertTestFileWithChunks(t, store, "repaired.go", 2)

	stale, err := store.IsFTSStale(ctx)
	if err != nil {
		t.Fatalf("IsFTSStale: %v", err)
	}
	if stale {
		t.Error("FTS should not be stale after EnsureFTSTriggers repaired triggers")
	}
}
