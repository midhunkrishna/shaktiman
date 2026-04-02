//go:build pgvector

package pgvector

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

		// Clean slate: drop and recreate the embeddings table per test.
		pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")

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
