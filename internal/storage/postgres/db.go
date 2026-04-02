// Package postgres provides a PostgreSQL-backed MetadataStore implementation.
package postgres

import (
	"context"
	"fmt"

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
	pool   *pgxpool.Pool
	schema string
}

// Compile-time check: *PgStore satisfies WriterStore.
var _ types.WriterStore = (*PgStore)(nil)

// NewPgStore creates a PgStore connected to the given Postgres instance.
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
		tx.Rollback(ctx)
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
