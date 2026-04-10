-- +goose up

-- Project registry for multi-project isolation.
CREATE TABLE IF NOT EXISTS projects (
    id         BIGSERIAL PRIMARY KEY,
    root_path  TEXT UNIQUE NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed default project so existing rows can reference it.
INSERT INTO projects (id, root_path, name) VALUES (1, '__default__', 'default')
ON CONFLICT DO NOTHING;

-- Ensure the sequence stays ahead of the seeded row.
SELECT setval('projects_id_seq', GREATEST(nextval('projects_id_seq'), 2));

-- Add project_id to files (three-step to avoid ACCESS EXCLUSIVE on large tables).
ALTER TABLE files ADD COLUMN project_id BIGINT REFERENCES projects(id);
UPDATE files SET project_id = 1 WHERE project_id IS NULL;
ALTER TABLE files ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE files ALTER COLUMN project_id SET DEFAULT 1;

-- Replace global UNIQUE(path) with per-project UNIQUE(project_id, path).
ALTER TABLE files DROP CONSTRAINT files_path_key;
ALTER TABLE files ADD CONSTRAINT files_project_path_key UNIQUE (project_id, path);
CREATE INDEX IF NOT EXISTS idx_files_project ON files(project_id);

-- Add project_id to embeddings (denormalized for vector search performance).
ALTER TABLE embeddings ADD COLUMN project_id BIGINT REFERENCES projects(id);
UPDATE embeddings SET project_id = 1 WHERE project_id IS NULL;
ALTER TABLE embeddings ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE embeddings ALTER COLUMN project_id SET DEFAULT 1;
CREATE INDEX IF NOT EXISTS idx_embeddings_project ON embeddings(project_id);

-- +goose down

-- Embeddings: drop project_id column and index.
DROP INDEX IF EXISTS idx_embeddings_project;
ALTER TABLE embeddings DROP COLUMN IF EXISTS project_id;

-- Files: restore global UNIQUE(path) constraint.
DROP INDEX IF EXISTS idx_files_project;
ALTER TABLE files DROP CONSTRAINT IF EXISTS files_project_path_key;
ALTER TABLE files DROP COLUMN IF EXISTS project_id;
ALTER TABLE files ADD CONSTRAINT files_path_key UNIQUE (path);

-- Drop projects table.
DROP TABLE IF EXISTS projects;
