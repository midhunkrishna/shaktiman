package vector

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// newTestVectorStore creates a brute_force VectorStore via the registry.
// The factory is registered by the blank imports in
// imports_bruteforce_xtest_test.go (requires the bruteforce build tag).
func newTestVectorStore(t testing.TB, dims int) types.VectorStore {
	t.Helper()
	if !HasVectorStore("brute_force") {
		t.Skip("brute_force backend not compiled in (missing build tag?)")
	}
	vs, err := NewVectorStore(StoreConfig{
		Backend: "brute_force",
		Dims:    dims,
	})
	if err != nil {
		t.Fatalf("newTestVectorStore: %v", err)
	}
	t.Cleanup(func() { vs.Close() })
	return vs
}
