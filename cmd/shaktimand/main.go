// Command shaktimand is the MCP stdio server for Shaktiman.
// It indexes a codebase and provides search/context tools via MCP protocol.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"time"

	"github.com/shaktimanai/shaktiman/internal/daemon"
	"github.com/shaktimanai/shaktiman/internal/lockfile"
	"github.com/shaktimanai/shaktiman/internal/proxy"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: shaktimand <project-root>\n")
		os.Exit(1)
	}

	projectRoot := os.Args[1]

	// Validate project root exists
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: %s is not a valid directory\n", projectRoot)
		os.Exit(1)
	}

	// Canonicalize project root to prevent two daemons on the same directory
	// via different paths (e.g. relative vs absolute, or via symlink).
	if abs, err := filepath.Abs(projectRoot); err == nil {
		projectRoot = abs
	}
	if resolved, err := filepath.EvalSymlinks(projectRoot); err == nil {
		projectRoot = resolved
	}

	// Configure structured logging to a file (stdout is reserved for MCP protocol).
	logDir := filepath.Join(projectRoot, ".shaktiman")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create log directory %s: %v\n", logDir, err)
		os.Exit(1)
	}

	// Rotate previous log into session-logs/<timestamp>.log
	logPath := filepath.Join(logDir, "shaktimand.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 0 {
		sessionDir := filepath.Join(logDir, "session-logs")
		if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr == nil {
			ts := info.ModTime().Format("2006-01-02T15-04-05")
			_ = os.Rename(logPath, filepath.Join(sessionDir, ts+".log"))
		}
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	logLevel := slog.LevelInfo
	if lvl := os.Getenv("SHAKTIMAN_LOG_LEVEL"); lvl != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(lvl)); err == nil {
			logLevel = l
		}
	}

	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	cfg := types.DefaultConfig(projectRoot)

	cfg, err = types.LoadConfigFromFile(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := types.ValidateBackendConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Acquire singleton lock for this project. Ensures exactly one leader daemon.
	// If another daemon holds the lock, enter proxy mode instead.
	lock, lockErr := lockfile.Acquire(projectRoot)
	if lockErr != nil {
		if errors.Is(lockErr, lockfile.ErrAlreadyLocked) {
			runAsProxy(projectRoot)
			return
		}
		fmt.Fprintf(os.Stderr, "error: acquire daemon lock: %v\n", lockErr)
		os.Exit(1)
	}
	defer lock.Release()

	// Create Unix domain socket for proxy clients.
	sockPath := lock.SocketPath()
	os.Remove(sockPath) // remove stale socket from previous unclean exit (safe: we hold flock)
	socketListener, err := net.Listen("unix", sockPath)
	if err != nil {
		slog.Error("failed to create socket listener", "path", sockPath, "err", err)
		os.Exit(1)
	}
	defer os.Remove(sockPath)
	defer socketListener.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("shaktimand starting",
		"project_root", cfg.ProjectRoot,
		"db_path", cfg.DBPath,
		"embed_enabled", cfg.EmbedEnabled,
		"embed_model", cfg.EmbeddingModel,
		"ollama_url", cfg.OllamaURL,
		"watcher_enabled", cfg.WatcherEnabled,
		"search_max_results", cfg.SearchMaxResults,
		"search_default_mode", cfg.SearchDefaultMode,
		"search_min_score", cfg.SearchMinScore,
		"context_enabled", cfg.ContextEnabled,
		"context_budget_tokens", cfg.ContextBudgetTokens,
		"pid", os.Getpid(),
	)

	d, err := daemon.New(cfg)
	if err != nil {
		slog.Error("failed to create daemon", "err", err)
		os.Exit(1)
	}
	d.SocketListener = socketListener

	if err := d.Start(ctx); err != nil {
		slog.Error("daemon exited with error", "err", err)
		os.Exit(1)
	}

	if err := d.Stop(); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}

// runAsProxy enters proxy mode: bridges this process's stdin/stdout to the
// leader daemon's Unix socket. On leader exit, attempts promotion via re-exec.
func runAsProxy(projectRoot string) {
	sockPath, err := lockfile.SocketPathFor(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: compute socket path: %v\n", err)
		os.Exit(1)
	}

	slog.Info("entering proxy mode", "project_root", projectRoot, "socket", sockPath)

	if err := waitForSocket(sockPath, 5*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "error: leader daemon socket not available: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: the leader daemon may still be starting up\n")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	b := &proxy.Bridge{
		SocketPath: sockPath,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Logger:     slog.Default().With("mode", "proxy"),
	}

	bridgeErr := b.Run(ctx)

	if errors.Is(bridgeErr, proxy.ErrLeaderGone) {
		slog.Info("leader exited, attempting promotion via re-exec")
		// Re-exec: flock fd has O_CLOEXEC (Go default), stdin/stdout preserved.
		// The re-exec'd process will attempt flock and succeed (old leader released it).
		// If another proxy wins the race, we re-enter proxy mode.
		execErr := syscall.Exec(os.Args[0], os.Args, os.Environ())
		// Exec replaces the process; if we get here, it failed.
		fmt.Fprintf(os.Stderr, "error: re-exec failed: %v\n", execErr)
		os.Exit(1)
	}

	if bridgeErr != nil {
		slog.Error("proxy error", "err", bridgeErr)
		os.Exit(1)
	}
}

// waitForSocket waits for a Unix domain socket to become connectable,
// using exponential backoff.
func waitForSocket(path string, timeout time.Duration) error {
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
			conn.Close()
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
