//go:build sqlite_fts5

package storage

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
	t.Helper()
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: path, ContentHash: "h_" + path, Mtime: 1.0,
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
		return store.InsertEdges(ctx, tx, fileID, edges, symbolIDs)
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
		return store.InsertEdges(ctx, tx, fileID, edges, symbolIDs)
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
		return store.InsertEdges(ctx, tx, fileID, edges, symbolIDs)
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Now create FuncZ
	_, _, symZID := insertTestFileChunkSymbol(t, store, "z.go", "FuncZ")

	// Resolve pending edges
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, tx, []string{"FuncZ"})
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
		return store.InsertEdges(ctx, tx, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symAID, "FuncB": symBID})
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
		return store.InsertEdges(ctx, tx, bFileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncB", DstSymbolName: "FuncC", Kind: "calls"},
		}, map[string]int64{"FuncB": symBID, "FuncC": symCID})
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
		return store.InsertEdges(ctx, tx, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symAID, "FuncB": symBID})
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
		return store.InsertEdges(ctx, tx, bFileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncB", DstSymbolName: "FuncC", Kind: "calls"},
		}, map[string]int64{"FuncB": symBID, "FuncC": symCID})
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
		if err := store.InsertEdges(ctx, tx, fileIDA, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}); err != nil {
			return err
		}
		return store.InsertEdges(ctx, tx, fileIDC, []types.EdgeRecord{
			{SrcSymbolName: "FuncC", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncC": symIDC, "FuncB": symIDB})
	}); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Delete edges owned by file A.
	if err := db.WithWriteTx(func(tx *sql.Tx) error {
		return store.DeleteEdgesByFile(ctx, tx, fileIDA)
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
		if err := store.InsertEdges(ctx, tx, fileIDA, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB}); err != nil {
			return err
		}
		return store.InsertEdges(ctx, tx, fileIDC, []types.EdgeRecord{
			{SrcSymbolName: "FuncC", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncC": symIDC, "FuncB": symIDB})
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
		return store.InsertEdges(ctx, tx, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB})
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
		return store.InsertEdges(ctx, tx, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA, "FuncB": symIDB})
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
		return store.InsertEdges(ctx, tx, fileID, edges, symbolIDs)
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
		return store.InsertEdges(ctx, tx, fileIDA, edges, symbolIDs)
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
		return store.InsertEdges(ctx, tx, fileID, []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncZ", Kind: "calls"},
		}, map[string]int64{"FuncA": symIDA})
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Resolve with a name that does NOT match any pending edge.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.ResolvePendingEdges(ctx, tx, []string{"FuncNope"})
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

