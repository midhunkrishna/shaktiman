//go:build pgvector

package pgvector

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/storage/postgres"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector/vectortest"
)

// testPool creates a pgxpool connected to the test Postgres or skips.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("set SHAKTIMAN_TEST_POSTGRES_URL to run pgvector tests")
	}
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// setupSchema creates base tables + seeds chunk rows for FK compliance.
func setupSchema(t *testing.T, pool *pgxpool.Pool, dims int) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"embeddings", "diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config", "goose_db_version", "projects"} {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}
	if err := postgres.RunMigrations(ctx, pool, dims); err != nil {
		t.Fatalf("Migrate base schema: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO files (id, path, content_hash, mtime, language, indexed_at, project_id)
		VALUES (1, 'test.go', 'abc', 0, 'go', NOW(), 1) ON CONFLICT DO NOTHING`)
	for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 42, 999} {
		pool.Exec(ctx, `INSERT INTO chunks (id, file_id, chunk_index, kind, start_line, end_line, content, token_count)
			VALUES ($1, 1, 0, 'function', 1, 10, 'test', 10) ON CONFLICT DO NOTHING`, id)
	}
}

func TestPgVectorCompliance(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("enable pgvector extension (is it installed?): %v", err)
	}

	vectortest.RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		setupSchema(t, pool, dims)

		store, err := NewStore(pool, dims, 1)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() {
			store.Close()
			pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")
		})
		return store
	})
}

// ── Edge case tests requiring real Postgres ──

func TestMigrate_FullCycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	// Second call is idempotent (goose already applied)
	if err := postgres.RunMigrations(ctx, pool, 4); err != nil {
		t.Fatalf("Migrate idempotent: %v", err)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestValidateDimensions_Match(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 8)

	// Should pass — dims match
	if err := ValidateDimensions(ctx, pool, 8); err != nil {
		t.Fatalf("ValidateDimensions: %v", err)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestValidateDimensions_Mismatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 8)

	// Should fail — dims don't match
	err := ValidateDimensions(ctx, pool, 16)
	if err == nil {
		t.Fatal("expected error for dimension mismatch")
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestValidateDimensions_NoTable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")

	// No embeddings table — should return nil (table will be created later)
	if err := ValidateDimensions(ctx, pool, 768); err != nil {
		t.Fatalf("ValidateDimensions no table: %v", err)
	}
}

func TestNewStore_DimsMismatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	// Table was created with dims=4 by setupSchema
	store, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.Close()

	// Try to open with dims=8 — should fail
	_, err = NewStore(pool, 8, 1)
	if err == nil {
		t.Fatal("expected error for dims mismatch")
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestUpsertBatch_WithZeroVectorsSkipped(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	store, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Mix of zero and non-zero vectors — zeros should be skipped
	err = store.UpsertBatch(ctx,
		[]int64{1, 2, 3},
		[][]float32{
			{0.1, 0.2, 0.3, 0.4}, // valid
			{0, 0, 0, 0},         // zero — skipped
			{0.5, 0.6, 0.7, 0.8}, // valid
		})
	if err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 2 {
		t.Errorf("Count = %d, want 2 (zero vector skipped)", count)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestSearch_WithTimeout(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	store, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	store.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	store.Upsert(ctx, 2, []float32{0, 1, 0, 0})

	// Normal search should complete within timeout
	results, err := store.Search(ctx, []float32{1, 0, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestDelete_Chunking(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 2)

	store, err := NewStore(pool, 2, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Seed more chunk rows
	for i := int64(1); i <= 150; i++ {
		pool.Exec(ctx, `INSERT INTO chunks (id, file_id, chunk_index, kind, start_line, end_line, content, token_count)
			VALUES ($1, 1, 0, 'function', 1, 10, 'test', 10) ON CONFLICT DO NOTHING`, i)
	}

	// Insert more than maxBatchSize vectors
	ids := make([]int64, 110)
	vecs := make([][]float32, 110)
	for i := range ids {
		ids[i] = int64(i + 1)
		vecs[i] = []float32{float32(i) * 0.01, 0.5}
	}
	if err := store.UpsertBatch(ctx, ids, vecs); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}

	// Delete all — should chunk into batches
	if err := store.Delete(ctx, ids); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 0 {
		t.Errorf("Count after delete = %d, want 0", count)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestHealthy_WithPool(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	store, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	if !store.Healthy(ctx) {
		t.Error("expected Healthy = true")
	}
}

func TestPgVectorStore_PurgeAll(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	store, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })

	// Seed vectors
	store.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	store.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	store.Upsert(ctx, 3, []float32{0, 0, 1, 0})

	count, _ := store.Count(ctx)
	if count != 3 {
		t.Fatalf("pre-purge count = %d, want 3", count)
	}

	if err := store.PurgeAll(ctx); err != nil {
		t.Fatalf("PurgeAll: %v", err)
	}

	count, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("Count after purge: %v", err)
	}
	if count != 0 {
		t.Errorf("post-purge count = %d, want 0", count)
	}

	// Store should still be usable
	if err := store.Upsert(ctx, 10, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert after purge: %v", err)
	}
	count, _ = store.Count(ctx)
	if count != 1 {
		t.Errorf("count after re-upsert = %d, want 1", count)
	}
}
