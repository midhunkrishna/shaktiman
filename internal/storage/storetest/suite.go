// Package storetest provides compliance test suites for MetadataStore
// and WriterStore implementations. Any backend (SQLite, Postgres) must
// pass these tests to be considered a valid implementation.
package storetest

import (
	"context"
	"testing"
	"time"

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

	t.Run("GetSiblingChunks_ReturnsSplitFragments", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "big.java", ContentHash: "h1", Mtime: 1.0,
			Language: "java", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		// Simulate a method split into 3 fragments by splitLargeChunks
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "header", SymbolName: "",
				StartLine: 1, EndLine: 5, Content: "package foo;", TokenCount: 5},
			{ChunkIndex: 1, Kind: "method", SymbolName: "processWild",
				StartLine: 10, EndLine: 50, Content: "part1", TokenCount: 500},
			{ChunkIndex: 2, Kind: "method", SymbolName: "processWild",
				StartLine: 51, EndLine: 100, Content: "part2", TokenCount: 500},
			{ChunkIndex: 3, Kind: "method", SymbolName: "processWild",
				StartLine: 101, EndLine: 130, Content: "part3", TokenCount: 300},
			{ChunkIndex: 4, Kind: "method", SymbolName: "otherMethod",
				StartLine: 135, EndLine: 150, Content: "other", TokenCount: 100},
		})

		siblings, err := store.GetSiblingChunks(ctx, fileID, "processWild", "method")
		if err != nil {
			t.Fatalf("GetSiblingChunks: %v", err)
		}
		if len(siblings) != 3 {
			t.Errorf("expected 3 sibling fragments, got %d", len(siblings))
		}
		// Verify ordering
		if len(siblings) >= 3 {
			if siblings[0].StartLine != 10 || siblings[2].StartLine != 101 {
				t.Error("siblings not ordered by chunk_index")
			}
		}

		// otherMethod should NOT be included
		for _, s := range siblings {
			if s.SymbolName != "processWild" {
				t.Errorf("unexpected symbol %q in siblings", s.SymbolName)
			}
		}
	})

	t.Run("GetSiblingChunks_SingleChunkNoSiblings", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "small.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "SmallFunc",
				StartLine: 1, EndLine: 10, Content: "func SmallFunc() {}", TokenCount: 20},
		})

		siblings, err := store.GetSiblingChunks(ctx, fileID, "SmallFunc", "function")
		if err != nil {
			t.Fatalf("GetSiblingChunks: %v", err)
		}
		if len(siblings) != 1 {
			t.Errorf("expected 1 chunk (not split), got %d", len(siblings))
		}
	})
}

// WriterStoreFactory creates a fresh WriterStore for each test.
type WriterStoreFactory func(t *testing.T) types.WriterStore

// RunWriterStoreTests runs compliance tests for WriterStore-specific methods
// that are not covered by RunMetadataStoreTests.
func RunWriterStoreTests(t *testing.T, factory WriterStoreFactory) {
	t.Run("DeleteFileByPath", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "bypath.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "F",
				StartLine: 1, EndLine: 5, Content: "func F() {}", TokenCount: 3},
		})

		deletedID, err := store.DeleteFileByPath(ctx, "bypath.go")
		if err != nil {
			t.Fatalf("DeleteFileByPath: %v", err)
		}
		if deletedID != fileID {
			t.Errorf("deleted ID = %d, want %d", deletedID, fileID)
		}

		chunks, _ := store.GetChunksByFile(ctx, fileID)
		if len(chunks) != 0 {
			t.Error("expected chunks to be cascade-deleted")
		}
	})

	t.Run("DeleteFileByPath_NotFound", func(t *testing.T) {
		store := factory(t)
		deletedID, err := store.DeleteFileByPath(context.Background(), "nonexistent.go")
		if err != nil {
			t.Fatalf("DeleteFileByPath not found: %v", err)
		}
		if deletedID != 0 {
			t.Errorf("expected 0 for missing file, got %d", deletedID)
		}
	})

	t.Run("GetEmbeddedChunkIDsByFile", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "embed.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "A",
				StartLine: 1, EndLine: 5, Content: "func A() {}", TokenCount: 3},
			{ChunkIndex: 1, Kind: "function", SymbolName: "B",
				StartLine: 6, EndLine: 10, Content: "func B() {}", TokenCount: 3},
			{ChunkIndex: 2, Kind: "function", SymbolName: "C",
				StartLine: 11, EndLine: 15, Content: "func C() {}", TokenCount: 3},
		})

		// Mark only first two chunks as embedded.
		if err := store.MarkChunksEmbedded(ctx, chunkIDs[:2]); err != nil {
			t.Fatalf("MarkChunksEmbedded: %v", err)
		}

		got, err := store.GetEmbeddedChunkIDsByFile(ctx, fileID)
		if err != nil {
			t.Fatalf("GetEmbeddedChunkIDsByFile: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 embedded chunk IDs, got %d", len(got))
		}
	})

	t.Run("GetEmbeddedChunkIDsByFile_NoneEmbedded", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "noembed.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "X",
				StartLine: 1, EndLine: 5, Content: "func X() {}", TokenCount: 3},
		})

		got, err := store.GetEmbeddedChunkIDsByFile(ctx, fileID)
		if err != nil {
			t.Fatalf("GetEmbeddedChunkIDsByFile: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 embedded chunk IDs, got %d", len(got))
		}
	})

	t.Run("UpdateChunkParents", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
			Path: "parents.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Parent",
				StartLine: 1, EndLine: 20, Content: "func Parent() {}", TokenCount: 5},
			{ChunkIndex: 1, Kind: "function", SymbolName: "Child",
				StartLine: 5, EndLine: 10, Content: "func Child() {}", TokenCount: 3},
		})

		updates := map[int64]int64{chunkIDs[1]: chunkIDs[0]}
		if err := store.UpdateChunkParents(ctx, updates); err != nil {
			t.Fatalf("UpdateChunkParents: %v", err)
		}

		chunks, _ := store.GetChunksByFile(ctx, fileID)
		found := false
		for _, c := range chunks {
			if c.ID == chunkIDs[1] && c.ParentChunkID != nil && *c.ParentChunkID == chunkIDs[0] {
				found = true
			}
		}
		if !found {
			t.Error("expected child chunk to have parent set")
		}
	})

	t.Run("UpdateChunkParents_EmptyMap", func(t *testing.T) {
		store := factory(t)
		if err := store.UpdateChunkParents(context.Background(), map[int64]int64{}); err != nil {
			t.Fatalf("UpdateChunkParents empty: %v", err)
		}
	})

	t.Run("RecordToolCalls", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		records := []types.ToolCallRecord{
			{
				SessionID: "sess-1", Timestamp: time.Now(),
				ToolName: "search", ArgsJSON: `{"q":"test"}`,
				ArgsBytes: 12, ResponseBytes: 256, ResponseTokensEst: 64,
				ResultCount: 3, DurationMs: 42, IsError: false,
			},
			{
				SessionID: "sess-1", Timestamp: time.Now(),
				ToolName: "context", ArgsJSON: `{"q":"auth"}`,
				ArgsBytes: 10, ResponseBytes: 512, ResponseTokensEst: 128,
				ResultCount: 5, DurationMs: 100, IsError: true,
			},
		}
		if err := store.RecordToolCalls(ctx, records); err != nil {
			t.Fatalf("RecordToolCalls: %v", err)
		}
	})

	t.Run("RecordToolCalls_Empty", func(t *testing.T) {
		store := factory(t)
		if err := store.RecordToolCalls(context.Background(), nil); err != nil {
			t.Fatalf("RecordToolCalls empty: %v", err)
		}
	})

	t.Run("GetConfig_Missing", func(t *testing.T) {
		store := factory(t)
		val, err := store.GetConfig(context.Background(), "nonexistent_key")
		if err != nil {
			t.Fatalf("GetConfig missing key: %v", err)
		}
		if val != "" {
			t.Errorf("expected empty string for missing key, got %q", val)
		}
	})

	t.Run("SetConfig_And_GetConfig", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		if err := store.SetConfig(ctx, "parser_version", "v2"); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		val, err := store.GetConfig(ctx, "parser_version")
		if err != nil {
			t.Fatalf("GetConfig: %v", err)
		}
		if val != "v2" {
			t.Errorf("GetConfig = %q, want v2", val)
		}
	})

	t.Run("SetConfig_Overwrite", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		store.SetConfig(ctx, "key", "old")
		store.SetConfig(ctx, "key", "new")

		val, _ := store.GetConfig(ctx, "key")
		if val != "new" {
			t.Errorf("GetConfig after overwrite = %q, want new", val)
		}
	})
}

// RunGraphMutatorTests runs compliance tests for graph mutation operations.
func RunGraphMutatorTests(t *testing.T, factory WriterStoreFactory) {
	// helper: creates a file with one chunk and one symbol, returns IDs.
	insertFCS := func(t *testing.T, store types.WriterStore, path, symbolName string) (fileID, chunkID, symbolID int64) {
		t.Helper()
		ctx := context.Background()
		fileID, _ = store.UpsertFile(ctx, &types.FileRecord{
			Path: path, ContentHash: "h_" + path, Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		})
		chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: symbolName,
				StartLine: 1, EndLine: 10, Content: "func " + symbolName + "() {}", TokenCount: 5},
		})
		symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
			{ChunkID: chunkIDs[0], Name: symbolName, Kind: "function", Line: 1,
				Visibility: "exported", IsExported: true},
		})
		return fileID, chunkIDs[0], symIDs[0]
	}

	t.Run("InsertEdges_Resolved", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileA, _, symA := insertFCS(t, store, "a.go", "FuncA")
		_, _, symB := insertFCS(t, store, "b.go", "FuncB")

		edges := []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}
		symbolIDs := map[string]int64{"FuncA": symA, "FuncB": symB}

		err := store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.InsertEdges(ctx, tx, fileA, edges, symbolIDs, "go")
		})
		if err != nil {
			t.Fatalf("InsertEdges: %v", err)
		}

		// Verify via Neighbors.
		neighbors, err := store.Neighbors(ctx, symA, 1, "outgoing")
		if err != nil {
			t.Fatalf("Neighbors: %v", err)
		}
		if len(neighbors) == 0 {
			t.Error("expected FuncB as neighbor of FuncA")
		}
	})

	t.Run("InsertEdges_Pending", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileA, _, symA := insertFCS(t, store, "a.go", "Caller")

		// Edge to unknown symbol — should become pending.
		edges := []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Unknown", Kind: "calls"},
		}
		symbolIDs := map[string]int64{"Caller": symA}

		err := store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.InsertEdges(ctx, tx, fileA, edges, symbolIDs, "go")
		})
		if err != nil {
			t.Fatalf("InsertEdges pending: %v", err)
		}

		// Verify pending edge exists.
		callers, err := store.PendingEdgeCallers(ctx, "Unknown")
		if err != nil {
			t.Fatalf("PendingEdgeCallers: %v", err)
		}
		if len(callers) != 1 || callers[0] != symA {
			t.Errorf("PendingEdgeCallers = %v, want [%d]", callers, symA)
		}
	})

	t.Run("PendingEdgeCallers_NoMatch", func(t *testing.T) {
		store := factory(t)
		callers, err := store.PendingEdgeCallers(context.Background(), "NothingHere")
		if err != nil {
			t.Fatalf("PendingEdgeCallers no match: %v", err)
		}
		if len(callers) != 0 {
			t.Errorf("expected empty, got %v", callers)
		}
	})

	t.Run("PendingEdgeCallersWithKind", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileA, _, symA := insertFCS(t, store, "a.go", "Src")

		edges := []types.EdgeRecord{
			{SrcSymbolName: "Src", DstSymbolName: "ExtLib", DstQualifiedName: "github.com/ext/lib.ExtLib", Kind: "calls"},
		}
		symbolIDs := map[string]int64{"Src": symA}

		err := store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.InsertEdges(ctx, tx, fileA, edges, symbolIDs, "go")
		})
		if err != nil {
			t.Fatalf("InsertEdges: %v", err)
		}

		results, err := store.PendingEdgeCallersWithKind(ctx, "ExtLib")
		if err != nil {
			t.Fatalf("PendingEdgeCallersWithKind: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].SrcSymbolID != symA {
			t.Errorf("SrcSymbolID = %d, want %d", results[0].SrcSymbolID, symA)
		}
		if results[0].Kind != "calls" {
			t.Errorf("Kind = %q, want calls", results[0].Kind)
		}
	})

	t.Run("DeleteEdgesByFile", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		fileA, _, symA := insertFCS(t, store, "a.go", "FuncA")
		_, _, symB := insertFCS(t, store, "b.go", "FuncB")

		edges := []types.EdgeRecord{
			{SrcSymbolName: "FuncA", DstSymbolName: "FuncB", Kind: "calls"},
		}
		symbolIDs := map[string]int64{"FuncA": symA, "FuncB": symB}

		store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.InsertEdges(ctx, tx, fileA, edges, symbolIDs, "go")
		})

		// Delete edges for fileA.
		err := store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.DeleteEdgesByFile(ctx, tx, fileA)
		})
		if err != nil {
			t.Fatalf("DeleteEdgesByFile: %v", err)
		}

		neighbors, _ := store.Neighbors(ctx, symA, 1, "outgoing")
		if len(neighbors) != 0 {
			t.Error("expected no neighbors after edge deletion")
		}
	})

	t.Run("ResolvePendingEdges", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()

		// Create caller with pending edge to "Target".
		fileA, _, symA := insertFCS(t, store, "a.go", "Caller")
		edges := []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Target", Kind: "calls"},
		}
		store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.InsertEdges(ctx, tx, fileA, edges, map[string]int64{"Caller": symA}, "go")
		})

		// Now define "Target" — this should resolve the pending edge.
		_, _, _ = insertFCS(t, store, "b.go", "Target")

		err := store.WithWriteTx(ctx, func(tx types.TxHandle) error {
			return store.ResolvePendingEdges(ctx, tx, []string{"Target"})
		})
		if err != nil {
			t.Fatalf("ResolvePendingEdges: %v", err)
		}

		// Verify: pending should be empty, resolved edge should exist.
		callers, _ := store.PendingEdgeCallers(ctx, "Target")
		if len(callers) != 0 {
			t.Errorf("expected 0 pending callers after resolve, got %d", len(callers))
		}

		neighbors, _ := store.Neighbors(ctx, symA, 1, "outgoing")
		if len(neighbors) == 0 {
			t.Error("expected resolved edge to appear in Neighbors")
		}
	})
}
