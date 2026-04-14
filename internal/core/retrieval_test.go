//go:build sqlite_fts5

package core

import (
	"context"
	"fmt"
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
		{ChunkID: chunkIDs[1], FileID: fileID, Score: 0.8, Path: "action.java",
			SymbolName: "bigMethod", Kind: "method",
			StartLine: 10, EndLine: 50, Content: "// part 1\nboolean flag = detect();", TokenCount: 500},
		{ChunkID: chunkIDs[4], FileID: fileID, Score: 0.5, Path: "action.java",
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
		{ChunkID: chunkIDs[0], FileID: fileID, Score: 0.9, Path: "small.go",
			SymbolName: "FuncA", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func A() {}", TokenCount: 20},
		{ChunkID: chunkIDs[1], FileID: fileID, Score: 0.7, Path: "small.go",
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

// setupExpandBenchStore creates a store with a realistic mix of split and
// non-split chunks across multiple files, mimicking a real search result set.
// Returns the store and a slice of ScoredResults representing a typical
// 20-result search (5 split methods across 3 files + 10 single-chunk functions).
func setupExpandBenchStore(b *testing.B) (types.WriterStore, []types.ScoredResult) {
	b.Helper()
	store := setupTestStore(&testing.T{})

	ctx := context.Background()

	// File 1: a large Java-like file with 3 split methods (3 fragments each) + 5 single-chunk methods
	f1, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "action.java", ContentHash: "h1", Mtime: 1.0,
		Language: "java", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	var f1Chunks []types.ChunkRecord
	// 3 split methods, each 3 fragments
	for m := 0; m < 3; m++ {
		for frag := 0; frag < 3; frag++ {
			idx := m*3 + frag
			startLine := idx*50 + 1
			f1Chunks = append(f1Chunks, types.ChunkRecord{
				ChunkIndex: idx, Kind: "method",
				SymbolName: fmt.Sprintf("bigMethod%d", m),
				StartLine: startLine, EndLine: startLine + 49,
				Content:    fmt.Sprintf("// fragment %d of method %d\ncode here...", frag, m),
				TokenCount: 500,
			})
		}
	}
	// 5 single-chunk methods
	for s := 0; s < 5; s++ {
		idx := 9 + s
		startLine := idx*50 + 1
		f1Chunks = append(f1Chunks, types.ChunkRecord{
			ChunkIndex: idx, Kind: "method",
			SymbolName: fmt.Sprintf("smallMethod%d", s),
			StartLine: startLine, EndLine: startLine + 20,
			Content:    fmt.Sprintf("void smallMethod%d() { /* small */ }", s),
			TokenCount: 50,
		})
	}
	f1IDs, _ := store.InsertChunks(ctx, f1, f1Chunks)

	// File 2: 2 split methods (2 fragments each) + 3 single-chunk
	f2, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "service.java", ContentHash: "h2", Mtime: 1.0,
		Language: "java", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	var f2Chunks []types.ChunkRecord
	for m := 0; m < 2; m++ {
		for frag := 0; frag < 2; frag++ {
			idx := m*2 + frag
			startLine := idx*60 + 1
			f2Chunks = append(f2Chunks, types.ChunkRecord{
				ChunkIndex: idx, Kind: "method",
				SymbolName: fmt.Sprintf("serviceMethod%d", m),
				StartLine: startLine, EndLine: startLine + 59,
				Content:    fmt.Sprintf("// service fragment %d of method %d", frag, m),
				TokenCount: 600,
			})
		}
	}
	for s := 0; s < 3; s++ {
		idx := 4 + s
		startLine := idx*60 + 1
		f2Chunks = append(f2Chunks, types.ChunkRecord{
			ChunkIndex: idx, Kind: "method",
			SymbolName: fmt.Sprintf("helper%d", s),
			StartLine: startLine, EndLine: startLine + 15,
			Content:    fmt.Sprintf("void helper%d() {}", s),
			TokenCount: 30,
		})
	}
	f2IDs, _ := store.InsertChunks(ctx, f2, f2Chunks)

	// File 3: only single-chunk functions (no splits)
	f3, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "util.go", ContentHash: "h3", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	var f3Chunks []types.ChunkRecord
	for s := 0; s < 5; s++ {
		startLine := s*20 + 1
		f3Chunks = append(f3Chunks, types.ChunkRecord{
			ChunkIndex: s, Kind: "function",
			SymbolName: fmt.Sprintf("utilFunc%d", s),
			StartLine: startLine, EndLine: startLine + 15,
			Content:    fmt.Sprintf("func utilFunc%d() {}", s),
			TokenCount: 40,
		})
	}
	f3IDs, _ := store.InsertChunks(ctx, f3, f3Chunks)

	// Build a 20-result search set: mix of fragments and singles
	results := []types.ScoredResult{
		// Hit on 1 fragment of each split method in file 1
		{ChunkID: f1IDs[1], FileID: f1, Score: 0.9, Path: "action.java", SymbolName: "bigMethod0", Kind: "method", StartLine: 51, EndLine: 100, Content: "frag", TokenCount: 500},
		{ChunkID: f1IDs[4], FileID: f1, Score: 0.85, Path: "action.java", SymbolName: "bigMethod1", Kind: "method", StartLine: 201, EndLine: 250, Content: "frag", TokenCount: 500},
		{ChunkID: f1IDs[7], FileID: f1, Score: 0.80, Path: "action.java", SymbolName: "bigMethod2", Kind: "method", StartLine: 351, EndLine: 400, Content: "frag", TokenCount: 500},
		// 5 single-chunk methods from file 1
		{ChunkID: f1IDs[9], FileID: f1, Score: 0.75, Path: "action.java", SymbolName: "smallMethod0", Kind: "method", StartLine: 451, EndLine: 471, Content: "small", TokenCount: 50},
		{ChunkID: f1IDs[10], FileID: f1, Score: 0.70, Path: "action.java", SymbolName: "smallMethod1", Kind: "method", StartLine: 501, EndLine: 521, Content: "small", TokenCount: 50},
		{ChunkID: f1IDs[11], FileID: f1, Score: 0.65, Path: "action.java", SymbolName: "smallMethod2", Kind: "method", StartLine: 551, EndLine: 571, Content: "small", TokenCount: 50},
		{ChunkID: f1IDs[12], FileID: f1, Score: 0.60, Path: "action.java", SymbolName: "smallMethod3", Kind: "method", StartLine: 601, EndLine: 621, Content: "small", TokenCount: 50},
		{ChunkID: f1IDs[13], FileID: f1, Score: 0.55, Path: "action.java", SymbolName: "smallMethod4", Kind: "method", StartLine: 651, EndLine: 671, Content: "small", TokenCount: 50},
		// Hit on 1 fragment of each split method in file 2
		{ChunkID: f2IDs[0], FileID: f2, Score: 0.50, Path: "service.java", SymbolName: "serviceMethod0", Kind: "method", StartLine: 1, EndLine: 60, Content: "svc", TokenCount: 600},
		{ChunkID: f2IDs[2], FileID: f2, Score: 0.45, Path: "service.java", SymbolName: "serviceMethod1", Kind: "method", StartLine: 121, EndLine: 180, Content: "svc", TokenCount: 600},
		// 3 single-chunk from file 2
		{ChunkID: f2IDs[4], FileID: f2, Score: 0.40, Path: "service.java", SymbolName: "helper0", Kind: "method", StartLine: 241, EndLine: 256, Content: "help", TokenCount: 30},
		{ChunkID: f2IDs[5], FileID: f2, Score: 0.35, Path: "service.java", SymbolName: "helper1", Kind: "method", StartLine: 301, EndLine: 316, Content: "help", TokenCount: 30},
		{ChunkID: f2IDs[6], FileID: f2, Score: 0.30, Path: "service.java", SymbolName: "helper2", Kind: "method", StartLine: 361, EndLine: 376, Content: "help", TokenCount: 30},
		// 5 single-chunk from file 3
		{ChunkID: f3IDs[0], FileID: f3, Score: 0.28, Path: "util.go", SymbolName: "utilFunc0", Kind: "function", StartLine: 1, EndLine: 16, Content: "util", TokenCount: 40},
		{ChunkID: f3IDs[1], FileID: f3, Score: 0.26, Path: "util.go", SymbolName: "utilFunc1", Kind: "function", StartLine: 21, EndLine: 36, Content: "util", TokenCount: 40},
		{ChunkID: f3IDs[2], FileID: f3, Score: 0.24, Path: "util.go", SymbolName: "utilFunc2", Kind: "function", StartLine: 41, EndLine: 56, Content: "util", TokenCount: 40},
		{ChunkID: f3IDs[3], FileID: f3, Score: 0.22, Path: "util.go", SymbolName: "utilFunc3", Kind: "function", StartLine: 61, EndLine: 76, Content: "util", TokenCount: 40},
		{ChunkID: f3IDs[4], FileID: f3, Score: 0.20, Path: "util.go", SymbolName: "utilFunc4", Kind: "function", StartLine: 81, EndLine: 96, Content: "util", TokenCount: 40},
	}

	return store, results
}

// BenchmarkExpandSplitSiblings measures the cost of sibling expansion on a
// realistic 18-result search set with 5 split methods and 13 single-chunk results.
// This is the hot path added by the merge-for-retrieval feature.
func BenchmarkExpandSplitSiblings(b *testing.B) {
	store, results := setupExpandBenchStore(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Make a copy to avoid mutation effects across iterations
		input := make([]types.ScoredResult, len(results))
		copy(input, results)
		expanded := ExpandSplitSiblings(ctx, store, input)
		if len(expanded) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

// BenchmarkExpandSplitSiblings_NoSplits measures the overhead when no chunks
// are split — the common case for small-method codebases.
func BenchmarkExpandSplitSiblings_NoSplits(b *testing.B) {
	store := setupTestStore(&testing.T{})
	ctx := context.Background()

	// Create 20 single-chunk functions
	fID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "nosplit.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	var chunks []types.ChunkRecord
	for i := 0; i < 20; i++ {
		chunks = append(chunks, types.ChunkRecord{
			ChunkIndex: i, Kind: "function",
			SymbolName: fmt.Sprintf("Func%d", i),
			StartLine: i*20 + 1, EndLine: i*20 + 15,
			Content:    fmt.Sprintf("func Func%d() {}", i),
			TokenCount: 30,
		})
	}
	ids, _ := store.InsertChunks(ctx, fID, chunks)

	var results []types.ScoredResult
	for i, id := range ids {
		results = append(results, types.ScoredResult{
			ChunkID: id, FileID: fID, Score: float64(20-i) / 20.0, Path: "nosplit.go",
			SymbolName: fmt.Sprintf("Func%d", i), Kind: "function",
			StartLine: i*20 + 1, EndLine: i*20 + 15,
			Content: fmt.Sprintf("func Func%d() {}", i), TokenCount: 30,
		})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		input := make([]types.ScoredResult, len(results))
		copy(input, results)
		expanded := ExpandSplitSiblings(ctx, store, input)
		if len(expanded) != 20 {
			b.Fatalf("expected 20 results, got %d", len(expanded))
		}
	}
}
