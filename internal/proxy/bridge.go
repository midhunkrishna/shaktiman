// Package proxy implements a stateless stdio-to-HTTP bridge for shaktimand
// proxy mode. When a second daemon starts on the same project, it becomes
// a proxy that bridges its Claude Code client's stdin/stdout to the leader
// daemon's StreamableHTTPServer over a Unix domain socket.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrLeaderGone indicates the leader daemon has exited or is unreachable.
var ErrLeaderGone = errors.New("leader daemon is no longer available")

// requestTimeout bounds a single MCP request round-trip. Generous enough
// for slow tool calls (large search/context) yet short enough that a
// genuinely wedged leader is detected instead of hanging the client
// forever.
const requestTimeout = 5 * time.Minute

// responseHeaderTimeout bounds how long we wait for the leader to begin
// responding. Catches a leader that accepted the connection but is stuck
// before producing headers.
const responseHeaderTimeout = 30 * time.Second

// Bridge connects a Claude Code client (via Stdin/Stdout) to a leader daemon
// (via Unix domain socket HTTP). It reads JSON-RPC messages from Stdin, POSTs
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
			ResponseHeaderTimeout: responseHeaderTimeout,
		},
	}

	// json.Decoder handles arbitrary message size and whitespace framing
	// without the 1 MiB cap that bufio.Scanner imposed (which silently
	// killed the bridge on large tool-call payloads).
	decoder := json.NewDecoder(b.Stdin)

	var sessionID string

	for {
		if ctx.Err() != nil {
			return nil
		}

		var msg json.RawMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("stdin decode: %w", err)
		}

		body, newSessionID, err := b.forward(ctx, client, msg, sessionID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if newSessionID != "" {
			sessionID = newSessionID
		}

		body = bytes.TrimRight(body, "\r\n")
		if _, err := fmt.Fprintf(b.Stdout, "%s\n", body); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
}

// forward sends one JSON-RPC message to the leader and returns the
// response body plus any Mcp-Session-Id header value the server sent.
// A per-request timeout bounds each round-trip independently of the
// outer context.
func (b *Bridge) forward(ctx context.Context, client *http.Client, msg json.RawMessage, sessionID string) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://unix/mcp", bytes.NewReader(msg))
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := client.Do(req)
	if err != nil {
		if isConnectionRefused(err) {
			return nil, "", ErrLeaderGone
		}
		if ctx.Err() != nil {
			return nil, "", nil //nolint:nilnil // signaled via outer ctx; caller treats as clean shutdown
		}
		return nil, "", fmt.Errorf("proxy request: %w", err)
	}

	// Refresh on every response — the leader may rotate session IDs (e.g.
	// after re-initialize) and the previous bridge only captured the
	// first.
	newSessionID := resp.Header.Get("Mcp-Session-Id")

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	return body, newSessionID, nil
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
