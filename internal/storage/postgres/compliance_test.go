//go:build postgres

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
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

func TestPostgres_ResolvePendingEdges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// File A has a call to Unknown (pending edge)
	fileA, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksA, _ := store.InsertChunks(ctx, fileA, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Caller", StartLine: 1, EndLine: 5,
			Content: "func Caller() {}", TokenCount: 3},
	})
	symsA, _ := store.InsertSymbols(ctx, fileA, []types.SymbolRecord{
		{ChunkID: chunksA[0], Name: "Caller", Kind: "function", Line: 1, Visibility: "exported"},
	})

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileA, []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Target", Kind: "calls"},
		}, map[string]int64{"Caller": symsA[0]}, "go")
	})

	// Verify pending
	callers, _ := store.PendingEdgeCallers(ctx, "Target")
	if len(callers) != 1 {
		t.Fatalf("expected 1 pending caller, got %d", len(callers))
	}

	// File B introduces Target — resolve pending edges
	fileB, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "b.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksB, _ := store.InsertChunks(ctx, fileB, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Target", StartLine: 1, EndLine: 5,
			Content: "func Target() {}", TokenCount: 3},
	})
	store.InsertSymbols(ctx, fileB, []types.SymbolRecord{
		{ChunkID: chunksB[0], Name: "Target", Kind: "function", Line: 1, Visibility: "exported"},
	})

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.ResolvePendingEdges(ctx, txh, []string{"Target"})
	})

	// Pending should be resolved
	callers, _ = store.PendingEdgeCallers(ctx, "Target")
	if len(callers) != 0 {
		t.Errorf("expected 0 pending callers after resolve, got %d", len(callers))
	}

	// Should now have a real edge
	neighbors, _ := store.Neighbors(ctx, symsA[0], 1, "outgoing")
	if len(neighbors) == 0 {
		t.Error("expected resolved edge from Caller to Target")
	}
}

func TestPostgres_DeleteEdgesByFile(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "del.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "X", StartLine: 1, EndLine: 5,
			Content: "func X() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "Y", StartLine: 6, EndLine: 10,
			Content: "func Y() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "X", Kind: "function", Line: 1, Visibility: "exported"},
		{ChunkID: chunkIDs[1], Name: "Y", Kind: "function", Line: 6, Visibility: "exported"},
	})

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "X", DstSymbolName: "Y", Kind: "calls"},
		}, map[string]int64{"X": symIDs[0], "Y": symIDs[1]}, "go")
	})

	// Verify edge exists
	neighbors, _ := store.Neighbors(ctx, symIDs[0], 1, "outgoing")
	if len(neighbors) != 1 {
		t.Fatalf("expected 1 neighbor before delete, got %d", len(neighbors))
	}

	// Delete edges
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.DeleteEdgesByFile(ctx, txh, fileID)
	})

	neighbors, _ = store.Neighbors(ctx, symIDs[0], 1, "outgoing")
	if len(neighbors) != 0 {
		t.Errorf("expected 0 neighbors after delete, got %d", len(neighbors))
	}
}

func TestPostgres_Neighbors_AllDirections(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "nav.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "A", StartLine: 1, EndLine: 5,
			Content: "func A() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "B", StartLine: 6, EndLine: 10,
			Content: "func B() {}", TokenCount: 3},
		{ChunkIndex: 2, Kind: "function", SymbolName: "C", StartLine: 11, EndLine: 15,
			Content: "func C() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "A", Kind: "function", Line: 1, Visibility: "exported"},
		{ChunkID: chunkIDs[1], Name: "B", Kind: "function", Line: 6, Visibility: "exported"},
		{ChunkID: chunkIDs[2], Name: "C", Kind: "function", Line: 11, Visibility: "exported"},
	})

	// A → B → C
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "A", DstSymbolName: "B", Kind: "calls"},
			{SrcSymbolName: "B", DstSymbolName: "C", Kind: "calls"},
		}, map[string]int64{"A": symIDs[0], "B": symIDs[1], "C": symIDs[2]}, "go")
	})

	// Outgoing from A: should find B
	out, err := store.Neighbors(ctx, symIDs[0], 1, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors outgoing: %v", err)
	}
	if len(out) != 1 || out[0] != symIDs[1] {
		t.Errorf("outgoing from A: got %v, want [%d]", out, symIDs[1])
	}

	// Incoming to C: should find B
	inc, err := store.Neighbors(ctx, symIDs[2], 1, "incoming")
	if err != nil {
		t.Fatalf("Neighbors incoming: %v", err)
	}
	if len(inc) != 1 || inc[0] != symIDs[1] {
		t.Errorf("incoming to C: got %v, want [%d]", inc, symIDs[1])
	}

	// Both from B: should find A and C
	both, err := store.Neighbors(ctx, symIDs[1], 1, "both")
	if err != nil {
		t.Fatalf("Neighbors both: %v", err)
	}
	if len(both) != 2 {
		t.Errorf("both from B: got %d neighbors, want 2", len(both))
	}

	// Depth 2 from A: should find B and C
	deep, err := store.Neighbors(ctx, symIDs[0], 2, "outgoing")
	if err != nil {
		t.Fatalf("Neighbors depth 2: %v", err)
	}
	if len(deep) != 2 {
		t.Errorf("depth 2 from A: got %d, want 2", len(deep))
	}

	// Invalid direction
	_, err = store.Neighbors(ctx, symIDs[0], 1, "sideways")
	if err == nil {
		t.Error("expected error for invalid direction")
	}
}

func TestPostgres_ComputeChangeScores(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "score.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "S", StartLine: 1, EndLine: 5,
			Content: "func S() {}", TokenCount: 3},
	})

	// Insert a diff
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		diffID, _ := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "modify", LinesAdded: 50, LinesRemoved: 10,
			HashBefore: "h0", HashAfter: "h1",
		})
		return store.InsertDiffSymbols(ctx, txh, diffID, []types.DiffSymbolEntry{
			{SymbolName: "S", ChangeType: "modified", ChunkID: chunkIDs[0]},
		})
	})

	scores, err := store.ComputeChangeScores(ctx, chunkIDs)
	if err != nil {
		t.Fatalf("ComputeChangeScores: %v", err)
	}
	if len(scores) == 0 {
		t.Error("expected non-empty scores")
	}
	if scores[chunkIDs[0]] <= 0 {
		t.Errorf("expected positive score, got %f", scores[chunkIDs[0]])
	}

	// Empty input
	empty, err := store.ComputeChangeScores(ctx, nil)
	if err != nil {
		t.Fatalf("ComputeChangeScores empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty scores for nil input, got %d", len(empty))
	}
}

func TestPostgres_GetEmbeddedChunkIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "emb.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "E1", StartLine: 1, EndLine: 5,
			Content: "func E1() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "E2", StartLine: 6, EndLine: 10,
			Content: "func E2() {}", TokenCount: 3},
	})

	// Mark as embedded
	store.MarkChunksEmbedded(ctx, chunkIDs)

	// Get embedded IDs with cursor
	ids, err := store.GetEmbeddedChunkIDs(ctx, 0, 10)
	if err != nil {
		t.Fatalf("GetEmbeddedChunkIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 embedded IDs, got %d", len(ids))
	}

	// Cursor pagination
	ids2, _ := store.GetEmbeddedChunkIDs(ctx, ids[0], 10)
	if len(ids2) != 1 {
		t.Errorf("expected 1 ID after cursor, got %d", len(ids2))
	}
}

func TestPostgres_ResetEmbeddedFlags_Targeted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "reset.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "R1", StartLine: 1, EndLine: 5,
			Content: "func R1() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "R2", StartLine: 6, EndLine: 10,
			Content: "func R2() {}", TokenCount: 3},
	})

	// Mark both as embedded
	store.MarkChunksEmbedded(ctx, chunkIDs)
	count, _ := store.CountChunksEmbedded(ctx)
	if count != 2 {
		t.Fatalf("expected 2 embedded, got %d", count)
	}

	// Reset only the first one
	err := store.ResetEmbeddedFlags(ctx, []int64{chunkIDs[0]})
	if err != nil {
		t.Fatalf("ResetEmbeddedFlags: %v", err)
	}

	count, _ = store.CountChunksEmbedded(ctx)
	if count != 1 {
		t.Errorf("expected 1 embedded after targeted reset, got %d", count)
	}

	// File should be 'partial' now
	f, _ := store.GetFileByPath(ctx, "reset.go")
	if f.EmbeddingStatus != "partial" {
		t.Errorf("embedding_status = %q, want partial", f.EmbeddingStatus)
	}

	// Reset empty slice — no-op
	err = store.ResetEmbeddedFlags(ctx, nil)
	if err != nil {
		t.Fatalf("ResetEmbeddedFlags empty: %v", err)
	}
}

func TestPostgres_BatchNeighbors(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "bn.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "P", StartLine: 1, EndLine: 5,
			Content: "func P() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "Q", StartLine: 6, EndLine: 10,
			Content: "func Q() {}", TokenCount: 3},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "P", Kind: "function", Line: 1, Visibility: "exported"},
		{ChunkID: chunkIDs[1], Name: "Q", Kind: "function", Line: 6, Visibility: "exported"},
	})

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "P", DstSymbolName: "Q", Kind: "calls"},
		}, map[string]int64{"P": symIDs[0], "Q": symIDs[1]}, "go")
	})

	result, err := store.BatchNeighbors(ctx, symIDs, 1)
	if err != nil {
		t.Fatalf("BatchNeighbors: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result))
	}
	if len(result[symIDs[0]]) == 0 {
		t.Error("expected neighbors for P")
	}
}

func TestPostgres_WithWriteTx_Rollback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "rb.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	err := store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "add", LinesAdded: 1, HashAfter: "h1",
		})
		return fmt.Errorf("forced rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Diff should not exist
	diffs, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: fileID})
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs after rollback, got %d", len(diffs))
	}
}

func TestPostgres_GetRecentDiffs_WithFileFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	f1, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "f1.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	f2, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "f2.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		store.InsertDiffLog(ctx, txh, types.DiffLogEntry{FileID: f1, ChangeType: "add", LinesAdded: 1, HashAfter: "h1"})
		store.InsertDiffLog(ctx, txh, types.DiffLogEntry{FileID: f2, ChangeType: "add", LinesAdded: 2, HashAfter: "h2"})
		return nil
	})

	// All diffs
	all, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{})
	if len(all) < 2 {
		t.Errorf("expected >= 2 diffs, got %d", len(all))
	}

	// Filter by file
	filtered, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: f1})
	if len(filtered) != 1 {
		t.Errorf("expected 1 diff for f1, got %d", len(filtered))
	}

	// With limit
	limited, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{Limit: 1})
	if len(limited) != 1 {
		t.Errorf("expected 1 diff with limit, got %d", len(limited))
	}
}

func TestPostgres_NewPgStore_InvalidConnStr(t *testing.T) {
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	_, err := NewPgStore(context.Background(), "postgres://invalid:5432/nonexistent?connect_timeout=1", 1, 1, "public")
	if err == nil {
		t.Error("expected error for invalid connection")
	}
}

func TestPostgres_ComputeChangeScores_FileLevelFallback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "fallback.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "NoSymDiff", StartLine: 1, EndLine: 5,
			Content: "func NoSymDiff() {}", TokenCount: 3},
	})

	// Insert diff_log WITHOUT diff_symbols — forces file-level fallback path
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		_, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "modify", LinesAdded: 30, LinesRemoved: 5,
			HashBefore: "h0", HashAfter: "h1",
		})
		return err
	})

	scores, err := store.ComputeChangeScores(ctx, chunkIDs)
	if err != nil {
		t.Fatalf("ComputeChangeScores fallback: %v", err)
	}
	if scores[chunkIDs[0]] <= 0 {
		t.Errorf("expected positive score from file-level fallback, got %f", scores[chunkIDs[0]])
	}
}

func TestPostgres_InsertEdges_CrossFileLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// File A has symbol Caller
	fileA, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "caller.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksA, _ := store.InsertChunks(ctx, fileA, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Caller", StartLine: 1, EndLine: 5,
			Content: "func Caller() {}", TokenCount: 3},
	})
	symsA, _ := store.InsertSymbols(ctx, fileA, []types.SymbolRecord{
		{ChunkID: chunksA[0], Name: "Caller", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// File B has symbol Target (different file, same language)
	fileB, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "target.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksB, _ := store.InsertChunks(ctx, fileB, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Target", StartLine: 1, EndLine: 5,
			Content: "func Target() {}", TokenCount: 3},
	})
	store.InsertSymbols(ctx, fileB, []types.SymbolRecord{
		{ChunkID: chunksB[0], Name: "Target", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// Insert edge from Caller to Target — Target is NOT in symbolIDs map,
	// so lookupSymbolIDPg must find it via same-language lookup
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileA, []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Target", Kind: "calls"},
		}, map[string]int64{"Caller": symsA[0]}, "go")
	})

	// Edge should be resolved (not pending)
	neighbors, _ := store.Neighbors(ctx, symsA[0], 1, "outgoing")
	if len(neighbors) != 1 {
		t.Errorf("expected resolved edge via cross-file lookup, got %d neighbors", len(neighbors))
	}
	pending, _ := store.PendingEdgeCallers(ctx, "Target")
	if len(pending) != 0 {
		t.Errorf("expected no pending edges, got %d", len(pending))
	}
}

func TestPostgres_InsertEdges_GlobalFallbackLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// File A in Go
	fileA, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksA, _ := store.InsertChunks(ctx, fileA, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Src", StartLine: 1, EndLine: 5,
			Content: "func Src() {}", TokenCount: 3},
	})
	symsA, _ := store.InsertSymbols(ctx, fileA, []types.SymbolRecord{
		{ChunkID: chunksA[0], Name: "Src", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// File B — no language (simulates pre-migration data)
	fileB, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "b.go", ContentHash: "h2", Mtime: 1.0,
		Language: "", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunksB, _ := store.InsertChunks(ctx, fileB, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Dst", StartLine: 1, EndLine: 5,
			Content: "func Dst() {}", TokenCount: 3},
	})
	store.InsertSymbols(ctx, fileB, []types.SymbolRecord{
		{ChunkID: chunksB[0], Name: "Dst", Kind: "function", Line: 1, Visibility: "exported"},
	})

	// Insert edge with empty language — triggers global fallback in lookupSymbolIDPg
	store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileA, []types.EdgeRecord{
			{SrcSymbolName: "Src", DstSymbolName: "Dst", Kind: "calls"},
		}, map[string]int64{"Src": symsA[0]}, "")
	})

	neighbors, _ := store.Neighbors(ctx, symsA[0], 1, "outgoing")
	if len(neighbors) != 1 {
		t.Errorf("expected resolved edge via global fallback, got %d neighbors", len(neighbors))
	}
}

func TestPostgres_EmbeddingReadiness_NoChunks(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	readiness, err := store.EmbeddingReadiness(ctx, 0)
	if err != nil {
		t.Fatalf("EmbeddingReadiness: %v", err)
	}
	if readiness != 0 {
		t.Errorf("expected 0 readiness with no chunks, got %f", readiness)
	}
}

func TestPostgres_Registration(t *testing.T) {
	// Verify the "postgres" backend is registered when built with the tag
	if !storage.HasMetadataStore("postgres") {
		t.Error("expected 'postgres' to be registered")
	}
}

func TestPostgres_RegistrationFactory(t *testing.T) {
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	// Clean DB
	ctx := context.Background()
	tempStore, _ := NewPgStore(ctx, connStr, 2, 1, "public")
	tables := []string{"diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config"}
	for _, table := range tables {
		tempStore.Pool().Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}
	tempStore.Close()

	store, lifecycle, closer, err := storage.NewMetadataStore(storage.MetadataStoreConfig{
		Backend:         "postgres",
		PostgresConnStr: connStr,
		PostgresMaxOpen: 5,
		PostgresMaxIdle: 2,
		PostgresSchema:  "public",
	})
	if err != nil {
		t.Fatalf("NewMetadataStore postgres: %v", err)
	}
	defer closer()

	// Postgres returns nil lifecycle
	if lifecycle != nil {
		t.Error("expected nil lifecycle for postgres")
	}

	// Store should be functional
	_, err = store.UpsertFile(ctx, &types.FileRecord{
		Path: "reg.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile via registry: %v", err)
	}
}

func TestPgStore_PurgeAll(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Seed data
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "purge.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	store.UpsertChunk(ctx, types.ChunkRecord{
		FileID: fileID, ChunkIndex: 0, Kind: "function",
		StartLine: 1, EndLine: 10, Content: "func Foo() {}", TokenCount: 5,
	})

	stats, err := store.GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles == 0 || stats.TotalChunks == 0 {
		t.Fatal("expected seeded data before purge")
	}

	// Purge
	if err := store.PurgeAll(ctx); err != nil {
		t.Fatalf("PurgeAll: %v", err)
	}

	// All data tables should be empty
	stats, err = store.GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats after purge: %v", err)
	}
	if stats.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d after purge, want 0", stats.TotalFiles)
	}
	if stats.TotalChunks != 0 {
		t.Errorf("TotalChunks = %d after purge, want 0", stats.TotalChunks)
	}

	// goose_db_version should survive
	var count int
	err = store.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM goose_db_version").Scan(&count)
	if err != nil {
		t.Fatalf("goose_db_version query: %v", err)
	}
	if count == 0 {
		t.Error("goose_db_version should not be empty after purge")
	}

	// Store should still be usable
	_, err = store.UpsertFile(ctx, &types.FileRecord{
		Path: "after.go", ContentHash: "h2", Mtime: 2.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile after purge: %v", err)
	}
}
