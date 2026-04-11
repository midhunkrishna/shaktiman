// Package backends provides shared store opening, closing, and lifecycle
// management for metadata and vector backends. Both the daemon and CLI
// use this package to create stores from config, avoiding duplication.
package backends

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// Backends holds opened backend resources.
type Backends struct {
	Store       types.WriterStore
	Lifecycle   types.StoreLifecycle // nil for Postgres
	VectorStore types.VectorStore    // nil if embeddings disabled or unavailable

	dbCloser func() error
}

// Open creates a metadata store and (optionally) a vector store from config.
// Does NOT load persisted embeddings from disk — callers do that as needed.
// Vector store failure is non-fatal: VectorStore will be nil.
func Open(cfg types.Config) (*Backends, error) {
	store, lifecycle, dbCloser, err := storage.NewMetadataStore(
		storage.MetadataStoreConfigFrom(cfg))
	if err != nil {
		return nil, fmt.Errorf("create metadata store: %w", err)
	}

	b := &Backends{Store: store, Lifecycle: lifecycle, dbCloser: dbCloser}

	if cfg.EmbedEnabled {
		vs, err := vector.NewVectorStore(
			vector.VectorStoreConfigFrom(cfg, store))
		if err != nil {
			slog.Warn("vector store unavailable", "err", err)
		} else {
			b.VectorStore = vs
		}
	}

	return b, nil
}

// OpenMetadataOnly creates just the metadata store (for read-only CLI commands).
func OpenMetadataOnly(cfg types.Config) (*Backends, error) {
	store, lifecycle, dbCloser, err := storage.NewMetadataStore(
		storage.MetadataStoreConfigFrom(cfg))
	if err != nil {
		return nil, fmt.Errorf("create metadata store: %w", err)
	}
	return &Backends{Store: store, Lifecycle: lifecycle, dbCloser: dbCloser}, nil
}

// Close releases resources. Closes vector store before DB pool
// (pgvector borrows the Postgres pool — pool must outlive vector store).
// Safe to call on partially initialized Backends.
func (b *Backends) Close() error {
	if b.VectorStore != nil {
		b.VectorStore.Close()
	}
	if b.dbCloser != nil {
		return b.dbCloser()
	}
	return nil
}

// EmbeddingsPath returns the persistence file path for the vector backend.
func EmbeddingsPath(cfg types.Config) string {
	if cfg.VectorBackend == "hnsw" {
		base := cfg.EmbeddingsPath
		ext := filepath.Ext(base)
		if ext != "" {
			return base[:len(base)-len(ext)] + ".hnsw"
		}
		return base + ".hnsw"
	}
	return cfg.EmbeddingsPath
}
