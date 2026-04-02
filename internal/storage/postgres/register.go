//go:build postgres

package postgres

import (
	"context"

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

		if err := Migrate(ctx, store.Pool()); err != nil {
			store.Close()
			return nil, nil, nil, err
		}

		closer := func() error { return store.Close() }
		// Postgres needs no StoreLifecycle (generated tsvector columns, no FTS triggers)
		return store, nil, closer, nil
	})
}
