// Package postgres provides a PostgreSQL-backed MetadataStore implementation.
package postgres

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// PgTxHandle wraps pgx.Tx to satisfy types.TxHandle.
type PgTxHandle struct{ Tx pgx.Tx }

// IsTxHandle implements types.TxHandle.
func (PgTxHandle) IsTxHandle() {}

// PgStore provides metadata CRUD operations backed by PostgreSQL.
type PgStore struct {
	pool      *pgxpool.Pool
	schema    string
	projectID int64
}

// Compile-time check: *PgStore satisfies WriterStore.
var _ types.WriterStore = (*PgStore)(nil)

// NewPgStore creates a PgStore connected to the given Postgres instance.
// Call EnsureProject after running migrations to register the project.
func NewPgStore(ctx context.Context, connStr string, maxOpen, maxIdle int, schema string) (*PgStore, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse postgres connection string: %w", err)
	}
	cfg.MaxConns = int32(maxOpen)
	cfg.MinConns = int32(maxIdle)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PgStore{pool: pool, schema: schema}, nil
}

// Pool returns the underlying connection pool (for pgvector pool sharing).
func (s *PgStore) Pool() *pgxpool.Pool {
	return s.pool
}

// RawPool returns the connection pool as an untyped value. Used by the daemon
// to pass the pool to pgvector without importing the pgxpool package.
func (s *PgStore) RawPool() any {
	return s.pool
}

// Close closes the connection pool.
func (s *PgStore) Close() error {
	s.pool.Close()
	return nil
}

// WithWriteTx executes fn within a write transaction.
func (s *PgStore) WithWriteTx(ctx context.Context, fn func(tx types.TxHandle) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(PgTxHandle{Tx: tx}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// query executes a read query using the connection pool.
func (s *PgStore) query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return s.pool.Query(ctx, sql, args...)
}

// queryRow executes a single-row read query using the pool.
func (s *PgStore) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, sql, args...)
}

// ProjectID returns the project ID for this store instance.
func (s *PgStore) ProjectID() int64 {
	return s.projectID
}

// EnsureProject registers the project in the projects table and stores its ID.
// Must be called after migrations have run (projects table must exist).
// Uses INSERT ... ON CONFLICT DO NOTHING + fallback SELECT to handle concurrent starts.
// The project root path is canonicalized to prevent duplicates from symlinks or relative paths.
func (s *PgStore) EnsureProject(ctx context.Context, projectRoot string) error {
	// Canonicalize path to prevent symlink/relative path duplicates.
	absPath, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("abs path %s: %w", projectRoot, err)
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Directory may not exist yet (e.g., fresh project). Fall back to abs path.
		resolved = absPath
	}

	name := filepath.Base(resolved)

	// First-run: claim the default project if it still holds the placeholder path.
	// This is idempotent — only the first daemon claims it, others get affected=0.
	if _, err := s.pool.Exec(ctx,
		`UPDATE projects SET root_path = $1, name = $2 WHERE id = 1 AND root_path = '__default__'`,
		resolved, name); err != nil {
		return fmt.Errorf("claim default project row: %w", err)
	}

	// Try insert; ON CONFLICT DO NOTHING avoids errors on concurrent starts.
	var id int64
	err = s.pool.QueryRow(ctx,
		`INSERT INTO projects (root_path, name) VALUES ($1, $2)
		 ON CONFLICT (root_path) DO NOTHING
		 RETURNING id`, resolved, name).Scan(&id)
	if err == nil {
		s.projectID = id
		return nil
	}

	// Row already exists (conflict) — select it.
	err = s.pool.QueryRow(ctx,
		`SELECT id FROM projects WHERE root_path = $1`, resolved).Scan(&id)
	if err != nil {
		return fmt.Errorf("lookup project %s: %w", resolved, err)
	}
	s.projectID = id
	return nil
}
