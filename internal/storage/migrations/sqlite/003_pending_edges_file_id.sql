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
-- fact that this table's schema is changing. The migration drops all
-- existing pending edges; a hard re-index is required after upgrade.
--
-- SQLite can't ADD COLUMN NOT NULL without DEFAULT, so we rewrite the
-- table (the data is being discarded anyway).

DROP TABLE IF EXISTS pending_edges;

CREATE TABLE pending_edges (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id      INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    file_id            INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    dst_symbol_name    TEXT NOT NULL,
    kind               TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    dst_qualified_name TEXT DEFAULT '',
    src_language       TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_pending_name ON pending_edges(dst_symbol_name);
CREATE INDEX IF NOT EXISTS idx_pending_src ON pending_edges(src_symbol_id);
CREATE INDEX IF NOT EXISTS idx_pending_file ON pending_edges(file_id);

-- +goose down

-- Downgrade: drop the table with the new shape and restore the original.
-- Any rows are lost (symmetric to the up migration).

DROP TABLE IF EXISTS pending_edges;

CREATE TABLE pending_edges (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id      INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    dst_symbol_name    TEXT NOT NULL,
    kind               TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    dst_qualified_name TEXT DEFAULT '',
    src_language       TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_pending_name ON pending_edges(dst_symbol_name);
CREATE INDEX IF NOT EXISTS idx_pending_src ON pending_edges(src_symbol_id);
