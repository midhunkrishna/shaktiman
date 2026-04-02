package pgvector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate ensures the pgvector extension and embeddings table exist.
// The pgvector package owns its own schema — this is NOT part of the
// Postgres MetadataStore migration.
//
// Prerequisites:
//   - PostgreSQL server must have the pgvector extension installed (v0.5.0+).
//   - The database role must have CREATE privilege to enable the extension,
//     or the extension must already be enabled by a superuser/admin.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dims int) error {
	if dims <= 0 || dims > 4096 {
		return fmt.Errorf("pgvector: invalid dimensions %d (must be 1-4096)", dims)
	}

	// Enable the pgvector extension (idempotent).
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("pgvector: enable extension (is pgvector installed on the server?): %w", err)
	}

	// Create the embeddings table with parameterized vector dimension.
	createTable := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS embeddings (
			chunk_id BIGINT PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
			embedding vector(%d) NOT NULL
		)`, dims)
	if _, err := pool.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("pgvector: create embeddings table: %w", err)
	}

	// Create HNSW index outside a transaction (CONCURRENTLY cannot run inside one).
	// IF NOT EXISTS makes this idempotent.
	createIndex := `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_embeddings_hnsw
		ON embeddings USING hnsw (embedding vector_cosine_ops)
		WITH (m = 16, ef_construction = 200)`
	if _, err := pool.Exec(ctx, createIndex); err != nil {
		return fmt.Errorf("pgvector: create HNSW index: %w", err)
	}

	return nil
}

// ValidateDimensions checks that the existing embeddings table (if any)
// matches the expected vector dimension. Returns nil if the table doesn't
// exist or dimensions match.
func ValidateDimensions(ctx context.Context, pool *pgxpool.Pool, expected int) error {
	// Query the vector column's dimension from pg_attribute.
	// atttypmod for vector(N) stores N.
	var typmod int
	err := pool.QueryRow(ctx, `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON a.attrelid = c.oid
		WHERE c.relname = 'embeddings'
		  AND a.attname = 'embedding'
		  AND a.atttypmod > 0
	`).Scan(&typmod)

	if err != nil {
		// Table doesn't exist or column not found — fine, will be created.
		return nil
	}

	if typmod != expected {
		return fmt.Errorf("pgvector: embeddings table has vector(%d) but config specifies dims=%d — "+
			"drop the embeddings table and re-embed, or revert the config change", typmod, expected)
	}
	return nil
}
