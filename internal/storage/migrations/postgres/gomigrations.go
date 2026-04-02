//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// GoMigrations returns the Go-defined migrations for Postgres. These are
// registered alongside the SQL files when creating a goose Provider.
//
// Migration 003 creates the pgvector embeddings table with a parameterized
// vector(dims) column type. SQL files can't express this — dims comes from
// the application config at runtime.
func GoMigrations(dims int) []*goose.Migration {
	return []*goose.Migration{
		goose.NewGoMigration(3,
			&goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, fmt.Sprintf(`
					CREATE TABLE IF NOT EXISTS embeddings (
						chunk_id  BIGINT PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
						embedding vector(%d) NOT NULL
					)`, dims))
				return err
			}},
			&goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS embeddings")
				return err
			}},
		),
	}
}
