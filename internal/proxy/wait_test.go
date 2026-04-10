package proxy

import (
	"net"
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
