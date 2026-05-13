// Package embedstatus owns the chunk→file embedding-status state machine.
//
// Every metadata backend that supports embedding marks individual chunks
// as embedded and recomputes the parent file's status from the per-chunk
// truth. The dialect-specific SQL belongs to each backend; the rules
// (`complete` when no chunk remains, `partial` when some are embedded,
// `pending` when none are, `complete` is preserved on partial marks) are
// universal. Pulling those rules into one place lets sqlite and postgres
// each supply a thin ChunkOps adapter and share the actual state
// machine — eliminating the divergent copy the postgres backend carried.
package embedstatus

import "context"

// ChunkOps abstracts the per-statement SQL the embed-status state machine
// needs from a backend. Each dialect provides an adapter bound to the
// in-progress write transaction; the state machine itself does not know
// about *sql.Tx or pgx.Tx. Implementations may batch large chunkIDs
// internally — SQLite must, to stay below SQLITE_MAX_VARIABLE_NUMBER.
type ChunkOps interface {
	// SetEmbedded sets chunks.embedded to 1 (when embedded=true) or to 0
	// (when embedded=false) for every id in chunkIDs.
	SetEmbedded(ctx context.Context, chunkIDs []int64, embedded bool) error
	// AffectedFiles returns the distinct file_id set referenced by
	// chunkIDs. Order is irrelevant; duplicates must be removed.
	AffectedFiles(ctx context.Context, chunkIDs []int64) ([]int64, error)
	// CountRemaining returns the number of chunks in fileID whose
	// embedded flag is 0.
	CountRemaining(ctx context.Context, fileID int64) (int, error)
	// CountEmbedded returns the number of chunks in fileID whose
	// embedded flag is 1.
	CountEmbedded(ctx context.Context, fileID int64) (int, error)
	// SetFileStatus updates files.embedding_status for fileID. When
	// keepCompleteOnly is true the update must NOT overwrite an existing
	// 'complete' value — this preserves the invariant that once a file
	// is fully embedded, a later partial mark on a subset of its chunks
	// cannot regress it.
	SetFileStatus(ctx context.Context, fileID int64, status string, keepCompleteOnly bool) error
}

// MarkEmbedded marks chunkIDs as embedded=1 and recomputes each affected
// file's embedding_status: 'complete' when no chunks remain unembedded,
// 'partial' otherwise (preserving an existing 'complete' status — see
// ChunkOps.SetFileStatus).
func MarkEmbedded(ctx context.Context, ops ChunkOps, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	if err := ops.SetEmbedded(ctx, chunkIDs, true); err != nil {
		return err
	}
	fileIDs, err := ops.AffectedFiles(ctx, chunkIDs)
	if err != nil {
		return err
	}
	for _, fid := range fileIDs {
		remaining, err := ops.CountRemaining(ctx, fid)
		if err != nil {
			return err
		}
		if remaining == 0 {
			if err := ops.SetFileStatus(ctx, fid, "complete", false); err != nil {
				return err
			}
			continue
		}
		if err := ops.SetFileStatus(ctx, fid, "partial", true); err != nil {
			return err
		}
	}
	return nil
}

// Reset marks chunkIDs as embedded=0 and recomputes each affected file's
// embedding_status: 'partial' when some chunks remain embedded, 'pending'
// otherwise. Unlike MarkEmbedded the partial transition here is
// unconditional — Reset is used by reconciliation paths that should drop
// stale 'complete' markers.
func Reset(ctx context.Context, ops ChunkOps, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	if err := ops.SetEmbedded(ctx, chunkIDs, false); err != nil {
		return err
	}
	fileIDs, err := ops.AffectedFiles(ctx, chunkIDs)
	if err != nil {
		return err
	}
	for _, fid := range fileIDs {
		embedded, err := ops.CountEmbedded(ctx, fid)
		if err != nil {
			return err
		}
		status := "pending"
		if embedded > 0 {
			status = "partial"
		}
		if err := ops.SetFileStatus(ctx, fid, status, false); err != nil {
			return err
		}
	}
	return nil
}
