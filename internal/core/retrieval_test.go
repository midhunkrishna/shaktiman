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

	results, err := hydrateFTSResults(ctx, store, ftsResults)
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
