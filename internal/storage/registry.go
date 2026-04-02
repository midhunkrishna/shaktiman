// Package storage provides the provider registry for MetadataStore backends.
// Each backend (sqlite, postgres) registers a factory via init().
// The daemon calls NewMetadataStore(cfg) to create the configured backend.
package storage

import (
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// MetadataStoreConfig holds backend-agnostic configuration for creating a MetadataStore.
type MetadataStoreConfig struct {
	Backend        string // "sqlite" or "postgres"
	SQLitePath     string // file path for SQLite database
	SQLiteInMemory bool   // use in-memory database (for tests)

	PostgresConnStr string
	PostgresMaxOpen int
	PostgresMaxIdle int
	PostgresSchema  string

	EmbeddingDims int // vector dimension for pgvector (e.g. 768)
}

// MetadataStoreFactory creates a WriterStore from config.
// Returns the store, an optional StoreLifecycle (nil if backend needs none),
// a closer function, and any error.
type MetadataStoreFactory func(cfg MetadataStoreConfig) (types.WriterStore, types.StoreLifecycle, func() error, error)

var metadataStoreFactories = map[string]MetadataStoreFactory{}

// RegisterMetadataStore registers a factory for a named backend.
// Called from init() in each backend sub-package.
func RegisterMetadataStore(name string, factory MetadataStoreFactory) {
	metadataStoreFactories[name] = factory
}

// NewMetadataStore creates a WriterStore for the configured backend.
// Returns the store, an optional lifecycle, a closer function, and any error.
func NewMetadataStore(cfg MetadataStoreConfig) (types.WriterStore, types.StoreLifecycle, func() error, error) {
	if cfg.Backend == "" {
		cfg.Backend = "sqlite"
	}
	factory, ok := metadataStoreFactories[cfg.Backend]
	if !ok {
		available := make([]string, 0, len(metadataStoreFactories))
		for name := range metadataStoreFactories {
			available = append(available, name)
		}
		return nil, nil, nil, fmt.Errorf("unknown metadata store backend: %q (available: %v)", cfg.Backend, available)
	}
	return factory(cfg)
}

// HasMetadataStore returns true if a factory is registered for the named backend.
func HasMetadataStore(name string) bool {
	_, ok := metadataStoreFactories[name]
	return ok
}

// MetadataStoreConfigFrom extracts a MetadataStoreConfig from the application config.
// This keeps backend-specific field mapping in the storage package, not the daemon.
func MetadataStoreConfigFrom(cfg types.Config) MetadataStoreConfig {
	return MetadataStoreConfig{
		Backend:         cfg.DatabaseBackend,
		SQLitePath:      cfg.DBPath,
		PostgresConnStr: cfg.PostgresConnString,
		PostgresMaxOpen: cfg.PostgresMaxOpen,
		PostgresMaxIdle: cfg.PostgresMaxIdle,
		PostgresSchema:  cfg.PostgresSchema,
		EmbeddingDims:   cfg.EmbeddingDims,
	}
}
