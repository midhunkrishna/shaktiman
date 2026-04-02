package storage

import (
	"fmt"
)

// ftsTriggers creates triggers to keep chunks_fts in sync with chunks (DM-1).
var ftsTriggers = []string{
	`CREATE TRIGGER IF NOT EXISTS chunks_fts_insert AFTER INSERT ON chunks BEGIN
		INSERT INTO chunks_fts(rowid, content, symbol_name)
		VALUES (new.id, new.content, new.symbol_name);
	END`,

	`CREATE TRIGGER IF NOT EXISTS chunks_fts_delete AFTER DELETE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
		VALUES ('delete', old.id, old.content, old.symbol_name);
	END`,

	`CREATE TRIGGER IF NOT EXISTS chunks_fts_update AFTER UPDATE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
		VALUES ('delete', old.id, old.content, old.symbol_name);
		INSERT INTO chunks_fts(rowid, content, symbol_name)
		VALUES (new.id, new.content, new.symbol_name);
	END`,
}

// Migrate creates all tables and indexes for the current schema.
// For fresh databases, goose runs all migrations. For existing databases
// with the legacy schema_version table, it bootstraps goose's version
// tracking first to avoid re-running already-applied migrations.
func Migrate(db *DB) error {
	writer := db.Writer()
	if err := bootstrapGooseFromLegacy(writer); err != nil {
		return fmt.Errorf("bootstrap goose: %w", err)
	}
	return RunSQLiteMigrations(writer)
}
