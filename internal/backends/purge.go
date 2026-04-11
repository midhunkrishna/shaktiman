package backends

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// PurgeBackends clears indexed data from server-based stores (Postgres,
// pgvector, Qdrant). Uses StorePurger/VectorPurger interface assertions.
// Silently skips stores that don't implement purge (e.g. SQLite — handled
// by PurgeFiles). Safe when vs is nil.
func PurgeBackends(ctx context.Context, store types.WriterStore, vs types.VectorStore) error {
	if p, ok := store.(types.StorePurger); ok {
		if err := p.PurgeAll(ctx); err != nil {
			return fmt.Errorf("purge metadata store: %w", err)
		}
	}
	if vs != nil {
		if p, ok := vs.(types.VectorPurger); ok {
			if err := p.PurgeAll(ctx); err != nil {
				return fmt.Errorf("purge vector store: %w", err)
			}
		}
	}
	return nil
}

// PurgeFiles removes SQLite database and vector persistence files from disk.
// Call after Close() to ensure all file handles are released.
func PurgeFiles(cfg types.Config) error {
	if cfg.DatabaseBackend == "" || cfg.DatabaseBackend == "sqlite" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			p := cfg.DBPath + suffix
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", p, err)
			}
		}
	}
	// Remove all possible vector persistence files.
	base := cfg.EmbeddingsPath
	ext := filepath.Ext(base)
	hnswPath := base + ".hnsw"
	if ext != "" {
		hnswPath = base[:len(base)-len(ext)] + ".hnsw"
	}
	for _, p := range []string{base, hnswPath} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}
