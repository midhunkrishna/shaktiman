//go:build sqlite_fts5

package storetest

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage/sqlite"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestSQLiteMetadataStoreCompliance(t *testing.T) {
	RunMetadataStoreTests(t, func(t *testing.T) types.MetadataStore {
		t.Helper()
		db, err := sqlite.Open(sqlite.OpenInput{InMemory: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := sqlite.Migrate(db); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		return sqlite.NewStore(db)
	})
}
