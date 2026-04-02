-- +goose up
CREATE EXTENSION IF NOT EXISTS vector;

-- +goose down
-- Not dropping the extension; other schemas may use it.
