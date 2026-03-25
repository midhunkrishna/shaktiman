package storage

import (
	"context"
	"fmt"
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

// insertTestFileWithChunks creates a file with n chunks and returns fileID + chunkIDs.
func insertTestFileWithChunks(t *testing.T, store *Store, path string, n int) (int64, []int64) {
	t.Helper()
	ctx := context.Background()
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: path, ContentHash: "hash", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunks := make([]types.ChunkRecord, n)
	for i := range chunks {
		chunks[i] = types.ChunkRecord{
			ChunkIndex: i,
			Kind:       "function",
			StartLine:  i*10 + 1,
			EndLine:    i*10 + 10,
			Content:    fmt.Sprintf("func chunk_%d() {}", i),
			TokenCount: 10,
		}
	}
	ids, err := store.InsertChunks(ctx, fileID, chunks)
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}
	return fileID, ids
}

func TestGetEmbedPage_Pagination(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, _ = insertTestFileWithChunks(t, store, "big.go", 100)

	// Page 1: afterID=0, limit=30
	page1, err := store.GetEmbedPage(ctx, 0, 30)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 30 {
		t.Fatalf("page1 len = %d, want 30", len(page1))
	}

	// Page 2: afterID = last from page1
	page2, err := store.GetEmbedPage(ctx, page1[29].ChunkID, 30)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 30 {
		t.Fatalf("page2 len = %d, want 30", len(page2))
	}
	if page2[0].ChunkID <= page1[29].ChunkID {
		t.Fatalf("page2 first ID %d should be > page1 last ID %d", page2[0].ChunkID, page1[29].ChunkID)
	}

	// Page 3 + 4: remaining 40 then 0
	page3, _ := store.GetEmbedPage(ctx, page2[29].ChunkID, 30)
	if len(page3) != 30 {
		t.Fatalf("page3 len = %d, want 30", len(page3))
	}
	page4, _ := store.GetEmbedPage(ctx, page3[29].ChunkID, 30)
	if len(page4) != 10 {
		t.Fatalf("page4 len = %d, want 10", len(page4))
	}
	page5, _ := store.GetEmbedPage(ctx, page4[9].ChunkID, 30)
	if len(page5) != 0 {
		t.Fatalf("page5 len = %d, want 0", len(page5))
	}
}

func TestCountChunksNeedingEmbedding(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "test.go", 100)

	count, err := store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 100 {
		t.Fatalf("count = %d, want 100", count)
	}

	// Mark 40 as embedded
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:40]); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}

	count, err = store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("count after mark: %v", err)
	}
	if count != 60 {
		t.Fatalf("count = %d, want 60", count)
	}
}

func TestMarkChunksEmbedded_Cumulative(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, chunkIDs := insertTestFileWithChunks(t, store, "multi.go", 50)

	// Mark first 32 chunks
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:32]); err != nil {
		t.Fatalf("mark batch 1: %v", err)
	}

	// File should be 'partial'
	f, err := store.GetFileByPath(ctx, "multi.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "partial" {
		t.Fatalf("status after 32/50 = %q, want 'partial'", f.EmbeddingStatus)
	}
	_ = fileID

	// Mark remaining 18 chunks
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[32:]); err != nil {
		t.Fatalf("mark batch 2: %v", err)
	}

	// File should be 'complete'
	f, err = store.GetFileByPath(ctx, "multi.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "complete" {
		t.Fatalf("status after 50/50 = %q, want 'complete'", f.EmbeddingStatus)
	}
}

func TestMarkChunksEmbedded_PartialBatch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "partial.go", 50)

	// Mark only 10
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:10]); err != nil {
		t.Fatalf("mark: %v", err)
	}

	f, _ := store.GetFileByPath(ctx, "partial.go")
	if f.EmbeddingStatus != "partial" {
		t.Fatalf("status after 10/50 = %q, want 'partial'", f.EmbeddingStatus)
	}

	// Verify remaining count
	count, _ := store.CountChunksNeedingEmbedding(ctx)
	if count != 40 {
		t.Fatalf("remaining = %d, want 40", count)
	}
}

func TestGetEmbedPage_SkipsEmbedded(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "mixed.go", 20)

	// Mark first 10 as embedded
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:10]); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// GetEmbedPage should only return the 10 un-embedded chunks
	page, err := store.GetEmbedPage(ctx, 0, 100)
	if err != nil {
		t.Fatalf("GetEmbedPage: %v", err)
	}
	if len(page) != 10 {
		t.Fatalf("page len = %d, want 10 (only un-embedded)", len(page))
	}
	for _, job := range page {
		if job.ChunkID <= chunkIDs[9] {
			t.Errorf("returned already-embedded chunk %d", job.ChunkID)
		}
	}
}

func TestMarkChunksEmbedded_Empty(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// Should be a no-op, no error
	if err := store.MarkChunksEmbedded(context.Background(), nil); err != nil {
		t.Fatalf("MarkChunksEmbedded(nil): %v", err)
	}
	if err := store.MarkChunksEmbedded(context.Background(), []int64{}); err != nil {
		t.Fatalf("MarkChunksEmbedded(empty): %v", err)
	}
}
