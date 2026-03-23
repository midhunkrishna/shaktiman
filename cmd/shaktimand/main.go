// Command shaktimand is the MCP stdio server for Shaktiman.
// It indexes a codebase and provides search/context tools via MCP protocol.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/shaktimanai/shaktiman/internal/daemon"
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

	if err := d.Start(ctx); err != nil {
		slog.Error("daemon exited with error", "err", err)
		os.Exit(1)
	}

	if err := d.Stop(); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}
