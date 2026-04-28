// Command shaktimand is the MCP stdio server for Shaktiman.
// It indexes a codebase and provides search/context tools via MCP protocol.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
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

// promotionWaitTimeout bounds how long a freshly-promoted proxy waits
// for the leader socket to come up. Cold indexes on large repositories
// routinely exceed the original 5s bound; 30s covers typical cold
// starts while still failing loudly on genuinely broken leaders.
const promotionWaitTimeout = 30 * time.Second

// promotionBackoffMin and Max bound the jittered sleep before a proxy
// re-execs itself to claim the leader slot. On a simultaneous
// leader-exit event, multiple proxies would otherwise all re-exec in
// lockstep; the loser(s) then race WaitForSocket against a cold leader
// start-up. Jitter spreads the attempts out.
const (
	promotionBackoffMin = 50 * time.Millisecond
	promotionBackoffMax = 500 * time.Millisecond
)

// promotionBackoff returns a uniformly-random sleep within the
// [promotionBackoffMin, promotionBackoffMax] window.
func promotionBackoff() time.Duration {
	return promotionBackoffMin + rand.N(promotionBackoffMax-promotionBackoffMin)
}

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
	if canonical, err := lockfile.Canonicalize(projectRoot); err == nil {
		projectRoot = canonical
	}

	// Configure structured logging to a file (stdout is reserved for MCP protocol).
	logDir := filepath.Join(projectRoot, ".shaktiman")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot create log directory %s: %v\n", logDir, err)
		os.Exit(1)
	}
	logPath := filepath.Join(logDir, "shaktimand.log")

	// Acquire singleton lock for this project. Ensures exactly one leader daemon.
	// If another daemon holds the lock, enter proxy mode instead.
	lock, lockErr := lockfile.Acquire(projectRoot)
	if lockErr != nil {
		if errors.Is(lockErr, lockfile.ErrAlreadyLocked) {
			// Proxy: append to shared log — never rotate or truncate, as
			// the leader's file descriptor would become detached.
			setupLogging(logPath, false)
			runAsProxy(projectRoot)
			return
		}
		fmt.Fprintf(os.Stderr, "error: acquire daemon lock: %v\n", lockErr)
		os.Exit(1)
	}
	defer func() { _ = lock.Release() }()

	// Leader: rotate previous log, then create a fresh one.
	setupLogging(logPath, true)

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

	// Create Unix domain socket for proxy clients.
	socketListener, err := lock.Listen()
	if err != nil {
		slog.Error("failed to create socket listener", "err", err)
		os.Exit(1)
	}
	defer func() { _ = os.Remove(lock.SocketPath()) }()
	defer func() { _ = socketListener.Close() }()

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

// setupLogging configures slog to write to logPath. When rotate is true
// (leader mode), the existing log is moved to session-logs/ and a fresh file
// is created. When rotate is false (proxy mode), the file is opened in append
// mode so the leader's file descriptor is not invalidated.
func setupLogging(logPath string, rotate bool) {
	if rotate {
		if info, err := os.Stat(logPath); err == nil && info.Size() > 0 {
			sessionDir := filepath.Join(filepath.Dir(logPath), "session-logs")
			if mkErr := os.MkdirAll(sessionDir, 0o750); mkErr == nil {
				ts := info.ModTime().Format("2006-01-02T15-04-05")
				_ = os.Rename(logPath, filepath.Join(sessionDir, ts+".log"))
			}
		}
	}

	var logFile *os.File
	var err error
	if rotate {
		logFile, err = os.Create(logPath)
	} else {
		logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot open log file: %v\n", err)
		os.Exit(1)
	}

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
}

// runAsProxy enters proxy mode: bridges this process's stdin/stdout to the
// leader daemon's Unix socket. On leader exit, attempts promotion via re-exec.
func runAsProxy(projectRoot string) {
	sockPath, err := lockfile.SocketPathFor(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: compute socket path: %v\n", err)
		os.Exit(1)
	}

	markerPath := daemon.ReadyMarkerPath(projectRoot)
	slog.Info("entering proxy mode",
		"project_root", projectRoot,
		"socket", sockPath,
		"marker", markerPath)

	if err := proxy.WaitForReady(sockPath, markerPath, promotionWaitTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "error: leader daemon not ready: %v\n", err)
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
		// Jittered backoff spreads simultaneous promotion attempts across
		// concurrent proxies; without it, all proxies that observe
		// ErrLeaderGone re-exec in lockstep and the losers race
		// WaitForSocket against a cold-starting leader.
		sleep := promotionBackoff()
		slog.Info("leader exited, promoting via re-exec after backoff",
			"backoff_ms", sleep.Milliseconds())
		time.Sleep(sleep)

		// Resolve our canonical executable path. os.Args[0] may be a
		// relative path or a name resolved via $PATH, which Exec will
		// re-resolve from the NEW process's cwd/env — brittle across
		// binary upgrades or working-directory changes between start
		// and promotion.
		exe, exeErr := os.Executable()
		if exeErr != nil {
			fmt.Fprintf(os.Stderr, "error: resolve own executable for re-exec: %v\n", exeErr)
			os.Exit(1)
		}

		// Re-exec: flock fd has O_CLOEXEC (Go default), stdin/stdout preserved.
		// The re-exec'd process will attempt flock and succeed (old leader released it).
		// If another proxy wins the race, we re-enter proxy mode.
		execErr := syscall.Exec(exe, os.Args, os.Environ()) //nolint:gosec // re-exec of own binary for leader promotion; exe resolved via os.Executable()

		// Exec replaces the process; if we get here, it failed.
		fmt.Fprintf(os.Stderr, "error: re-exec failed: %v\n", execErr)
		os.Exit(1)
	}

	if bridgeErr != nil {
		slog.Error("proxy error", "err", bridgeErr)
		os.Exit(1)
	}
}

