// Package storage provides SQLite-backed persistence for the Shaktiman index.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"github.com/shaktimanai/shaktiman/internal/types"
)

// SqliteTxHandle wraps *sql.Tx to satisfy types.TxHandle.
type SqliteTxHandle struct{ Tx *sql.Tx }

// IsTxHandle implements types.TxHandle.
func (SqliteTxHandle) IsTxHandle() {}

// inMemoryCounter generates unique names for in-memory databases to prevent
// shared cache conflicts when tests run in parallel.
var inMemoryCounter atomic.Int64

// DB holds dual SQLite connections: a single-writer and a reader pool (IP-3).
// Writer (MaxOpenConns=1) ensures serialized writes with exclusive PRAGMA control.
// Reader pool (MaxOpenConns=4) enables concurrent read-only queries via WAL.
type DB struct {
	writer *sql.DB
	reader *sql.DB
	dbPath string
}

// OpenInput configures how the database is opened.
type OpenInput struct {
	Path     string // file path to the SQLite database
	InMemory bool   // if true, use :memory: (for tests)
}

// Open creates a dual-connection SQLite database.
// It ensures the parent directory exists and configures WAL mode.
func Open(input OpenInput) (*DB, error) {
	path := input.Path
	if input.InMemory {
		id := inMemoryCounter.Add(1)
		path = fmt.Sprintf("file:inmem_%d?mode=memory&cache=shared", id)
	} else {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory %s: %w", dir, err)
		}
	}

	writer, err := openWriter(path, input.InMemory)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}

	reader, err := openReader(path, input.InMemory)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}

	return &DB{writer: writer, reader: reader, dbPath: path}, nil
}

func openWriter(path string, inMemory bool) (*sql.DB, error) {
	dsn := path
	if !inMemory {
		dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_foreign_keys=ON", path)
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open writer: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA cache_size = -8000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set writer cache_size: %w", err)
	}

	if inMemory {
		// In-memory needs WAL and foreign_keys set explicitly
		for _, pragma := range []string{
			"PRAGMA journal_mode = WAL",
			"PRAGMA synchronous = NORMAL",
			"PRAGMA foreign_keys = ON",
		} {
			if _, err := db.Exec(pragma); err != nil {
				db.Close()
				return nil, fmt.Errorf("set writer %s: %w", pragma, err)
			}
		}
	}

	return db, nil
}

func openReader(path string, inMemory bool) (*sql.DB, error) {
	dsn := path
	if !inMemory {
		dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&mode=ro&_foreign_keys=ON", path)
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open reader: %w", err)
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0) // PRAGMAs stick for connection lifetime

	// Increase reader page cache for large indexes (32MB vs SQLite default 2MB)
	if _, err := db.Exec("PRAGMA cache_size = -32000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set reader cache_size: %w", err)
	}
	// Enable memory-mapped I/O for reads (256MB)
	if _, err := db.Exec("PRAGMA mmap_size = 268435456"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set reader mmap_size: %w", err)
	}

	return db, nil
}

// Close closes both writer and reader connections.
func (db *DB) Close() error {
	wErr := db.writer.Close()
	rErr := db.reader.Close()
	if wErr != nil {
		return fmt.Errorf("close writer: %w", wErr)
	}
	if rErr != nil {
		return fmt.Errorf("close reader: %w", rErr)
	}
	return nil
}

// WithWriteTx executes fn within a write transaction.
// The transaction is committed on success or rolled back on error.
func (db *DB) WithWriteTx(fn func(tx *sql.Tx) error) error {
	tx, err := db.writer.Begin()
	if err != nil {
		return fmt.Errorf("begin write tx: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// WithWriteTxCtx executes fn within a write transaction using a types.TxHandle.
// This is the backend-agnostic version used by the WriterStore interface.
func (db *DB) WithWriteTxCtx(ctx context.Context, fn func(tx types.TxHandle) error) error {
	return db.WithWriteTx(func(tx *sql.Tx) error {
		return fn(SqliteTxHandle{Tx: tx})
	})
}

// QueryContext executes a read query using the reader connection pool.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.reader.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a single-row read query using the reader pool.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.reader.QueryRowContext(ctx, query, args...)
}

// Writer returns the underlying writer database for direct use (e.g., schema migration).
func (db *DB) Writer() *sql.DB {
	return db.writer
}
