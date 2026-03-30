package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

const schemaVersion = 2

// schemaV1 contains all DDL statements for schema version 1.
// Tables: files, chunks, symbols, edges, pending_edges, diff_log, diff_symbols,
// chunks_fts (FTS5), access_log, working_set, schema_version, config.
var schemaV1 = []string{
	// ── System tables ──
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`,

	`CREATE TABLE IF NOT EXISTS config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,

	// ── Core entities ──
	`CREATE TABLE IF NOT EXISTS files (
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
			CHECK (parse_quality IN ('full', 'partial', 'error', 'unparseable'))
	)`,

	`CREATE TABLE IF NOT EXISTS chunks (
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
	)`,

	`CREATE TABLE IF NOT EXISTS symbols (
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
	)`,

	// ── Graph ──
	`CREATE TABLE IF NOT EXISTS edges (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		src_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		dst_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		kind          TEXT NOT NULL
			CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
		file_id       INTEGER REFERENCES files(id) ON DELETE CASCADE,
		UNIQUE (src_symbol_id, dst_symbol_id, kind)
	)`,

	`CREATE TABLE IF NOT EXISTS pending_edges (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		src_symbol_id   INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
		dst_symbol_name TEXT NOT NULL,
		kind            TEXT NOT NULL
			CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
		created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`,

	// ── Diff tracking ──
	`CREATE TABLE IF NOT EXISTS diff_log (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
		timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		change_type   TEXT NOT NULL
			CHECK (change_type IN ('add', 'modify', 'delete', 'rename')),
		lines_added   INTEGER NOT NULL DEFAULT 0,
		lines_removed INTEGER NOT NULL DEFAULT 0,
		hash_before   TEXT,
		hash_after    TEXT
	)`,

	`CREATE TABLE IF NOT EXISTS diff_symbols (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		diff_id     INTEGER NOT NULL REFERENCES diff_log(id) ON DELETE CASCADE,
		symbol_id   INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
		symbol_name TEXT NOT NULL,
		change_type TEXT NOT NULL
			CHECK (change_type IN ('added', 'modified', 'removed', 'signature_changed', 'moved')),
		chunk_id    INTEGER REFERENCES chunks(id) ON DELETE SET NULL
	)`,

	// ── Full-text search (DM-1: external content FTS5) ──
	`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
		content,
		symbol_name,
		content=chunks,
		content_rowid=id
	)`,

	// ── Session tracking (DM-2: stable keys) ──
	`CREATE TABLE IF NOT EXISTS access_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  TEXT NOT NULL,
		timestamp   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
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
		last_accessed          TEXT NOT NULL,
		queries_since_last_hit INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_id, file_path, chunk_index)
	)`,

	// ── Tool call metrics ──
	`CREATE TABLE IF NOT EXISTS tool_calls (
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
	)`,

	// ── Indexes ──

	// Files (DM-8: no idx_files_path — UNIQUE creates implicit index)
	`CREATE INDEX IF NOT EXISTS idx_files_language ON files(language)`,
	`CREATE INDEX IF NOT EXISTS idx_files_embedding_status ON files(embedding_status)`,

	// Chunks
	`CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_file_index ON chunks(file_id, chunk_index)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_symbol_name ON chunks(symbol_name)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_kind ON chunks(kind)`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_embedded ON chunks(embedded, id)`,

	// Symbols
	`CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(qualified_name)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_chunk ON symbols(chunk_id)`,
	`CREATE INDEX IF NOT EXISTS idx_symbols_kind ON symbols(kind)`,

	// Edges
	`CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src_symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst_symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind)`,
	`CREATE INDEX IF NOT EXISTS idx_edges_file ON edges(file_id)`,

	// Pending edges
	`CREATE INDEX IF NOT EXISTS idx_pending_name ON pending_edges(dst_symbol_name)`,
	`CREATE INDEX IF NOT EXISTS idx_pending_src ON pending_edges(src_symbol_id)`,

	// Diff
	`CREATE INDEX IF NOT EXISTS idx_difflog_file_ts ON diff_log(file_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_difflog_ts ON diff_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_symbol ON diff_symbols(symbol_id)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_diff ON diff_symbols(diff_id)`,
	`CREATE INDEX IF NOT EXISTS idx_diffsym_chunk ON diff_symbols(chunk_id)`,

	// Session
	`CREATE INDEX IF NOT EXISTS idx_access_session ON access_log(session_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_access_file ON access_log(file_path, chunk_index)`,

	// Tool call metrics
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_tool ON tool_calls(tool_name, timestamp)`,
}

// ftsTriggers creates triggers to keep chunks_fts in sync with chunks (DM-1).
var ftsTriggers = []string{
	`CREATE TRIGGER IF NOT EXISTS chunks_fts_insert AFTER INSERT ON chunks BEGIN
		INSERT INTO chunks_fts(rowid, content, symbol_name)
		VALUES (new.id, new.content, new.symbol_name);
	END`,

	`CREATE TRIGGER IF NOT EXISTS chunks_fts_delete AFTER DELETE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
		VALUES ('delete', old.id, old.content, old.symbol_name);
	END`,

	`CREATE TRIGGER IF NOT EXISTS chunks_fts_update AFTER UPDATE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
		VALUES ('delete', old.id, old.content, old.symbol_name);
		INSERT INTO chunks_fts(rowid, content, symbol_name)
		VALUES (new.id, new.content, new.symbol_name);
	END`,
}

// migrationsV1toV2 adds per-chunk embedding tracking.
var migrationsV1toV2 = []string{
	`ALTER TABLE chunks ADD COLUMN embedded INTEGER NOT NULL DEFAULT 0`,
	`CREATE INDEX IF NOT EXISTS idx_chunks_embedded ON chunks(embedded, id)`,
}

// currentSchemaVersion returns the highest version recorded, or 0 if the
// schema_version table does not yet exist.
func currentSchemaVersion(tx *sql.Tx) (int, error) {
	var version int
	err := tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		// Table might not exist yet (fresh DB).
		return 0, nil
	}
	return version, nil
}

// Migrate creates all tables and indexes for the current schema version.
// It is idempotent — safe to call on an existing database.
// For existing v1 databases, it runs incremental migrations to reach v2.
func Migrate(db *DB) error {
	return db.WithWriteTx(func(tx *sql.Tx) error {
		// Apply base schema (all CREATE IF NOT EXISTS — idempotent).
		for _, stmt := range schemaV1 {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("exec schema DDL: %w", err)
			}
		}

		for _, stmt := range ftsTriggers {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("exec FTS trigger: %w", err)
			}
		}

		// Determine current version and apply incremental migrations.
		cur, err := currentSchemaVersion(tx)
		if err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}

		if cur < 2 {
			for _, stmt := range migrationsV1toV2 {
				if _, err := tx.Exec(stmt); err != nil {
					// "duplicate column" means migration already applied partially.
					if isDuplicateColumnError(err) {
						continue
					}
					return fmt.Errorf("migrate v1→v2: %w", err)
				}
			}
		}

		// Record schema version if not present.
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = ?", schemaVersion).Scan(&count); err != nil {
			return fmt.Errorf("check schema version: %w", err)
		}
		if count == 0 {
			if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion); err != nil {
				return fmt.Errorf("insert schema version: %w", err)
			}
		}

		return nil
	})
}

// isDuplicateColumnError checks if the error is a SQLite "duplicate column name" error.
func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
