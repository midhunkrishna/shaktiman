package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileChangeEvent represents a debounced file change.
type FileChangeEvent struct {
	Path       string // project-relative path
	AbsPath    string
	ChangeType string // add | modify | delete
	Timestamp  time.Time
}

// Watcher monitors the project directory for file changes using fsnotify.
// It watches directories (not individual files) to conserve FDs (IP-16).
type Watcher struct {
	fsw            *fsnotify.Watcher
	root           string
	debounceMs     int
	eventCh        chan FileChangeEvent
	branchSwitchCh chan struct{} // capacity 1, non-blocking signal on bulk changes
	pending        map[string]time.Time
	mu             sync.Mutex
	logger         *slog.Logger
	dropCount      atomic.Int64
}

// NewWatcher creates a file watcher for the given project root.
func NewWatcher(root string, debounceMs int) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		fsw:            fsw,
		root:           root,
		debounceMs:     debounceMs,
		eventCh:        make(chan FileChangeEvent, 100),
		branchSwitchCh: make(chan struct{}, 1),
		pending:        make(map[string]time.Time),
		logger:         slog.Default().With("component", "watcher"),
	}, nil
}

// Events returns the channel of debounced file change events.
func (w *Watcher) Events() <-chan FileChangeEvent {
	return w.eventCh
}

// BranchSwitchCh returns a channel that signals when a bulk file change
// (likely branch switch) is detected: >20 source files in one flush cycle.
func (w *Watcher) BranchSwitchCh() <-chan struct{} {
	return w.branchSwitchCh
}

// Start begins watching and emitting debounced events. Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	// Add directory watches recursively
	if err := w.addDirRecursive(w.root); err != nil {
		return err
	}

	// Start debounce flusher
	go w.flushLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			w.fsw.Close()
			close(w.eventCh)
			return nil

		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(event)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			w.logger.Warn("fsnotify error", "err", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := event.Name

	// If a new directory was created, watch it
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			name := filepath.Base(path)
			if !skipDirs[name] && !strings.HasPrefix(name, ".") {
				_ = w.fsw.Add(path)
			}
			return
		}
	}

	// Only process source files
	ext := strings.ToLower(filepath.Ext(path))
	if _, supported := LanguageForExt(ext); !supported {
		return
	}

	// Resolve symlinks and verify inside project root (TOCTOU protection)
	absPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return
	}
	absRoot, _ := filepath.EvalSymlinks(w.root)
	if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		w.logger.Debug("symlink outside project, ignoring", "path", path)
		return
	}

	// Debounce: record/update the pending timestamp using resolved path
	w.mu.Lock()
	w.pending[absPath] = time.Now()
	w.mu.Unlock()
}

func (w *Watcher) flushLoop(ctx context.Context) {
	debounce := time.Duration(w.debounceMs) * time.Millisecond
	ticker := time.NewTicker(debounce / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.flushPending(debounce)
		}
	}
}

func (w *Watcher) flushPending(debounce time.Duration) {
	w.mu.Lock()
	now := time.Now()
	var ready []string
	for path, ts := range w.pending {
		if now.Sub(ts) >= debounce {
			ready = append(ready, path)
		}
	}
	for _, path := range ready {
		delete(w.pending, path)
	}
	w.mu.Unlock()

	if len(ready) > 0 {
		w.logger.Debug("flush", "files", len(ready))
	}

	// Detect likely branch switch: >20 source files changed in one flush
	if len(ready) > 20 {
		select {
		case w.branchSwitchCh <- struct{}{}:
			w.logger.Info("branch switch detected", "changed_files", len(ready))
		default:
		}
	}

	for _, absPath := range ready {
		relPath, err := filepath.Rel(w.root, absPath)
		if err != nil {
			continue
		}

		changeType := "modify"
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			changeType = "delete"
		}

		w.logger.Debug("emit", "path", relPath, "type", changeType)
		select {
		case w.eventCh <- FileChangeEvent{
			Path:       relPath,
			AbsPath:    absPath,
			ChangeType: changeType,
			Timestamp:  time.Now(),
		}:
		case <-time.After(time.Second):
			w.dropCount.Add(1)
			if w.dropCount.Load()%10 == 1 {
				w.logger.Warn("watcher dropping events", "total_dropped", w.dropCount.Load())
			}
		}
	}
}

func (w *Watcher) addDirRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if skipDirs[name] || (strings.HasPrefix(name, ".") && name != "." && path != root) {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}
