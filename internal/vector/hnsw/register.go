// Package hnsw implements a vector store backed by an HNSW (Hierarchical
// Navigable Small World) index for approximate nearest-neighbor search.
package hnsw

import (
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("hnsw", func(cfg vector.StoreConfig) (types.VectorStore, error) {
		return NewStore(StoreInput{Dim: cfg.Dims})
	})
}
