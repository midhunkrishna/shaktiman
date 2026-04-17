package sqlite

import (
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// init registers the SQLite backend with the provider registry.
// This is called automatically when the sqlite package is imported
// via a blank import in the binary's main package.
func init() {
	storage.RegisterMetadataStore("sqlite", func(cfg storage.MetadataStoreConfig) (types.WriterStore, types.StoreLifecycle, func() error, error) {
		input := OpenInput{
			Path:     cfg.SQLitePath,
			InMemory: cfg.SQLiteInMemory,
		}
		db, err := Open(input)
		if err != nil {
			return nil, nil, nil, err
		}

		if err := Migrate(db); err != nil {
			db.Close()
			return nil, nil, nil, err
		}

		store := NewStore(db)
		lifecycle := NewLifecycle(store)
		closer := func() error { return db.Close() }

		return store, lifecycle, closer, nil
	})
}
