package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const pgSchemaVersion = 1

// schemaDDL contains all Postgres DDL statements.
// Differences from SQLite: BIGSERIAL, TIMESTAMPTZ, BOOLEAN, tsvector, no FTS5.
var schemaDDL = []string{
	// ── System tables ──
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

	`CREATE TABLE IF NOT EXISTS config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,

	// ── Core entities ──
	`CREATE TABLE IF NOT EXISTS files (
		id               BIGSERIAL PRIMARY KEY,
		path             TEXT UNIQUE NOT NULL,
		content_hash     TEXT NOT NULL,
		mtime            DOUBLE PRECISION NOT NULL,
		size             BIGINT DEFAULT 0,
		language         TEXT DEFAULT '',
		indexed_at       TIMESTAMPTZ,
		embedding_status TEXT DEFAULT 'pending'
			CHECK (embedding_status IN ('pending', 'partial', 'complete')),
		parse_quality    TEXT DEFAULT 'full'
			CHECK (parse_quality IN ('full', 'partial', 'error', 'unparseable')),
		is_test          BOOLEAN NOT NULL DEFAULT FALSE
	)`,

	`CREATE TABLE IF NOT EXISTS chunks (
		id              BIGSERIAL PRIMARY KEY,
		file_id         BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
		parent_chunk_id BIGINT REFERENCES chunks(id) ON DELETE SET NULL,
		chunk_index     INTEGER NOT NULL,
		symbol_name     TEXT DEFAULT '',
		kind            TEXT NOT NULL
			CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'header', 'block')),
		start_line      INTEGER NOT NULL,
		end_line        INTEGER NOT NULL,
		content         TEXT NOT NULL,
		token_count     INTEGER NOT NULL,
		signature       TEXT DEFAULT '',
		parse_quality   TEXT NOT NULL DEFAULT 'full'
			CHECK (parse_quality IN ('full', 'partial')),
		embedded        INTEGER NOT NULL DEFAULT 0,
		content_tsv     tsvector GENERATED ALWAYS AS (
			to_tsvector('simple', coalesce(content, '') || ' ' || coalesce(symbol_name, ''))
		) STORED
	)`,

	`CREATE TABLE IF NOT EXISTS symbols (
		id             BIGSERIAL PRIMARY KEY,
		chunk_id       BIGINT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
		file_id        BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
		name           TEXT NOT NULL,
		qualified_name TEXT DEFAULT '',
		kind           TEXT NOT NULL
			CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant')),
		line           INTEGER NOT NULL,
		signature      TEXT DEFAULT '',
		visibility     TEXT CHECK (visibility IN ('public', 'private', 'internal', 'exported')),
		is_exported    BOOLEAN NOT NULL DEFAULT FALSE
	)`,

	// ── Graph ──
	`CREATE TABLE IF NOT EXISTS edges (
		id            BIGSERIAL PRIMARY KEY,
		src_symbol_id BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		dst_symbol_id BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		kind          TEXT NOT NULL
			CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
		file_id       BIGINT REFERENCES files(id) ON DELETE CASCADE,
		UNIQUE (src_symbol_id, dst_symbol_id, kind)
	)`,

	`CREATE TABLE IF NOT EXISTS pending_edges (
		id                BIGSERIAL PRIMARY KEY,
		src_symbol_id     BIGINT NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		dst_symbol_name   TEXT NOT NULL,
		kind              TEXT NOT NULL
			CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
		created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		dst_qualified_name TEXT DEFAULT '',
		src_language       TEXT DEFAULT ''
	)`,

	// ── Diff tracking ──
	`CREATE TABLE IF NOT EXISTS diff_log (
		id            BIGSERIAL PRIMARY KEY,
		file_id       BIGINT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
		timestamp     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		change_type   TEXT NOT NULL
			CHECK (change_type IN ('add', 'modify', 'delete', 'rename')),
		lines_added   INTEGER NOT NULL DEFAULT 0,
		lines_removed INTEGER NOT NULL DEFAULT 0,
		hash_before   TEXT,
		hash_after    TEXT
	)`,

	`CREATE TABLE IF NOT EXISTS diff_symbols (
		id          BIGSERIAL PRIMARY KEY,
		diff_id     BIGINT NOT NULL REFERENCES diff_log(id) ON DELETE CASCADE,
		symbol_id   BIGINT REFERENCES symbols(id) ON DELETE SET NULL,
		symbol_name TEXT NOT NULL,
		change_type TEXT NOT NULL
			CHECK (change_type IN ('added', 'modified', 'removed', 'signature_changed', 'moved')),
		chunk_id    BIGINT REFERENCES chunks(id) ON DELETE SET NULL
	)`,

	// ── Session tracking ──
	`CREATE TABLE IF NOT EXISTS access_log (
		id          BIGSERIAL PRIMARY KEY,
		session_id  TEXT NOT NULL,
		timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		file_path   TEXT NOT NULL,
		chunk_index INTEGER NOT NULL,
		operation   TEXT NOT NULL
			CHECK (operation IN ('search_hit', 'context_include', 'direct_read'))
	)`,

	`CREATE TABLE IF NOT EXISTS working_set (
		session_id             TEXT NOT NULL,
		file_path              TEXT NOT NULL,
		chunk_index            INTEGER NOT NULL,
		access_count           INTEGER NOT NULL DEFAULT 1,
		last_accessed          TIMESTAMPTZ NOT NULL,
		queries_since_last_hit INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_id, file_path, chunk_index)
	)`,

	// ── Tool call metrics ──
	`CREATE TABLE IF NOT EXISTS tool_calls (
		id                  BIGSERIAL PRIMARY KEY,
		session_id          TEXT NOT NULL,
		timestamp           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		tool_name           TEXT NOT NULL,
		args_json           TEXT,
		args_bytes          INTEGER NOT NULL DEFAULT 0,
		response_bytes      INTEGER NOT NULL DEFAULT 0,
		response_tokens_est INTEGER NOT NULL DEFAULT 0,
		result_count        INTEGER NOT NULL DEFAULT 0,
		duration_ms         INTEGER NOT NULL DEFAULT 0,
		is_error            BOOLEAN NOT NULL DEFAULT FALSE
	)`,

	// ── Indexes ──
	`CREATE INDEX IF NOT EXISTS idx_files_language ON files(language)`,
	`CREATE INDEX IF NOT EXISTS idx_files_embedding_status ON files(embedding_status)`,

	`CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_file_index ON chunks(file_id, chunk_index)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_symbol_name ON chunks(symbol_name)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_kind ON chunks(kind)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_embedded ON chunks(embedded, id)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_fts ON chunks USING GIN(content_tsv)`,

	`CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(qualified_name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_chunk ON symbols(chunk_id)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind)`,

	`CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src_symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst_symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_file ON edges(file_id)`,

	`CREATE INDEX IF NOT EXISTS idx_pending_name ON pending_edges(dst_symbol_name)`,
	`CREATE INDEX IF NOT EXISTS idx_pending_src ON pending_edges(src_symbol_id)`,

	`CREATE INDEX IF NOT EXISTS idx_difflog_file_ts ON diff_log(file_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_difflog_ts ON diff_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_symbol ON diff_symbols(symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_diff ON diff_symbols(diff_id)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_chunk ON diff_symbols(chunk_id)`,

	`CREATE INDEX IF NOT EXISTS idx_access_session ON access_log(session_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_access_file ON access_log(file_path, chunk_index)`,

	`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_tool ON tool_calls(tool_name, timestamp)`,
}

// Migrate creates all tables and indexes for the Postgres schema.
// Idempotent — all DDL uses IF NOT EXISTS.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, stmt := range schemaDDL {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("exec DDL: %w\nstatement: %s", err, stmt)
		}
	}

	// Record schema version
	var count int
	err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM schema_version WHERE version = $1", pgSchemaVersion).Scan(&count)
	if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if count == 0 {
		if _, err := tx.Exec(ctx, "INSERT INTO schema_version (version) VALUES ($1)", pgSchemaVersion); err != nil {
			return fmt.Errorf("insert schema version: %w", err)
		}
	}

	return tx.Commit(ctx)
}
