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

func TestPgVectorCompliance(t *testing.T) {
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("set SHAKTIMAN_TEST_POSTGRES_URL to run pgvector compliance tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	defer pool.Close()

	// Ensure pgvector extension is available.
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("enable pgvector extension (is it installed?): %v", err)
	}

	vectortest.RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()

		// Clean slate: drop embeddings first (FK depends on chunks), then base tables.
		pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")
		for _, table := range []string{"diff_symbols", "diff_log", "edges", "pending_edges",
			"symbols", "chunks", "files", "access_log", "working_set",
			"tool_calls", "schema_version", "config"} {
			pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
		}

		// Create base Postgres schema (embeddings REFERENCES chunks).
		if err := postgres.Migrate(ctx, pool); err != nil {
			t.Fatalf("Migrate base schema: %v", err)
		}

		// Seed dummy file + chunks to satisfy FK constraint.
		// The compliance suite uses chunk IDs 1-10 and 42.
		pool.Exec(ctx, `INSERT INTO files (id, path, content_hash, mtime, language, indexed_at)
			VALUES (1, 'test.go', 'abc', 0, 'go', NOW()) ON CONFLICT DO NOTHING`)
		for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 42, 999} {
			pool.Exec(ctx, `INSERT INTO chunks (id, file_id, chunk_index, kind, start_line, end_line, content, token_count)
				VALUES ($1, 1, 0, 'function', 1, 10, 'test', 10) ON CONFLICT DO NOTHING`, id)
		}

		store, err := NewPgVectorStore(pool, dims)
		if err != nil {
			t.Fatalf("NewPgVectorStore: %v", err)
		}

		t.Cleanup(func() {
			store.Close()
			pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")
		})

		return store
	})
}
