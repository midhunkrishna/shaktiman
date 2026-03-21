package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// FTSResult represents a single full-text search match.
type FTSResult struct {
	ChunkID int64
	Rank    float64 // BM25 rank (lower = more relevant)
}

// KeywordSearch performs an FTS5 full-text search on chunk content and symbol names.
// Returns results ordered by BM25 relevance, limited to the given count.
func (s *Store) KeywordSearch(ctx context.Context, query string, limit int) ([]FTSResult, error) {
	if query == "" {
		return nil, nil
	}

	// Sanitize query for FTS5: escape double quotes, wrap terms
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT rowid, rank
		FROM chunks_fts
		WHERE chunks_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS5 search %q: %w", query, err)
	}
	defer rows.Close()

	var results []FTSResult
	for rows.Next() {
		var r FTSResult
		if err := rows.Scan(&r.ChunkID, &r.Rank); err != nil {
			return nil, fmt.Errorf("scan FTS result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// DisableFTSTriggers drops FTS sync triggers for bulk insert performance (A11).
func (s *Store) DisableFTSTriggers(ctx context.Context) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		for _, name := range []string{"chunks_fts_insert", "chunks_fts_delete", "chunks_fts_update"} {
			if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name); err != nil {
				return fmt.Errorf("drop trigger %s: %w", name, err)
			}
		}
		return nil
	})
}

// EnableFTSTriggers recreates FTS sync triggers after bulk insert.
func (s *Store) EnableFTSTriggers(ctx context.Context) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		for _, stmt := range ftsTriggers {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create FTS trigger: %w", err)
			}
		}
		return nil
	})
}

// EnsureFTSTriggers unconditionally re-creates the three FTS triggers.
// Idempotent — safe to call on startup for crash recovery.
func (s *Store) EnsureFTSTriggers(ctx context.Context) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		for _, stmt := range ftsTriggers {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

// RebuildFTS rebuilds the FTS5 index from the chunks table.
// Call after bulk insert with triggers disabled.
func (s *Store) RebuildFTS(ctx context.Context) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild FTS: %w", err)
		}
		return nil
	})
}

// sanitizeFTSQuery prepares a user query for FTS5 MATCH.
// Splits on whitespace, removes FTS5 special chars, wraps each term.
func sanitizeFTSQuery(query string) string {
	words := strings.Fields(query)
	var terms []string
	for _, w := range words {
		// Remove FTS5 operators and special chars
		clean := strings.Map(func(r rune) rune {
			if r == '"' || r == '*' || r == '+' || r == '-' || r == '^' {
				return -1
			}
			return r
		}, w)
		clean = strings.TrimSpace(clean)
		if clean != "" {
			terms = append(terms, `"`+clean+`"`)
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}
