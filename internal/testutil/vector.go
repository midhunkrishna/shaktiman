// Package testutil provides backend-agnostic test helpers for creating
// MetadataStore and VectorStore instances. The active backend is selected
// via environment variables, allowing CI to run the full test suite
// through different backend combinations.
package testutil

import (
	"os"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// testVectorFactory creates a VectorStore for testing. Implementations
// are registered from build-tagged init() functions for backends that
// need special setup (qdrant, pgvector).
type testVectorFactory func(t *testing.T, dims int) types.VectorStore

var extraVectorFactories = map[string]testVectorFactory{}

// NewTestVectorStore creates a VectorStore using the backend specified by
// SHAKTIMAN_TEST_VECTOR_BACKEND (default: "brute_force"). The store is
// closed automatically via t.Cleanup.
//
// For pgvector: the Postgres MetadataStore must be created first (via
// NewTestWriterStore) so the chunks table exists for FK constraints.
func NewTestVectorStore(t *testing.T, dims int) types.VectorStore {
	t.Helper()

	backend := os.Getenv("SHAKTIMAN_TEST_VECTOR_BACKEND")
	if backend == "" {
		backend = "brute_force"
	}

	// Backends requiring special setup register their own factory.
	if f, ok := extraVectorFactories[backend]; ok {
		return f(t, dims)
	}

	// Default path: use the production registry directly.
	if !vector.HasVectorStore(backend) {
		t.Skipf("vector backend %q not compiled in (missing build tag?)", backend)
		return nil
	}

	cfg := vector.VectorStoreConfig{
		Backend: backend,
		Dims:    dims,
	}
	store, err := vector.NewVectorStore(cfg)
	if err != nil {
		t.Fatalf("NewTestVectorStore(%s, dims=%d): %v", backend, dims, err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
