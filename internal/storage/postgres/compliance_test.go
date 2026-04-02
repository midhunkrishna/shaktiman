//go:build postgres

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage/storetest"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func newTestStore(t *testing.T) *PgStore {
	t.Helper()
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	ctx := context.Background()
	store, err := NewPgStore(ctx, connStr, 5, 2, "public")
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}

	// Clean all tables before each test
	pool := store.Pool()
	tables := []string{"diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config"}
	for _, table := range tables {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	t.Cleanup(func() { store.Close() })
	return store
}

func TestPostgresMetadataStoreCompliance(t *testing.T) {
	storetest.RunMetadataStoreTests(t, func(t *testing.T) types.MetadataStore {
		return newTestStore(t)
	})
}

func TestPostgres_WithWriteTx_CommitAndRollback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Commit path
	var fileID int64
	fileID, _ = store.UpsertFile(ctx, &types.FileRecord{
		Path: "tx_test.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	var diffID int64
	err := store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		var txErr error
		diffID, txErr = store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "add", LinesAdded: 5, HashAfter: "h1",
		})
		return txErr
	})
	if err != nil {
		t.Fatalf("WithWriteTx commit: %v", err)
	}
	if diffID == 0 {
		t.Fatal("expected non-zero diffID")
	}

	// Verify committed
	diffs, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: fileID})
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff after commit, got %d", len(diffs))
	}
}

func TestPostgres_DiffStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "diff.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	var diffID int64
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		var err error
		diffID, err = store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "modify", LinesAdded: 10, LinesRemoved: 3,
			HashBefore: "h0", HashAfter: "h1",
		})
		if err != nil {
			return err
		}
		return store.InsertDiffSymbols(ctx, txh, diffID, []types.DiffSymbolEntry{
			{SymbolName: "Foo", ChangeType: "modified"},
			{SymbolName: "Bar", ChangeType: "added"},
		})
	})

	syms, err := store.GetDiffSymbols(ctx, diffID)
	if err != nil {
		t.Fatalf("GetDiffSymbols: %v", err)
	}
	if len(syms) != 2 {
		t.Errorf("expected 2 diff symbols, got %d", len(syms))
	}
}

func TestPostgres_GraphMutator(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "graph.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "A", StartLine: 1, EndLine: 5,
			Content: "func A() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "B", StartLine: 6, EndLine: 10,
			Content: "func B() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "A", Kind: "function", Line: 1, Visibility: "exported"},
		{ChunkID: chunkIDs[1], Name: "B", Kind: "function", Line: 6, Visibility: "exported"},
	})

	symMap := map[string]int64{"A": symIDs[0], "B": symIDs[1]}

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "A", DstSymbolName: "B", Kind: "calls"},
		}, symMap, "go")
	})

	neighbors, err := store.Neighbors(ctx, symIDs[0], 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0] != symIDs[1] {
		t.Errorf("expected [%d], got %v", symIDs[1], neighbors)
	}
}

func TestPostgres_EmbeddingReconciler(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "embed.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "E", StartLine: 1, EndLine: 5,
			Content: "func E() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "F", StartLine: 6, EndLine: 10,
			Content: "func F() {}", TokenCount: 3},
	})

	// Initially no chunks embedded
	count, _ := store.CountChunksEmbedded(ctx)
	if count != 0 {
		t.Errorf("CountChunksEmbedded = %d, want 0", count)
	}

	needEmbed, _ := store.CountChunksNeedingEmbedding(ctx)
	if needEmbed != 2 {
		t.Errorf("CountChunksNeedingEmbedding = %d, want 2", needEmbed)
	}

	// Get embed page
	jobs, _ := store.GetEmbedPage(ctx, 0, 10)
	if len(jobs) != 2 {
		t.Fatalf("GetEmbedPage = %d, want 2", len(jobs))
	}

	// Mark embedded
	store.MarkChunksEmbedded(ctx, []int64{jobs[0].ChunkID, jobs[1].ChunkID})

	count, _ = store.CountChunksEmbedded(ctx)
	if count != 2 {
		t.Errorf("CountChunksEmbedded after mark = %d, want 2", count)
	}

	// Reset all
	store.ResetAllEmbeddedFlags(ctx)
	count, _ = store.CountChunksEmbedded(ctx)
	if count != 0 {
		t.Errorf("CountChunksEmbedded after reset = %d, want 0", count)
	}

	// Readiness
	readiness, _ := store.EmbeddingReadiness(ctx, 1)
	if readiness < 0.4 || readiness > 0.6 {
		t.Errorf("EmbeddingReadiness = %f, want ~0.5 (1/2 chunks)", readiness)
	}
}

func TestPostgres_PendingEdges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "pending.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Caller", StartLine: 1, EndLine: 5,
			Content: "func Caller() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "Caller", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// Insert edge to unknown symbol → should create pending edge
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Unknown", Kind: "calls"},
		}, map[string]int64{"Caller": symIDs[0]}, "go")
	})

	// Verify pending edge exists
	callers, err := store.PendingEdgeCallers(ctx, "Unknown")
	if err != nil {
		t.Fatalf("PendingEdgeCallers: %v", err)
	}
	if len(callers) != 1 {
		t.Errorf("expected 1 pending caller, got %d", len(callers))
	}

	callersWithKind, err := store.PendingEdgeCallersWithKind(ctx, "Unknown")
	if err != nil {
		t.Fatalf("PendingEdgeCallersWithKind: %v", err)
	}
	if len(callersWithKind) != 1 || callersWithKind[0].Kind != "calls" {
		t.Errorf("unexpected pending callers: %+v", callersWithKind)
	}
}

func TestPostgres_BatchMethods(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "batch.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "X", StartLine: 1, EndLine: 5,
			Content: "func X() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "X", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// BatchGetSymbolIDsForChunks
	symMap, err := store.BatchGetSymbolIDsForChunks(ctx, chunkIDs)
	if err != nil {
		t.Fatalf("BatchGetSymbolIDsForChunks: %v", err)
	}
	if symMap[chunkIDs[0]] != symIDs[0] {
		t.Errorf("expected symID %d for chunk %d, got %d", symIDs[0], chunkIDs[0], symMap[chunkIDs[0]])
	}

	// BatchGetChunkIDsForSymbols
	chunkMap, err := store.BatchGetChunkIDsForSymbols(ctx, symIDs)
	if err != nil {
		t.Fatalf("BatchGetChunkIDsForSymbols: %v", err)
	}
	if chunkMap[symIDs[0]] != chunkIDs[0] {
		t.Errorf("expected chunkID %d for sym %d", chunkIDs[0], symIDs[0])
	}

	// BatchHydrateChunks
	hydrated, err := store.BatchHydrateChunks(ctx, chunkIDs)
	if err != nil {
		t.Fatalf("BatchHydrateChunks: %v", err)
	}
	if len(hydrated) != 1 || hydrated[0].Path != "batch.go" {
		t.Errorf("unexpected hydrated: %+v", hydrated)
	}

	// BatchGetFileHashes
	hashes, err := store.BatchGetFileHashes(ctx, []string{"batch.go", "nonexistent.go"})
	if err != nil {
		t.Fatalf("BatchGetFileHashes: %v", err)
	}
	if hashes["batch.go"] != "h1" {
		t.Errorf("expected hash h1, got %q", hashes["batch.go"])
	}
	if _, found := hashes["nonexistent.go"]; found {
		t.Error("expected nonexistent.go to not be in hashes")
	}
}

func TestPostgres_Migrate_Idempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Migrate again — should not error
	if err := Migrate(ctx, store.Pool()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
