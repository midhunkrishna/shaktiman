package proxy

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testSocketPath is defined in bridge_test.go and shared across all test files.

func TestWaitForSocket_Timeout(t *testing.T) {
	t.Parallel()
	path := testSocketPath(t) // nonexistent after helper removes it
	err := WaitForSocket(path, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWaitForSocket_ImmediateSuccess(t *testing.T) {
	t.Parallel()
	path := testSocketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	if err := WaitForSocket(path, 2*time.Second); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestWaitForSocket_EventualSuccess(t *testing.T) {
	t.Parallel()
	path := testSocketPath(t)

	// Start listening after a delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		ln, err := net.Listen("unix", path)
		if err != nil {
			return
		}
		defer ln.Close()
		time.Sleep(5 * time.Second)
	}()

	if err := WaitForSocket(path, 3*time.Second); err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
}

func TestWaitForSocket_BackoffProgression(t *testing.T) {
	t.Parallel()
	path := testSocketPath(t)

	start := time.Now()
	_ = WaitForSocket(path, 150*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected at least 100ms elapsed, got %v", elapsed)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("took too long: %v", elapsed)
	}
}

// TestWaitForReady_RequiresMarker verifies that WaitForReady will not
// return success while the readiness marker is absent, even if the
// socket itself is dialable. This is the core invariant: a bare
// listener is not a ready leader.
func TestWaitForReady_RequiresMarker(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	markerPath := filepath.Join(t.TempDir(), "ready")
	// Marker missing → must time out despite socket being dialable.
	if err := WaitForReady(sockPath, markerPath, 250*time.Millisecond); err == nil {
		t.Fatal("expected timeout when marker absent")
	}
}

// TestWaitForReady_Success verifies the happy path: marker present and
// socket dialable.
func TestWaitForReady_Success(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	markerPath := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(markerPath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := WaitForReady(sockPath, markerPath, 2*time.Second); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestWaitForReady_EventualMarker verifies that WaitForReady polls and
// eventually succeeds once the marker shows up. Models the realistic
// startup path where the proxy connects before the leader's HTTP
// server has finished wiring up.
func TestWaitForReady_EventualMarker(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	markerPath := filepath.Join(t.TempDir(), "ready")
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(markerPath, []byte("ok"), 0o600)
	}()

	if err := WaitForReady(sockPath, markerPath, 3*time.Second); err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
}

// TestWaitForReady_SocketUnreachable verifies that a stale marker (left
// behind after a crash) does not falsely report ready when the socket
// is unreachable.
func TestWaitForReady_SocketUnreachable(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t) // helper removes the file
	markerPath := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(markerPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := WaitForReady(sockPath, markerPath, 250*time.Millisecond); err == nil {
		t.Fatal("expected timeout when socket unreachable")
	}
}
