package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── DiffStore interface ──

func (s *PgStore) InsertDiffLog(ctx context.Context, txh types.TxHandle, entry types.DiffLogEntry) (int64, error) {
	tx := txh.(PgTxHandle).Tx
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO diff_log (file_id, change_type, lines_added, lines_removed, hash_before, hash_after)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		entry.FileID, entry.ChangeType, entry.LinesAdded, entry.LinesRemoved,
		entry.HashBefore, entry.HashAfter,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert diff_log: %w", err)
	}
	return id, nil
}

func (s *PgStore) InsertDiffSymbols(ctx context.Context, txh types.TxHandle, diffID int64, symbols []types.DiffSymbolEntry) error {
	if len(symbols) == 0 {
		return nil
	}
	tx := txh.(PgTxHandle).Tx
	for _, ds := range symbols {
		var symID, chunkID *int64
		if ds.SymbolID != 0 {
			symID = &ds.SymbolID
		}
		if ds.ChunkID != 0 {
			chunkID = &ds.ChunkID
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
			VALUES ($1, $2, $3, $4, $5)`,
			diffID, symID, ds.SymbolName, ds.ChangeType, chunkID)
		if err != nil {
			return fmt.Errorf("insert diff_symbol %s: %w", ds.SymbolName, err)
		}
	}
	return nil
}

func (s *PgStore) GetRecentDiffs(ctx context.Context, input types.RecentDiffsInput) ([]types.DiffLogEntry, error) {
	var query string
	var args []any

	if input.FileID != 0 {
		query = `SELECT id, file_id, timestamp, change_type, lines_added, lines_removed,
		         hash_before, hash_after
		         FROM diff_log WHERE file_id = $1 AND timestamp >= $2 ORDER BY timestamp DESC`
		args = append(args, input.FileID, input.Since)
	} else {
		query = `SELECT id, file_id, timestamp, change_type, lines_added, lines_removed,
		         hash_before, hash_after
		         FROM diff_log WHERE timestamp >= $1 ORDER BY timestamp DESC`
		args = append(args, input.Since)
	}

	if input.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", input.Limit)
	}

	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get recent diffs: %w", err)
	}
	defer rows.Close()

	var diffs []types.DiffLogEntry
	for rows.Next() {
		var d types.DiffLogEntry
		var ts time.Time
		var hashBefore, hashAfter *string
		if err := rows.Scan(&d.ID, &d.FileID, &ts, &d.ChangeType,
			&d.LinesAdded, &d.LinesRemoved, &hashBefore, &hashAfter); err != nil {
			return nil, err
		}
		d.Timestamp = ts.Format(time.RFC3339Nano)
		if hashBefore != nil {
			d.HashBefore = *hashBefore
		}
		if hashAfter != nil {
			d.HashAfter = *hashAfter
		}
		diffs = append(diffs, d)
	}
	return diffs, rows.Err()
}

func (s *PgStore) GetDiffSymbols(ctx context.Context, diffID int64) ([]types.DiffSymbolEntry, error) {
	rows, err := s.query(ctx, `
		SELECT symbol_name, COALESCE(symbol_id, 0), change_type, COALESCE(chunk_id, 0)
		FROM diff_symbols WHERE diff_id = $1`, diffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []types.DiffSymbolEntry
	for rows.Next() {
		var ds types.DiffSymbolEntry
		rows.Scan(&ds.SymbolName, &ds.SymbolID, &ds.ChangeType, &ds.ChunkID)
		symbols = append(symbols, ds)
	}
	return symbols, rows.Err()
}

// ── ComputeChangeScores ──

func computeChangeScoresPg(ctx context.Context, s *PgStore, chunkIDs []int64) (map[int64]float64, error) {
	scores := make(map[int64]float64, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return scores, nil
	}

	now := time.Now().UTC()

	scoreRow := func(ts time.Time, linesAdded, linesRemoved int) float64 {
		hours := now.Sub(ts).Hours()
		magnitude := float64(linesAdded + linesRemoved)
		return math.Exp(-0.05*hours) * math.Min(magnitude/50.0, 1.0)
	}

	// Query 1: Symbol-level diffs
	rows, err := s.query(ctx, `
		SELECT ds.chunk_id, dl.timestamp, dl.lines_added, dl.lines_removed
		FROM diff_symbols ds
		JOIN diff_log dl ON dl.id = ds.diff_id
		WHERE ds.chunk_id = ANY($1)
		ORDER BY dl.timestamp DESC`, chunkIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var chunkID int64
		var ts time.Time
		var linesAdded, linesRemoved int
		if err := rows.Scan(&chunkID, &ts, &linesAdded, &linesRemoved); err != nil {
			continue
		}
		if _, exists := scores[chunkID]; exists {
			continue
		}
		if s := scoreRow(ts, linesAdded, linesRemoved); s > 0 {
			scores[chunkID] = s
		}
	}

	// Query 2: File-level fallback
	var missing []int64
	for _, id := range chunkIDs {
		if _, found := scores[id]; !found {
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		rows2, err := s.query(ctx, `
			SELECT c.id, dl.timestamp, dl.lines_added, dl.lines_removed
			FROM chunks c
			JOIN diff_log dl ON dl.file_id = c.file_id
			WHERE c.id = ANY($1)
			ORDER BY dl.timestamp DESC`, missing)
		if err != nil {
			return nil, err
		}
		defer rows2.Close()

		for rows2.Next() {
			var chunkID int64
			var ts time.Time
			var linesAdded, linesRemoved int
			if err := rows2.Scan(&chunkID, &ts, &linesAdded, &linesRemoved); err != nil {
				continue
			}
			if _, exists := scores[chunkID]; exists {
				continue
			}
			if s := scoreRow(ts, linesAdded, linesRemoved); s > 0 {
				scores[chunkID] = s
			}
		}
	}

	return scores, nil
}
