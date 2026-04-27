package proxy

import (
	"fmt"
	"net"
	"os"
	"time"
)

// waitBackoffs is the shared exponential progression used by both
// WaitForSocket and WaitForReady to space their poll attempts.
var waitBackoffs = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
}

// WaitForSocket waits for a Unix domain socket to become connectable,
// using exponential backoff. Returns nil when the socket accepts a connection,
// or an error if the timeout is reached.
func WaitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for i := 0; ; i++ {
		conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("socket %s not available after %v", path, timeout)
		}
		time.Sleep(backoffAt(i))
	}
}

// WaitForReady waits until both (a) the leader has written its readiness
// marker file and (b) its Unix socket is dialable. The marker is the
// authoritative signal that the leader's MCP HTTP server is wired up
// and serving — a bare socket connect can otherwise succeed against the
// kernel listen backlog before Serve is actually accepting.
//
// Returns nil on success, or a descriptive error after the timeout.
func WaitForReady(socketPath, markerPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for i := 0; ; i++ {
		if _, err := os.Stat(markerPath); err == nil {
			conn, dialErr := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("leader not ready after %v (marker=%s socket=%s)",
				timeout, markerPath, socketPath)
		}
		time.Sleep(backoffAt(i))
	}
}

func backoffAt(i int) time.Duration {
	if i >= len(waitBackoffs) {
		return waitBackoffs[len(waitBackoffs)-1]
	}
	return waitBackoffs[i]
}
