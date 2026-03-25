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
	if version != schemaVersion {
		t.Errorf("expected schema version %d, got %d", schemaVersion, version)
	}

	// Verify idempotent
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestEmbeddedColumnMigration(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Run migration (fresh DB gets embedded column in DDL)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()

	// Insert a file and chunks
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality)
			VALUES ('test.go', 'hash1', 1.0, 'pending', 'full')`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO chunks (file_id, chunk_index, kind, start_line, end_line, content, token_count)
			VALUES (1, 0, 'function', 1, 10, 'func main() {}', 5)`)
		return err
	})
	if err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	// Verify embedded column exists and defaults to 0
	var embedded int
	err = db.QueryRowContext(ctx, "SELECT embedded FROM chunks WHERE id = 1").Scan(&embedded)
	if err != nil {
		t.Fatalf("query embedded column: %v", err)
	}
	if embedded != 0 {
		t.Errorf("embedded = %d, want 0 (default)", embedded)
	}

	// Verify the index exists
	var indexName string
	err = db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_chunks_embedded'").Scan(&indexName)
	if err != nil {
		t.Fatalf("idx_chunks_embedded not found: %v", err)
	}

	// Verify idempotent re-migration
	if err := Migrate(db); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
}
