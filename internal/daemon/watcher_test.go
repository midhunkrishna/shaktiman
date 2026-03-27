package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestNewWatcher(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	// Clean up fsnotify watcher
	w.fsw.Close()
}

func TestWatcher_Accessors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	if w.Events() == nil {
		t.Error("Events() returned nil channel")
	}
	if w.BranchSwitchCh() == nil {
		t.Error("BranchSwitchCh() returned nil channel")
	}
}

func TestFlushPending_EmitsEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create a real source file
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	// Manually populate pending with stale timestamp
	w.mu.Lock()
	w.pending[goFile] = time.Now().Add(-time.Second)
	w.mu.Unlock()

	// Flush with zero debounce → all entries are "ready"
	w.flushPending(0)

	// Read emitted event
	select {
	case event := <-w.eventCh:
		if event.ChangeType != "modify" {
			t.Errorf("expected 'modify', got %q", event.ChangeType)
		}
		if event.AbsPath != goFile {
			t.Errorf("expected AbsPath %q, got %q", goFile, event.AbsPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event from flushPending")
	}
}

func TestFlushPending_DeletedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	deletedFile := filepath.Join(dir, "deleted.go")
	// Don't create the file — it's "deleted"

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	w.mu.Lock()
	w.pending[deletedFile] = time.Now().Add(-time.Second)
	w.mu.Unlock()

	w.flushPending(0)

	select {
	case event := <-w.eventCh:
		if event.ChangeType != "delete" {
			t.Errorf("expected 'delete' for missing file, got %q", event.ChangeType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delete event")
	}
}

func TestFlushPending_BranchSwitch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create >20 source files
	for i := 0; i < 25; i++ {
		f := filepath.Join(dir, filepath.Base(dir), fmt.Sprintf("file%d.go", i))
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("package main\n"), 0o644)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	// Populate pending with >20 files
	w.mu.Lock()
	for i := 0; i < 25; i++ {
		f := filepath.Join(dir, filepath.Base(dir), fmt.Sprintf("file%d.go", i))
		w.pending[f] = time.Now().Add(-time.Second)
	}
	w.mu.Unlock()

	w.flushPending(0)

	// Should signal branch switch
	select {
	case <-w.branchSwitchCh:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("expected branch switch signal for >20 files")
	}
}

func TestAddDirRecursive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create nested structure with a skip dir
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "lodash"), 0o755)

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	if err := w.addDirRecursive(dir); err != nil {
		t.Fatalf("addDirRecursive: %v", err)
	}
	// If it didn't error, the test passes — we're mainly checking it
	// doesn't panic or fail on skip dirs.
}

func TestWatcher_StartAndCancel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w, err := NewWatcher(dir, 50)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Start(ctx)
	}()

	// Give Start time to set up watches
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not exit after context cancellation")
	}

	// eventCh should be closed
	_, ok := <-w.eventCh
	if ok {
		t.Error("eventCh should be closed after Start returns")
	}
}

func TestHandleEvent_SourceFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	w.handleEvent(fsnotifyEvent(goFile, fsnotify.Write))

	// handleEvent resolves symlinks before storing in pending,
	// so look up the resolved path (macOS: /tmp → /private/tmp).
	absGoFile, err := filepath.EvalSymlinks(goFile)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	w.mu.Lock()
	_, found := w.pending[absGoFile]
	w.mu.Unlock()

	if !found {
		t.Error("expected file in pending after handleEvent")
	}
}

func TestHandleEvent_IgnoredFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logFile := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logFile, []byte("log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	w.handleEvent(fsnotifyEvent(logFile, fsnotify.Write))

	w.mu.Lock()
	count := len(w.pending)
	w.mu.Unlock()

	if count != 0 {
		t.Errorf("expected empty pending for .log file, got %d", count)
	}
}

func TestHandleEvent_CreateDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subDir := filepath.Join(dir, "newpkg")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	// Create event on a directory — should add it to fsw watches
	w.handleEvent(fsnotifyEvent(subDir, fsnotify.Create))

	// pending should be empty (directories are not added to pending)
	w.mu.Lock()
	count := len(w.pending)
	w.mu.Unlock()

	if count != 0 {
		t.Errorf("expected empty pending for directory Create event, got %d", count)
	}
}

func TestHandleEvent_RemoveEvent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	goFile := filepath.Join(dir, "removed.go")
	// Create the file so EvalSymlinks works
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	w.handleEvent(fsnotifyEvent(goFile, fsnotify.Remove))

	absGoFile, _ := filepath.EvalSymlinks(goFile)

	w.mu.Lock()
	_, found := w.pending[absGoFile]
	w.mu.Unlock()

	if !found {
		t.Error("expected file in pending after Remove event")
	}
}

func TestFlushPending_NotReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	goFile := filepath.Join(dir, "fresh.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	// Populate pending with a FUTURE timestamp (not yet ready for flush)
	w.mu.Lock()
	w.pending[goFile] = time.Now().Add(time.Hour)
	w.mu.Unlock()

	// Flush with a normal debounce — entry shouldn't be flushed
	w.flushPending(100 * time.Millisecond)

	// Should still be in pending (not flushed)
	w.mu.Lock()
	_, found := w.pending[goFile]
	w.mu.Unlock()

	if !found {
		t.Error("expected file to remain in pending (not yet ready)")
	}
}

// fsnotifyEvent creates an fsnotify.Event for testing.
func fsnotifyEvent(name string, op fsnotify.Op) fsnotify.Event {
	return fsnotify.Event{Name: name, Op: op}
}
