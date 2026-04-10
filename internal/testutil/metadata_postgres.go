//go:build postgres

package testutil

import (
	"context"
	"net/url"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // register pgx driver for pgtestdb
	"github.com/peterldowns/pgtestdb"

	"github.com/shaktimanai/shaktiman/internal/storage/postgres"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// testPgConnStrs stores the per-test Postgres connection URL. The pgvector
// factory reads this to create a pool pointing at the same test database.
var testPgConnStrs sync.Map // t.Name() -> string

func init() {
	extraMetadataFactories["postgres"] = newPostgresTestStore
}

// parsePgTestDBConfig extracts pgtestdb.Config from a Postgres URL.
func parsePgTestDBConfig(connStr string) pgtestdb.Config {
	u, _ := url.Parse(connStr)
	password, _ := u.User.Password()
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       u.Hostname(),
		Port:       u.Port(),
		User:       u.User.Username(),
		Password:   password,
		Database:   u.Path[1:], // strip leading "/"
		Options:    u.RawQuery,
	}
}

func newPostgresTestStore(t testing.TB) types.WriterStore {
	t.Helper()

	connStr := os.Getenv("SHAKTIMAN_TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("SHAKTIMAN_TEST_POSTGRES_URL not set")
	}

	pgConf := parsePgTestDBConfig(connStr)
	// The test role needs SUPERUSER to CREATE EXTENSION vector.
	pgConf.TestRole = &pgtestdb.Role{
		Username:     pgtestdb.DefaultRoleUsername,
		Password:     pgtestdb.DefaultRolePassword,
		Capabilities: "SUPERUSER",
	}
	migrator := &GooseMigrator{Dims: 768}

	// pgtestdb.Custom creates a fresh database cloned from a migrated
	// template, then returns its connection details. Migrations run once
	// into the template; each test gets a cheap filesystem-level copy.
	dbConf := pgtestdb.Custom(t, pgConf, migrator)
	testURL := dbConf.URL()

	ctx := context.Background()
	store, err := postgres.NewPgStore(ctx, testURL, 5, 2, "public")
	if err != nil {
		t.Fatalf("NewTestWriterStore(postgres): %v", err)
	}
	// Register a test project for multi-project isolation.
	if err := store.EnsureProject(ctx, "/tmp/test-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// Publish connection URL so the pgvector factory can reuse it.
	testPgConnStrs.Store(t.Name(), testURL)

	t.Cleanup(func() {
		testPgConnStrs.Delete(t.Name())
		store.Close()
		// pgtestdb handles database cleanup automatically.
	})

	return store
}
