//go:build postgres

package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/storage/postgres"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func init() {
	metadataCleanupFns["postgres"] = cleanupPostgresStore
}

func cleanupPostgresStore(t *testing.T, store types.WriterStore) {
	t.Helper()

	type rawPooler interface{ RawPool() any }
	rp, ok := store.(rawPooler)
	if !ok {
		t.Fatal("postgres store doesn't implement RawPool()")
	}
	pool := rp.RawPool().(*pgxpool.Pool)
	ctx := context.Background()

	// Drop all tables and re-migrate for a clean slate per test.
	for _, table := range []string{
		"embeddings", "diff_symbols", "diff_log", "edges", "pending_edges",
		"symbols", "chunks", "files", "access_log", "working_set",
		"tool_calls", "schema_version", "config",
	} {
		pool.Exec(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}

	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("postgres re-migrate: %v", err)
	}
}
