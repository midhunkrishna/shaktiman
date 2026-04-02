package testutil

import (
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// extraMetadataFactory creates a WriterStore with backend-specific setup
// (e.g. per-test Postgres schema). Registered from build-tagged init().
type extraMetadataFactory func(t *testing.T) types.WriterStore

var extraMetadataFactories = map[string]extraMetadataFactory{}

// NewTestWriterStore creates a WriterStore using the backend specified by
// SHAKTIMAN_TEST_DB_BACKEND (default: "sqlite"). The store is closed
// automatically via t.Cleanup.
//
// For sqlite, each call returns a fresh in-memory database.
// For postgres, each call creates an isolated schema for parallel safety.
func NewTestWriterStore(t *testing.T) types.WriterStore {
	t.Helper()

	backend := os.Getenv("SHAKTIMAN_TEST_DB_BACKEND")
	if backend == "" {
		backend = "sqlite"
	}

	// Backends that need special setup register their own factory.
	if f, ok := extraMetadataFactories[backend]; ok {
		return f(t)
	}

	// Default: use the production registry.
	cfg := storage.MetadataStoreConfig{
		Backend:        backend,
		SQLiteInMemory: true,
	}

	store, _, closer, err := storage.NewMetadataStore(cfg)
	if err != nil {
		t.Fatalf("NewTestWriterStore(%s): %v", backend, err)
	}
	t.Cleanup(func() { closer() })
	return store
}
