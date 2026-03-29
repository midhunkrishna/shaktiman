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

func TestUpsertFile_OnConflictUpdate(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// First insert.
	file := &types.FileRecord{
		Path: "conflict.go", ContentHash: "hash1", Mtime: 1.0,
		Size: 100, Language: "go",
		EmbeddingStatus: "pending", ParseQuality: "full",
	}
	id1, err := store.UpsertFile(ctx, file)
	if err != nil {
		t.Fatalf("first UpsertFile: %v", err)
	}

	// Second insert with same path but different hash/size to exercise
	// the ON CONFLICT UPDATE path and the id==0 fallback query.
	file.ContentHash = "hash2"
	file.Size = 200
	id2, err := store.UpsertFile(ctx, file)
	if err != nil {
		t.Fatalf("second UpsertFile: %v", err)
	}
	if id2 != id1 {
		t.Errorf("UpsertFile returned id=%d on conflict update, want %d", id2, id1)
	}

	// Verify the record was updated, not duplicated.
	got, err := store.GetFileByPath(ctx, "conflict.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got.ContentHash != "hash2" {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, "hash2")
	}
	if got.Size != 200 {
		t.Errorf("Size = %d, want 200", got.Size)
	}
}

func TestInsertChunks_ParentIndex(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "parent.go", ContentHash: "h", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	parentIdx := 0
	chunks := []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "class", StartLine: 1, EndLine: 50,
			Content: "class Foo {}", TokenCount: 20},
		{ChunkIndex: 1, Kind: "method", StartLine: 5, EndLine: 15,
			Content: "  func bar() {}", TokenCount: 10,
			ParentIndex: &parentIdx},
	}

	ids, err := store.InsertChunks(ctx, fileID, chunks)
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}

	// The child chunk should have ParentChunkID set to the parent's ID.
	child, err := store.GetChunkByID(ctx, ids[1])
	if err != nil {
		t.Fatalf("GetChunkByID: %v", err)
	}
	if child == nil {
		t.Fatal("expected non-nil child chunk")
	}
	if child.ParentChunkID == nil {
		t.Fatal("expected ParentChunkID to be set")
	}
	if *child.ParentChunkID != ids[0] {
		t.Errorf("ParentChunkID = %d, want %d", *child.ParentChunkID, ids[0])
	}

	// Parent chunk should have no parent.
	parent, err := store.GetChunkByID(ctx, ids[0])
	if err != nil {
		t.Fatalf("GetChunkByID(parent): %v", err)
	}
	if parent.ParentChunkID != nil {
		t.Errorf("parent ParentChunkID = %d, want nil", *parent.ParentChunkID)
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

func TestCountChunksEmbedded(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "test.go", 50)

	// Initially no chunks are embedded.
	count, err := store.CountChunksEmbedded(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	// Mark 20 as embedded.
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:20]); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}
	count, err = store.CountChunksEmbedded(ctx)
	if err != nil {
		t.Fatalf("count after mark 20: %v", err)
	}
	if count != 20 {
		t.Fatalf("count = %d, want 20", count)
	}

	// Mark remaining 30 — should be complementary to CountChunksNeedingEmbedding.
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[20:]); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}
	count, err = store.CountChunksEmbedded(ctx)
	if err != nil {
		t.Fatalf("count after mark all: %v", err)
	}
	if count != 50 {
		t.Fatalf("count = %d, want 50", count)
	}
	need, err := store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding: %v", err)
	}
	if need != 0 {
		t.Fatalf("need = %d, want 0", need)
	}
}

// TestResetAllEmbeddedFlags verifies that ResetAllEmbeddedFlags sets all chunks
// to embedded=0 and all files to embedding_status='pending'.
func TestResetAllEmbeddedFlags(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert two files with chunks.
	_, chunkIDs1 := insertTestFileWithChunks(t, store, "a.go", 30)
	_, chunkIDs2 := insertTestFileWithChunks(t, store, "b.go", 20)
	allIDs := append(chunkIDs1, chunkIDs2...)

	// Mark all as embedded → files become 'complete'.
	if err := store.MarkChunksEmbedded(ctx, allIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}
	embedded, _ := store.CountChunksEmbedded(ctx)
	if embedded != 50 {
		t.Fatalf("embedded = %d, want 50", embedded)
	}
	for _, path := range []string{"a.go", "b.go"} {
		f, _ := store.GetFileByPath(ctx, path)
		if f.EmbeddingStatus != "complete" {
			t.Fatalf("%s status = %q, want 'complete'", path, f.EmbeddingStatus)
		}
	}

	// Reset all flags.
	if err := store.ResetAllEmbeddedFlags(ctx); err != nil {
		t.Fatalf("ResetAllEmbeddedFlags: %v", err)
	}

	// All chunks back to embedded=0.
	embedded, _ = store.CountChunksEmbedded(ctx)
	if embedded != 0 {
		t.Fatalf("embedded after reset = %d, want 0", embedded)
	}
	need, _ := store.CountChunksNeedingEmbedding(ctx)
	if need != 50 {
		t.Fatalf("need after reset = %d, want 50", need)
	}

	// All files back to 'pending'.
	for _, path := range []string{"a.go", "b.go"} {
		f, _ := store.GetFileByPath(ctx, path)
		if f.EmbeddingStatus != "pending" {
			t.Fatalf("%s status after reset = %q, want 'pending'", path, f.EmbeddingStatus)
		}
	}
}

// TestResetAllEmbeddedFlags_NoOp verifies that ResetAllEmbeddedFlags is safe
// when no chunks are embedded.
func TestResetAllEmbeddedFlags_NoOp(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, _ = insertTestFileWithChunks(t, store, "clean.go", 10)

	if err := store.ResetAllEmbeddedFlags(ctx); err != nil {
		t.Fatalf("ResetAllEmbeddedFlags: %v", err)
	}

	need, _ := store.CountChunksNeedingEmbedding(ctx)
	if need != 10 {
		t.Fatalf("need = %d, want 10", need)
	}
	embedded, _ := store.CountChunksEmbedded(ctx)
	if embedded != 0 {
		t.Fatalf("embedded = %d, want 0", embedded)
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

func TestMarkChunksEmbedded_LargeBatch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Create 600 chunks to exercise the batching path (batchLimit=500).
	_, chunkIDs := insertTestFileWithChunks(t, store, "large.go", 600)

	// Mark all 600 as embedded in one call.
	if err := store.MarkChunksEmbedded(ctx, chunkIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded(600): %v", err)
	}

	// Verify all are embedded.
	count, err := store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("remaining = %d, want 0", count)
	}

	// Verify file is complete.
	f, err := store.GetFileByPath(ctx, "large.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "complete" {
		t.Errorf("status = %q, want 'complete'", f.EmbeddingStatus)
	}
}


func TestGetEmbeddedChunkIDs(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "embed.go", 100)

	// Mark first 50 as embedded.
	if err := store.MarkChunksEmbedded(ctx, chunkIDs[:50]); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}

	// Page through embedded IDs.
	var got []int64
	afterID := int64(0)
	for {
		page, err := store.GetEmbeddedChunkIDs(ctx, afterID, 20)
		if err != nil {
			t.Fatalf("GetEmbeddedChunkIDs: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		afterID = page[len(page)-1]
	}

	if len(got) != 50 {
		t.Fatalf("got %d embedded IDs, want 50", len(got))
	}
	// Verify they're sorted and match the first 50 chunk IDs.
	for i, id := range got {
		if id != chunkIDs[i] {
			t.Errorf("got[%d] = %d, want %d", i, id, chunkIDs[i])
		}
	}
}

func TestGetEmbeddedChunkIDs_Empty(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	insertTestFileWithChunks(t, store, "none.go", 10)

	ids, err := store.GetEmbeddedChunkIDs(ctx, 0, 100)
	if err != nil {
		t.Fatalf("GetEmbeddedChunkIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %d IDs, want 0", len(ids))
	}
}

func TestResetEmbeddedFlags(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "reset.go", 50)

	// Mark all as embedded.
	if err := store.MarkChunksEmbedded(ctx, chunkIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}

	// File should be 'complete'.
	f, err := store.GetFileByPath(ctx, "reset.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "complete" {
		t.Fatalf("status = %q, want 'complete'", f.EmbeddingStatus)
	}

	// Reset 20 chunks.
	if err := store.ResetEmbeddedFlags(ctx, chunkIDs[:20]); err != nil {
		t.Fatalf("ResetEmbeddedFlags: %v", err)
	}

	// 20 should need embedding now.
	need, err := store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding: %v", err)
	}
	if need != 20 {
		t.Errorf("need = %d, want 20", need)
	}

	// 30 should remain embedded.
	embedded, err := store.CountChunksEmbedded(ctx)
	if err != nil {
		t.Fatalf("CountChunksEmbedded: %v", err)
	}
	if embedded != 30 {
		t.Errorf("embedded = %d, want 30", embedded)
	}

	// File should be 'partial' (some still embedded).
	f, err = store.GetFileByPath(ctx, "reset.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "partial" {
		t.Errorf("status = %q, want 'partial'", f.EmbeddingStatus)
	}
}

func TestResetEmbeddedFlags_AllReset(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "allreset.go", 20)
	if err := store.MarkChunksEmbedded(ctx, chunkIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}

	// Reset all → file should go back to 'pending'.
	if err := store.ResetEmbeddedFlags(ctx, chunkIDs); err != nil {
		t.Fatalf("ResetEmbeddedFlags: %v", err)
	}

	f, err := store.GetFileByPath(ctx, "allreset.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.EmbeddingStatus != "pending" {
		t.Errorf("status = %q, want 'pending'", f.EmbeddingStatus)
	}
}

func TestResetEmbeddedFlags_LargeBatch(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	_, chunkIDs := insertTestFileWithChunks(t, store, "large_reset.go", 600)
	if err := store.MarkChunksEmbedded(ctx, chunkIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}

	// Reset all 600 in one call to exercise batching (batchLimit=500).
	if err := store.ResetEmbeddedFlags(ctx, chunkIDs); err != nil {
		t.Fatalf("ResetEmbeddedFlags: %v", err)
	}

	need, err := store.CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding: %v", err)
	}
	if need != 600 {
		t.Errorf("need = %d, want 600", need)
	}
}

func TestResetEmbeddedFlags_Empty(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// No-op on empty slice.
	if err := store.ResetEmbeddedFlags(ctx, nil); err != nil {
		t.Fatalf("ResetEmbeddedFlags(nil): %v", err)
	}
	if err := store.ResetEmbeddedFlags(ctx, []int64{}); err != nil {
		t.Fatalf("ResetEmbeddedFlags(empty): %v", err)
	}
}

func TestListFiles_MultipleFiles(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert files in non-alphabetical order.
	for _, path := range []string{"z.go", "a.go", "m.go"} {
		if _, err := store.UpsertFile(ctx, &types.FileRecord{
			Path: path, ContentHash: "h", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		}); err != nil {
			t.Fatalf("UpsertFile(%s): %v", path, err)
		}
	}

	files, err := store.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	// ListFiles should return files sorted by path.
	if files[0].Path != "a.go" || files[1].Path != "m.go" || files[2].Path != "z.go" {
		t.Errorf("unexpected order: %q, %q, %q", files[0].Path, files[1].Path, files[2].Path)
	}
	// Verify NullString fields are properly scanned.
	if files[0].Language != "go" {
		t.Errorf("language = %q, want %q", files[0].Language, "go")
	}
}

func TestDeleteChunksByFile_RemovesTargetOnly(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileA, _ := insertTestFileWithChunks(t, store, "a.go", 3)
	_, _ = insertTestFileWithChunks(t, store, "b.go", 2)

	// Delete chunks for file A only.
	if err := store.DeleteChunksByFile(ctx, fileA); err != nil {
		t.Fatalf("DeleteChunksByFile: %v", err)
	}

	// File A's chunks should be gone.
	chunksA, _ := store.GetChunksByFile(ctx, fileA)
	if len(chunksA) != 0 {
		t.Errorf("expected 0 chunks for file A, got %d", len(chunksA))
	}

	// File A record should still exist (only chunks deleted, not the file).
	f, _ := store.GetFileByPath(ctx, "a.go")
	if f == nil {
		t.Error("expected file A record to survive DeleteChunksByFile")
	}
}


func TestGetSymbolsByFile_ReturnsOrdered(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, chunkIDs := insertTestFileWithChunks(t, store, "sym.go", 2)

	// Insert symbols on different lines to verify ordering.
	_, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[1], Name: "Beta", Kind: "function", Line: 20, Visibility: "exported", IsExported: true},
		{ChunkID: chunkIDs[0], Name: "Alpha", Kind: "function", Line: 5, Visibility: "private", IsExported: false},
	})
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}

	syms, err := store.GetSymbolsByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("GetSymbolsByFile: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(syms))
	}
	// Should be sorted by line: Alpha(5) before Beta(20).
	if syms[0].Name != "Alpha" {
		t.Errorf("first symbol = %q, want Alpha (sorted by line)", syms[0].Name)
	}
	if syms[1].Name != "Beta" {
		t.Errorf("second symbol = %q, want Beta", syms[1].Name)
	}
}


func TestGetSymbolByID_Found(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, chunkIDs := insertTestFileWithChunks(t, store, "sym.go", 1)
	symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "Foo", Kind: "function", Line: 1,
			Signature: "func Foo()", Visibility: "exported", IsExported: true},
	})
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}

	sym, err := store.GetSymbolByID(ctx, symIDs[0])
	if err != nil {
		t.Fatalf("GetSymbolByID: %v", err)
	}
	if sym == nil {
		t.Fatal("expected non-nil symbol")
	}
	if sym.Name != "Foo" {
		t.Errorf("Name = %q, want %q", sym.Name, "Foo")
	}
	if sym.Kind != "function" {
		t.Errorf("Kind = %q, want %q", sym.Kind, "function")
	}
	if sym.Signature != "func Foo()" {
		t.Errorf("Signature = %q, want %q", sym.Signature, "func Foo()")
	}
	if !sym.IsExported {
		t.Error("expected IsExported = true")
	}
}


func TestDeleteSymbolsByFile_RemovesTargetOnly(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileA, chunkIDsA := insertTestFileWithChunks(t, store, "a.go", 1)
	fileB, chunkIDsB := insertTestFileWithChunks(t, store, "b.go", 1)

	store.InsertSymbols(ctx, fileA, []types.SymbolRecord{
		{ChunkID: chunkIDsA[0], Name: "SymA", Kind: "function", Line: 1, Visibility: "exported"},
	})
	store.InsertSymbols(ctx, fileB, []types.SymbolRecord{
		{ChunkID: chunkIDsB[0], Name: "SymB", Kind: "function", Line: 1, Visibility: "exported"},
	})

	if err := store.DeleteSymbolsByFile(ctx, fileA); err != nil {
		t.Fatalf("DeleteSymbolsByFile: %v", err)
	}

	// File A symbols should be gone.
	symsA, _ := store.GetSymbolsByFile(ctx, fileA)
	if len(symsA) != 0 {
		t.Errorf("expected 0 symbols for file A, got %d", len(symsA))
	}
	// File B symbols should remain.
	symsB, _ := store.GetSymbolsByFile(ctx, fileB)
	if len(symsB) != 1 {
		t.Errorf("expected 1 symbol for file B, got %d", len(symsB))
	}
}


func TestGetFilePathByID_NotFound(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// Unknown ID should return a non-nil error.
	_, err := store.GetFilePathByID(context.Background(), 99999)
	if err == nil {
		t.Fatal("expected error for unknown file ID")
	}
}

func TestGetChunksNeedingEmbedding_FiltersComplete(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// File with pending status -- chunks should be returned.
	_, _ = insertTestFileWithChunks(t, store, "pending.go", 2)

	// File with complete status -- chunks should be excluded.
	completeFileID, completeChunkIDs := insertTestFileWithChunks(t, store, "done.go", 3)
	// Mark all chunks as embedded so the file becomes "complete".
	if err := store.MarkChunksEmbedded(ctx, completeChunkIDs); err != nil {
		t.Fatalf("MarkChunksEmbedded: %v", err)
	}
	_ = completeFileID

	jobs, err := store.GetChunksNeedingEmbedding(ctx, nil)
	if err != nil {
		t.Fatalf("GetChunksNeedingEmbedding: %v", err)
	}
	// Only the 2 pending chunks should be returned.
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs (from pending file), got %d", len(jobs))
	}
}

func TestEmbeddingReadiness_NoChunks(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// No chunks -- should return 0.0 without division-by-zero.
	readiness, err := store.EmbeddingReadiness(context.Background(), 5)
	if err != nil {
		t.Fatalf("EmbeddingReadiness: %v", err)
	}
	if readiness != 0.0 {
		t.Errorf("readiness = %f, want 0.0", readiness)
	}
}

func TestEmbeddingReadiness_Partial(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	insertTestFileWithChunks(t, store, "test.go", 10)

	readiness, err := store.EmbeddingReadiness(context.Background(), 4)
	if err != nil {
		t.Fatalf("EmbeddingReadiness: %v", err)
	}
	// 4 vectors / 10 chunks = 0.4
	if readiness != 0.4 {
		t.Errorf("readiness = %f, want 0.4", readiness)
	}
}

func TestGetIndexStats_WithParseErrors(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert a normal file.
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "good.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	// Insert a file with parse error.
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "bad.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "error",
	})
	// Insert a file with unparseable quality.
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "ugly.py", ContentHash: "h3", Mtime: 1.0,
		Language: "python", EmbeddingStatus: "pending", ParseQuality: "unparseable",
	})

	stats, err := store.GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", stats.TotalFiles)
	}
	// Both "error" and "unparseable" should count as parse errors.
	if stats.ParseErrors != 2 {
		t.Errorf("ParseErrors = %d, want 2", stats.ParseErrors)
	}
	// Language breakdown should show 2 go files and 1 python file.
	if stats.Languages["go"] != 2 {
		t.Errorf("Languages[go] = %d, want 2", stats.Languages["go"])
	}
	if stats.Languages["python"] != 1 {
		t.Errorf("Languages[python] = %d, want 1", stats.Languages["python"])
	}
}

func TestGetIndexStats_LanguageBreakdown(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert files with various languages, including one with no language.
	for _, tc := range []struct {
		path string
		lang string
	}{
		{"a.go", "go"},
		{"b.go", "go"},
		{"c.rs", "rust"},
		{"d.txt", ""}, // no language
	} {
		store.UpsertFile(ctx, &types.FileRecord{
			Path: tc.path, ContentHash: "h", Mtime: 1.0,
			Language: tc.lang, EmbeddingStatus: "pending", ParseQuality: "full",
		})
	}

	stats, err := store.GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles != 4 {
		t.Errorf("TotalFiles = %d, want 4", stats.TotalFiles)
	}
	if stats.Languages["go"] != 2 {
		t.Errorf("Languages[go] = %d, want 2", stats.Languages["go"])
	}
	if stats.Languages["rust"] != 1 {
		t.Errorf("Languages[rust] = %d, want 1", stats.Languages["rust"])
	}
	// Files with empty language should not appear in the map.
	if _, ok := stats.Languages[""]; ok {
		t.Error("empty language should not appear in Languages map")
	}
}

func TestGetChunkByID_NotFound(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	chunk, err := store.GetChunkByID(context.Background(), 99999)
	if err != nil {
		t.Fatalf("GetChunkByID: %v", err)
	}
	if chunk != nil {
		t.Errorf("expected nil chunk for unknown ID, got %+v", chunk)
	}
}

func TestDeleteFile_NonExistent(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	// Deleting a non-existent file should not error.
	if err := store.DeleteFile(context.Background(), 99999); err != nil {
		t.Fatalf("DeleteFile(unknown): %v", err)
	}
}

func TestUpsertFile_DefaultValues(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// EmbeddingStatus and ParseQuality are empty -- should default to "pending" and "full".
	id, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "defaults.go", ContentHash: "h", Mtime: 1.0,
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := store.GetFileByPath(ctx, "defaults.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got.EmbeddingStatus != "pending" {
		t.Errorf("EmbeddingStatus = %q, want %q", got.EmbeddingStatus, "pending")
	}
	if got.ParseQuality != "full" {
		t.Errorf("ParseQuality = %q, want %q", got.ParseQuality, "full")
	}
}

func TestGetSymbolByName_Found(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert two files, each with a symbol named "Render" to exercise
	// the multi-row path in GetSymbolByName / scanSymbols.
	fileA, chunkIDsA := insertTestFileWithChunks(t, store, "a.go", 1)
	fileB, chunkIDsB := insertTestFileWithChunks(t, store, "b.go", 1)

	store.InsertSymbols(ctx, fileA, []types.SymbolRecord{
		{ChunkID: chunkIDsA[0], Name: "Render", Kind: "function", Line: 1,
			Signature: "func Render()", Visibility: "exported", IsExported: true},
	})
	store.InsertSymbols(ctx, fileB, []types.SymbolRecord{
		{ChunkID: chunkIDsB[0], Name: "Render", Kind: "method", Line: 5,
			Signature: "func (v *View) Render()", Visibility: "exported", IsExported: true},
	})

	got, err := store.GetSymbolByName(ctx, "Render")
	if err != nil {
		t.Fatalf("GetSymbolByName: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 symbols named Render, got %d", len(got))
	}

	// Verify fields are populated correctly.
	kinds := map[string]bool{}
	for _, sym := range got {
		if sym.Name != "Render" {
			t.Errorf("Name = %q, want Render", sym.Name)
		}
		if !sym.IsExported {
			t.Errorf("expected IsExported = true for %q", sym.Kind)
		}
		if sym.Signature == "" {
			t.Errorf("expected non-empty Signature for %q", sym.Kind)
		}
		kinds[sym.Kind] = true
	}
	if !kinds["function"] || !kinds["method"] {
		t.Errorf("expected both function and method kinds, got %v", kinds)
	}
}

func TestGetSymbolByName_NotFound(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)

	got, err := store.GetSymbolByName(context.Background(), "NonexistentSymbol")
	if err != nil {
		t.Fatalf("GetSymbolByName: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 symbols, got %d", len(got))
	}
}

func TestGetChunksByFile_DirectQuery(t *testing.T) {
	t.Parallel()
	store := setupTestStore(t)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "multi_chunk.go", ContentHash: "h", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	parentIdx := 0
	chunks := []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "MyClass", Kind: "class",
			StartLine: 1, EndLine: 50, Content: "class MyClass {}", TokenCount: 30,
			Signature: "class MyClass"},
		{ChunkIndex: 1, SymbolName: "myMethod", Kind: "method",
			StartLine: 10, EndLine: 25, Content: "func myMethod() {}", TokenCount: 15,
			Signature: "func myMethod()", ParentIndex: &parentIdx},
		{ChunkIndex: 2, SymbolName: "helper", Kind: "function",
			StartLine: 30, EndLine: 40, Content: "func helper() {}", TokenCount: 8},
	}

	_, err := store.InsertChunks(ctx, fileID, chunks)
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	got, err := store.GetChunksByFile(ctx, fileID)
	if err != nil {
		t.Fatalf("GetChunksByFile: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(got))
	}

	// Verify ordering by chunk_index.
	for i, c := range got {
		if c.ChunkIndex != i {
			t.Errorf("chunk[%d].ChunkIndex = %d, want %d", i, c.ChunkIndex, i)
		}
		if c.FileID != fileID {
			t.Errorf("chunk[%d].FileID = %d, want %d", i, c.FileID, fileID)
		}
	}

	// Verify field mapping.
	if got[0].SymbolName != "MyClass" {
		t.Errorf("chunk[0].SymbolName = %q, want MyClass", got[0].SymbolName)
	}
	if got[0].Kind != "class" {
		t.Errorf("chunk[0].Kind = %q, want class", got[0].Kind)
	}
	if got[0].Signature != "class MyClass" {
		t.Errorf("chunk[0].Signature = %q, want 'class MyClass'", got[0].Signature)
	}
	if got[0].TokenCount != 30 {
		t.Errorf("chunk[0].TokenCount = %d, want 30", got[0].TokenCount)
	}
	if got[0].StartLine != 1 || got[0].EndLine != 50 {
		t.Errorf("chunk[0] lines = %d-%d, want 1-50", got[0].StartLine, got[0].EndLine)
	}

	// Second chunk should have a parent.
	if got[1].ParentChunkID == nil {
		t.Fatal("expected chunk[1].ParentChunkID to be set")
	}
	if *got[1].ParentChunkID != got[0].ID {
		t.Errorf("chunk[1].ParentChunkID = %d, want %d", *got[1].ParentChunkID, got[0].ID)
	}

	// Third chunk should have no parent.
	if got[2].ParentChunkID != nil {
		t.Errorf("expected chunk[2].ParentChunkID to be nil, got %d", *got[2].ParentChunkID)
	}
}
