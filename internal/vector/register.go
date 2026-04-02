package vector

import "github.com/shaktimanai/shaktiman/internal/types"

// init registers the built-in vector backends with the provider registry.
// Future backends (qdrant, pgvector) will register from their own sub-packages.
func init() {
	RegisterVectorStore("brute_force", func(cfg VectorStoreConfig) (types.VectorStore, error) {
		return NewBruteForceStore(cfg.Dims), nil
	})

	RegisterVectorStore("hnsw", func(cfg VectorStoreConfig) (types.VectorStore, error) {
		return NewHNSWStore(HNSWStoreInput{
			Dim: cfg.Dims,
		})
	})
}
