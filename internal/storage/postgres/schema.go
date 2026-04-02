//go:build postgres

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate is a backward-compatible wrapper that delegates to goose-based
// RunMigrations with default embedding dimensions. Prefer RunMigrations
// directly when dims are known.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return RunMigrations(ctx, pool, 768)
}
