package storage

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestNewMetadataStore_SQLite(t *testing.T) {
	t.Parallel()

	store, lifecycle, closer, err := NewMetadataStore(MetadataStoreConfig{
		Backend:        "sqlite",
		SQLiteInMemory: true,
	})
	if err != nil {
		t.Fatalf("NewMetadataStore: %v", err)
	}
	defer closer()

	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if lifecycle == nil {
		t.Fatal("expected non-nil lifecycle for SQLite")
	}

	// Verify the store is functional
	ctx := context.Background()
	_, err = store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	f, err := store.GetFileByPath(ctx, "test.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f == nil || f.ContentHash != "h1" {
		t.Errorf("unexpected file: %+v", f)
	}
}

func TestNewMetadataStore_SQLiteWithLifecycle(t *testing.T) {
	t.Parallel()

	store, lifecycle, closer, err := NewMetadataStore(MetadataStoreConfig{
		Backend:        "sqlite",
		SQLiteInMemory: true,
	})
	if err != nil {
		t.Fatalf("NewMetadataStore: %v", err)
	}
	defer closer()

	ctx := context.Background()

	// Lifecycle hooks should work
	if err := lifecycle.OnStartup(ctx); err != nil {
		t.Fatalf("OnStartup: %v", err)
	}
	if err := lifecycle.OnBulkWriteBegin(ctx); err != nil {
		t.Fatalf("OnBulkWriteBegin: %v", err)
	}

	// Insert during bulk mode
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "bulk.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, 1, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "BulkFunc",
			StartLine: 1, EndLine: 5, Content: "func BulkFunc() {}", TokenCount: 3},
	})

	if err := lifecycle.OnBulkWriteEnd(ctx); err != nil {
		t.Fatalf("OnBulkWriteEnd: %v", err)
	}

	// FTS should work after lifecycle cycle
	results, err := store.KeywordSearch(ctx, "BulkFunc", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected FTS results after lifecycle bulk write cycle")
	}
}

func TestNewMetadataStore_UnknownBackend(t *testing.T) {
	t.Parallel()

	_, _, _, err := NewMetadataStore(MetadataStoreConfig{
		Backend: "mysql",
	})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestHasMetadataStore(t *testing.T) {
	if !HasMetadataStore("sqlite") {
		t.Error("expected SQLite to be registered")
	}
	if HasMetadataStore("postgres") {
		t.Error("expected postgres to NOT be registered yet")
	}
	if HasMetadataStore("nonexistent") {
		t.Error("expected nonexistent to NOT be registered")
	}
}

func TestNewMetadataStore_CloserWorks(t *testing.T) {
	t.Parallel()

	_, _, closer, err := NewMetadataStore(MetadataStoreConfig{
		Backend:        "sqlite",
		SQLiteInMemory: true,
	})
	if err != nil {
		t.Fatalf("NewMetadataStore: %v", err)
	}

	// Closer should not error
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
}
