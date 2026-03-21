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
