package storage

import "github.com/shaktimanai/shaktiman/internal/types"

// init registers the SQLite backend with the provider registry.
// This is called automatically when the storage package is imported.
// Future backends (postgres) will register from their own sub-packages.
func init() {
	RegisterMetadataStore("sqlite", func(cfg MetadataStoreConfig) (types.WriterStore, types.StoreLifecycle, func() error, error) {
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
		lifecycle := NewSQLiteLifecycle(store)
		closer := func() error { return db.Close() }

		return store, lifecycle, closer, nil
	})
}
