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
