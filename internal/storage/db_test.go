package storage

import (
	"context"
	"database/sql"
	"testing"
)

func TestOpenInMemory(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open in-memory: %v", err)
	}
	defer db.Close()

	// Verify writer works
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
		return err
	})
	if err != nil {
		t.Fatalf("write tx: %v", err)
	}

	// Verify reader works
	row := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM test_table")
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("read query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows, got %d", count)
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Verify core tables exist
	tables := []string{"files", "chunks", "symbols", "edges", "pending_edges",
		"diff_log", "diff_symbols", "access_log", "working_set",
		"schema_version", "config"}

	ctx := context.Background()
	for _, table := range tables {
		row := db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		var name string
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	// Verify FTS5 virtual table
	row := db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='chunks_fts'")
	var name string
	if err := row.Scan(&name); err != nil {
		t.Errorf("chunks_fts not found: %v", err)
	}

	// Verify schema version
	row = db.QueryRowContext(ctx, "SELECT version FROM schema_version")
	var version int
	if err := row.Scan(&version); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected schema version 1, got %d", version)
	}

	// Verify idempotent
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
