//go:build pgvector

package pgvector

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("pgvector", func(cfg vector.VectorStoreConfig) (types.VectorStore, error) {
		pool, ok := cfg.PgPool.(*pgxpool.Pool)
		if !ok || pool == nil {
			return nil, fmt.Errorf("pgvector backend requires a Postgres connection pool "+
				"(set database.backend = \"postgres\" in shaktiman.toml); got PgPool=%T", cfg.PgPool)
		}
		return NewPgVectorStore(pool, cfg.Dims, cfg.ProjectID)
	})
}
