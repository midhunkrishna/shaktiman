package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	sqlitemigrations "github.com/shaktimanai/shaktiman/internal/storage/migrations/sqlite"
)

// RunSQLiteMigrations applies all pending goose migrations for SQLite.
func RunSQLiteMigrations(db *sql.DB) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sqlitemigrations.FS)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// bootstrapGooseFromLegacy seeds goose's version table from the legacy
// schema_version table so that goose doesn't re-run already-applied
// migrations. This is needed because SQLite's ALTER TABLE ADD COLUMN
// has no IF NOT EXISTS — re-running would fail.
func bootstrapGooseFromLegacy(db *sql.DB) error {
	// Check if legacy schema_version exists and has records.
	var legacyVersion int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&legacyVersion)
	if err != nil {
		// Table doesn't exist — fresh database, nothing to bootstrap.
		return nil
	}
	if legacyVersion == 0 {
		return nil
	}

	// Check if goose has already been bootstrapped.
	var gooseExists int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&gooseExists)
	if err != nil {
		return fmt.Errorf("check goose table: %w", err)
	}
	if gooseExists > 0 {
		return nil // Already bootstrapped.
	}

	// Seed goose version table. We have 1 migration file (001_base_schema)
	// that covers the full current schema. If the legacy DB has any version,
	// it means the schema is already applied.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS goose_db_version (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp     TEXT DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create goose table: %w", err)
	}

	// Mark migration 001 as applied.
	if _, err := db.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, 1)"); err != nil {
		return fmt.Errorf("seed goose version: %w", err)
	}

	return nil
}
