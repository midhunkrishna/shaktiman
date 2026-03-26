//go:build sqlite_fts5

package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestInsertDiffLog(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, _ := insertTestFileChunkSymbol(t, store, "diff.go", "DiffFunc")

	entry := DiffLogEntry{
		FileID:       fileID,
		ChangeType:   "modify",
		LinesAdded:   10,
		LinesRemoved: 3,
		HashBefore:   "aaa",
		HashAfter:    "bbb",
	}

	var diffID int64
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		var insertErr error
		diffID, insertErr = store.InsertDiffLog(ctx, tx, entry)
		return insertErr
	})
	if err != nil {
		t.Fatalf("InsertDiffLog: %v", err)
	}
	if diffID <= 0 {
		t.Fatalf("expected positive diff ID, got %d", diffID)
	}

	// Retrieve via GetRecentDiffs
	diffs, err := store.GetRecentDiffs(ctx, RecentDiffsInput{
		Since: time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("GetRecentDiffs: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].FileID != fileID {
		t.Errorf("FileID = %d, want %d", diffs[0].FileID, fileID)
	}
	if diffs[0].ChangeType != "modify" {
		t.Errorf("ChangeType = %q, want %q", diffs[0].ChangeType, "modify")
	}
	if diffs[0].LinesAdded != 10 {
		t.Errorf("LinesAdded = %d, want 10", diffs[0].LinesAdded)
	}
	if diffs[0].LinesRemoved != 3 {
		t.Errorf("LinesRemoved = %d, want 3", diffs[0].LinesRemoved)
	}
	if diffs[0].HashBefore != "aaa" {
		t.Errorf("HashBefore = %q, want %q", diffs[0].HashBefore, "aaa")
	}
	if diffs[0].HashAfter != "bbb" {
		t.Errorf("HashAfter = %q, want %q", diffs[0].HashAfter, "bbb")
	}
}

func TestInsertDiffSymbols(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, chunkID, symID := insertTestFileChunkSymbol(t, store, "sym.go", "SymFunc")

	// Create a diff log entry first
	var diffID int64
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		var insertErr error
		diffID, insertErr = store.InsertDiffLog(ctx, tx, DiffLogEntry{
			FileID:     fileID,
			ChangeType: "modify",
			LinesAdded: 5,
		})
		if insertErr != nil {
			return insertErr
		}

		return store.InsertDiffSymbols(ctx, tx, diffID, []DiffSymbolEntry{
			{SymbolName: "SymFunc", SymbolID: symID, ChangeType: "modified", ChunkID: chunkID},
			{SymbolName: "NewFunc", SymbolID: 0, ChangeType: "added", ChunkID: 0},
		})
	})
	if err != nil {
		t.Fatalf("InsertDiffSymbols: %v", err)
	}

	// Retrieve
	symbols, err := store.GetDiffSymbols(ctx, diffID)
	if err != nil {
		t.Fatalf("GetDiffSymbols: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("expected 2 diff symbols, got %d", len(symbols))
	}

	// Find the known symbol
	var found bool
	for _, s := range symbols {
		if s.SymbolName == "SymFunc" {
			found = true
			if s.SymbolID != symID {
				t.Errorf("SymbolID = %d, want %d", s.SymbolID, symID)
			}
			if s.ChangeType != "modified" {
				t.Errorf("ChangeType = %q, want %q", s.ChangeType, "modified")
			}
			if s.ChunkID != chunkID {
				t.Errorf("ChunkID = %d, want %d", s.ChunkID, chunkID)
			}
		}
	}
	if !found {
		t.Error("expected to find SymFunc in diff symbols")
	}
}

func TestGetRecentDiffs(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, _ := insertTestFileChunkSymbol(t, store, "recent.go", "RecentFunc")

	// Insert 3 diff entries with different timestamps via direct SQL
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		for i, ts := range []string{
			"2025-01-01T00:00:00Z", // old
			"2025-06-15T12:00:00Z", // mid
			"2025-12-31T23:59:59Z", // recent
		} {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
				VALUES (?, ?, 'modify', ?, 0)`, fileID, ts, (i+1)*10)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("insert diffs: %v", err)
	}

	// Query since mid-2025 -- should get 2 entries
	since, _ := time.Parse(time.RFC3339, "2025-06-01T00:00:00Z")
	diffs, err := store.GetRecentDiffs(ctx, RecentDiffsInput{Since: since})
	if err != nil {
		t.Fatalf("GetRecentDiffs: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs since mid-2025, got %d", len(diffs))
	}

	// Verify ordering is DESC (most recent first)
	if diffs[0].LinesAdded != 30 {
		t.Errorf("first diff LinesAdded = %d, want 30 (most recent)", diffs[0].LinesAdded)
	}

	// Query with file filter
	otherFileID, _, _ := insertTestFileChunkSymbol(t, store, "other.go", "OtherFunc")
	diffs, err = store.GetRecentDiffs(ctx, RecentDiffsInput{
		Since:  since,
		FileID: otherFileID,
	})
	if err != nil {
		t.Fatalf("GetRecentDiffs with file filter: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for other file, got %d", len(diffs))
	}

	// Query with limit
	since2, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	diffs, err = store.GetRecentDiffs(ctx, RecentDiffsInput{Since: since2, Limit: 1})
	if err != nil {
		t.Fatalf("GetRecentDiffs with limit: %v", err)
	}
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff with limit, got %d", len(diffs))
	}
}

func TestComputeChangeScores(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, chunkID, symID := insertTestFileChunkSymbol(t, store, "score.go", "ScoreFunc")

	// Insert a diff log entry with a recent timestamp and a diff symbol pointing to the chunk
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
			VALUES (?, ?, 'modify', 20, 5)`, fileID, now)
		if err != nil {
			return err
		}
		diffID, _ := res.LastInsertId()

		_, err = tx.ExecContext(ctx, `
			INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
			VALUES (?, ?, 'ScoreFunc', 'modified', ?)`, diffID, symID, chunkID)
		return err
	})
	if err != nil {
		t.Fatalf("insert diff data: %v", err)
	}

	scores, err := store.ComputeChangeScores(ctx, []int64{chunkID})
	if err != nil {
		t.Fatalf("ComputeChangeScores: %v", err)
	}

	score, ok := scores[chunkID]
	if !ok {
		t.Fatal("expected score for chunk, got none")
	}
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
	// With 25 total lines changed and ~0 hours elapsed:
	// score = exp(-0.05 * ~0) * min(25/50, 1.0) = ~1.0 * 0.5 = ~0.5
	if score < 0.4 || score > 0.6 {
		t.Errorf("expected score ~0.5 for 25 lines changed recently, got %f", score)
	}

	// Chunk with no diffs should have no score
	scores, err = store.ComputeChangeScores(ctx, []int64{999999})
	if err != nil {
		t.Fatalf("ComputeChangeScores for unknown chunk: %v", err)
	}
	if _, ok := scores[999999]; ok {
		t.Error("expected no score for unknown chunk")
	}

	// Empty input
	scores, err = store.ComputeChangeScores(ctx, []int64{})
	if err != nil {
		t.Fatalf("ComputeChangeScores empty: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("expected empty scores map, got %d entries", len(scores))
	}
}

func TestComputeChangeScores_InvalidTimestamp(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, chunkID, symID := insertTestFileChunkSymbol(t, store, "badts.go", "BadTsFunc")

	// Insert a diff with an invalid (unparseable) timestamp to exercise scoreRow returning 0.
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
			VALUES (?, 'not-a-timestamp', 'modify', 20, 5)`, fileID)
		if err != nil {
			return err
		}
		diffID, _ := res.LastInsertId()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
			VALUES (?, ?, 'BadTsFunc', 'modified', ?)`, diffID, symID, chunkID)
		return err
	})
	if err != nil {
		t.Fatalf("insert diff data: %v", err)
	}

	// scoreRow should return 0 for the invalid timestamp, so no score assigned.
	scores, err := store.ComputeChangeScores(ctx, []int64{chunkID})
	if err != nil {
		t.Fatalf("ComputeChangeScores: %v", err)
	}
	if _, ok := scores[chunkID]; ok {
		t.Error("expected no score for chunk with invalid timestamp")
	}
}

func TestComputeChangeScores_FileLevelFallback(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Create a file with a chunk but no symbol-level diff_symbols entry.
	// This exercises the file-level fallback path (Query 2).
	fileID, chunkID, _ := insertTestFileChunkSymbol(t, store, "fallback.go", "FallbackFunc")

	// Insert a diff log entry for the file but do NOT insert diff_symbols for the chunk.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
			VALUES (?, ?, 'modify', 40, 10)`, fileID, now)
		return err
	})
	if err != nil {
		t.Fatalf("insert diff data: %v", err)
	}

	scores, err := store.ComputeChangeScores(ctx, []int64{chunkID})
	if err != nil {
		t.Fatalf("ComputeChangeScores: %v", err)
	}

	score, ok := scores[chunkID]
	if !ok {
		t.Fatal("expected score for chunk via file-level fallback, got none")
	}
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
	// With 50 total lines changed and ~0 hours elapsed:
	// score = exp(-0.05 * ~0) * min(50/50, 1.0) = ~1.0 * 1.0 = ~1.0
	if score < 0.9 || score > 1.1 {
		t.Errorf("expected score ~1.0 for 50 lines changed recently, got %f", score)
	}
}

func TestInsertDiffLog_Direct(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, _ := insertTestFileChunkSymbol(t, store, "direct_diff.go", "DirectFunc")

	entry := DiffLogEntry{
		FileID:       fileID,
		ChangeType:   "add",
		LinesAdded:   42,
		LinesRemoved: 0,
		HashBefore:   "",
		HashAfter:    "newfile",
	}

	var diffID int64
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		var insertErr error
		diffID, insertErr = store.InsertDiffLog(ctx, tx, entry)
		return insertErr
	})
	if err != nil {
		t.Fatalf("InsertDiffLog: %v", err)
	}
	if diffID <= 0 {
		t.Fatalf("expected positive diff ID, got %d", diffID)
	}

	// Insert a second diff and verify IDs are sequential.
	entry2 := DiffLogEntry{
		FileID:       fileID,
		ChangeType:   "delete",
		LinesAdded:   0,
		LinesRemoved: 20,
		HashBefore:   "old",
		HashAfter:    "",
	}
	var diffID2 int64
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		var insertErr error
		diffID2, insertErr = store.InsertDiffLog(ctx, tx, entry2)
		return insertErr
	})
	if err != nil {
		t.Fatalf("InsertDiffLog second: %v", err)
	}
	if diffID2 <= diffID {
		t.Errorf("expected second diff ID %d > first %d", diffID2, diffID)
	}

	// Verify both diffs are retrievable.
	diffs, err := store.GetRecentDiffs(ctx, RecentDiffsInput{
		Since: time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("GetRecentDiffs: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}
}

func TestGetDiffSymbols_Empty(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	fileID, _, _ := insertTestFileChunkSymbol(t, store, "nosyms.go", "NoSymsFunc")

	// Insert a diff log entry but no diff symbols.
	var diffID int64
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		var insertErr error
		diffID, insertErr = store.InsertDiffLog(ctx, tx, DiffLogEntry{
			FileID:     fileID,
			ChangeType: "modify",
			LinesAdded: 1,
		})
		return insertErr
	})
	if err != nil {
		t.Fatalf("InsertDiffLog: %v", err)
	}

	symbols, err := store.GetDiffSymbols(ctx, diffID)
	if err != nil {
		t.Fatalf("GetDiffSymbols: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected 0 diff symbols, got %d", len(symbols))
	}

	// Also query a completely nonexistent diff ID.
	symbols, err = store.GetDiffSymbols(ctx, 999999)
	if err != nil {
		t.Fatalf("GetDiffSymbols(nonexistent): %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected 0 diff symbols for nonexistent diff, got %d", len(symbols))
	}
}

func TestComputeChangeScores_MixedSymbolAndFileFallback(t *testing.T) {
	t.Parallel()
	db, store := setupTestDB(t)
	ctx := context.Background()

	// Chunk A: has a symbol-level diff (should use symbol path).
	fileA, chunkA, symA := insertTestFileChunkSymbol(t, store, "sym_score.go", "SymScore")

	// Chunk B: has only a file-level diff (should use file-level fallback).
	fileB, chunkB, _ := insertTestFileChunkSymbol(t, store, "file_score.go", "FileScore")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := db.WithWriteTx(func(tx *sql.Tx) error {
		// Diff for file A with symbol-level entry.
		res, err := tx.ExecContext(ctx, `
			INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
			VALUES (?, ?, 'modify', 30, 10)`, fileA, now)
		if err != nil {
			return err
		}
		diffID, _ := res.LastInsertId()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO diff_symbols (diff_id, symbol_id, symbol_name, change_type, chunk_id)
			VALUES (?, ?, 'SymScore', 'modified', ?)`, diffID, symA, chunkA)
		if err != nil {
			return err
		}

		// Diff for file B, no symbol-level entry.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO diff_log (file_id, timestamp, change_type, lines_added, lines_removed)
			VALUES (?, ?, 'modify', 20, 5)`, fileB, now)
		return err
	})
	if err != nil {
		t.Fatalf("insert diff data: %v", err)
	}

	scores, err := store.ComputeChangeScores(ctx, []int64{chunkA, chunkB})
	if err != nil {
		t.Fatalf("ComputeChangeScores: %v", err)
	}

	// Both chunks should have scores.
	if _, ok := scores[chunkA]; !ok {
		t.Error("expected score for chunkA (symbol path)")
	}
	if _, ok := scores[chunkB]; !ok {
		t.Error("expected score for chunkB (file-level fallback)")
	}

	// chunkA: 40 lines -> min(40/50, 1.0) = 0.8
	if s := scores[chunkA]; s < 0.7 || s > 0.9 {
		t.Errorf("chunkA score = %f, want ~0.8", s)
	}
	// chunkB: 25 lines -> min(25/50, 1.0) = 0.5
	if s := scores[chunkB]; s < 0.4 || s > 0.6 {
		t.Errorf("chunkB score = %f, want ~0.5", s)
	}
}
