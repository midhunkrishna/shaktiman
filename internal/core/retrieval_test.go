//go:build sqlite_fts5

package core

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestHydrateFTSResults_MissingChunk(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert a real file and chunk.
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "funcA", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content: "func A() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// Create FTS results: one valid, one with a nonexistent chunk ID.
	ftsResults := []types.FTSResult{
		{ChunkID: 99999, Rank: -10.0},      // Missing chunk -- should be skipped
		{ChunkID: chunkIDs[0], Rank: -5.0},  // Valid chunk
	}

	results, err := hydrateFTSResults(ctx, store, ftsResults, TestFilter{})
	if err != nil {
		t.Fatalf("hydrateFTSResults: %v", err)
	}

	// Only the valid chunk should be in results.
	if len(results) != 1 {
		t.Fatalf("expected 1 result (skipping missing chunk), got %d", len(results))
	}
	if results[0].ChunkID != chunkIDs[0] {
		t.Errorf("ChunkID = %d, want %d", results[0].ChunkID, chunkIDs[0])
	}
}

// nonBatchStore wraps a WriterStore but hides the BatchMetadataStore interface,
// forcing callers to use the legacy per-item query path.
type nonBatchStore struct {
	types.WriterStore
}

func TestHydrateFTSResults_LegacyPath(t *testing.T) {
	t.Parallel()
	store := &nonBatchStore{setupTestStore(t)}
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "legacy.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "LegacyFunc", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func LegacyFunc() {}", TokenCount: 5},
	})

	ftsResults := []types.FTSResult{
		{ChunkID: chunkIDs[0], Rank: -10.0},
	}

	results, err := hydrateFTSResults(ctx, store, ftsResults, TestFilter{})
	if err != nil {
		t.Fatalf("hydrateFTSResults legacy: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Path != "legacy.go" {
		t.Errorf("Path = %q, want legacy.go", results[0].Path)
	}
}

func TestHydrateFTSResults_LegacyPath_WithFilter(t *testing.T) {
	t.Parallel()
	store := &nonBatchStore{setupTestStore(t)}
	ctx := context.Background()

	testFileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "legacy_test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	implFileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "legacy.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: false,
	})

	testChunkIDs, _ := store.InsertChunks(ctx, testFileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "TestLegacy", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func TestLegacy() {}", TokenCount: 5},
	})
	implChunkIDs, _ := store.InsertChunks(ctx, implFileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Legacy", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func Legacy() {}", TokenCount: 5},
	})

	ftsResults := []types.FTSResult{
		{ChunkID: testChunkIDs[0], Rank: -10.0},
		{ChunkID: implChunkIDs[0], Rank: -5.0},
	}

	// ExcludeTests via legacy path
	results, err := hydrateFTSResults(ctx, store, ftsResults, TestFilter{ExcludeTests: true})
	if err != nil {
		t.Fatalf("legacy ExcludeTests: %v", err)
	}
	if len(results) != 1 || results[0].Path != "legacy.go" {
		t.Errorf("ExcludeTests: expected only legacy.go, got %v", results)
	}

	// TestOnly via legacy path
	results, err = hydrateFTSResults(ctx, store, ftsResults, TestFilter{TestOnly: true})
	if err != nil {
		t.Fatalf("legacy TestOnly: %v", err)
	}
	if len(results) != 1 || results[0].Path != "legacy_test.go" {
		t.Errorf("TestOnly: expected only legacy_test.go, got %v", results)
	}
}

func TestHydrateFTSResults_ExcludeTests(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert a test file and an impl file
	testFileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server_test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	implFileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: false,
	})

	testChunkIDs, _ := store.InsertChunks(ctx, testFileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "TestServe", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func TestServe() {}", TokenCount: 5},
	})
	implChunkIDs, _ := store.InsertChunks(ctx, implFileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Serve", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func Serve() {}", TokenCount: 5},
	})

	ftsResults := []types.FTSResult{
		{ChunkID: testChunkIDs[0], Rank: -10.0},
		{ChunkID: implChunkIDs[0], Rank: -5.0},
	}

	// ExcludeTests: should only return impl chunk
	results, err := hydrateFTSResults(ctx, store, ftsResults, TestFilter{ExcludeTests: true})
	if err != nil {
		t.Fatalf("hydrateFTSResults ExcludeTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ExcludeTests: expected 1 result, got %d", len(results))
	}
	if results[0].Path != "server.go" {
		t.Errorf("ExcludeTests: got path %q, want server.go", results[0].Path)
	}

	// TestOnly: should only return test chunk
	results, err = hydrateFTSResults(ctx, store, ftsResults, TestFilter{TestOnly: true})
	if err != nil {
		t.Fatalf("hydrateFTSResults TestOnly: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("TestOnly: expected 1 result, got %d", len(results))
	}
	if results[0].Path != "server_test.go" {
		t.Errorf("TestOnly: got path %q, want server_test.go", results[0].Path)
	}

	// No filter: should return both
	results, err = hydrateFTSResults(ctx, store, ftsResults, TestFilter{})
	if err != nil {
		t.Fatalf("hydrateFTSResults no filter: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("no filter: expected 2 results, got %d", len(results))
	}
}

func TestExpandSplitSiblings_MergesFragments(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "action.java", ContentHash: "h1", Mtime: 1.0,
		Language: "java", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	// Simulate a method split into 3 fragments
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "header", SymbolName: "",
			StartLine: 1, EndLine: 5, Content: "package foo;", TokenCount: 5},
		{ChunkIndex: 1, Kind: "method", SymbolName: "bigMethod",
			StartLine: 10, EndLine: 50, Content: "// part 1\nboolean flag = detect();", TokenCount: 500},
		{ChunkIndex: 2, Kind: "method", SymbolName: "bigMethod",
			StartLine: 51, EndLine: 100, Content: "// part 2\nrate = calculate();", TokenCount: 500},
		{ChunkIndex: 3, Kind: "method", SymbolName: "bigMethod",
			StartLine: 101, EndLine: 130, Content: "// part 3\ndiceRoll = flag ? 1.0 : rand();", TokenCount: 300},
		{ChunkIndex: 4, Kind: "method", SymbolName: "smallMethod",
			StartLine: 135, EndLine: 145, Content: "void smallMethod() {}", TokenCount: 50},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// Only one fragment matched the search, plus the small method
	input := []types.ScoredResult{
		{ChunkID: chunkIDs[1], Score: 0.8, Path: "action.java",
			SymbolName: "bigMethod", Kind: "method",
			StartLine: 10, EndLine: 50, Content: "// part 1\nboolean flag = detect();", TokenCount: 500},
		{ChunkID: chunkIDs[4], Score: 0.5, Path: "action.java",
			SymbolName: "smallMethod", Kind: "method",
			StartLine: 135, EndLine: 145, Content: "void smallMethod() {}", TokenCount: 50},
	}

	expanded := ExpandSplitSiblings(ctx, store, input)

	if len(expanded) != 2 {
		t.Fatalf("expected 2 results (1 merged + 1 unchanged), got %d", len(expanded))
	}

	// Find the merged result
	var mergedResult *types.ScoredResult
	for i, r := range expanded {
		if r.SymbolName == "bigMethod" {
			mergedResult = &expanded[i]
			break
		}
	}
	if mergedResult == nil {
		t.Fatal("merged bigMethod result not found")
	}

	// Verify merged boundaries
	if mergedResult.StartLine != 10 {
		t.Errorf("merged StartLine = %d, want 10", mergedResult.StartLine)
	}
	if mergedResult.EndLine != 130 {
		t.Errorf("merged EndLine = %d, want 130", mergedResult.EndLine)
	}
	if mergedResult.TokenCount != 1300 {
		t.Errorf("merged TokenCount = %d, want 1300", mergedResult.TokenCount)
	}
	if mergedResult.Score != 0.8 {
		t.Errorf("merged Score = %f, want 0.8", mergedResult.Score)
	}

	// Verify content includes all parts
	if !contains(mergedResult.Content, "part 1") ||
		!contains(mergedResult.Content, "part 2") ||
		!contains(mergedResult.Content, "part 3") {
		t.Error("merged content should include all 3 parts")
	}

	// smallMethod should be unchanged
	var smallResult *types.ScoredResult
	for i, r := range expanded {
		if r.SymbolName == "smallMethod" {
			smallResult = &expanded[i]
			break
		}
	}
	if smallResult == nil {
		t.Fatal("smallMethod result not found")
	}
	if smallResult.TokenCount != 50 {
		t.Errorf("smallMethod TokenCount = %d, want 50 (unchanged)", smallResult.TokenCount)
	}
}

func TestExpandSplitSiblings_NoSplitChunks(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "small.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "FuncA",
			StartLine: 1, EndLine: 10, Content: "func A() {}", TokenCount: 20},
		{ChunkIndex: 1, Kind: "function", SymbolName: "FuncB",
			StartLine: 12, EndLine: 20, Content: "func B() {}", TokenCount: 20},
	})

	input := []types.ScoredResult{
		{ChunkID: chunkIDs[0], Score: 0.9, Path: "small.go",
			SymbolName: "FuncA", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func A() {}", TokenCount: 20},
		{ChunkID: chunkIDs[1], Score: 0.7, Path: "small.go",
			SymbolName: "FuncB", Kind: "function",
			StartLine: 12, EndLine: 20, Content: "func B() {}", TokenCount: 20},
	}

	expanded := ExpandSplitSiblings(ctx, store, input)

	if len(expanded) != 2 {
		t.Fatalf("expected 2 unchanged results, got %d", len(expanded))
	}
	// Token counts should be unchanged
	if expanded[0].TokenCount != 20 || expanded[1].TokenCount != 20 {
		t.Error("results should be unchanged when no split siblings exist")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
