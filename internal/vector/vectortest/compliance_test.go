package vectortest

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func TestBruteForceCompliance(t *testing.T) {
	RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		return vector.NewBruteForceStore(dims)
	})
}

func TestHNSWCompliance(t *testing.T) {
	RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		vs, err := vector.NewHNSWStore(vector.HNSWStoreInput{Dim: dims})
		if err != nil {
			t.Fatalf("NewHNSWStore: %v", err)
		}
		return vs
	})
}
