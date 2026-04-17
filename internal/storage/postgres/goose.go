//go:build postgres

package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	pgmigrations "github.com/shaktimanai/shaktiman/internal/storage/migrations/postgres"
)

// NewGooseProvider creates a goose provider for Postgres migrations.
// The dims parameter controls the pgvector embeddings column dimension
// (used in Go migration 003). Exported for reuse by the pgtestdb migrator.
func NewGooseProvider(connConfig *pgxpool.Config, dims int) (*goose.Provider, error) {
	db := stdlib.OpenDB(*connConfig.ConnConfig)

	provider, err := goose.NewProvider(goose.DialectPostgres, db, pgmigrations.FS,
		goose.WithGoMigrations(pgmigrations.GoMigrations(dims)...),
	)
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("close goose db after provider init error", "err", cerr)
		}
		return nil, fmt.Errorf("create goose provider: %w", err)
	}
	return provider, nil
}

// RunMigrations applies all pending Postgres + pgvector migrations via goose.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, dims int) error {
	provider, err := NewGooseProvider(pool.Config(), dims)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
