package sqlite

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

	// Verify goose version table exists
	var gooseCount int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&gooseCount)
	if err != nil || gooseCount == 0 {
		t.Error("goose_db_version table not found")
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

func TestMigrate_Idempotent(t *testing.T) {
	t.Parallel()
	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// Second call should be a no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
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

func TestWithWriteTxCtx_CommitAndRollback(t *testing.T) {
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

	// Successful transaction via TxHandle
	err = db.WithWriteTxCtx(ctx, func(txh types.TxHandle) error {
		tx := txh.(SqliteTxHandle).Tx
		_, err := tx.ExecContext(ctx, `INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality)
			VALUES ('txh_test.go', 'h1', 1.0, 'pending', 'full')`)
		return err
	})
	if err != nil {
		t.Fatalf("WithWriteTxCtx commit: %v", err)
	}

	// Verify the row exists
	var path string
	err = db.QueryRowContext(ctx, "SELECT path FROM files WHERE path = 'txh_test.go'").Scan(&path)
	if err != nil {
		t.Fatalf("row not found after commit: %v", err)
	}

	// Rolled-back transaction via TxHandle
	rollbackErr := errors.New("forced rollback")
	err = db.WithWriteTxCtx(ctx, func(txh types.TxHandle) error {
		tx := txh.(SqliteTxHandle).Tx
		tx.ExecContext(ctx, `INSERT INTO files (path, content_hash, mtime, embedding_status, parse_quality)
			VALUES ('should_not_exist.go', 'h2', 1.0, 'pending', 'full')`)
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback error, got %v", err)
	}

	// Verify the rolled-back row does not exist
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM files WHERE path = 'should_not_exist.go'").Scan(&count)
	if count != 0 {
		t.Error("rolled-back row should not exist")
	}
}

func TestSqliteTxHandle_SatisfiesTxHandle(t *testing.T) {
	// Compile-time check is implicit, but verify the assertion works at runtime
	var txh types.TxHandle = SqliteTxHandle{Tx: nil}
	txh.IsTxHandle() // should not panic

	// Verify type assertion round-trip
	recovered := txh.(SqliteTxHandle)
	if recovered.Tx != nil {
		t.Error("expected nil Tx")
	}
}

func TestStoreWithWriteTx(t *testing.T) {
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

	// Use Store.WithWriteTx (the WriterStore interface method)
	// Insert a diff log entry inside a TxHandle transaction
	var fileID int64
	fileID, _ = store.UpsertFile(ctx, &types.FileRecord{
		Path: "writer_store.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	var diffID int64
	err = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		var txErr error
		diffID, txErr = store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "add", LinesAdded: 5, HashAfter: "h1",
		})
		return txErr
	})
	if err != nil {
		t.Fatalf("Store.WithWriteTx: %v", err)
	}
	if diffID == 0 {
		t.Fatal("expected non-zero diffID from Store.WithWriteTx")
	}

	// Verify the diff was persisted
	diffs, err := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: fileID})
	if err != nil {
		t.Fatalf("GetRecentDiffs: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
}

func TestStoreWithWriteTx_DiffInTransaction(t *testing.T) {
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

	// Insert a file
	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})

	// Use WriterStore.WithWriteTx to insert diff via TxHandle
	var diffID int64
	err = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		var txErr error
		diffID, txErr = store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID:     fileID,
			ChangeType: "add",
			LinesAdded: 10,
			HashAfter:  "h1",
		})
		if txErr != nil {
			return txErr
		}
		return store.InsertDiffSymbols(ctx, txh, diffID, []types.DiffSymbolEntry{
			{SymbolName: "main", ChangeType: "added"},
		})
	})
	if err != nil {
		t.Fatalf("WithWriteTx diff: %v", err)
	}
	if diffID == 0 {
		t.Fatal("expected non-zero diffID")
	}

	// Verify diff was recorded
	symbols, err := store.GetDiffSymbols(ctx, diffID)
	if err != nil {
		t.Fatalf("GetDiffSymbols: %v", err)
	}
	if len(symbols) != 1 || symbols[0].SymbolName != "main" {
		t.Errorf("unexpected diff symbols: %+v", symbols)
	}
}

func TestStoreWithWriteTx_Rollback(t *testing.T) {
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

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})

	// A transaction that returns error should rollback
	rollbackErr := errors.New("abort")
	err = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		_, _ = store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "add", LinesAdded: 1, HashAfter: "h1",
		})
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback error, got %v", err)
	}

	// Diff should not exist after rollback
	diffs, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: fileID})
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs after rollback, got %d", len(diffs))
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
