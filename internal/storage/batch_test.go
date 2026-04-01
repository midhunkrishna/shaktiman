package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ---------------------------------------------------------------------------
// BatchGetSymbolIDsForChunks
// ---------------------------------------------------------------------------

func TestBatchGetSymbolIDsForChunks(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Create two files with chunks and symbols, same symbol name "Foo"
	fA, cA, sA := insertTestFileChunkSymbol(t, store, "a.go", "Foo")
	_, cB, _ := insertTestFileChunkSymbol(t, store, "b.go", "Foo")
	_ = fA

	result, err := store.BatchGetSymbolIDsForChunks(ctx, []int64{cA, cB})
	if err != nil {
		t.Fatalf("BatchGetSymbolIDsForChunks: %v", err)
	}

	// cA's chunk is in a.go; symbol "Foo" in a.go should be preferred
	if result[cA] != sA {
		t.Errorf("cA: got symbolID %d, want %d (same-file match)", result[cA], sA)
	}
	// cB's chunk is in b.go; symbol "Foo" in b.go should be preferred
	if result[cB] == 0 {
		t.Error("cB: got symbolID 0, want non-zero")
	}

	_ = db
}

func TestBatchGetSymbolIDsForChunks_Empty(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchGetSymbolIDsForChunks(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestBatchGetSymbolIDsForChunks_NoSymbolName(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	// Create a chunk with no symbol name
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "x.go", ContentHash: "h", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatal(err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "", Kind: "block", StartLine: 1, EndLine: 5,
			Content: "// header", TokenCount: 2},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.BatchGetSymbolIDsForChunks(ctx, chunkIDs)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := result[chunkIDs[0]]; found {
		t.Error("expected no mapping for chunk with empty symbol name")
	}
}

func TestBatchGetSymbolIDsForChunks_SameFilePreference(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// File A has symbol "Handler" and a chunk referencing it
	fA, cA, sA := insertTestFileChunkSymbol(t, store, "handler.go", "Handler")
	// File B also has symbol "Handler" (different file)
	_, _, sB := insertTestFileChunkSymbol(t, store, "other.go", "Handler")
	_ = fA

	result, err := store.BatchGetSymbolIDsForChunks(ctx, []int64{cA})
	if err != nil {
		t.Fatal(err)
	}
	if result[cA] != sA {
		t.Errorf("expected same-file symbol %d, got %d (should not pick %d from other file)", sA, result[cA], sB)
	}
	_ = db
}

// ---------------------------------------------------------------------------
// BatchNeighbors
// ---------------------------------------------------------------------------

func TestBatchNeighbors(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// A → B → C chain
	fileID, _, symA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")
	_, _, symC := insertTestFileChunkSymbol(t, store, "c.go", "FuncC")

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		if err := store.InsertEdges(ctx, SqliteTxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symA, "FuncB": symB}, ""); err != nil {
			return err
		}
		return store.InsertEdges(ctx, SqliteTxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncB", DstSymbolName: "FuncC", Kind: "calls"},
		}, map[string]int64{"FuncB": symB, "FuncC": symC}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	result, err := store.BatchNeighbors(ctx, []int64{symA, symC}, 2)
	if err != nil {
		t.Fatalf("BatchNeighbors: %v", err)
	}

	// symA outgoing depth 2: B, C
	if len(result[symA]) == 0 {
		t.Error("expected neighbors for symA, got none")
	}
	// symC incoming depth 2: B, A
	if len(result[symC]) == 0 {
		t.Error("expected neighbors for symC, got none")
	}
}

func TestBatchNeighbors_Empty(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchNeighbors(ctx, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestBatchNeighbors_NoEdges(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	_, _, symA := insertTestFileChunkSymbol(t, store, "lonely.go", "Lonely")

	result, err := store.BatchNeighbors(ctx, []int64{symA}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result[symA]) != 0 {
		t.Errorf("expected no neighbors for isolated symbol, got %d", len(result[symA]))
	}
}

// ---------------------------------------------------------------------------
// BatchGetChunkIDsForSymbols
// ---------------------------------------------------------------------------

func TestBatchGetChunkIDsForSymbols(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	_, cA, sA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, cB, sB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	result, err := store.BatchGetChunkIDsForSymbols(ctx, []int64{sA, sB})
	if err != nil {
		t.Fatal(err)
	}

	if result[sA] != cA {
		t.Errorf("symA: got chunkID %d, want %d", result[sA], cA)
	}
	if result[sB] != cB {
		t.Errorf("symB: got chunkID %d, want %d", result[sB], cB)
	}
}

func TestBatchGetChunkIDsForSymbols_Empty(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchGetChunkIDsForSymbols(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestBatchGetChunkIDsForSymbols_NonExistent(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchGetChunkIDsForSymbols(ctx, []int64{999, 888})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty for non-existent IDs, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// BatchHydrateChunks
// ---------------------------------------------------------------------------

func TestBatchHydrateChunks(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	_, cA, _ := insertTestFileChunkSymbol(t, store, "main.go", "Main")
	_, cB, _ := insertTestFileChunkSymbol(t, store, "util.go", "Helper")

	results, err := store.BatchHydrateChunks(ctx, []int64{cA, cB})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	byID := map[int64]types.HydratedChunk{}
	for _, h := range results {
		byID[h.ChunkID] = h
	}

	hA := byID[cA]
	if hA.Path != "main.go" {
		t.Errorf("chunk A path = %q, want %q", hA.Path, "main.go")
	}
	if hA.SymbolName != "Main" {
		t.Errorf("chunk A symbol = %q, want %q", hA.SymbolName, "Main")
	}
	if hA.Kind != "function" {
		t.Errorf("chunk A kind = %q, want %q", hA.Kind, "function")
	}
	if hA.IsTest {
		t.Error("chunk A: IsTest should be false")
	}
}

func TestBatchHydrateChunks_IsTest(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	// Create a test file (IsTest=true)
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "main_test.go", ContentHash: "h_test", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "TestMain", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func TestMain(t *testing.T) {}", TokenCount: 8},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := store.BatchHydrateChunks(ctx, chunkIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsTest {
		t.Error("expected IsTest=true for test file chunk")
	}
	if results[0].Path != "main_test.go" {
		t.Errorf("path = %q, want %q", results[0].Path, "main_test.go")
	}
}

func TestBatchHydrateChunks_Empty(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	results, err := store.BatchHydrateChunks(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Errorf("expected nil, got %d results", len(results))
	}
}

func TestBatchHydrateChunks_NonExistent(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	results, err := store.BatchHydrateChunks(ctx, []int64{999})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-existent chunk, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// BatchGetFileHashes
// ---------------------------------------------------------------------------

func TestBatchGetFileHashes(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "hash_a", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertFile(ctx, &types.FileRecord{
		Path: "b.go", ContentHash: "hash_b", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.BatchGetFileHashes(ctx, []string{"a.go", "b.go", "nonexistent.go"})
	if err != nil {
		t.Fatal(err)
	}

	if result["a.go"] != "hash_a" {
		t.Errorf("a.go: got %q, want %q", result["a.go"], "hash_a")
	}
	if result["b.go"] != "hash_b" {
		t.Errorf("b.go: got %q, want %q", result["b.go"], "hash_b")
	}
	if _, found := result["nonexistent.go"]; found {
		t.Error("nonexistent.go should not be in results")
	}
}

func TestBatchGetFileHashes_Empty(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchGetFileHashes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestBatchGetFileHashes_AllNew(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	result, err := store.BatchGetFileHashes(ctx, []string{"new1.go", "new2.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty for all-new files, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Large batch (>500) to test chunking
// ---------------------------------------------------------------------------

func TestBatchHydrateChunks_LargeBatch(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	// Create 600 chunks (exceeds batchLimit=500)
	_, chunkIDs := insertTestFileWithChunks(t, store, "big.go", 600)

	results, err := store.BatchHydrateChunks(ctx, chunkIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 600 {
		t.Errorf("expected 600 results, got %d", len(results))
	}
}

func TestBatchGetFileHashes_LargeBatch(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	// Create 600 files
	paths := make([]string, 600)
	for i := range paths {
		path := fmt.Sprintf("file_%04d.go", i)
		paths[i] = path
		_, err := store.UpsertFile(ctx, &types.FileRecord{
			Path: path, ContentHash: fmt.Sprintf("hash_%04d", i), Mtime: 1.0,
			EmbeddingStatus: "pending", ParseQuality: "full",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	result, err := store.BatchGetFileHashes(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 600 {
		t.Errorf("expected 600 entries, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface check
// ---------------------------------------------------------------------------

var _ types.BatchMetadataStore = (*Store)(nil)
