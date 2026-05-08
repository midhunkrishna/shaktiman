package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// chunkBatchLimit caps each chunk-id batch fed into IN(...) clauses so
// we stay safely below SQLite's SQLITE_MAX_VARIABLE_NUMBER (default
// 999 in older builds, 32766 in newer; 500 is conservative for both).
const chunkBatchLimit = 500

// forEachChunkBatch slices chunkIDs into batches of at most
// chunkBatchLimit and calls fn with the comma-joined placeholder
// string and matching args. Both ResetEmbeddedFlags and
// MarkChunksEmbedded used to inline this loop verbatim — extracting
// it means a future bump to chunkBatchLimit (or a switch to JSON
// arrays) lands in one place.
func forEachChunkBatch(chunkIDs []int64, fn func(ph string, args []any, batch []int64) error) error {
	for start := 0; start < len(chunkIDs); start += chunkBatchLimit {
		end := start + chunkBatchLimit
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		batch := chunkIDs[start:end]

		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		if err := fn(strings.Join(placeholders, ","), args, batch); err != nil {
			return err
		}
	}
	return nil
}

// affectedFileIDs returns the distinct file_id set for the chunks
// referenced by the placeholder string. Both embed-status writers
// hit this query identically.
func affectedFileIDs(ctx context.Context, tx *sql.Tx, ph string, args []any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		fmt.Sprintf("SELECT DISTINCT file_id FROM chunks WHERE id IN (%s)", ph),
		args...)
	if err != nil {
		return nil, fmt.Errorf("get affected file IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var fileIDs []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return nil, fmt.Errorf("scan file ID: %w", err)
		}
		fileIDs = append(fileIDs, fid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file IDs: %w", err)
	}
	return fileIDs, nil
}
