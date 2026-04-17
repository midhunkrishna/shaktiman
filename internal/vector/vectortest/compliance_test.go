package vectortest

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func TestBruteForceCompliance(t *testing.T) {
	if !vector.HasVectorStore("brute_force") {
		t.Skip("brute_force backend not compiled in")
	}
	RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		vs, err := vector.NewVectorStore(vector.StoreConfig{
			Backend: "brute_force",
			Dims:    dims,
		})
		if err != nil {
			t.Fatalf("NewVectorStore brute_force: %v", err)
		}
		return vs
	})
}

func TestHNSWCompliance(t *testing.T) {
	if !vector.HasVectorStore("hnsw") {
		t.Skip("hnsw backend not compiled in")
	}
	RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		vs, err := vector.NewVectorStore(vector.StoreConfig{
			Backend: "hnsw",
			Dims:    dims,
		})
		if err != nil {
			t.Fatalf("NewVectorStore hnsw: %v", err)
		}
		return vs
	})
}
