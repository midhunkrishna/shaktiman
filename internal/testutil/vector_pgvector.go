//go:build postgres && pgvector

package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector/pgvector"
)

func init() {
	extraVectorFactories["pgvector"] = newPgVectorTestStore
}

func newPgVectorTestStore(t *testing.T, dims int) types.VectorStore {
	t.Helper()

	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	// Clean and recreate the embeddings table for test isolation.
	// The base schema (chunks table for FK) must already exist — call
	// NewTestWriterStore before NewTestVectorStore when using pgvector.
	pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")

	if err := pgvector.Migrate(ctx, pool, dims); err != nil {
		t.Fatalf("pgvector.Migrate: %v", err)
	}

	store, err := pgvector.NewPgVectorStore(pool, dims)
	if err != nil {
		t.Fatalf("NewPgVectorStore: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
		pool.Exec(context.Background(), "DROP TABLE IF EXISTS embeddings")
		pool.Close()
	})
	return store
}
