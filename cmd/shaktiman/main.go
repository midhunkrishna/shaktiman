// Command shaktiman is the CLI for Shaktiman.
// It provides direct access to indexing, search, and status without MCP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/backends"
	"github.com/shaktimanai/shaktiman/internal/daemon"
	"github.com/shaktimanai/shaktiman/internal/lockfile"
	"github.com/shaktimanai/shaktiman/internal/types"
)

var outputFormat string

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	slog.SetDefault(logger)

	rootCmd := &cobra.Command{
		Use:   "shaktiman",
		Short: "Shaktiman code indexing and retrieval CLI",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if outputFormat != "json" && outputFormat != "text" {
				return fmt.Errorf("--format must be 'json' or 'text', got %q", outputFormat)
			}
			return nil
		},
	}
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "json",
		`Output format: "json" (default) or "text"`)

	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(indexCmd())
	rootCmd.AddCommand(reindexCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(contextCmd())
	rootCmd.AddCommand(symbolsCmd())
	rootCmd.AddCommand(depsCmd())
	rootCmd.AddCommand(diffCmd())
	rootCmd.AddCommand(enrichmentStatusCmd())
	rootCmd.AddCommand(summaryCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <project-root>",
		Short: "Initialize a .shaktiman config directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			tomlPath := filepath.Join(projectRoot, ".shaktiman", "shaktiman.toml")
			if _, err := os.Stat(tomlPath); err == nil {
				fmt.Printf("Config already exists: %s\n", tomlPath)
				return nil
			}
			types.WriteSampleConfig(projectRoot)
			if _, err := os.Stat(tomlPath); err != nil {
				return fmt.Errorf("failed to create config: %w", err)
			}
			fmt.Printf("Created config: %s\n", tomlPath)
			return nil
		},
	}
}

// applyBackendFlags applies CLI flag overrides to the config.
func applyBackendFlags(cfg *types.Config, vectorBackend, dbBackend, postgresURL, qdrantURL string) error {
	if vectorBackend != "" {
		switch vectorBackend {
		case "brute_force", "hnsw", "qdrant", "pgvector":
			cfg.VectorBackend = vectorBackend
		default:
			return fmt.Errorf("--vector must be 'brute_force', 'hnsw', 'qdrant', or 'pgvector', got %q", vectorBackend)
		}
	}
	if dbBackend != "" {
		if dbBackend != "sqlite" && dbBackend != "postgres" {
			return fmt.Errorf("--db must be 'sqlite' or 'postgres', got %q", dbBackend)
		}
		cfg.DatabaseBackend = dbBackend
	}
	if postgresURL != "" {
		cfg.PostgresConnString = postgresURL
	}
	if qdrantURL != "" {
		cfg.QdrantURL = qdrantURL
	}
	return nil
}

// runIndexPipeline runs IndexProject and optionally EmbedProject with progress output.
func runIndexPipeline(ctx context.Context, d *daemon.Daemon, cfg types.Config, embed bool) error {
	tty := isTTY()

	var lastIndexPct int
	if err := d.IndexProject(ctx, func(p daemon.IndexProgress) {
		if p.Total == 0 {
			return
		}
		pct := p.Indexed * 100 / p.Total
		if tty {
			fmt.Fprintf(os.Stdout, "\rIndexing: %d/%d files (%d%%)", p.Indexed, p.Total, pct)
		} else if pct >= lastIndexPct+10 || p.Indexed == p.Total {
			fmt.Fprintf(os.Stdout, "Indexing: %d/%d files (%d%%)\n", p.Indexed, p.Total, pct)
			lastIndexPct = pct
		}
	}); err != nil {
		if tty {
			fmt.Fprintln(os.Stdout)
		}
		return fmt.Errorf("index: %w", err)
	}
	if tty {
		fmt.Fprintln(os.Stdout)
	}

	stats, err := d.Store().GetIndexStats(ctx)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}

	fmt.Printf("Indexed: %d files | %d chunks | %d symbols\n",
		stats.TotalFiles, stats.TotalChunks, stats.TotalSymbols)
	for lang, count := range stats.Languages {
		fmt.Printf("  %s: %d files\n", lang, count)
	}

	if embed {
		saveCtx, saveCancel := context.WithCancel(ctx)
		go d.RunPeriodicEmbeddingSave(saveCtx)

		var lastEmbedPct int
		count, err := d.EmbedProject(ctx, func(p types.EmbedProgress) {
			if p.Warning != "" {
				if tty {
					fmt.Fprintf(os.Stdout, "\r%s%s", p.Warning, strings.Repeat(" ", 20))
				} else {
					fmt.Fprintf(os.Stdout, "%s\n", p.Warning)
				}
				return
			}
			if p.Total == 0 {
				return
			}
			pct := p.Embedded * 100 / p.Total
			if tty {
				fmt.Fprintf(os.Stdout, "\rEmbedding: %d/%d chunks (%d%%)", p.Embedded, p.Total, pct)
			} else if pct >= lastEmbedPct+10 || p.Embedded == p.Total {
				fmt.Fprintf(os.Stdout, "Embedding: %d/%d chunks (%d%%)\n", p.Embedded, p.Total, pct)
				lastEmbedPct = pct
			}
		})
		saveCancel()

		if tty {
			fmt.Fprintln(os.Stdout)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintf(os.Stderr, "Indexing completed without embeddings. Run 'shaktiman index --embed' to retry.\n")
			if count > 0 {
				fmt.Printf("Embedded: %d/%d chunks (partial) → %s\n", count, stats.TotalChunks, cfg.EmbeddingsPath)
			}
		} else {
			if count < stats.TotalChunks && stats.TotalChunks > 0 {
				fmt.Fprintf(os.Stderr, "Warning: %d/%d chunks could not be embedded (Ollama errors).\n", stats.TotalChunks-count, stats.TotalChunks)
				fmt.Fprintf(os.Stderr, "Run 'shaktiman index --embed' to retry failed chunks.\n")
			}
			fmt.Printf("Embedded: %d/%d chunks → %s\n", count, stats.TotalChunks, cfg.EmbeddingsPath)
		}
	}
	return nil
}

// addBackendFlags registers the common backend flags on a cobra command.
func addBackendFlags(cmd *cobra.Command, embed *bool, vectorBackend, dbBackend, postgresURL, qdrantURL *string) {
	cmd.Flags().BoolVar(embed, "embed", false, "Also generate embeddings (requires Ollama)")
	cmd.Flags().StringVar(vectorBackend, "vector", "", `Vector backend: "brute_force", "hnsw", "qdrant", or "pgvector"`)
	cmd.Flags().StringVar(dbBackend, "db", "", `Database backend: "sqlite" or "postgres"`)
	cmd.Flags().StringVar(postgresURL, "postgres-url", "", "PostgreSQL connection string (overrides TOML)")
	cmd.Flags().StringVar(qdrantURL, "qdrant-url", "", "Qdrant URL (overrides TOML)")
}

// loadAndConfigureProject loads the config for a project root and applies CLI flag overrides.
func loadAndConfigureProject(projectRoot, vectorBackend, dbBackend, postgresURL, qdrantURL string) (types.Config, error) {
	cfg := types.DefaultConfig(projectRoot)
	cfg, err := types.LoadConfigFromFile(cfg)
	if err != nil {
		return cfg, fmt.Errorf("load config: %w", err)
	}
	if err := applyBackendFlags(&cfg, vectorBackend, dbBackend, postgresURL, qdrantURL); err != nil {
		return cfg, err
	}
	if err := types.ValidateBackendConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func indexCmd() *cobra.Command {
	var (
		embed         bool
		vectorBackend string
		dbBackend     string
		postgresURL   string
		qdrantURL     string
	)
	cmd := &cobra.Command{
		Use:   "index <project-root>",
		Short: "Index a project directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAndConfigureProject(args[0], vectorBackend, dbBackend, postgresURL, qdrantURL)
			if err != nil {
				return err
			}

			// Refuse to index if a daemon is already running (would race on writes).
			lock, lockErr := lockfile.Acquire(args[0])
			if lockErr != nil {
				if errors.Is(lockErr, lockfile.ErrAlreadyLocked) {
					return fmt.Errorf("a shaktimand daemon is running for this project; " +
						"it handles indexing automatically")
				}
				return fmt.Errorf("check daemon lock: %w", lockErr)
			}
			defer lock.Release()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			d, err := daemon.New(cfg)
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}
			defer d.Stop()

			return runIndexPipeline(ctx, d, cfg, embed)
		},
	}
	addBackendFlags(cmd, &embed, &vectorBackend, &dbBackend, &postgresURL, &qdrantURL)
	return cmd
}

func reindexCmd() *cobra.Command {
	var (
		embed         bool
		vectorBackend string
		dbBackend     string
		postgresURL   string
		qdrantURL     string
		force         bool
	)
	cmd := &cobra.Command{
		Use:   "reindex <project-root>",
		Short: "Purge all indexed data and reindex from scratch",
		Long:  "Deletes all indexed data (metadata, embeddings, vectors) and re-runs the full index pipeline. Preserves configuration.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadAndConfigureProject(args[0], vectorBackend, dbBackend, postgresURL, qdrantURL)
			if err != nil {
				return err
			}

			// Confirmation prompt
			if !force {
				if !isTTY() {
					return fmt.Errorf("reindex is destructive; use --force in non-interactive mode")
				}
				fmt.Fprint(os.Stderr, "This will delete ALL indexed data and reindex from scratch. Continue? [y/N] ")
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" {
					fmt.Fprintln(os.Stderr, "Aborted.")
					return nil
				}
			}

			// Refuse to reindex if a daemon is already running (would race on writes).
			lock, lockErr := lockfile.Acquire(args[0])
			if lockErr != nil {
				if errors.Is(lockErr, lockfile.ErrAlreadyLocked) {
					return fmt.Errorf("a shaktimand daemon is running for this project; " +
						"stop it before running reindex")
				}
				return fmt.Errorf("check daemon lock: %w", lockErr)
			}
			defer lock.Release()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Phase 1: Open backends and purge server-based stores.
			b, err := backends.Open(cfg)
			if err != nil {
				return fmt.Errorf("open backends for purge: %w", err)
			}
			if err := backends.PurgeBackends(ctx, b.Store, b.VectorStore); err != nil {
				b.Close()
				return fmt.Errorf("purge: %w", err)
			}
			b.Close()

			// Phase 2: Delete local files (SQLite DB, vector persistence).
			if err := backends.PurgeFiles(cfg); err != nil {
				return fmt.Errorf("purge files: %w", err)
			}

			fmt.Fprintln(os.Stdout, "Purged all indexed data.")

			// Phase 3: Fresh index via daemon (daemon.New calls backends.Open internally).
			d, err := daemon.New(cfg)
			if err != nil {
				return fmt.Errorf("init for reindex: %w", err)
			}
			defer d.Stop()

			return runIndexPipeline(ctx, d, cfg, embed)
		},
	}
	addBackendFlags(cmd, &embed, &vectorBackend, &dbBackend, &postgresURL, &qdrantURL)
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

// isTTY returns true if stdout is a terminal (not piped or redirected).
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <project-root>",
		Short: "Show index status for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			cfg := types.DefaultConfig(projectRoot)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()
			ctx := context.Background()

			stats, err := store.GetIndexStats(ctx)
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}

			fmt.Printf("Files:   %d\n", stats.TotalFiles)
			fmt.Printf("Chunks:  %d\n", stats.TotalChunks)
			fmt.Printf("Symbols: %d\n", stats.TotalSymbols)
			fmt.Printf("Errors:  %d\n", stats.ParseErrors)
			if len(stats.Languages) > 0 {
				fmt.Println("Languages:")
				for lang, count := range stats.Languages {
					fmt.Printf("  %s: %d\n", lang, count)
				}
			}
			return nil
		},
	}
}
