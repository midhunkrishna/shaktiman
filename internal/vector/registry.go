package vector

import (
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// StoreConfig holds backend-agnostic configuration for creating a VectorStore.
type StoreConfig struct {
	Backend  string // "brute_force" (default), "hnsw", "qdrant", "pgvector"
	Dims     int    // vector dimensionality (e.g. 768)
	DataPath string // persistence file path (for BruteForce/HNSW)

	// Qdrant-specific
	QdrantURL        string
	QdrantCollection string
	QdrantAPIKey     string

	// pgvector-specific (pool shared with MetadataStore)
	PgPool interface{} // *pgxpool.Pool, set by daemon when pgvector shares pool

	// ProjectID for multi-project isolation (pgvector, qdrant).
	ProjectID int64

	// Store is the MetadataStore, passed so backends like pgvector can
	// extract a shared resource (e.g. connection pool) via type assertion.
	// The daemon passes this; backends that don't need it ignore it.
	Store interface{}
}

// StoreConfigFrom extracts a StoreConfig from the application config
// and the active MetadataStore. This keeps backend-specific wiring out of the daemon.
func StoreConfigFrom(cfg types.Config, store interface{}) StoreConfig {
	vsc := StoreConfig{
		Backend:          cfg.VectorBackend,
		Dims:             cfg.EmbeddingDims,
		QdrantURL:        cfg.QdrantURL,
		QdrantCollection: cfg.QdrantCollection,
		QdrantAPIKey:     cfg.QdrantAPIKey,
		Store:            store,
	}

	// pgvector needs the shared Postgres pool.
	if cfg.VectorBackend == "pgvector" {
		type rawPooler interface{ RawPool() any }
		if p, ok := store.(rawPooler); ok {
			vsc.PgPool = p.RawPool()
		}
	}

	// pgvector and qdrant both support multi-project isolation.
	if cfg.VectorBackend == "pgvector" || cfg.VectorBackend == "qdrant" {
		type projectIDer interface{ ProjectID() int64 }
		if p, ok := store.(projectIDer); ok {
			vsc.ProjectID = p.ProjectID()
		}
	}

	return vsc
}

// StoreFactory creates a VectorStore from config.
type StoreFactory func(cfg StoreConfig) (types.VectorStore, error)

var vectorStoreFactories = map[string]StoreFactory{}

// RegisterVectorStore registers a factory for a named backend.
// Called from init() in each backend.
func RegisterVectorStore(name string, factory StoreFactory) {
	vectorStoreFactories[name] = factory
}

// NewVectorStore creates a VectorStore for the configured backend.
func NewVectorStore(cfg StoreConfig) (types.VectorStore, error) {
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
