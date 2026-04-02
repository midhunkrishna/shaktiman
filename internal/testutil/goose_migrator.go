//go:build postgres

package testutil

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/common"
	"github.com/pressly/goose/v3"

	pgmigrations "github.com/shaktimanai/shaktiman/internal/storage/migrations/postgres"
)

// GooseMigrator implements pgtestdb.Migrator using goose v3. It runs the
// Postgres migration sequence (base schema + pgvector) against the template
// database. pgtestdb then clones the template for each test.
type GooseMigrator struct {
	Dims int // embedding dimensions for pgvector
}

var _ pgtestdb.Migrator = (*GooseMigrator)(nil)

// Hash returns a unique identifier for the current migration state. pgtestdb
// uses this to decide whether the template database needs re-creation.
func (m *GooseMigrator) Hash() (string, error) {
	h := common.NewRecursiveHash(
		common.Field("dims", m.Dims),
	)
	if err := h.AddDirs(pgmigrations.FS, "*.sql", "."); err != nil {
		return "", fmt.Errorf("hash migration files: %w", err)
	}
	return h.String(), nil
}

// Migrate runs all goose migrations against the provided database.
func (m *GooseMigrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	provider, err := goose.NewProvider(goose.DialectPostgres, db, pgmigrations.FS,
		goose.WithGoMigrations(pgmigrations.GoMigrations(m.Dims)...),
	)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
