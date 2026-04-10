package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// testSocketPath returns a short socket path suitable for macOS (104-byte limit).
func testSocketPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "shaktiman-proxy-test-*.sock")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	path := f.Name()
	f.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })
	return path
}

// startTestHTTPServer starts a simple HTTP server on a Unix socket that
// echoes back the request body wrapped in a JSON-RPC response.
func startTestHTTPServer(t *testing.T, sockPath string) (net.Listener, *http.Server) {
	t.Helper()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	var mu sync.Mutex
	var lastSessionID string

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()

		mu.Lock()
		// Track session ID for testing.
		if id := r.Header.Get("Mcp-Session-Id"); id != "" {
			lastSessionID = id
		}
		_ = lastSessionID
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "test-session-123")
		w.WriteHeader(http.StatusOK)
		w.Write(body) // echo back
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return ln, srv
}

func TestBridge_ForwardsRequest(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)
	defer ln.Close()
	defer srv.Close()

	// Pipe a JSON-RPC request to stdin.
	input := `{"jsonrpc":"2.0","id":1,"method":"test"}` + "\n"
	stdin := strings.NewReader(input)
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the response was written to stdout.
	output := strings.TrimSpace(stdout.String())
	if output != `{"jsonrpc":"2.0","id":1,"method":"test"}` {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestBridge_LeaderGone(t *testing.T) {
	t.Parallel()

	// Use a socket path that doesn't exist.
	sockPath := testSocketPath(t) // file removed by helper

	input := `{"jsonrpc":"2.0","id":1,"method":"test"}` + "\n"
	stdin := strings.NewReader(input)
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	err := b.Run(context.Background())
	if err != ErrLeaderGone {
		t.Fatalf("expected ErrLeaderGone, got: %v", err)
	}
}

func TestBridge_StdinEOF(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)
	defer ln.Close()
	defer srv.Close()

	// Empty stdin (immediate EOF).
	stdin := strings.NewReader("")
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil on EOF, got: %v", err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected no output, got: %q", stdout.String())
	}
}

func TestBridge_LargePayload(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)
	defer ln.Close()
	defer srv.Close()

	// 500KB JSON-RPC message.
	large := `{"jsonrpc":"2.0","id":1,"method":"test","params":{"data":"` +
		strings.Repeat("x", 500*1024) +
		`"}}` + "\n"
	stdin := strings.NewReader(large)
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stdout.Len() < 500*1024 {
		t.Fatalf("response too short (%d bytes)", stdout.Len())
	}
}

func TestBridge_ContextCancel(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)
	defer ln.Close()
	defer srv.Close()

	// Use a pipe so stdin blocks until we close it.
	pr, pw := io.Pipe()

	var stdout bytes.Buffer
	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      pr,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	// Let the bridge start reading.
	time.Sleep(50 * time.Millisecond)

	// Cancel context and close pipe.
	cancel()
	pw.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on cancel, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s")
	}
}

func TestBridge_SessionHeader(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)

	var mu sync.Mutex
	var receivedSessionIDs []string

	// Custom server that tracks session ID headers.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedSessionIDs = append(receivedSessionIDs, r.Header.Get("Mcp-Session-Id"))
		mu.Unlock()

		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "server-session-abc")
		w.Write(body)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	// Send two requests.
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	stdin := strings.NewReader(input)
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedSessionIDs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(receivedSessionIDs))
	}

	// First request should have no session ID.
	if receivedSessionIDs[0] != "" {
		t.Fatalf("first request should have no session ID, got %q", receivedSessionIDs[0])
	}

	// Second request should echo the server's session ID.
	if receivedSessionIDs[1] != "server-session-abc" {
		t.Fatalf("second request should echo session ID, got %q", receivedSessionIDs[1])
	}

	// Verify two lines in stdout.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d", len(lines))
	}
}

func TestBridge_LeaderExitsMidStream(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)

	// Use a pipe so we can control when stdin sends data.
	pr, pw := io.Pipe()
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      pr,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()

	// Send first request — should succeed.
	pw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}` + "\n"))
	time.Sleep(50 * time.Millisecond)

	// Kill the server (simulate leader exit).
	srv.Close()
	ln.Close()

	// Send second request — should detect leader gone.
	pw.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"test"}` + "\n"))

	select {
	case err := <-done:
		if err != ErrLeaderGone {
			// Close pipe to ensure clean exit if error is different.
			pw.Close()
			t.Fatalf("expected ErrLeaderGone, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		pw.Close()
		t.Fatal("Run did not return within 5s")
	}
	pw.Close()
}

func TestIsConnectionRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"ECONNREFUSED", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"ECONNRESET", &net.OpError{Op: "dial", Err: syscall.ECONNRESET}, true},
		{"ENOENT", &net.OpError{Op: "dial", Err: syscall.ENOENT}, true},
		{"dial OpError", &net.OpError{Op: "dial", Err: io.EOF}, true},
		{"read OpError", &net.OpError{Op: "read", Err: io.EOF}, false},
		{"other error", io.EOF, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConnectionRefused(tt.err); got != tt.want {
				t.Fatalf("isConnectionRefused(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBridge_MultipleRequests(t *testing.T) {
	t.Parallel()

	sockPath := testSocketPath(t)
	ln, srv := startTestHTTPServer(t, sockPath)
	defer ln.Close()
	defer srv.Close()

	input := `{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n"
	stdin := strings.NewReader(input)
	var stdout bytes.Buffer

	b := &Bridge{
		SocketPath: sockPath,
		Stdin:      stdin,
		Stdout:     &stdout,
		Logger:     slog.Default(),
	}

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %v", len(lines), lines)
	}
}
