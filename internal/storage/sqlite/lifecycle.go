package sqlite

import (
	"context"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Lifecycle adapts SQLite FTS5 trigger management into the
// generic StoreLifecycle interface. Postgres returns nil for lifecycle
// since generated tsvector columns need no manual management.
type Lifecycle struct {
	store *Store
}

// Compile-time check.
var _ types.StoreLifecycle = (*Lifecycle)(nil)

// NewLifecycle creates a StoreLifecycle for the SQLite backend.
func NewLifecycle(store *Store) *Lifecycle {
	return &Lifecycle{store: store}
}

// OnStartup performs crash recovery: ensures FTS triggers exist and
// rebuilds the FTS index if it is stale (mismatched row count).
func (l *Lifecycle) OnStartup(ctx context.Context) error {
	if err := l.store.EnsureFTSTriggers(ctx); err != nil {
		return err
	}
	stale, err := l.store.IsFTSStale(ctx)
	if err != nil {
		return err
	}
	if stale {
		return l.store.RebuildFTS(ctx)
	}
	return nil
}

// OnBulkWriteBegin disables FTS triggers for bulk insert performance.
func (l *Lifecycle) OnBulkWriteBegin(ctx context.Context) error {
	return l.store.DisableFTSTriggers(ctx)
}

// OnBulkWriteEnd rebuilds the FTS index and re-enables triggers.
func (l *Lifecycle) OnBulkWriteEnd(ctx context.Context) error {
	if err := l.store.RebuildFTS(ctx); err != nil {
		return err
	}
	return l.store.EnableFTSTriggers(ctx)
}
