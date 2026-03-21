package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// DiffLogEntry represents a file-level change record.
type DiffLogEntry struct {
	ID           int64
	FileID       int64
	Timestamp    string
	ChangeType   string // add | modify | delete | rename
	LinesAdded   int
	LinesRemoved int
	HashBefore   string
	HashAfter    string
}

// DiffSymbolEntry represents a symbol-level change within a diff.
type DiffSymbolEntry struct {
	SymbolName string
	SymbolID   int64  // 0 if unknown
	ChangeType string // added | modified | removed | signature_changed
	ChunkID    int64  // 0 if unknown
}

// InsertDiffLog records a file-level change within a transaction.
func (s *Store) InsertDiffLog(ctx context.Context, tx *sql.Tx, entry DiffLogEntry) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO diff_log (file_id, change_type, lines_added, lines_removed, hash_before, hash_after)
		VALUES (?, ?, ?, ?, ?, ?)`,
		entry.FileID, entry.ChangeType, entry.LinesAdded, entry.LinesRemoved,
		entry.HashBefore, entry.HashAfter)
	if err != nil {
		return 0, fmt.Errorf("insert diff_log: %w", err)
	}
	return res.LastInsertId()
}

// InsertDiffSymbols records symbol-level changes for a diff.
func (s *Store) InsertDiffSymbols(ctx context.Context, tx *sql.Tx, diffID int64, symbols []DiffSymbolEntry) error {
	if len(symbols) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare diff_symbols insert: %w", err)
	}
	defer stmt.Close()

	for _, ds := range symbols {
		symID := sql.NullInt64{Int64: ds.SymbolID, Valid: ds.SymbolID != 0}
		chunkID := sql.NullInt64{Int64: ds.ChunkID, Valid: ds.ChunkID != 0}
		if _, err := stmt.ExecContext(ctx, diffID, symID, ds.SymbolName, ds.ChangeType, chunkID); err != nil {
			return fmt.Errorf("insert diff_symbol %s: %w", ds.SymbolName, err)
		}
	}
	return nil
}

// RecentDiffsInput configures a recent diffs query.
type RecentDiffsInput struct {
	Since  time.Time
	FileID int64 // 0 for all files
	Limit  int   // 0 for no limit
}

// GetRecentDiffs returns diffs within the given time window.
func (s *Store) GetRecentDiffs(ctx context.Context, input RecentDiffsInput) ([]DiffLogEntry, error) {
	since := input.Since.UTC().Format(time.RFC3339Nano)
	var query string
	var args []any

	if input.FileID != 0 {
		query = `SELECT id, file_id, timestamp, change_type, lines_added, lines_removed,
		         hash_before, hash_after
		         FROM diff_log WHERE file_id = ? AND timestamp >= ? ORDER BY timestamp DESC`
		args = append(args, input.FileID, since)
	} else {
		query = `SELECT id, file_id, timestamp, change_type, lines_added, lines_removed,
		         hash_before, hash_after
		         FROM diff_log WHERE timestamp >= ? ORDER BY timestamp DESC`
		args = append(args, since)
	}

	if input.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", input.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get recent diffs: %w", err)
	}
	defer rows.Close()

	var diffs []DiffLogEntry
	for rows.Next() {
		var d DiffLogEntry
		var hashBefore, hashAfter sql.NullString
		if err := rows.Scan(&d.ID, &d.FileID, &d.Timestamp, &d.ChangeType,
			&d.LinesAdded, &d.LinesRemoved, &hashBefore, &hashAfter); err != nil {
			return nil, fmt.Errorf("scan diff_log: %w", err)
		}
		d.HashBefore = hashBefore.String
		d.HashAfter = hashAfter.String
		diffs = append(diffs, d)
	}
	return diffs, rows.Err()
}

// GetDiffSymbols returns symbol-level changes for a diff.
func (s *Store) GetDiffSymbols(ctx context.Context, diffID int64) ([]DiffSymbolEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_name, COALESCE(symbol_id, 0), change_type, COALESCE(chunk_id, 0)
		FROM diff_symbols WHERE diff_id = ?`, diffID)
	if err != nil {
		return nil, fmt.Errorf("get diff symbols: %w", err)
	}
	defer rows.Close()

	var symbols []DiffSymbolEntry
	for rows.Next() {
		var ds DiffSymbolEntry
		if err := rows.Scan(&ds.SymbolName, &ds.SymbolID, &ds.ChangeType, &ds.ChunkID); err != nil {
			return nil, fmt.Errorf("scan diff_symbol: %w", err)
		}
		symbols = append(symbols, ds)
	}
	return symbols, rows.Err()
}

// ComputeChangeScores returns recency*magnitude scores for the given chunk IDs.
// Score = exp(-0.05 * hours_since_change) * min(magnitude / 50.0, 1.0)
func (s *Store) ComputeChangeScores(ctx context.Context, chunkIDs []int64) (map[int64]float64, error) {
	scores := make(map[int64]float64, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return scores, nil
	}

	now := time.Now().UTC()

	// For each chunk, find the most recent diff that affected it
	// via diff_symbols.chunk_id or by matching the file
	for _, chunkID := range chunkIDs {
		var timestamp string
		var linesAdded, linesRemoved int

		err := s.db.QueryRowContext(ctx, `
			SELECT dl.timestamp, dl.lines_added, dl.lines_removed
			FROM diff_log dl
			JOIN diff_symbols ds ON ds.diff_id = dl.id
			WHERE ds.chunk_id = ?
			ORDER BY dl.timestamp DESC LIMIT 1`, chunkID).Scan(&timestamp, &linesAdded, &linesRemoved)

		if err == sql.ErrNoRows {
			// No diff found for this chunk — check file-level
			err = s.db.QueryRowContext(ctx, `
				SELECT dl.timestamp, dl.lines_added, dl.lines_removed
				FROM diff_log dl
				JOIN chunks c ON c.file_id = dl.file_id
				WHERE c.id = ?
				ORDER BY dl.timestamp DESC LIMIT 1`, chunkID).Scan(&timestamp, &linesAdded, &linesRemoved)
		}
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("change score for chunk %d: %w", chunkID, err)
		}

		t, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			continue
		}

		hours := now.Sub(t).Hours()
		magnitude := float64(linesAdded + linesRemoved)
		score := math.Exp(-0.05*hours) * math.Min(magnitude/50.0, 1.0)
		scores[chunkID] = score
	}

	return scores, nil
}
