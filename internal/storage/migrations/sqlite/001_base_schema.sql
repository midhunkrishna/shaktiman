-- +goose up

-- System tables
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Core entities
CREATE TABLE IF NOT EXISTS files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    path             TEXT UNIQUE NOT NULL,
    content_hash     TEXT NOT NULL,
    mtime            REAL NOT NULL,
    size             INTEGER,
    language         TEXT,
    indexed_at       TEXT,
    embedding_status TEXT DEFAULT 'pending'
        CHECK (embedding_status IN ('pending', 'partial', 'complete')),
    parse_quality    TEXT DEFAULT 'full'
        CHECK (parse_quality IN ('full', 'partial', 'error', 'unparseable')),
    is_test          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS chunks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id         INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    parent_chunk_id INTEGER REFERENCES chunks(id) ON DELETE SET NULL,
    chunk_index     INTEGER NOT NULL,
    symbol_name     TEXT,
    kind            TEXT NOT NULL
        CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'header', 'block')),
    start_line      INTEGER NOT NULL,
    end_line        INTEGER NOT NULL,
    content         TEXT NOT NULL,
    token_count     INTEGER NOT NULL,
    signature       TEXT,
    parse_quality   TEXT NOT NULL DEFAULT 'full'
        CHECK (parse_quality IN ('full', 'partial')),
    embedded        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS symbols (
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

-- Graph
CREATE TABLE IF NOT EXISTS edges (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    dst_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    file_id       INTEGER REFERENCES files(id) ON DELETE CASCADE,
    UNIQUE (src_symbol_id, dst_symbol_id, kind)
);

CREATE TABLE IF NOT EXISTS pending_edges (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id      INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    dst_symbol_name    TEXT NOT NULL,
    kind               TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    dst_qualified_name TEXT DEFAULT '',
    src_language       TEXT DEFAULT ''
);

-- Diff tracking
CREATE TABLE IF NOT EXISTS diff_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    change_type   TEXT NOT NULL
        CHECK (change_type IN ('add', 'modify', 'delete', 'rename')),
    lines_added   INTEGER NOT NULL DEFAULT 0,
    lines_removed INTEGER NOT NULL DEFAULT 0,
    hash_before   TEXT,
    hash_after    TEXT
);

CREATE TABLE IF NOT EXISTS diff_symbols (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    diff_id     INTEGER NOT NULL REFERENCES diff_log(id) ON DELETE CASCADE,
    symbol_id   INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    symbol_name TEXT NOT NULL,
    change_type TEXT NOT NULL
        CHECK (change_type IN ('added', 'modified', 'removed', 'signature_changed', 'moved')),
    chunk_id    INTEGER REFERENCES chunks(id) ON DELETE SET NULL
);

-- Full-text search (external content FTS5)
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    content,
    symbol_name,
    content=chunks,
    content_rowid=id
);

-- FTS triggers (StatementBegin/End needed because triggers contain semicolons)
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS chunks_fts_insert AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, content, symbol_name)
    VALUES (new.id, new.content, new.symbol_name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS chunks_fts_delete AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
    VALUES ('delete', old.id, old.content, old.symbol_name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS chunks_fts_update AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
    VALUES ('delete', old.id, old.content, old.symbol_name);
    INSERT INTO chunks_fts(rowid, content, symbol_name)
    VALUES (new.id, new.content, new.symbol_name);
END;
-- +goose StatementEnd

-- Session tracking
CREATE TABLE IF NOT EXISTS access_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL,
    timestamp   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    file_path   TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    operation   TEXT NOT NULL
        CHECK (operation IN ('search_hit', 'context_include', 'direct_read'))
);

CREATE TABLE IF NOT EXISTS working_set (
    session_id             TEXT NOT NULL,
    file_path              TEXT NOT NULL,
    chunk_index            INTEGER NOT NULL,
    access_count           INTEGER NOT NULL DEFAULT 1,
    last_accessed          TEXT NOT NULL,
    queries_since_last_hit INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, file_path, chunk_index)
);

-- Tool call metrics
CREATE TABLE IF NOT EXISTS tool_calls (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id          TEXT NOT NULL,
    timestamp           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    tool_name           TEXT NOT NULL,
    args_json           TEXT,
    args_bytes          INTEGER NOT NULL DEFAULT 0,
    response_bytes      INTEGER NOT NULL DEFAULT 0,
    response_tokens_est INTEGER NOT NULL DEFAULT 0,
    result_count        INTEGER NOT NULL DEFAULT 0,
    duration_ms         INTEGER NOT NULL DEFAULT 0,
    is_error            INTEGER NOT NULL DEFAULT 0
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_files_language ON files(language);
CREATE INDEX IF NOT EXISTS idx_files_embedding_status ON files(embedding_status);

CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id);
CREATE INDEX IF NOT EXISTS idx_chunks_file_index ON chunks(file_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_chunks_symbol_name ON chunks(symbol_name);
CREATE INDEX IF NOT EXISTS idx_chunks_kind ON chunks(kind);
CREATE INDEX IF NOT EXISTS idx_chunks_embedded ON chunks(embedded, id);

CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id);
CREATE INDEX IF NOT EXISTS idx_symbols_chunk ON symbols(chunk_id);
CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind);

CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src_symbol_id);
CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst_symbol_id);
CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_file ON edges(file_id);

CREATE INDEX IF NOT EXISTS idx_pending_name ON pending_edges(dst_symbol_name);
CREATE INDEX IF NOT EXISTS idx_pending_src ON pending_edges(src_symbol_id);

CREATE INDEX IF NOT EXISTS idx_difflog_file_ts ON diff_log(file_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_difflog_ts ON diff_log(timestamp);
CREATE INDEX IF NOT EXISTS idx_diffsym_symbol ON diff_symbols(symbol_id);
CREATE INDEX IF NOT EXISTS idx_diffsym_diff ON diff_symbols(diff_id);
CREATE INDEX IF NOT EXISTS idx_diffsym_chunk ON diff_symbols(chunk_id);

CREATE INDEX IF NOT EXISTS idx_access_session ON access_log(session_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_access_file ON access_log(file_path, chunk_index);

CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool ON tool_calls(tool_name, timestamp);

-- +goose down
DROP TABLE IF EXISTS tool_calls;
DROP TABLE IF EXISTS working_set;
DROP TABLE IF EXISTS access_log;
DROP TABLE IF EXISTS diff_symbols;
DROP TABLE IF EXISTS diff_log;
DROP TABLE IF EXISTS pending_edges;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS symbols;
DROP TABLE IF EXISTS chunks_fts;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS config;
DROP TABLE IF EXISTS schema_version;
