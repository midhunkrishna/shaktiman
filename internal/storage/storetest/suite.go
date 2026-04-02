// Package storetest provides compliance test suites for MetadataStore
// and WriterStore implementations. Any backend (SQLite, Postgres) must
// pass these tests to be considered a valid implementation.
package storetest

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// MetadataStoreFactory creates a fresh MetadataStore for each test.
// The store should be empty (freshly migrated).
type MetadataStoreFactory func(t *testing.T) types.MetadataStore

// RunMetadataStoreTests runs the full compliance suite against a MetadataStore.
func RunMetadataStoreTests(t *testing.T, factory MetadataStoreFactory) {
	t.Run("UpsertFile_Insert", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		id, err := store.UpsertFile(ctx, &types.FileRecord{
			Path: "main.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		if err != nil {
			t.Fatalf("UpsertFile: %v", err)
		}
		if id == 0 {
			t.Error("expected non-zero file ID")
		}
	})

	t.Run("UpsertFile_Update", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		id1, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "main.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		id2, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "main.go", ContentHash: "h2", Mtime: 2.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		if id1 != id2 {
			t.Errorf("upsert should return same ID: got %d and %d", id1, id2)
		}
		f, _ := store.GetFileByPath(ctx, "main.go")
		if f.ContentHash != "h2" {
			t.Errorf("ContentHash = %q, want h2", f.ContentHash)
		}
	})

	t.Run("GetFileByPath_NotFound", func(t *testing.T) {
		store := factory(t)
		f, err := store.GetFileByPath(context.Background(), "nonexistent.go")
		if err == nil && f != nil {
			t.Error("expected nil or error for nonexistent file")
		}
	})

	t.Run("ListFiles", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		store.UpsertFile(ctx, &types.FileRecord{
			Path: "a.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.UpsertFile(ctx, &types.FileRecord{
			Path: "b.go", ContentHash: "h2", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})

		files, err := store.ListFiles(ctx)
		if err != nil {
			t.Fatalf("ListFiles: %v", err)
		}
		if len(files) != 2 {
			t.Errorf("ListFiles: got %d, want 2", len(files))
		}
	})

	t.Run("DeleteFile_Cascades", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "del.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Foo",
				StartLine: 1, EndLine: 5, Content: "func Foo() {}", TokenCount: 3},
		})

		if err := store.DeleteFile(ctx, fileID); err != nil {
			t.Fatalf("DeleteFile: %v", err)
		}
		chunks, _ := store.GetChunksByFile(ctx, fileID)
		if len(chunks) != 0 {
			t.Error("expected chunks to be cascade-deleted")
		}
	})

	t.Run("InsertChunks_And_GetByFile", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "chunks.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		ids, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "A",
				StartLine: 1, EndLine: 5, Content: "func A() {}", TokenCount: 3},
			{ChunkIndex: 1, Kind: "function", SymbolName: "B",
				StartLine: 6, EndLine: 10, Content: "func B() {}", TokenCount: 3},
		})
		if err != nil {
			t.Fatalf("InsertChunks: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("expected 2 chunk IDs, got %d", len(ids))
		}

		chunks, err := store.GetChunksByFile(ctx, fileID)
		if err != nil {
			t.Fatalf("GetChunksByFile: %v", err)
		}
		if len(chunks) != 2 {
			t.Errorf("GetChunksByFile: got %d, want 2", len(chunks))
		}
		if chunks[0].SymbolName != "A" || chunks[1].SymbolName != "B" {
			t.Errorf("chunks not ordered by index: %v", chunks)
		}
	})

	t.Run("GetChunkByID", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "c.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		ids, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "X",
				StartLine: 1, EndLine: 5, Content: "func X() {}", TokenCount: 3},
		})

		chunk, err := store.GetChunkByID(ctx, ids[0])
		if err != nil {
			t.Fatalf("GetChunkByID: %v", err)
		}
		if chunk.SymbolName != "X" {
			t.Errorf("SymbolName = %q, want X", chunk.SymbolName)
		}
	})

	t.Run("InsertSymbols_And_GetByName", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "sym.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "MyFunc",
				StartLine: 1, EndLine: 5, Content: "func MyFunc() {}", TokenCount: 3},
		})
		symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
			{ChunkID: chunkIDs[0], Name: "MyFunc", Kind: "function", Line: 1,
				Visibility: "exported", IsExported: true},
		})
		if err != nil {
			t.Fatalf("InsertSymbols: %v", err)
		}
		if len(symIDs) != 1 {
			t.Fatalf("expected 1 symbol ID, got %d", len(symIDs))
		}

		syms, err := store.GetSymbolByName(ctx, "MyFunc")
		if err != nil {
			t.Fatalf("GetSymbolByName: %v", err)
		}
		if len(syms) != 1 || syms[0].Name != "MyFunc" {
			t.Errorf("unexpected symbols: %+v", syms)
		}
	})

	t.Run("GetSymbolByID", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "sid.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Fn",
				StartLine: 1, EndLine: 5, Content: "func Fn() {}", TokenCount: 3},
		})
		if err != nil {
			t.Fatalf("InsertChunks: %v", err)
		}
		symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
			{ChunkID: chunkIDs[0], Name: "Fn", Kind: "function", Line: 1, Visibility: "exported"},
		})
		if err != nil {
			t.Fatalf("InsertSymbols: %v", err)
		}
		if len(symIDs) == 0 {
			t.Fatal("InsertSymbols returned no IDs")
		}

		sym, err := store.GetSymbolByID(ctx, symIDs[0])
		if err != nil {
			t.Fatalf("GetSymbolByID: %v", err)
		}
		if sym == nil || sym.Name != "Fn" {
			t.Errorf("unexpected symbol: %+v", sym)
		}
	})

	t.Run("GetFilePathByID", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "path/to/file.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		path, err := store.GetFilePathByID(ctx, fileID)
		if err != nil {
			t.Fatalf("GetFilePathByID: %v", err)
		}
		if path != "path/to/file.go" {
			t.Errorf("path = %q, want path/to/file.go", path)
		}
	})

	t.Run("GetFileIsTestByID", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		testID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "x_test.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
			IsTest: true,
		})
		implID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "x.go", ContentHash: "h2", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
			IsTest: false,
		})

		isTest, _ := store.GetFileIsTestByID(ctx, testID)
		if !isTest {
			t.Error("expected true for test file")
		}
		isTest, _ = store.GetFileIsTestByID(ctx, implID)
		if isTest {
			t.Error("expected false for impl file")
		}
	})

	t.Run("GetIndexStats", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "stats.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "F",
				StartLine: 1, EndLine: 5, Content: "func F() {}", TokenCount: 3},
		})
		store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
			{ChunkID: 1, Name: "F", Kind: "function", Line: 1, Visibility: "exported"},
		})

		stats, err := store.GetIndexStats(ctx)
		if err != nil {
			t.Fatalf("GetIndexStats: %v", err)
		}
		if stats.TotalFiles != 1 {
			t.Errorf("TotalFiles = %d, want 1", stats.TotalFiles)
		}
		if stats.TotalChunks != 1 {
			t.Errorf("TotalChunks = %d, want 1", stats.TotalChunks)
		}
	})

	t.Run("KeywordSearch", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "search.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "HandleRequest",
				StartLine: 1, EndLine: 10, Content: "func HandleRequest(w http.ResponseWriter, r *http.Request) {}", TokenCount: 15},
		})

		results, err := store.KeywordSearch(ctx, "HandleRequest", 10)
		if err != nil {
			t.Fatalf("KeywordSearch: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 FTS result")
		}
	})

	t.Run("KeywordSearch_Empty", func(t *testing.T) {
		store := factory(t)
		results, err := store.KeywordSearch(context.Background(), "", 10)
		if err != nil {
			t.Fatalf("KeywordSearch empty: %v", err)
		}
		if results != nil {
			t.Errorf("expected nil for empty query, got %d results", len(results))
		}
	})

	t.Run("DeleteChunksByFile", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "dc.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Del",
				StartLine: 1, EndLine: 5, Content: "func Del() {}", TokenCount: 3},
		})

		if err := store.DeleteChunksByFile(ctx, fileID); err != nil {
			t.Fatalf("DeleteChunksByFile: %v", err)
		}
		chunks, _ := store.GetChunksByFile(ctx, fileID)
		if len(chunks) != 0 {
			t.Error("expected 0 chunks after delete")
		}
	})

	t.Run("DeleteSymbolsByFile", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "ds.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "S",
				StartLine: 1, EndLine: 5, Content: "func S() {}", TokenCount: 3},
		})
		store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
			{ChunkID: chunkIDs[0], Name: "S", Kind: "function", Line: 1, Visibility: "exported"},
		})

		if err := store.DeleteSymbolsByFile(ctx, fileID); err != nil {
			t.Fatalf("DeleteSymbolsByFile: %v", err)
		}
		syms, _ := store.GetSymbolsByFile(ctx, fileID)
		if len(syms) != 0 {
			t.Error("expected 0 symbols after delete")
		}
	})
}
