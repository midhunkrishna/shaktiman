package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/storage/embedstatus"
)

// sqliteChunkOps adapts a *sql.Tx to embedstatus.ChunkOps so the
// dialect-agnostic state machine can drive sqlite. The struct holds the
// outer context so the ChunkOps methods do not need to thread it through
// every loop body — they are always called from inside the same call
// site, so passing two ctxs around buys nothing.
type sqliteChunkOps struct {
	tx *sql.Tx
}

func newSQLiteChunkOps(tx *sql.Tx) *sqliteChunkOps {
	return &sqliteChunkOps{tx: tx}
}

// Compile-time assertion: *sqliteChunkOps satisfies the shared interface.
var _ embedstatus.ChunkOps = (*sqliteChunkOps)(nil)

func (o *sqliteChunkOps) SetEmbedded(ctx context.Context, chunkIDs []int64, embedded bool) error {
	flag := 0
	if embedded {
		flag = 1
	}
	return forEachChunkBatch(chunkIDs, func(ph string, args []any, _ []int64) error {
		if _, err := o.tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE chunks SET embedded = %d WHERE id IN (%s)", flag, ph),
			args...); err != nil {
			return fmt.Errorf("set embedded=%d: %w", flag, err)
		}
		return nil
	})
}

func (o *sqliteChunkOps) AffectedFiles(ctx context.Context, chunkIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(chunkIDs))
	var out []int64
	err := forEachChunkBatch(chunkIDs, func(ph string, args []any, _ []int64) error {
		ids, err := affectedFileIDs(ctx, o.tx, ph, args)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (o *sqliteChunkOps) CountRemaining(ctx context.Context, fileID int64) (int, error) {
	var n int
	err := o.tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM chunks WHERE file_id = ? AND embedded = 0", fileID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count remaining for file %d: %w", fileID, err)
	}
	return n, nil
}

func (o *sqliteChunkOps) CountEmbedded(ctx context.Context, fileID int64) (int, error) {
	var n int
	err := o.tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM chunks WHERE file_id = ? AND embedded = 1", fileID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count embedded for file %d: %w", fileID, err)
	}
	return n, nil
}

func (o *sqliteChunkOps) SetFileStatus(ctx context.Context, fileID int64, status string, keepCompleteOnly bool) error {
	if keepCompleteOnly {
		if _, err := o.tx.ExecContext(ctx,
			"UPDATE files SET embedding_status = ? WHERE id = ? AND embedding_status != 'complete'",
			status, fileID); err != nil {
			return fmt.Errorf("update file %d embedding status: %w", fileID, err)
		}
		return nil
	}
	if _, err := o.tx.ExecContext(ctx,
		"UPDATE files SET embedding_status = ? WHERE id = ?",
		status, fileID); err != nil {
		return fmt.Errorf("update file %d embedding status: %w", fileID, err)
	}
	return nil
}

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
