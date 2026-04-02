//go:build sqlite_fts5

package storetest

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestSQLiteMetadataStoreCompliance(t *testing.T) {
	RunMetadataStoreTests(t, func(t *testing.T) types.MetadataStore {
		t.Helper()
		db, err := storage.Open(storage.OpenInput{InMemory: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := storage.Migrate(db); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		return storage.NewStore(db)
	})
}
