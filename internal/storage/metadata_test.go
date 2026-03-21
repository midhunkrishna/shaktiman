package storage

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return NewStore(db)
}

func TestUpsertAndGetFile(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	file := &types.FileRecord{
		Path:            "src/main.ts",
		ContentHash:     "abc123",
		Mtime:           1234567890.0,
		Size:            1024,
		Language:        "typescript",
		EmbeddingStatus: "pending",
		ParseQuality:    "full",
	}

	id, err := store.UpsertFile(ctx, file)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := store.GetFileByPath(ctx, "src/main.ts")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil file")
	}
	if got.Path != "src/main.ts" {
		t.Errorf("path = %q, want %q", got.Path, "src/main.ts")
	}
	if got.ContentHash != "abc123" {
		t.Errorf("hash = %q, want %q", got.ContentHash, "abc123")
	}
	if got.Language != "typescript" {
		t.Errorf("language = %q, want %q", got.Language, "typescript")
	}

	// Upsert again with different hash
	file.ContentHash = "def456"
	id2, err := store.UpsertFile(ctx, file)
	if err != nil {
		t.Fatalf("second UpsertFile: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same id %d, got %d", id, id2)
	}

	got, _ = store.GetFileByPath(ctx, "src/main.ts")
	if got.ContentHash != "def456" {
		t.Errorf("hash after upsert = %q, want %q", got.ContentHash, "def456")
	}
}

func TestGetFileByPath_NotFound(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	got, err := store.GetFileByPath(context.Background(), "nonexistent.ts")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestInsertAndGetChunks(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	chunks := []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "header", StartLine: 1, EndLine: 5,
			Content: "import { foo } from 'bar'", TokenCount: 10},
		{ChunkIndex: 1, SymbolName: "doStuff", Kind: "function", StartLine: 7, EndLine: 20,
			Content: "function doStuff() { ... }", TokenCount: 50,
			Signature: "(x: number): void"},
	}

	ids, err := store.InsertChunks(ctx, fileID, chunks)
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}

	got, err := store.GetChunksByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("GetChunksByFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0].Kind != "header" {
		t.Errorf("chunk[0].Kind = %q, want %q", got[0].Kind, "header")
	}
	if got[1].SymbolName != "doStuff" {
		t.Errorf("chunk[1].SymbolName = %q, want %q", got[1].SymbolName, "doStuff")
	}
}

func TestInsertAndGetSymbols(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "function hello() {}", TokenCount: 5},
	})

	symbols := []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "hello", Kind: "function",
			Line: 1, Visibility: "exported", IsExported: true},
	}

	ids, err := store.InsertSymbols(ctx, fileID, symbols)
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d", len(ids))
	}

	got, err := store.GetSymbolByName(ctx, "hello")
	if err != nil {
		t.Fatalf("GetSymbolByName: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(got))
	}
	if !got[0].IsExported {
		t.Error("expected is_exported = true")
	}
}

func TestDeleteFileCascade(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "function x() {}", TokenCount: 5},
	})

	if err := store.DeleteFile(ctx, fileID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	chunks, _ := store.GetChunksByFile(ctx, fileID)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks after cascade delete, got %d", len(chunks))
	}
}

func TestGetIndexStats(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Empty stats
	stats, err := store.GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles != 0 {
		t.Errorf("expected 0 files, got %d", stats.TotalFiles)
	}

	// Add a file
	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		Language: "typescript", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "function x() {}", TokenCount: 5},
	})

	stats, _ = store.GetIndexStats(ctx)
	if stats.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", stats.TotalFiles)
	}
	if stats.TotalChunks != 1 {
		t.Errorf("expected 1 chunk, got %d", stats.TotalChunks)
	}
	if stats.Languages["typescript"] != 1 {
		t.Errorf("expected typescript=1, got %d", stats.Languages["typescript"])
	}
}
