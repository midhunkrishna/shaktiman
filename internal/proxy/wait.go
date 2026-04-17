package proxy

import (
	"fmt"
	"net"
	"time"
)

// WaitForSocket waits for a Unix domain socket to become connectable,
// using exponential backoff. Returns nil when the socket accepts a connection,
// or an error if the timeout is reached.
func WaitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoffs := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
	}
	for i := 0; ; i++ {
		conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("socket %s not available after %v", path, timeout)
		}
		idx := i
		if idx >= len(backoffs) {
			idx = len(backoffs) - 1
		}
		time.Sleep(backoffs[idx])
	}
}
