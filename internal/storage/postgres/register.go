//go:build postgres

package postgres

import (
	"context"
	"log/slog"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func init() {
	storage.RegisterMetadataStore("postgres", func(cfg storage.MetadataStoreConfig) (types.WriterStore, types.StoreLifecycle, func() error, error) {
		ctx := context.Background()
		store, err := NewPgStore(ctx, cfg.PostgresConnStr, cfg.PostgresMaxOpen, cfg.PostgresMaxIdle, cfg.PostgresSchema)
		if err != nil {
			return nil, nil, nil, err
		}

		dims := cfg.EmbeddingDims
		if dims == 0 {
			dims = 768 // default
		}
		if err := RunMigrations(ctx, store.Pool(), dims); err != nil {
			if cerr := store.Close(); cerr != nil {
				slog.Warn("close postgres store after migration error", "err", cerr)
			}
			return nil, nil, nil, err
		}

		// Register project after migrations (projects table must exist).
		// When no ProjectRoot is configured, fall back to the seeded default
		// project (id=1) created by migration 006_add_project_id.sql so the
		// store remains usable in single-project / backward-compat setups.
		if cfg.ProjectRoot != "" {
			if err := store.EnsureProject(ctx, cfg.ProjectRoot); err != nil {
				if cerr := store.Close(); cerr != nil {
					slog.Warn("close postgres store after project register error", "err", cerr)
				}
				return nil, nil, nil, err
			}
		} else {
			store.projectID = 1
		}

		closer := func() error { return store.Close() }
		// Postgres needs no StoreLifecycle (generated tsvector columns, no FTS triggers)
		return store, nil, closer, nil
	})
}
