package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shaktimanai/shaktiman/internal/storage/embedstatus"
)

// pgChunkOps adapts a pgx.Tx to embedstatus.ChunkOps so the shared
// state machine in internal/storage/embedstatus drives postgres the
// same way it drives sqlite. No batching is needed — Postgres takes the
// chunkID list as a single ANY($1) array parameter.
type pgChunkOps struct {
	tx pgx.Tx
}

func newPgChunkOps(tx pgx.Tx) *pgChunkOps { return &pgChunkOps{tx: tx} }

var _ embedstatus.ChunkOps = (*pgChunkOps)(nil)

func (o *pgChunkOps) SetEmbedded(ctx context.Context, chunkIDs []int64, embedded bool) error {
	flag := 0
	if embedded {
		flag = 1
	}
	if _, err := o.tx.Exec(ctx,
		fmt.Sprintf("UPDATE chunks SET embedded = %d WHERE id = ANY($1)", flag),
		chunkIDs); err != nil {
		return fmt.Errorf("set embedded=%d: %w", flag, err)
	}
	return nil
}

func (o *pgChunkOps) AffectedFiles(ctx context.Context, chunkIDs []int64) ([]int64, error) {
	rows, err := o.tx.Query(ctx,
		"SELECT DISTINCT file_id FROM chunks WHERE id = ANY($1)", chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("get affected file IDs: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan file id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (o *pgChunkOps) CountRemaining(ctx context.Context, fileID int64) (int, error) {
	var n int
	err := o.tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM chunks WHERE file_id = $1 AND embedded = 0",
		fileID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count remaining embeddings for file %d: %w", fileID, err)
	}
	return n, nil
}

func (o *pgChunkOps) CountEmbedded(ctx context.Context, fileID int64) (int, error) {
	var n int
	err := o.tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM chunks WHERE file_id = $1 AND embedded = 1",
		fileID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count embedded chunks for file %d: %w", fileID, err)
	}
	return n, nil
}

func (o *pgChunkOps) SetFileStatus(ctx context.Context, fileID int64, status string, keepCompleteOnly bool) error {
	if keepCompleteOnly {
		if _, err := o.tx.Exec(ctx,
			"UPDATE files SET embedding_status = $1 WHERE id = $2 AND embedding_status != 'complete'",
			status, fileID); err != nil {
			return fmt.Errorf("set embedding_status for file %d: %w", fileID, err)
		}
		return nil
	}
	if _, err := o.tx.Exec(ctx,
		"UPDATE files SET embedding_status = $1 WHERE id = $2",
		status, fileID); err != nil {
		return fmt.Errorf("set embedding_status for file %d: %w", fileID, err)
	}
	return nil
}
