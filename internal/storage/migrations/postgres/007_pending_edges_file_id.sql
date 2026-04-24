-- +goose up

-- Add file_id to pending_edges so resolved edges record their source file,
-- allowing DeleteEdgesByFile to cascade correctly when a file is re-indexed.
--
-- Previously, ResolvePendingEdges INSERTed resolved rows into edges without
-- file_id. DeleteEdgesByFile(fileID) filters by file_id, so resolved edges
-- with NULL file_id never got cleaned up on file modification — the
-- dependency graph silently accumulated stale edges.
--
-- No backfill: existing pending_edges rows lack the file_id we need, and
-- reconstructing it from src_symbol_id → symbols.file_id would hide the
-- fact that this table's schema is changing. The migration truncates all
-- existing pending edges; a hard re-index is required after upgrade.

TRUNCATE pending_edges;

ALTER TABLE pending_edges
    ADD COLUMN file_id BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_pending_file ON pending_edges(file_id);

-- +goose down

DROP INDEX IF EXISTS idx_pending_file;
ALTER TABLE pending_edges DROP COLUMN IF EXISTS file_id;
