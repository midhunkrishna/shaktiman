package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadyMarkerPath(t *testing.T) {
	t.Parallel()

	got := ReadyMarkerPath("/tmp/proj")
	want := filepath.Join("/tmp/proj", ".shaktiman", "ready")
	if got != want {
		t.Fatalf("ReadyMarkerPath = %q, want %q", got, want)
	}
}

func TestWriteReadyMarker_Atomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".shaktiman", "ready")

	if err := writeReadyMarker(path, "session-abc"); err != nil {
		t.Fatalf("writeReadyMarker: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	contents := string(data)
	if !strings.Contains(contents, "session_id=session-abc") {
		t.Errorf("marker missing session_id, got: %q", contents)
	}
	if !strings.Contains(contents, "pid=") {
		t.Errorf("marker missing pid, got: %q", contents)
	}
	if !strings.Contains(contents, "started=") {
		t.Errorf("marker missing started timestamp, got: %q", contents)
	}

	// No leftover .ready-* tempfile in the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ready-") {
			t.Fatalf("leftover tempfile: %s", e.Name())
		}
	}
}

func TestWriteReadyMarker_Overwrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".shaktiman", "ready")

	if err := writeReadyMarker(path, "first"); err != nil {
		t.Fatalf("first writeReadyMarker: %v", err)
	}
	if err := writeReadyMarker(path, "second"); err != nil {
		t.Fatalf("second writeReadyMarker: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if !strings.Contains(string(data), "session_id=second") {
		t.Errorf("marker not overwritten, got: %q", data)
	}
}
