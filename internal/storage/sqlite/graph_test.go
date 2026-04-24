//go:build sqlite_fts5

package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func setupTestDB(t *testing.T) (*DB, *Store) {
	t.Helper()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewStore(db)
}

// insertTestFileChunkSymbol creates a file, chunk, and symbol in one shot.
// Returns fileID, chunkID, symbolID.
func insertTestFileChunkSymbol(t *testing.T, store *Store, path, symbolName string) (int64, int64, int64) {
	return insertTestFileChunkSymbolWithLang(t, store, path, symbolName, "")
}

// insertTestFileChunkSymbolWithLang creates a file with a specific language, chunk, and symbol.
// Returns fileID, chunkID, symbolID.
func insertTestFileChunkSymbolWithLang(t *testing.T, store *Store, path, symbolName, language string) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: path, ContentHash: "h_" + path, Mtime: 1.0,
		Language:        language,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile %s: %v", path, err)
	}

	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: symbolName, Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func " + symbolName + "() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks %s: %v", path, err)
	}

	symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: symbolName, Kind: "function", Line: 1,
			Visibility: "exported", IsExported: true},
	})
	if err != nil {
		t.Fatalf("InsertSymbols %s: %v", symbolName, err)
	}

	return fileID, chunkIDs[0], symIDs[0]
}

func TestInsertEdges_Resolved(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symBID := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	edges := []types.EdgeRecord{
		{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
	}
	symbolIDs := map[string]int64{
		"FuncA": symAID,
		"FuncB": symBID,
	}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, edges, symbolIDs, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Verify via Neighbors
	neighbors, err := store.Neighbors(ctx, symAID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(neighbors))
	}
	if neighbors[0] != symBID {
		t.Errorf("expected neighbor %d, got %d", symBID, neighbors[0])
	}
}

func TestInsertEdges_Pending(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")

	// FuncZ does not exist in the DB
	edges := []types.EdgeRecord{
		{SrcSymbolName: "FuncA", DstSymbolName: "FuncZ", Kind: "calls"},
	}
	symbolIDs := map[string]int64{
		"FuncA": symAID,
	}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, edges, symbolIDs, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Verify pending_edges has the entry
	var count int
	row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'FuncZ'")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query pending_edges: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 pending edge for FuncZ, got %d", count)
	}

	// Verify no resolved edge exists
	neighbors, err := store.Neighbors(ctx, symAID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 0 {
		t.Errorf("expected 0 resolved neighbors, got %d", len(neighbors))
	}
}

func TestResolvePendingEdges(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")

	// Insert pending edge for FuncZ which doesn't exist yet
	edges := []types.EdgeRecord{
		{SrcSymbolName: "FuncA", DstSymbolName: "FuncZ", Kind: "calls"},
	}
	symbolIDs := map[string]int64{
		"FuncA": symAID,
	}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, edges, symbolIDs, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Now create FuncZ
	_, _, symZID := insertTestFileChunkSymbol(t, store, "z.go", "FuncZ")

	// Resolve pending edges
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, TxHandle{Tx: tx}, []string{"FuncZ"})
	})
	if err != nil {
		t.Fatalf("ResolvePendingEdges: %v", err)
	}

	// Verify the edge is now resolved
	neighbors, err := store.Neighbors(ctx, symAID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 {
		t.Fatalf("expected 1 resolved neighbor, got %d", len(neighbors))
	}
	if neighbors[0] != symZID {
		t.Errorf("expected neighbor %d, got %d", symZID, neighbors[0])
	}

	// Verify pending_edges is cleared
	var count int
	row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'FuncZ'")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("query pending_edges: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 pending edges for FuncZ, got %d", count)
	}
}

func TestNeighbors_Outgoing(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Create chain A -> B -> C
	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symBID := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")
	_, _, symCID := insertTestFileChunkSymbol(t, store, "c.go", "FuncC")

	// Insert A -> B
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symAID, "FuncB": symBID}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges A->B: %v", err)
	}

	// Insert B -> C (use a different file_id context for the edge)
	bFileID := int64(0)
	row := db.QueryRowContext(ctx, "SELECT id FROM files WHERE path = 'b.go'")
	if err := row.Scan(&bFileID); err != nil {
		t.Fatalf("lookup b.go file id: %v", err)
	}

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, bFileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncB", DstSymbolName: "FuncC", Kind: "calls"},
		}, map[string]int64{"FuncB": symBID, "FuncC": symCID}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges B->C: %v", err)
	}

	// Neighbors(A, 2, "outgoing") should return B and C
	neighbors, err := store.Neighbors(ctx, symAID, 2, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	sort.Slice(neighbors, func(i, j int) bool { return neighbors[i] < neighbors[j] })

	expected := []int64{symBID, symCID}
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })

	if len(neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d: %v", len(neighbors), neighbors)
	}
	for i, id := range expected {
		if neighbors[i] != id {
			t.Errorf("neighbor[%d] = %d, want %d", i, neighbors[i], id)
		}
	}
}

func TestNeighbors_Incoming(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Create chain A -> B -> C
	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symBID := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")
	_, _, symCID := insertTestFileChunkSymbol(t, store, "c.go", "FuncC")

	// Insert A -> B
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symAID, "FuncB": symBID}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges A->B: %v", err)
	}

	// Insert B -> C
	bFileID := int64(0)
	row := db.QueryRowContext(ctx, "SELECT id FROM files WHERE path = 'b.go'")
	if err := row.Scan(&bFileID); err != nil {
		t.Fatalf("lookup b.go file id: %v", err)
	}

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, bFileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncB", DstSymbolName: "FuncC", Kind: "calls"},
		}, map[string]int64{"FuncB": symBID, "FuncC": symCID}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges B->C: %v", err)
	}

	// Neighbors(C, 2, "incoming") should return B and A
	neighbors, err := store.Neighbors(ctx, symCID, 2, "incoming")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	sort.Slice(neighbors, func(i, j int) bool { return neighbors[i] < neighbors[j] })

	expected := []int64{symAID, symBID}
	sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })

	if len(neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d: %v", len(neighbors), neighbors)
	}
	for i, id := range expected {
		if neighbors[i] != id {
			t.Errorf("neighbor[%d] = %d, want %d", i, neighbors[i], id)
		}
	}
}

func TestDeleteEdgesByFile_RemovesTargetEdges(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileIDA, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")
	fileIDC, _, symIDC := insertTestFileChunkSymbol(t, store, "c.go", "FuncC")

	// Insert edge A->B (owned by file A) and C->B (owned by file C).
	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		if err := store.InsertEdges(ctx, TxHandle{Tx: tx}, fileIDA, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}, ""); err != nil {
			return err
		}
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileIDC, []types.EdgeRecord{
			{SrcSymbolName: "FuncC", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncC": symIDC, "FuncB": symIDB}, "")
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Delete edges owned by file A.
	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.DeleteEdgesByFile(ctx, TxHandle{Tx: tx}, fileIDA)
	}); err != nil {
		t.Fatalf("DeleteEdgesByFile: %v", err)
	}

	// A->B should be gone: A's outgoing neighbors should be empty.
	neighborsA, err := store.Neighbors(ctx, symIDA, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors(A): %v", err)
	}
	if len(neighborsA) != 0 {
		t.Errorf("expected 0 neighbors for A after delete, got %d", len(neighborsA))
	}

	// C->B should remain: C's outgoing neighbors should include B.
	neighborsC, err := store.Neighbors(ctx, symIDC, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors(C): %v", err)
	}
	if len(neighborsC) != 1 || neighborsC[0] != symIDB {
		t.Errorf("expected C->B edge to survive, got neighbors %v", neighborsC)
	}
}

func TestNeighbors_Both(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileIDA, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")
	fileIDC, _, symIDC := insertTestFileChunkSymbol(t, store, "c.go", "FuncC")

	// A->B and C->B
	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		if err := store.InsertEdges(ctx, TxHandle{Tx: tx}, fileIDA, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}, ""); err != nil {
			return err
		}
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileIDC, []types.EdgeRecord{
			{SrcSymbolName: "FuncC", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncC": symIDC, "FuncB": symIDB}, "")
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// B has incoming from A and C.
	// direction="both" should return both incoming and outgoing neighbors.
	neighbors, err := store.Neighbors(ctx, symIDB, 1, "both")
	if err != nil {
		t.Fatalf("Neighbors(both): %v", err)
	}

	// B has callers A and C (incoming), and no callees (outgoing).
	got := make(map[int64]bool)
	for _, n := range neighbors {
		got[n] = true
	}
	if !got[symIDA] {
		t.Error("expected A in B's 'both' neighbors (incoming)")
	}
	if !got[symIDC] {
		t.Error("expected C in B's 'both' neighbors (incoming)")
	}
}

func TestNeighbors_InvalidDirection(t *testing.T) {
	t.Parallel()
	_, store := setupTestDB(t)
	ctx := context.Background()

	_, _, symID := insertTestFileChunkSymbol(t, store, "a.go", "Func")

	_, err := store.Neighbors(ctx, symID, 1, "sideways")
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
}

func TestNeighbors_DepthClampBelow(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}, "")
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Depth 0 should be clamped to 1 and still return neighbors.
	neighbors, err := store.Neighbors(ctx, symIDA, 0, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors(depth=0): %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symIDB {
		t.Errorf("expected [%d], got %v", symIDB, neighbors)
	}
}

func TestNeighbors_DepthClampAbove(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}, "")
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Depth 99 should be clamped to 10 and still work without error.
	neighbors, err := store.Neighbors(ctx, symIDA, 99, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors(depth=99): %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symIDB {
		t.Errorf("expected [%d], got %v", symIDB, neighbors)
	}
}

func TestInsertEdges_SkipUnknownSrc(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	// Edge where src is not in symbolIDs map (srcID == 0) -- should be skipped.
	edges := []types.EdgeRecord{
		{SrcSymbolName: "UnknownSrc", DstSymbolName: "FuncB", Kind: "calls"},
		{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
	}
	symbolIDs := map[string]int64{
		"FuncA": symIDA,
		"FuncB": symIDB,
	}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, edges, symbolIDs, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Only A->B should exist, not UnknownSrc->B.
	neighbors, err := store.Neighbors(ctx, symIDA, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symIDB {
		t.Errorf("expected [%d], got %v", symIDB, neighbors)
	}
}

func TestInsertEdges_CrossFileLookup(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// FuncA is in file a.go, FuncB is in file b.go.
	// Insert edge from A->B where B is NOT in the symbolIDs map.
	// This exercises the lookupSymbolIDTx path (dst lookup via tx).
	fileIDA, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	_, _, symIDB := insertTestFileChunkSymbol(t, store, "b.go", "FuncB")

	edges := []types.EdgeRecord{
		{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
	}
	// Only include FuncA in the symbolIDs map -- FuncB must be looked up.
	symbolIDs := map[string]int64{
		"FuncA": symIDA,
	}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileIDA, edges, symbolIDs, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Verify the edge was resolved via cross-file lookup.
	neighbors, err := store.Neighbors(ctx, symIDA, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symIDB {
		t.Errorf("expected [%d], got %v", symIDB, neighbors)
	}
}

func TestResolvePendingEdges_NoMatch(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symIDA := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")

	// Insert pending edge for FuncZ.
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncZ", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Resolve with a name that does NOT match any pending edge.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, TxHandle{Tx: tx}, []string{"FuncNope"})
	})
	if err != nil {
		t.Fatalf("ResolvePendingEdges: %v", err)
	}

	// Pending edge should still be there.
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'FuncZ'").Scan(&count)
	if count != 1 {
		t.Errorf("expected pending edge to remain, got count=%d", count)
	}
}

// ── Language filter and qualified name tests ──

func TestInsertEdges_QualifiedNameAndLanguageStored(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, symAID := insertTestFileChunkSymbolWithLang(t, store, "MyService.java", "MyService", "java")

	edges := []types.EdgeRecord{
		{SrcSymbolName: "MyService", DstSymbolName: "List", DstQualifiedName: "java.util.List", Kind: "imports"},
	}
	symbolIDs := map[string]int64{"MyService": symAID}

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, edges, symbolIDs, "java")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Verify pending_edges has correct dst_qualified_name and src_language
	var qualifiedName, srcLang string
	err = db.QueryRowContext(ctx,
		"SELECT dst_qualified_name, src_language FROM pending_edges WHERE dst_symbol_name = 'List'",
	).Scan(&qualifiedName, &srcLang)
	if err != nil {
		t.Fatalf("query pending_edges: %v", err)
	}
	if qualifiedName != "java.util.List" {
		t.Errorf("dst_qualified_name=%q, want %q", qualifiedName, "java.util.List")
	}
	if srcLang != "java" {
		t.Errorf("src_language=%q, want %q", srcLang, "java")
	}
}

func TestResolvePendingEdges_LanguageFilter(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Java file imports "Config"
	javaFileID, _, javaSymID := insertTestFileChunkSymbolWithLang(t, store, "App.java", "App", "java")

	// Insert pending edge from Java for "Config"
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, javaFileID, []types.EdgeRecord{
			{SrcSymbolName: "App", DstSymbolName: "Config", DstQualifiedName: "com.example.Config", Kind: "imports"},
		}, map[string]int64{"App": javaSymID}, "java")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Python file defines "Config" — should NOT resolve the Java pending edge
	insertTestFileChunkSymbolWithLang(t, store, "config.py", "Config", "python")

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, TxHandle{Tx: tx}, []string{"Config"})
	})
	if err != nil {
		t.Fatalf("ResolvePendingEdges: %v", err)
	}

	// Pending edge should still exist (not resolved to Python symbol)
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'Config'").Scan(&count)
	if count != 1 {
		t.Errorf("expected Java pending edge for Config to remain (not resolve to Python), got count=%d", count)
	}

	// No resolved edge should exist
	neighbors, err := store.Neighbors(ctx, javaSymID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 0 {
		t.Errorf("expected 0 resolved neighbors (cross-language should not resolve), got %d", len(neighbors))
	}
}

func TestResolvePendingEdges_SameLanguageResolves(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Java file imports "Config"
	javaFileID, _, javaSymID := insertTestFileChunkSymbolWithLang(t, store, "App.java", "App", "java")

	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, javaFileID, []types.EdgeRecord{
			{SrcSymbolName: "App", DstSymbolName: "Config", DstQualifiedName: "com.example.Config", Kind: "imports"},
		}, map[string]int64{"App": javaSymID}, "java")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Java file defines "Config" — should resolve the Java pending edge
	_, _, javaConfigID := insertTestFileChunkSymbolWithLang(t, store, "Config.java", "Config", "java")

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, TxHandle{Tx: tx}, []string{"Config"})
	})
	if err != nil {
		t.Fatalf("ResolvePendingEdges: %v", err)
	}

	// Pending edge should be resolved
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'Config'").Scan(&count)
	if count != 0 {
		t.Errorf("expected pending edge to be resolved, got count=%d", count)
	}

	// Resolved edge should exist
	neighbors, err := store.Neighbors(ctx, javaSymID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != javaConfigID {
		t.Errorf("expected resolved edge to Java Config (%d), got %v", javaConfigID, neighbors)
	}
}

func TestLookupSymbolIDTx_LanguageFilter(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Create Config in both Python and Java
	_, _, pyConfigID := insertTestFileChunkSymbolWithLang(t, store, "config.py", "Config", "python")
	_, _, javaConfigID := insertTestFileChunkSymbolWithLang(t, store, "Config.java", "Config", "java")

	// lookupSymbolIDTx with language="java" should return the Java symbol
	var gotID int64
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		var lookupErr error
		gotID, lookupErr = lookupSymbolIDTx(ctx, tx, "Config", 0, "java")
		return lookupErr
	})
	if err != nil {
		t.Fatalf("lookupSymbolIDTx: %v", err)
	}
	if gotID != javaConfigID {
		t.Errorf("lookupSymbolIDTx(Config, java)=%d, want %d (Java); Python=%d", gotID, javaConfigID, pyConfigID)
	}

	// lookupSymbolIDTx with language="python" should return the Python symbol
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		var lookupErr error
		gotID, lookupErr = lookupSymbolIDTx(ctx, tx, "Config", 0, "python")
		return lookupErr
	})
	if err != nil {
		t.Fatalf("lookupSymbolIDTx: %v", err)
	}
	if gotID != pyConfigID {
		t.Errorf("lookupSymbolIDTx(Config, python)=%d, want %d (Python)", gotID, pyConfigID)
	}

	// lookupSymbolIDTx with language="" falls back to global (any match)
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		var lookupErr error
		gotID, lookupErr = lookupSymbolIDTx(ctx, tx, "Config", 0, "")
		return lookupErr
	})
	if err != nil {
		t.Fatalf("lookupSymbolIDTx: %v", err)
	}
	if gotID == 0 {
		t.Error("lookupSymbolIDTx(Config, '') returned 0, want any Config symbol")
	}
}

func TestIntegration_CrossLanguageNoMisresolution(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Polyglot scenario: Python defines Config, Java imports Config
	insertTestFileChunkSymbolWithLang(t, store, "config.py", "Config", "python")

	javaFileID, _, javaSymID := insertTestFileChunkSymbolWithLang(t, store, "App.java", "App", "java")

	// Java imports Config — should NOT resolve to Python Config
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, javaFileID, []types.EdgeRecord{
			{SrcSymbolName: "App", DstSymbolName: "Config", DstQualifiedName: "com.example.Config", Kind: "imports"},
		}, map[string]int64{"App": javaSymID}, "java")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Config should be in pending_edges, not resolved
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_edges WHERE dst_symbol_name = 'Config'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 pending edge for Config, got %d", count)
	}

	// No resolved edge from App
	neighbors, err := store.Neighbors(ctx, javaSymID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 0 {
		t.Errorf("expected no resolved edges (Java→Python should not resolve), got %d", len(neighbors))
	}
}

func TestPendingEdges_QualifiedNameAndLanguageColumns(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		if _, execErr := tx.ExecContext(ctx,
			"INSERT INTO files (path, content_hash, mtime, language) VALUES ('test.java', 'h1', 1.0, 'java')"); execErr != nil {
			return execErr
		}
		if _, execErr := tx.ExecContext(ctx,
			"INSERT INTO chunks (file_id, chunk_index, kind, start_line, end_line, content, token_count) VALUES (1, 0, 'class', 1, 10, 'class App {}', 5)"); execErr != nil {
			return execErr
		}
		if _, execErr := tx.ExecContext(ctx,
			"INSERT INTO symbols (chunk_id, file_id, name, kind, line) VALUES (1, 1, 'App', 'class', 1)"); execErr != nil {
			return execErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("setup test data: %v", err)
	}

	// Verify dst_qualified_name and src_language columns exist
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx,
			"INSERT INTO pending_edges (src_symbol_id, file_id, dst_symbol_name, dst_qualified_name, kind, src_language) VALUES (1, 1, 'Foo', 'com.example.Foo', 'imports', 'java')")
		return execErr
	})
	if err != nil {
		t.Fatalf("insert with qualified columns failed: %v", err)
	}

	var qualifiedName, srcLang string
	err = db.QueryRowContext(ctx,
		"SELECT dst_qualified_name, src_language FROM pending_edges WHERE dst_symbol_name = 'Foo'",
	).Scan(&qualifiedName, &srcLang)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	if qualifiedName != "com.example.Foo" {
		t.Errorf("dst_qualified_name=%q, want %q", qualifiedName, "com.example.Foo")
	}
	if srcLang != "java" {
		t.Errorf("src_language=%q, want %q", srcLang, "java")
	}
}

func TestResolvePendingEdges_BackwardCompat_EmptyLanguage(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Simulate pre-migration pending edge with empty src_language
	fileID, _, symAID := insertTestFileChunkSymbol(t, store, "a.go", "FuncA")
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		// Insert with empty language (like pre-migration data)
		return store.InsertEdges(ctx, TxHandle{Tx: tx}, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncZ", Kind: "calls"},
		}, map[string]int64{"FuncA": symAID}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Create FuncZ
	_, _, symZID := insertTestFileChunkSymbol(t, store, "z.go", "FuncZ")

	// Resolve — should work even with empty src_language (backward compat fallback)
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, TxHandle{Tx: tx}, []string{"FuncZ"})
	})
	if err != nil {
		t.Fatalf("ResolvePendingEdges: %v", err)
	}

	neighbors, err := store.Neighbors(ctx, symAID, 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symZID {
		t.Errorf("expected edge to FuncZ (%d), got %v", symZID, neighbors)
	}
}

