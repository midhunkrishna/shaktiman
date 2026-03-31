package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
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

func TestWithWriteTx_Rollback(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	// Insert a file first.
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "before.go", ContentHash: "h", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	// A failing transaction should rollback -- no new file inserted.
	wantErr := errors.New("intentional failure")
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		tx.ExecContext(ctx, `INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality) VALUES ('rollback.go', 'h', 1.0, 'pending', 'full')`)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}

	// The "rollback.go" file should not exist.
	f, _ := store.GetFileByPath(ctx, "rollback.go")
	if f != nil {
		t.Error("expected rollback.go to not exist after rolled-back transaction")
	}
}

func TestMigrate_DuplicateColumnRecovery(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Run migration once to set up all tables.
	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	// Simulate a partial v1->v2 migration crash: reset the schema version to 1.
	// On the next Migrate call, it will try to add the "embedded" column again,
	// hitting isDuplicateColumnError, which should be tolerated.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE schema_version SET version = 1")
		return err
	})
	if err != nil {
		t.Fatalf("reset schema version: %v", err)
	}

	// This should succeed, exercising the isDuplicateColumnError path.
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate after version reset: %v", err)
	}

	// Verify version is back to current.
	var version int
	if err := db.QueryRowContext(context.Background(),
		"SELECT MAX(version) FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != schemaVersion {
		t.Errorf("version = %d, want %d", version, schemaVersion)
	}
}

func TestIsTestColumnMigration(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()

	// Insert a file and verify is_test defaults to 0
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality)
			VALUES ('server.go', 'hash1', 1.0, 'pending', 'full')`)
		return err
	})
	if err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	var isTest int
	err = db.QueryRowContext(ctx, "SELECT is_test FROM files WHERE path = 'server.go'").Scan(&isTest)
	if err != nil {
		t.Fatalf("query is_test column: %v", err)
	}
	if isTest != 0 {
		t.Errorf("is_test = %d, want 0 (default)", isTest)
	}

	// Insert a test file with is_test = 1
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality, is_test)
			VALUES ('server_test.go', 'hash2', 1.0, 'pending', 'full', 1)`)
		return err
	})
	if err != nil {
		t.Fatalf("insert test file: %v", err)
	}

	err = db.QueryRowContext(ctx, "SELECT is_test FROM files WHERE path = 'server_test.go'").Scan(&isTest)
	if err != nil {
		t.Fatalf("query is_test for test file: %v", err)
	}
	if isTest != 1 {
		t.Errorf("is_test = %d, want 1", isTest)
	}

	// Verify idempotent re-migration
	if err := Migrate(db); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
}

func TestUpsertFile_IsTest(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()

	// Upsert a test file
	id, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server_test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero file ID")
	}

	// Read it back
	f, err := store.GetFileByPath(ctx, "server_test.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if !f.IsTest {
		t.Error("GetFileByPath: IsTest = false, want true")
	}

	// Upsert an impl file
	_, err = store.UpsertFile(ctx, &types.FileRecord{
		Path: "server.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: false,
	})
	if err != nil {
		t.Fatalf("UpsertFile impl: %v", err)
	}

	f2, err := store.GetFileByPath(ctx, "server.go")
	if err != nil {
		t.Fatalf("GetFileByPath impl: %v", err)
	}
	if f2.IsTest {
		t.Error("GetFileByPath impl: IsTest = true, want false")
	}

	// Verify ListFiles also populates IsTest
	files, err := store.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	testCount := 0
	for _, fi := range files {
		if fi.IsTest {
			testCount++
		}
	}
	if testCount != 1 {
		t.Errorf("ListFiles: %d test files, want 1", testCount)
	}
}

func TestGetFileIsTestByID(t *testing.T) {
	t.Parallel()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := NewStore(db)
	ctx := context.Background()

	// Insert test and impl files
	testID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "auth_test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	implID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "auth.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full", IsTest: false,
	})

	isTest, err := store.GetFileIsTestByID(ctx, testID)
	if err != nil {
		t.Fatalf("GetFileIsTestByID test: %v", err)
	}
	if !isTest {
		t.Error("expected true for test file")
	}

	isTest, err = store.GetFileIsTestByID(ctx, implID)
	if err != nil {
		t.Fatalf("GetFileIsTestByID impl: %v", err)
	}
	if isTest {
		t.Error("expected false for impl file")
	}

	// Non-existent ID
	_, err = store.GetFileIsTestByID(ctx, 9999)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	t.Parallel()

	// Path under /dev/null is not a valid directory and MkdirAll will fail.
	_, err := Open(OpenInput{Path: "/dev/null/subdir/test.db"})
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// First Close should succeed.
	if err := db.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
}

func TestOpen_FileMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(OpenInput{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Verify the database is functional.
	store := NewStore(db)
	_, err = store.UpsertFile(context.Background(), &types.FileRecord{
		Path: "test.go", ContentHash: "h", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile on file-based DB: %v", err)
	}
}
