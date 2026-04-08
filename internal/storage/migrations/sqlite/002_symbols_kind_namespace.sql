-- +goose up

-- Add 'namespace' to the symbols.kind CHECK constraint.
--
-- ADR-004 added TypeScript `internal_module → namespace` to the parser's
-- SymbolKindMap, but the original schema's CHECK constraint in
-- 001_base_schema.sql only permits {function, class, method, type,
-- interface, variable, constant}. Inserting a namespace symbol fails at
-- the DB layer, so TypeScript namespace symbols have been silently
-- dropped from the index.
--
-- SQLite doesn't support ALTER TABLE ALTER CONSTRAINT, so we rewrite the
-- table: create a new symbols_new with the updated CHECK, copy rows,
-- drop the old table, rename the new one, and rebuild the indexes that
-- belonged to the old table.

CREATE TABLE symbols_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    chunk_id       INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    file_id        INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    qualified_name TEXT,
    kind           TEXT NOT NULL
        CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant', 'namespace')),
    line           INTEGER NOT NULL,
    signature      TEXT,
    visibility     TEXT CHECK (visibility IN ('public', 'private', 'internal', 'exported')),
    is_exported    INTEGER NOT NULL DEFAULT 0
);

INSERT INTO symbols_new
    (id, chunk_id, file_id, name, qualified_name, kind, line, signature, visibility, is_exported)
SELECT
    id, chunk_id, file_id, name, qualified_name, kind, line, signature, visibility, is_exported
FROM symbols;

DROP TABLE symbols;
ALTER TABLE symbols_new RENAME TO symbols;

-- Recreate the symbol indexes from 001_base_schema.sql
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id);
CREATE INDEX IF NOT EXISTS idx_symbols_chunk ON symbols(chunk_id);
CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind);

-- +goose down

-- Downgrade: recreate the symbols table without 'namespace' in the CHECK.
-- Any existing rows with kind='namespace' are silently dropped on downgrade
-- because the old CHECK would reject them.

CREATE TABLE symbols_old (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    chunk_id       INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    file_id        INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    qualified_name TEXT,
    kind           TEXT NOT NULL
        CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant')),
    line           INTEGER NOT NULL,
    signature      TEXT,
    visibility     TEXT CHECK (visibility IN ('public', 'private', 'internal', 'exported')),
    is_exported    INTEGER NOT NULL DEFAULT 0
);

INSERT INTO symbols_old
    (id, chunk_id, file_id, name, qualified_name, kind, line, signature, visibility, is_exported)
SELECT
    id, chunk_id, file_id, name, qualified_name, kind, line, signature, visibility, is_exported
FROM symbols WHERE kind != 'namespace';

DROP TABLE symbols;
ALTER TABLE symbols_old RENAME TO symbols;

CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id);
CREATE INDEX IF NOT EXISTS idx_symbols_chunk ON symbols(chunk_id);
CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind);
