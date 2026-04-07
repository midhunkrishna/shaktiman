package core

import (
	"context"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/testutil"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestScopeToFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		scope        string
		excludeTests bool
		testOnly     bool
	}{
		{"impl", true, false},
		{"test", false, true},
		{"all", false, false},
		{"", true, false},       // default
		{"unknown", true, false}, // unknown treated as impl
	}
	for _, tt := range tests {
		f := ScopeToFilter(tt.scope)
		if f.ExcludeTests != tt.excludeTests || f.TestOnly != tt.testOnly {
			t.Errorf("ScopeToFilter(%q) = {ExcludeTests:%v, TestOnly:%v}, want {%v, %v}",
				tt.scope, f.ExcludeTests, f.TestOnly, tt.excludeTests, tt.testOnly)
		}
	}
}

// seedLookupStore creates a store with two files (one impl, one test),
// symbols, edges, and a diff entry for testing lookup functions.
func seedLookupStore(t *testing.T) types.WriterStore {
	t.Helper()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()

	// Impl file
	implID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		IsTest: false,
	})
	chunkIDs, _ := store.InsertChunks(ctx, implID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Serve",
			StartLine: 1, EndLine: 10, Content: "func Serve() {}", TokenCount: 5, ParseQuality: "full"},
		{ChunkIndex: 1, Kind: "function", SymbolName: "Handler",
			StartLine: 12, EndLine: 20, Content: "func Handler() {}", TokenCount: 5, ParseQuality: "full"},
	})
	symIDs, _ := store.InsertSymbols(ctx, implID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "Serve", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
		{ChunkID: chunkIDs[1], Name: "Handler", Kind: "method", Line: 12, Visibility: "exported", IsExported: true},
	})

	// Edge: Handler -> Serve
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, implID, []types.EdgeRecord{
			{SrcSymbolName: "Handler", DstSymbolName: "Serve", Kind: "calls"},
		}, map[string]int64{"Handler": symIDs[1], "Serve": symIDs[0]}, "go")
	})

	// Pending edge: Serve -> ExternalLib (unresolvable)
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, implID, []types.EdgeRecord{
			{SrcSymbolName: "Serve", DstSymbolName: "ExternalLib", Kind: "imports"},
		}, map[string]int64{"Serve": symIDs[0]}, "go")
	})

	// Diff entry
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		diffID, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: implID, ChangeType: "add", LinesAdded: 20, HashAfter: "h1",
		})
		if err != nil {
			return err
		}
		return store.InsertDiffSymbols(ctx, txh, diffID, []types.DiffSymbolEntry{
			{SymbolName: "Serve", ChangeType: "added"},
		})
	})

	// Test file
	testID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server_test.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		IsTest: true,
	})
	testChunkIDs, _ := store.InsertChunks(ctx, testID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "TestServe",
			StartLine: 1, EndLine: 10, Content: "func TestServe() {}", TokenCount: 5, ParseQuality: "full"},
	})
	store.InsertSymbols(ctx, testID, []types.SymbolRecord{
		{ChunkID: testChunkIDs[0], Name: "TestServe", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
	})

	// Diff for test file
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		_, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: testID, ChangeType: "add", LinesAdded: 10, HashAfter: "h2",
		})
		return err
	})

	return store
}

func TestLookupSymbols_Found(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	result, err := LookupSymbols(context.Background(), store, "Serve", "", TestFilter{})
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	if len(result.Definitions) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(result.Definitions))
	}
	if result.Definitions[0].Name != "Serve" {
		t.Errorf("expected Serve, got %q", result.Definitions[0].Name)
	}
}

func TestLookupSymbols_KindFilter(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	result, err := LookupSymbols(context.Background(), store, "Handler", "function", TestFilter{})
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	// Handler is kind "method", not "function" — should be filtered out
	if len(result.Definitions) != 0 {
		t.Errorf("expected 0 definitions with kind=function, got %d", len(result.Definitions))
	}
}

func TestLookupSymbols_ScopeExcludesTests(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	// scope=impl should exclude TestServe
	result, err := LookupSymbols(context.Background(), store, "TestServe", "", ScopeToFilter("impl"))
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	if len(result.Definitions) != 0 {
		t.Errorf("expected 0 definitions with scope=impl for test symbol, got %d", len(result.Definitions))
	}

	// scope=test should include TestServe
	result, err = LookupSymbols(context.Background(), store, "TestServe", "", ScopeToFilter("test"))
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	if len(result.Definitions) != 1 {
		t.Errorf("expected 1 definition with scope=test for test symbol, got %d", len(result.Definitions))
	}
}

func TestLookupSymbols_PendingEdgesFallback(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	result, err := LookupSymbols(context.Background(), store, "ExternalLib", "", TestFilter{})
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	if len(result.Definitions) != 0 {
		t.Errorf("expected 0 definitions for external symbol, got %d", len(result.Definitions))
	}
	if len(result.ReferencedBy) == 0 {
		t.Fatal("expected ReferencedBy to be populated via pending_edges fallback")
	}
	if result.ReferencedBy[0].Symbol != "Serve" {
		t.Errorf("expected referencing symbol Serve, got %q", result.ReferencedBy[0].Symbol)
	}
}

func TestLookupDependencies_Callers(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	results, err := LookupDependencies(context.Background(), store, "Serve", "incoming", 2, TestFilter{})
	if err != nil {
		t.Fatalf("LookupDependencies: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one caller of Serve")
	}
	found := false
	for _, r := range results {
		if r.Name == "Handler" {
			found = true
		}
	}
	if !found {
		t.Error("expected Handler as caller of Serve")
	}
}

func TestLookupDependencies_PendingEdgesFallback(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	results, err := LookupDependencies(context.Background(), store, "ExternalLib", "incoming", 2, TestFilter{})
	if err != nil {
		t.Fatalf("LookupDependencies: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected pending_edges fallback to find callers of ExternalLib")
	}
}

func TestLookupDependencies_ScopeTest(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	// scope=test — impl symbols should be excluded
	results, err := LookupDependencies(context.Background(), store, "Serve", "incoming", 2, ScopeToFilter("test"))
	if err != nil {
		t.Fatalf("LookupDependencies: %v", err)
	}
	for _, r := range results {
		if r.Name == "Handler" {
			t.Error("Handler (impl) should be excluded with scope=test")
		}
	}
}

func TestLookupDiffs_ScopeImpl(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	since := time.Now().Add(-1 * time.Hour)
	results, err := LookupDiffs(context.Background(), store, since, 50, ScopeToFilter("impl"))
	if err != nil {
		t.Fatalf("LookupDiffs: %v", err)
	}
	for _, r := range results {
		if r.FilePath == "server_test.go" {
			t.Error("test file diff should be excluded with scope=impl")
		}
	}
	// Should have the impl file diff
	found := false
	for _, r := range results {
		if r.FilePath == "server.go" {
			found = true
		}
	}
	if !found {
		t.Error("expected server.go diff in results")
	}
}

func TestGetSummary(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	result, err := GetSummary(context.Background(), store, nil)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2", result.TotalFiles)
	}
	if result.TotalChunks < 2 {
		t.Errorf("TotalChunks = %d, want >= 2", result.TotalChunks)
	}
}

func TestGetEnrichmentStatus_NoVector(t *testing.T) {
	t.Parallel()
	store := seedLookupStore(t)

	result, err := GetEnrichmentStatus(context.Background(), EnrichmentStatusInput{
		Store: store,
	})
	if err != nil {
		t.Fatalf("GetEnrichmentStatus: %v", err)
	}
	if result.EmbeddedChunks != 0 {
		t.Errorf("EmbeddedChunks = %d, want 0 with nil vector store", result.EmbeddedChunks)
	}
	if result.CircuitState != "n/a" {
		t.Errorf("CircuitState = %q, want n/a", result.CircuitState)
	}
	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2", result.TotalFiles)
	}
}
