package testutil

import (
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// metadataCleanupFn performs backend-specific cleanup (e.g. DROP+re-migrate
// for Postgres). Registered from build-tagged init() functions.
type metadataCleanupFn func(t *testing.T, store types.WriterStore)

var metadataCleanupFns = map[string]metadataCleanupFn{}

// NewTestWriterStore creates a WriterStore using the backend specified by
// SHAKTIMAN_TEST_DB_BACKEND (default: "sqlite"). The store is closed
// automatically via t.Cleanup.
//
// For sqlite, each call returns a fresh in-memory database.
// For postgres, tables are dropped and re-migrated for test isolation.
func NewTestWriterStore(t *testing.T) types.WriterStore {
	t.Helper()

	backend := os.Getenv("SHAKTIMAN_TEST_DB_BACKEND")
	if backend == "" {
		backend = "sqlite"
	}

	cfg := storage.MetadataStoreConfig{
		Backend: backend,
	}

	switch backend {
	case "sqlite":
		cfg.SQLiteInMemory = true
	case "postgres":
		cfg.PostgresConnStr = os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
		if cfg.PostgresConnStr == "" {
			t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
		}
		cfg.PostgresMaxOpen = 5
		cfg.PostgresMaxIdle = 2
		cfg.PostgresSchema = "public"
	default:
		t.Fatalf("unknown db backend: %s", backend)
	}

	store, _, closer, err := storage.NewMetadataStore(cfg)
	if err != nil {
		t.Fatalf("NewTestWriterStore(%s): %v", backend, err)
	}

	// Backend-specific cleanup (e.g. drop+re-migrate for Postgres).
	if fn, ok := metadataCleanupFns[backend]; ok {
		fn(t, store)
	}

	t.Cleanup(func() { closer() })
	return store
}
