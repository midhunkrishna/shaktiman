//go:build postgres

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage/storetest"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestPostgresMetadataStoreCompliance(t *testing.T) {
	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	storetest.RunMetadataStoreTests(t, func(t *testing.T) types.MetadataStore {
		t.Helper()
		ctx := context.Background()

		store, err := NewPgStore(ctx, connStr, 5, 2, "public")
		if err != nil {
			t.Fatalf("NewPgStore: %v", err)
		}

		// Clean all tables before each test
		pool := store.Pool()
		tables := []string{"diff_symbols", "diff_log", "edges", "pending_edges",
			"symbols", "chunks", "files", "access_log", "working_set",
			"tool_calls", "schema_version", "config"}
		for _, table := range tables {
			pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
		}

		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		t.Cleanup(func() { store.Close() })
		return store
	})
}
