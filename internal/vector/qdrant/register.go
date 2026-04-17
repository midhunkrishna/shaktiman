//go:build qdrant

package qdrant

import (
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("qdrant", func(cfg vector.StoreConfig) (types.VectorStore, error) {
		client := NewClient(cfg.QdrantURL, cfg.QdrantAPIKey)
		return NewStore(client, cfg.QdrantCollection, cfg.Dims, cfg.ProjectID)
	})
}
