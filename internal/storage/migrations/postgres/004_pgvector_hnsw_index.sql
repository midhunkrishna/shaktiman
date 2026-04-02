-- +goose no transaction

-- +goose up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_embeddings_hnsw
    ON embeddings USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 200);

-- +goose down
DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings_hnsw;
