package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// DiffLogEntry is a backward-compatible alias for types.DiffLogEntry.
type DiffLogEntry = types.DiffLogEntry

// DiffSymbolEntry is a backward-compatible alias for types.DiffSymbolEntry.
type DiffSymbolEntry = types.DiffSymbolEntry

// InsertDiffLog records a file-level change within a transaction.
func (s *Store) InsertDiffLog(ctx context.Context, txh types.TxHandle, entry DiffLogEntry) (int64, error) {
	tx := txh.(TxHandle).Tx
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
func (s *Store) InsertDiffSymbols(ctx context.Context, txh types.TxHandle, diffID int64, symbols []DiffSymbolEntry) error {
	if len(symbols) == 0 {
		return nil
	}
	tx := txh.(TxHandle).Tx

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare diff_symbols insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, ds := range symbols {
		symID := sql.NullInt64{Int64: ds.SymbolID, Valid: ds.SymbolID != 0}
		chunkID := sql.NullInt64{Int64: ds.ChunkID, Valid: ds.ChunkID != 0}
		if _, err := stmt.ExecContext(ctx, diffID, symID, ds.SymbolName, ds.ChangeType, chunkID); err != nil {
			return fmt.Errorf("insert diff_symbol %s: %w", ds.SymbolName, err)
		}
	}
	return nil
}

// RecentDiffsInput is a backward-compatible alias for types.RecentDiffsInput.
type RecentDiffsInput = types.RecentDiffsInput

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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
// Uses two batched queries instead of per-chunk lookups.
func (s *Store) ComputeChangeScores(ctx context.Context, chunkIDs []int64) (map[int64]float64, error) {
	scores := make(map[int64]float64, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return scores, nil
	}

	now := time.Now().UTC()

	placeholders := make([]string, len(chunkIDs))
	args := make([]any, len(chunkIDs))
	for i, id := range chunkIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := strings.Join(placeholders, ",")

	scoreRow := func(timestamp string, linesAdded, linesRemoved int) float64 {
		t, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return 0
		}
		hours := now.Sub(t).Hours()
		magnitude := float64(linesAdded + linesRemoved)
		return math.Exp(-0.05*hours) * math.Min(magnitude/50.0, 1.0)
	}

	// Query 1: Symbol-level diffs (direct chunk match via diff_symbols)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT ds.chunk_id, dl.timestamp, dl.lines_added, dl.lines_removed
		FROM diff_symbols ds
		JOIN diff_log dl ON dl.id = ds.diff_id
		WHERE ds.chunk_id IN (%s)
		ORDER BY dl.timestamp DESC`, inClause), args...)
	if err != nil {
		return nil, fmt.Errorf("batch symbol-level change scores: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var chunkID int64
		var timestamp string
		var linesAdded, linesRemoved int
		if err := rows.Scan(&chunkID, &timestamp, &linesAdded, &linesRemoved); err != nil {
			continue
		}
		// Keep only the most recent (first row per chunk due to ORDER BY DESC)
		if _, exists := scores[chunkID]; exists {
			continue
		}
		if s := scoreRow(timestamp, linesAdded, linesRemoved); s > 0 {
			scores[chunkID] = s
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan symbol-level change scores: %w", err)
	}

	// Query 2: File-level fallback for chunks not found in symbol-level diffs
	var missing []any
	var missingPH []string
	for _, id := range chunkIDs {
		if _, found := scores[id]; !found {
			missing = append(missing, id)
			missingPH = append(missingPH, "?")
		}
	}

	if len(missing) > 0 {
		rows2, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT c.id, dl.timestamp, dl.lines_added, dl.lines_removed
			FROM chunks c
			JOIN diff_log dl ON dl.file_id = c.file_id
			WHERE c.id IN (%s)
			ORDER BY dl.timestamp DESC`, strings.Join(missingPH, ",")), missing...)
		if err != nil {
			return nil, fmt.Errorf("batch file-level change scores: %w", err)
		}
		defer func() { _ = rows2.Close() }()

		for rows2.Next() {
			var chunkID int64
			var timestamp string
			var linesAdded, linesRemoved int
			if err := rows2.Scan(&chunkID, &timestamp, &linesAdded, &linesRemoved); err != nil {
				continue
			}
			if _, exists := scores[chunkID]; exists {
				continue
			}
			if s := scoreRow(timestamp, linesAdded, linesRemoved); s > 0 {
				scores[chunkID] = s
			}
		}
		if err := rows2.Err(); err != nil {
			return nil, fmt.Errorf("scan file-level change scores: %w", err)
		}
	}

	return scores, nil
}
