//go:build postgres

package testutil

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// testPgConnStrs stores the per-test Postgres connection string (with
// search_path set to the test's isolated schema). The pgvector factory
// reads this to create a pool in the same schema.
var testPgConnStrs sync.Map // t.Name() -> string

func init() {
	// Override the default postgres path: each test gets its own schema
	// so parallel tests don't interfere with each other.
	extraMetadataFactories["postgres"] = newPostgresTestStore
}

func newPostgresTestStore(t *testing.T) types.WriterStore {
	t.Helper()

	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	ctx := context.Background()

	// Create a unique schema per test for full parallel isolation.
	safe := strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name())
	schema := fmt.Sprintf("t_%s_%d", safe, time.Now().UnixNano()%1e6)
	// Postgres identifiers max 63 chars — truncate if needed.
	if len(schema) > 63 {
		schema = schema[:63]
	}

	// Admin pool to create the isolated schema.
	adminPool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		adminPool.Close()
		t.Fatalf("CREATE SCHEMA: %v", err)
	}
	adminPool.Close()

	// Build connection string that sets search_path to the test schema.
	sep := "?"
	if strings.Contains(connStr, "?") {
		sep = "&"
	}
	testConnStr := fmt.Sprintf("%s%ssearch_path=%s", connStr, sep, schema)

	cfg := storage.MetadataStoreConfig{
		Backend:         "postgres",
		PostgresConnStr: testConnStr,
		PostgresMaxOpen: 5,
		PostgresMaxIdle: 2,
		PostgresSchema:  schema,
	}

	store, _, closer, err := storage.NewMetadataStore(cfg)
	if err != nil {
		t.Fatalf("NewTestWriterStore(postgres): %v", err)
	}

	// Publish the conn string so the pgvector factory can reuse it.
	testPgConnStrs.Store(t.Name(), testConnStr)

	t.Cleanup(func() {
		testPgConnStrs.Delete(t.Name())
		closer()
		// Drop the entire test schema.
		cleanup, cerr := pgxpool.New(context.Background(), connStr)
		if cerr == nil {
			cleanup.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA %q CASCADE", schema))
			cleanup.Close()
		}
	})

	return store
}
