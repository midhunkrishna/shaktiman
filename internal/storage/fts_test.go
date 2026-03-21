package storage

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestKeywordSearch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "validateToken", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content: "function validateToken(token: string): boolean { return token.length > 0; }",
			TokenCount: 20},
		{ChunkIndex: 1, SymbolName: "hashPassword", Kind: "function",
			StartLine: 12, EndLine: 20,
			Content: "function hashPassword(password: string): string { return hash(password); }",
			TokenCount: 18},
	})

	results, err := store.KeywordSearch(ctx, "validate token", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// The validateToken chunk should match
	found := false
	for _, r := range results {
		chunk, _ := store.GetChunkByID(ctx, r.ChunkID)
		if chunk != nil && chunk.SymbolName == "validateToken" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected validateToken in results")
	}
}

func TestFTSRebuild(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Disable triggers
	if err := store.DisableFTSTriggers(ctx); err != nil {
		t.Fatalf("DisableFTSTriggers: %v", err)
	}

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "hello", Kind: "function",
			StartLine: 1, EndLine: 5,
			Content: "function hello() { console.log('hello'); }",
			TokenCount: 10},
	})

	// Search should fail without triggers and rebuild
	results, _ := store.KeywordSearch(ctx, "hello", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results before rebuild, got %d", len(results))
	}

	// Rebuild FTS
	if err := store.RebuildFTS(ctx); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	// Re-enable triggers
	if err := store.EnableFTSTriggers(ctx); err != nil {
		t.Fatalf("EnableFTSTriggers: %v", err)
	}

	// Now search should work
	results, err := store.KeywordSearch(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("KeywordSearch after rebuild: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results after rebuild")
	}
}
