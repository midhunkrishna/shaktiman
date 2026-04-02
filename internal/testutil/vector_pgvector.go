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

	// Prefer the per-test connection URL from NewTestWriterStore (which
	// points to a pgtestdb-managed database with migrations applied).
	var connStr string
	if v, ok := testPgConnStrs.Load(t.Name()); ok {
		connStr = v.(string)
	} else {
		connStr = os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
		if connStr == "" {
			t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
		}
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	// The embeddings table already exists in the pgtestdb template database
	// (created by goose migration 003). Just create the store.
	store, err := pgvector.NewPgVectorStore(pool, dims)
	if err != nil {
		pool.Close()
		t.Fatalf("NewPgVectorStore: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
		pool.Close()
	})
	return store
}
