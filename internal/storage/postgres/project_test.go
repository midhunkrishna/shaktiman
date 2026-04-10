//go:build postgres

package postgres

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// newProjectTestStore creates a fresh PgStore with migrations applied.
// Does NOT call EnsureProject — tests do that explicitly.
func newProjectTestStore(t *testing.T) *PgStore {
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

	pool := store.Pool()
	tables := []string{"embeddings", "diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config", "goose_db_version", "projects"}
	for _, table := range tables {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	t.Cleanup(func() { store.Close() })
	return store
}

// newProjectTestStorePair creates two PgStore instances sharing the same database
// but registered to different projects.
func newProjectTestStorePair(t *testing.T) (storeA, storeB *PgStore) {
	t.Helper()
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	ctx := context.Background()

	// First store creates the schema.
	storeA, err := NewPgStore(ctx, connStr, 5, 2, "public")
	if err != nil {
		t.Fatalf("NewPgStore A: %v", err)
	}
	pool := storeA.Pool()
	tables := []string{"embeddings", "diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config", "goose_db_version", "projects"}
	for _, table := range tables {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := storeA.EnsureProject(ctx, "/tmp/project-A"); err != nil {
		t.Fatalf("EnsureProject A: %v", err)
	}

	// Second store shares the same DB but different project.
	storeB, err = NewPgStore(ctx, connStr, 5, 2, "public")
	if err != nil {
		t.Fatalf("NewPgStore B: %v", err)
	}
	if err := storeB.EnsureProject(ctx, "/tmp/project-B"); err != nil {
		t.Fatalf("EnsureProject B: %v", err)
	}

	t.Cleanup(func() {
		storeA.Close()
		storeB.Close()
	})
	return storeA, storeB
}

func TestEnsureProject_Idempotent(t *testing.T) {
	store := newProjectTestStore(t)
	ctx := context.Background()

	if err := store.EnsureProject(ctx, "/tmp/test-idempotent"); err != nil {
		t.Fatalf("first EnsureProject: %v", err)
	}
	id1 := store.ProjectID()

	if err := store.EnsureProject(ctx, "/tmp/test-idempotent"); err != nil {
		t.Fatalf("second EnsureProject: %v", err)
	}
	id2 := store.ProjectID()

	if id1 != id2 {
		t.Errorf("idempotent IDs differ: %d vs %d", id1, id2)
	}
}

func TestEnsureProject_DifferentPaths(t *testing.T) {
	store := newProjectTestStore(t)
	ctx := context.Background()

	if err := store.EnsureProject(ctx, "/tmp/proj-alpha"); err != nil {
		t.Fatalf("EnsureProject alpha: %v", err)
	}
	idA := store.ProjectID()

	if err := store.EnsureProject(ctx, "/tmp/proj-beta"); err != nil {
		t.Fatalf("EnsureProject beta: %v", err)
	}
	idB := store.ProjectID()

	if idA == idB {
		t.Errorf("different paths got same ID: %d", idA)
	}
}

func TestEnsureProject_ConcurrentStart(t *testing.T) {
	store := newProjectTestStore(t)
	ctx := context.Background()

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := NewPgStore(ctx,
				os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL"), 2, 1, "public")
			if err != nil {
				errs[i] = err
				return
			}
			defer s.Close()
			errs[i] = s.EnsureProject(ctx, "/tmp/concurrent-project")
		}(i)
	}
	wg.Wait()
	_ = store // keep original store alive

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

func TestMultiProject_FileIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	// Both projects upsert "main.go" with different content.
	_, err := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hash-A", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile A: %v", err)
	}

	_, err = storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hash-B", Mtime: 2.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile B: %v", err)
	}

	// ListFiles should return only own project's files.
	filesA, _ := storeA.ListFiles(ctx)
	filesB, _ := storeB.ListFiles(ctx)
	if len(filesA) != 1 || filesA[0].ContentHash != "hash-A" {
		t.Errorf("project A: expected 1 file with hash-A, got %d files", len(filesA))
	}
	if len(filesB) != 1 || filesB[0].ContentHash != "hash-B" {
		t.Errorf("project B: expected 1 file with hash-B, got %d files", len(filesB))
	}

	// GetFileByPath should return own version.
	fA, _ := storeA.GetFileByPath(ctx, "main.go")
	fB, _ := storeB.GetFileByPath(ctx, "main.go")
	if fA == nil || fA.ContentHash != "hash-A" {
		t.Error("project A GetFileByPath returned wrong file")
	}
	if fB == nil || fB.ContentHash != "hash-B" {
		t.Error("project B GetFileByPath returned wrong file")
	}
}

func TestMultiProject_SymbolIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	// Insert file + symbol in each project.
	fidA, _ := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hA", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	cidA, _ := storeA.InsertChunks(ctx, fidA, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "main", Kind: "function", StartLine: 1, EndLine: 5, Content: "func main() {}", TokenCount: 3},
	})
	storeA.InsertSymbols(ctx, fidA, []types.SymbolRecord{
		{ChunkID: cidA[0], Name: "main", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
	})

	fidB, _ := storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hB", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	cidB, _ := storeB.InsertChunks(ctx, fidB, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "main", Kind: "function", StartLine: 1, EndLine: 10, Content: "func main() { different }", TokenCount: 5},
	})
	storeB.InsertSymbols(ctx, fidB, []types.SymbolRecord{
		{ChunkID: cidB[0], Name: "main", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
	})

	// GetSymbolByName should return only own project's symbols.
	symsA, _ := storeA.GetSymbolByName(ctx, "main")
	symsB, _ := storeB.GetSymbolByName(ctx, "main")
	if len(symsA) != 1 {
		t.Errorf("project A: expected 1 symbol, got %d", len(symsA))
	}
	if len(symsB) != 1 {
		t.Errorf("project B: expected 1 symbol, got %d", len(symsB))
	}
	if len(symsA) > 0 && symsA[0].FileID != fidA {
		t.Error("project A symbol has wrong fileID")
	}
	if len(symsB) > 0 && symsB[0].FileID != fidB {
		t.Error("project B symbol has wrong fileID")
	}
}

func TestMultiProject_FTSIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	fidA, _ := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "auth.go", ContentHash: "hA", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	storeA.InsertChunks(ctx, fidA, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "authenticate", Kind: "function", StartLine: 1, EndLine: 5,
			Content: "func authenticate() { validate token }", TokenCount: 5},
	})

	fidB, _ := storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "auth.go", ContentHash: "hB", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	storeB.InsertChunks(ctx, fidB, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "login", Kind: "function", StartLine: 1, EndLine: 5,
			Content: "func login() { authenticate user }", TokenCount: 5},
	})

	// KeywordSearch should return only own project's chunks.
	resultsA, _ := storeA.KeywordSearch(ctx, "authenticate", 10)
	resultsB, _ := storeB.KeywordSearch(ctx, "authenticate", 10)

	if len(resultsA) != 1 {
		t.Errorf("project A FTS: expected 1 result, got %d", len(resultsA))
	}
	if len(resultsB) != 1 {
		t.Errorf("project B FTS: expected 1 result, got %d", len(resultsB))
	}
}

func TestMultiProject_StatsIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	// Add 2 files to project A, 1 file to project B.
	for _, path := range []string{"a.go", "b.go"} {
		storeA.UpsertFile(ctx, &types.FileRecord{
			Path: path, ContentHash: "h", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
	}
	storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "c.go", ContentHash: "h", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	statsA, _ := storeA.GetIndexStats(ctx)
	statsB, _ := storeB.GetIndexStats(ctx)

	if statsA.TotalFiles != 2 {
		t.Errorf("project A stats: expected 2 files, got %d", statsA.TotalFiles)
	}
	if statsB.TotalFiles != 1 {
		t.Errorf("project B stats: expected 1 file, got %d", statsB.TotalFiles)
	}
}

func TestMultiProject_DiffIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	fidA, _ := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hA", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	fidB, _ := storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hB", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	// Insert diffs in both projects.
	storeA.WithWriteTx(ctx, func(txh types.TxHandle) error {
		storeA.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fidA, ChangeType: "add", LinesAdded: 10, HashAfter: "hA",
		})
		return nil
	})
	storeB.WithWriteTx(ctx, func(txh types.TxHandle) error {
		storeB.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fidB, ChangeType: "add", LinesAdded: 20, HashAfter: "hB",
		})
		return nil
	})

	// GetRecentDiffs without FileID should return only own project's diffs.
	since := time.Now().Add(-1 * time.Hour)
	diffsA, _ := storeA.GetRecentDiffs(ctx, types.RecentDiffsInput{Since: since})
	diffsB, _ := storeB.GetRecentDiffs(ctx, types.RecentDiffsInput{Since: since})

	if len(diffsA) != 1 || diffsA[0].LinesAdded != 10 {
		t.Errorf("project A diffs: expected 1 diff with 10 lines, got %d diffs", len(diffsA))
	}
	if len(diffsB) != 1 || diffsB[0].LinesAdded != 20 {
		t.Errorf("project B diffs: expected 1 diff with 20 lines, got %d diffs", len(diffsB))
	}
}

func TestMultiProject_EdgeResolution(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	// Project A has symbol "Helper".
	fidA, _ := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "helper.go", ContentHash: "hA", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	cidA, _ := storeA.InsertChunks(ctx, fidA, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Helper", Kind: "function", StartLine: 1, EndLine: 5, Content: "func Helper() {}", TokenCount: 3},
	})
	storeA.InsertSymbols(ctx, fidA, []types.SymbolRecord{
		{ChunkID: cidA[0], Name: "Helper", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
	})

	// Project B has a pending edge to "Helper" — should NOT resolve to A's symbol.
	fidB, _ := storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "caller.go", ContentHash: "hB", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	cidB, _ := storeB.InsertChunks(ctx, fidB, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Caller", Kind: "function", StartLine: 1, EndLine: 5, Content: "func Caller() { Helper() }", TokenCount: 5},
	})
	symIDs, _ := storeB.InsertSymbols(ctx, fidB, []types.SymbolRecord{
		{ChunkID: cidB[0], Name: "Caller", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
	})

	symbolIDMap := map[string]int64{"Caller": symIDs[0]}
	edges := []types.EdgeRecord{{SrcSymbolName: "Caller", DstSymbolName: "Helper", Kind: "calls"}}

	storeB.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return storeB.InsertEdges(ctx, txh, fidB, edges, symbolIDMap, "go")
	})

	// The edge to "Helper" should be pending (not resolved to A's symbol).
	callers, _ := storeB.PendingEdgeCallers(ctx, "Helper")
	if len(callers) != 1 {
		t.Errorf("expected 1 pending caller for Helper in project B, got %d", len(callers))
	}
}

func TestMultiProject_EmbedPageIsolation(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	fidA, _ := storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "hA", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	storeA.InsertChunks(ctx, fidA, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "FuncA", Kind: "function", StartLine: 1, EndLine: 5, Content: "func A() {}", TokenCount: 3},
	})

	fidB, _ := storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "b.go", ContentHash: "hB", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	storeB.InsertChunks(ctx, fidB, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "FuncB", Kind: "function", StartLine: 1, EndLine: 5, Content: "func B() {}", TokenCount: 3},
	})

	pageA, _ := storeA.GetEmbedPage(ctx, 0, 100)
	pageB, _ := storeB.GetEmbedPage(ctx, 0, 100)

	if len(pageA) != 1 {
		t.Errorf("project A embed page: expected 1, got %d", len(pageA))
	}
	if len(pageB) != 1 {
		t.Errorf("project B embed page: expected 1, got %d", len(pageB))
	}
}

func TestMultiProject_BatchGetFileHashes(t *testing.T) {
	storeA, storeB := newProjectTestStorePair(t)
	ctx := context.Background()

	storeA.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hash-A", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	storeB.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "hash-B", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	hashesA, _ := storeA.BatchGetFileHashes(ctx, []string{"main.go"})
	hashesB, _ := storeB.BatchGetFileHashes(ctx, []string{"main.go"})

	if hashesA["main.go"] != "hash-A" {
		t.Errorf("project A hash: expected hash-A, got %s", hashesA["main.go"])
	}
	if hashesB["main.go"] != "hash-B" {
		t.Errorf("project B hash: expected hash-B, got %s", hashesB["main.go"])
	}
}

func TestBackwardCompat_DefaultProject(t *testing.T) {
	store := newProjectTestStore(t)
	ctx := context.Background()

	// The migration seeds a default project with id=1, root_path='__default__'.
	// EnsureProject should claim it.
	if err := store.EnsureProject(ctx, "/tmp/first-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// Should have claimed the default (id=1).
	if store.ProjectID() != 1 {
		t.Errorf("expected project ID 1 (claimed default), got %d", store.ProjectID())
	}

	// Verify the root_path was updated.
	var rootPath string
	store.Pool().QueryRow(ctx, "SELECT root_path FROM projects WHERE id = 1").Scan(&rootPath)
	if rootPath == "__default__" {
		t.Error("default project root_path was not claimed")
	}
}
