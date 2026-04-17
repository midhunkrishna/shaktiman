// Package proxy implements a stateless stdio-to-HTTP bridge for shaktimand
// proxy mode. When a second daemon starts on the same project, it becomes
// a proxy that bridges its Claude Code client's stdin/stdout to the leader
// daemon's StreamableHTTPServer over a Unix domain socket.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
)

// ErrLeaderGone indicates the leader daemon has exited or is unreachable.
var ErrLeaderGone = errors.New("leader daemon is no longer available")

// maxMessageSize is the maximum JSON-RPC message size (1 MB).
const maxMessageSize = 1 << 20

// Bridge connects a Claude Code client (via Stdin/Stdout) to a leader daemon
// (via Unix domain socket HTTP). It reads JSON-RPC lines from Stdin, POSTs
// them to the leader's /mcp endpoint, and writes responses to Stdout.
type Bridge struct {
	SocketPath string
	Stdin      io.Reader
	Stdout     io.Writer
	Logger     *slog.Logger
}

// Run bridges JSON-RPC between Stdin/Stdout and the leader's HTTP endpoint.
// Returns nil on Stdin EOF (clean exit).
// Returns ErrLeaderGone when the leader's socket becomes unreachable.
func (b *Bridge) Run(ctx context.Context) error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", b.SocketPath)
			},
		},
	}

	scanner := bufio.NewScanner(b.Stdin)
	scanner.Buffer(make([]byte, 0, maxMessageSize), maxMessageSize)

	var sessionID string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/mcp", bytes.NewReader(line))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}

		resp, err := client.Do(req)
		if err != nil {
			if isConnectionRefused(err) {
				return ErrLeaderGone
			}
			if ctx.Err() != nil {
				return nil // context cancelled = clean shutdown
			}
			return fmt.Errorf("proxy request: %w", err)
		}

		// Capture session ID from the first response.
		if sessionID == "" {
			if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
				sessionID = id
			}
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		// Write response followed by newline (JSON-RPC line framing).
		body = bytes.TrimRight(body, "\r\n")
		if _, err := fmt.Fprintf(b.Stdout, "%s\n", body); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin scan: %w", err)
	}

	// Stdin closed (EOF) — clean exit.
	return nil
}

// isConnectionRefused returns true if the error indicates the Unix socket
// is not available (leader has exited).
func isConnectionRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if errors.Is(err, syscall.ENOENT) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}
