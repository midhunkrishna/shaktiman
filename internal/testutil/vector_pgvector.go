//go:build postgres && pgvector

package testutil

import (
	"context"
	"fmt"
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

	// The template DB has embeddings with vector(768). If the test needs
	// different dims, drop and recreate the table in this throwaway clone.
	if dims != 768 {
		pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings")
		pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS embeddings (
			chunk_id BIGINT PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
			embedding vector(%d) NOT NULL,
			project_id BIGINT NOT NULL DEFAULT 1
		)`, dims))
	}

	// Use project_id=1 (the default seeded by migration 005).
	store, err := pgvector.NewStore(pool, dims, 1)
	if err != nil {
		pool.Close()
		t.Fatalf("NewStore: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
		pool.Close()
	})
	return store
}
