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
