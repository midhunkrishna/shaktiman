// Package pgvector implements a vector store backed by PostgreSQL with
// the pgvector extension, sharing the Postgres connection pool with the
// metadata store.
package pgvector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvec "github.com/pgvector/pgvector-go"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// maxBatchSize is the maximum number of points per upsert/delete request.
const maxBatchSize = 100

// searchTimeout is the maximum time for a single vector search query.
const searchTimeout = 30 * time.Second

// Store implements types.VectorStore backed by pgvector in PostgreSQL.
// It borrows a *pgxpool.Pool from the Postgres MetadataStore and does NOT
// own the pool lifecycle — Close() must not close the pool.
type Store struct {
	pool      *pgxpool.Pool
	dims      int
	projectID int64

	mu     sync.Mutex
	closed bool
}

// Compile-time check.
var _ types.VectorStore = (*Store)(nil)

// ValidateDimensions checks that the existing embeddings table (if any)
// matches the expected vector dimension. Returns nil if the table doesn't
// exist or dimensions match.
func ValidateDimensions(ctx context.Context, pool *pgxpool.Pool, expected int) error {
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

// NewStore creates a Store. The embeddings table and pgvector
// extension must already exist (created by goose migrations during MetadataStore
// initialization). This constructor only validates dimensions.
// projectID scopes all operations to the given project for multi-project isolation.
func NewStore(pool *pgxpool.Pool, dims int, projectID int64) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("pgvector: pool is nil (pgvector requires database.backend = postgres)")
	}

	if err := ValidateDimensions(context.Background(), pool, dims); err != nil {
		return nil, err
	}

	return &Store{pool: pool, dims: dims, projectID: projectID}, nil
}

// Search returns the topK most similar vectors by cosine similarity,
// scoped to the current project. Over-fetches by 3x to compensate for
// HNSW post-filtering, then trims to topK.
func (s *Store) Search(ctx context.Context, query []float32, topK int) ([]types.VectorResult, error) {
	if topK <= 0 {
		return nil, nil
	}
	if isZeroVector(query) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	// Over-fetch to compensate for HNSW post-filter on project_id.
	fetchLimit := topK * 3

	rows, err := s.pool.Query(ctx, `
		SELECT chunk_id, 1 - (embedding <=> $1::vector) AS score
		FROM embeddings
		WHERE project_id = $3
		ORDER BY embedding <=> $1::vector
		LIMIT $2`,
		pgvec.NewVector(query), fetchLimit, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("pgvector search: %w", err)
	}
	defer rows.Close()

	var results []types.VectorResult
	for rows.Next() {
		var r types.VectorResult
		if err := rows.Scan(&r.ChunkID, &r.Score); err != nil {
			return nil, fmt.Errorf("pgvector scan: %w", err)
		}
		results = append(results, r)
		if len(results) >= topK {
			break
		}
	}
	return results, rows.Err()
}

// Upsert inserts or replaces a single vector.
func (s *Store) Upsert(ctx context.Context, chunkID int64, vector []float32) error {
	if len(vector) != s.dims {
		return fmt.Errorf("pgvector: vector dim %d != store dim %d", len(vector), s.dims)
	}
	if isZeroVector(vector) {
		return fmt.Errorf("pgvector: zero vector not allowed (produces NaN in cosine distance)")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO embeddings (chunk_id, embedding, project_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (chunk_id) DO UPDATE SET embedding = EXCLUDED.embedding`,
		chunkID, pgvec.NewVector(vector), s.projectID)
	return err
}

// UpsertBatch inserts multiple vectors, chunked to maxBatchSize per batch.
func (s *Store) UpsertBatch(ctx context.Context, chunkIDs []int64, vectors [][]float32) error {
	if len(chunkIDs) != len(vectors) {
		return fmt.Errorf("pgvector: chunkIDs len %d != vectors len %d", len(chunkIDs), len(vectors))
	}

	for i := 0; i < len(chunkIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}

		batch := &pgx.Batch{}
		for j := i; j < end; j++ {
			if isZeroVector(vectors[j]) {
				continue
			}
			batch.Queue(`
				INSERT INTO embeddings (chunk_id, embedding, project_id)
				VALUES ($1, $2, $3)
				ON CONFLICT (chunk_id) DO UPDATE SET embedding = EXCLUDED.embedding`,
				chunkIDs[j], pgvec.NewVector(vectors[j]), s.projectID)
		}
		if batch.Len() == 0 {
			continue
		}

		br := s.pool.SendBatch(ctx, batch)
		for k := 0; k < batch.Len(); k++ {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("pgvector upsert batch [%d:%d] item %d: %w", i, end, k, err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("pgvector close batch [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// Delete removes vectors by chunk IDs, chunked to maxBatchSize.
func (s *Store) Delete(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	for i := 0; i < len(chunkIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}

		_, err := s.pool.Exec(ctx,
			"DELETE FROM embeddings WHERE chunk_id = ANY($1)",
			chunkIDs[i:end])
		if err != nil {
			return fmt.Errorf("pgvector delete [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// PurgeAll deletes all embeddings for the current project.
// Other projects sharing the same Postgres database are not affected.
func (s *Store) PurgeAll(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM embeddings WHERE project_id = $1", s.projectID)
	return err
}

// Has returns true if a vector exists for the given chunk ID.
func (s *Store) Has(ctx context.Context, chunkID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM embeddings WHERE chunk_id = $1)",
		chunkID).Scan(&exists)
	return exists, err
}

// Count returns the exact number of stored vectors for the current project.
func (s *Store) Count(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM embeddings WHERE project_id = $1", s.projectID).Scan(&count)
	return count, err
}

// Close marks the store as closed. Does NOT close the pool — it is shared
// with the Postgres MetadataStore and owned by the daemon.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	slog.Info("pgvector store closed")
	return nil
}

// Healthy returns true if the Postgres pool is reachable.
func (s *Store) Healthy(ctx context.Context) bool {
	return s.pool.Ping(ctx) == nil
}

// isZeroVector returns true if all elements are zero.
func isZeroVector(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}
