package vector

import (
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// VectorStoreConfig holds backend-agnostic configuration for creating a VectorStore.
type VectorStoreConfig struct {
	Backend  string // "brute_force" (default), "hnsw", "qdrant", "pgvector"
	Dims     int    // vector dimensionality (e.g. 768)
	DataPath string // persistence file path (for BruteForce/HNSW)

	// Qdrant-specific
	QdrantURL        string
	QdrantCollection string
	QdrantAPIKey     string

	// pgvector-specific (pool shared with MetadataStore)
	PgPool interface{} // *pgxpool.Pool, set by daemon when pgvector shares pool
}

// VectorStoreFactory creates a VectorStore from config.
type VectorStoreFactory func(cfg VectorStoreConfig) (types.VectorStore, error)

var vectorStoreFactories = map[string]VectorStoreFactory{}

// RegisterVectorStore registers a factory for a named backend.
// Called from init() in each backend.
func RegisterVectorStore(name string, factory VectorStoreFactory) {
	vectorStoreFactories[name] = factory
}

// NewVectorStore creates a VectorStore for the configured backend.
func NewVectorStore(cfg VectorStoreConfig) (types.VectorStore, error) {
	if cfg.Backend == "" {
		cfg.Backend = "brute_force"
	}
	factory, ok := vectorStoreFactories[cfg.Backend]
	if !ok {
		available := make([]string, 0, len(vectorStoreFactories))
		for name := range vectorStoreFactories {
			available = append(available, name)
		}
		return nil, fmt.Errorf("unknown vector store backend: %q (available: %v)", cfg.Backend, available)
	}
	return factory(cfg)
}

// HasVectorStore returns true if a factory is registered for the named backend.
func HasVectorStore(name string) bool {
	_, ok := vectorStoreFactories[name]
	return ok
}
