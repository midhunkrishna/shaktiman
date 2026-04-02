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

	// Store is the MetadataStore, passed so backends like pgvector can
	// extract a shared resource (e.g. connection pool) via type assertion.
	// The daemon passes this; backends that don't need it ignore it.
	Store interface{}
}

// VectorStoreConfigFrom extracts a VectorStoreConfig from the application config
// and the active MetadataStore. This keeps backend-specific wiring out of the daemon.
func VectorStoreConfigFrom(cfg types.Config, store interface{}) VectorStoreConfig {
	vsc := VectorStoreConfig{
		Backend:          cfg.VectorBackend,
		Dims:             cfg.EmbeddingDims,
		QdrantURL:        cfg.QdrantURL,
		QdrantCollection: cfg.QdrantCollection,
		QdrantAPIKey:     cfg.QdrantAPIKey,
		Store:            store,
	}

	// pgvector extracts the pool from the store — let it self-serve.
	if cfg.VectorBackend == "pgvector" {
		type rawPooler interface{ RawPool() any }
		if p, ok := store.(rawPooler); ok {
			vsc.PgPool = p.RawPool()
		}
	}

	return vsc
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
