package sqlite

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestSQLiteLifecycle_OnStartup_FreshDB(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	lifecycle := NewLifecycle(store)

	// OnStartup on a fresh DB should succeed (no stale FTS, triggers already exist)
	if err := lifecycle.OnStartup(context.Background()); err != nil {
		t.Fatalf("OnStartup: %v", err)
	}
}

func TestSQLiteLifecycle_OnStartup_WithData(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()

	// Seed data
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, 1, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "main",
			StartLine: 1, EndLine: 10, Content: "func main() {}", TokenCount: 5},
	})

	lifecycle := NewLifecycle(store)

	// OnStartup should succeed and ensure triggers + FTS are healthy
	if err := lifecycle.OnStartup(ctx); err != nil {
		t.Fatalf("OnStartup: %v", err)
	}

	// Search should work after startup
	results, err := store.KeywordSearch(ctx, "main", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result, got %d", len(results))
	}
}

func TestSQLiteLifecycle_BulkWriteCycle(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()
	lifecycle := NewLifecycle(store)

	// Begin bulk write (disables triggers)
	if err := lifecycle.OnBulkWriteBegin(ctx); err != nil {
		t.Fatalf("OnBulkWriteBegin: %v", err)
	}

	// Insert data while triggers are disabled
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, 1, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Foo",
			StartLine: 1, EndLine: 5, Content: "func Foo() {}", TokenCount: 3},
		{ChunkIndex: 1, Kind: "function", SymbolName: "Bar",
			StartLine: 6, EndLine: 10, Content: "func Bar() {}", TokenCount: 3},
	})

	// Search should NOT find results (triggers disabled, FTS not updated)
	results, err := store.KeywordSearch(ctx, "Foo", 10)
	if err != nil {
		t.Fatalf("KeywordSearch during bulk: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 search results during bulk write, got %d", len(results))
	}

	// End bulk write (rebuild + re-enable triggers)
	if err := lifecycle.OnBulkWriteEnd(ctx); err != nil {
		t.Fatalf("OnBulkWriteEnd: %v", err)
	}

	// Now search should find results
	results, err = store.KeywordSearch(ctx, "Foo", 10)
	if err != nil {
		t.Fatalf("KeywordSearch after bulk: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result after bulk write, got %d", len(results))
	}
}

func TestSQLiteLifecycle_BulkWriteEnd_RestoresTriggers(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()
	lifecycle := NewLifecycle(store)

	// Full bulk write cycle
	lifecycle.OnBulkWriteBegin(ctx)
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "a.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, 1, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Alpha",
			StartLine: 1, EndLine: 5, Content: "func Alpha() {}", TokenCount: 3},
	})
	lifecycle.OnBulkWriteEnd(ctx)

	// After OnBulkWriteEnd, triggers should be restored.
	// Insert more data — triggers should auto-update FTS.
	store.InsertChunks(ctx, 1, []types.ChunkRecord{
		{ChunkIndex: 1, Kind: "function", SymbolName: "Beta",
			StartLine: 6, EndLine: 10, Content: "func Beta() {}", TokenCount: 3},
	})

	// Both should be findable
	results, err := store.KeywordSearch(ctx, "Beta", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for Beta (trigger-inserted), got %d", len(results))
	}
}

func TestSQLiteLifecycle_OnBulkWriteEnd_Idempotent(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()
	lifecycle := NewLifecycle(store)

	// Calling OnBulkWriteEnd without OnBulkWriteBegin should still work
	if err := lifecycle.OnBulkWriteEnd(ctx); err != nil {
		t.Fatalf("OnBulkWriteEnd without Begin: %v", err)
	}

	// Calling twice should also be fine
	if err := lifecycle.OnBulkWriteEnd(ctx); err != nil {
		t.Fatalf("second OnBulkWriteEnd: %v", err)
	}
}

func TestSQLiteLifecycle_OnStartup_Idempotent(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	lifecycle := NewLifecycle(store)
	ctx := context.Background()

	// Multiple OnStartup calls should all succeed
	for i := 0; i < 3; i++ {
		if err := lifecycle.OnStartup(ctx); err != nil {
			t.Fatalf("OnStartup call %d: %v", i+1, err)
		}
	}
}
