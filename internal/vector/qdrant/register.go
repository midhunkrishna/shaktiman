//go:build qdrant

package qdrant

import (
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("qdrant", func(cfg vector.VectorStoreConfig) (types.VectorStore, error) {
		client := NewClient(cfg.QdrantURL, cfg.QdrantAPIKey)
		return NewQdrantStore(client, cfg.QdrantCollection, cfg.Dims)
	})
}
