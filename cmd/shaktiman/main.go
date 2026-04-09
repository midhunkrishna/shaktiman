// Command shaktiman is the CLI for Shaktiman.
// It provides direct access to indexing, search, and status without MCP.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/daemon"
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
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(contextCmd())
	rootCmd.AddCommand(symbolsCmd())
	rootCmd.AddCommand(depsCmd())
	rootCmd.AddCommand(diffCmd())
	rootCmd.AddCommand(enrichmentStatusCmd())
	rootCmd.AddCommand(summaryCmd())
	rootCmd.AddCommand(reEmbedCmd())

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

func indexCmd() *cobra.Command {
	var (
		embed          bool
		vectorBackend  string
		dbBackend      string
		postgresURL    string
		qdrantURL      string
	)
	cmd := &cobra.Command{
		Use:   "index <project-root>",
		Short: "Index a project directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			cfg := types.DefaultConfig(projectRoot)

			// Load TOML config if it exists
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// CLI flags override TOML and default
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

			if err := types.ValidateBackendConfig(cfg); err != nil {
				return err
			}

			// Signal handling for graceful shutdown
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			d, err := daemon.New(cfg)
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}
			defer d.Stop()

			tty := isTTY()

			// Index with progress
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
				// Start periodic embedding checkpoint saves
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
				saveCancel() // stop periodic saves

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
		},
	}
	cmd.Flags().BoolVar(&embed, "embed", false, "Also generate embeddings (requires Ollama)")
	cmd.Flags().StringVar(&vectorBackend, "vector", "", `Vector backend: "brute_force", "hnsw", "qdrant", or "pgvector"`)
	cmd.Flags().StringVar(&dbBackend, "db", "", `Database backend: "sqlite" or "postgres"`)
	cmd.Flags().StringVar(&postgresURL, "postgres-url", "", "PostgreSQL connection string (overrides TOML)")
	cmd.Flags().StringVar(&qdrantURL, "qdrant-url", "", "Qdrant URL (overrides TOML)")
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

func reEmbedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "re-embed <project-root>",
		Short: "Discard existing embeddings and regenerate with current config",
		Long: `Deletes embeddings.bin and resets all embedded flags in the database.
After running this, execute 'shaktiman index --embed <project-root>' to regenerate
all embeddings using the prefixes configured in shaktiman.toml.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			cfg := types.DefaultConfig(projectRoot)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx := context.Background()

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			// Delete embeddings.bin
			if err := os.Remove(cfg.EmbeddingsPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete embeddings: %w", err)
			}
			fmt.Printf("Deleted: %s\n", cfg.EmbeddingsPath)

			// Reset all embedded flags so index --embed regenerates everything
			if err := store.ResetAllEmbeddedFlags(ctx); err != nil {
				return fmt.Errorf("reset embedded flags: %w", err)
			}
			fmt.Println("Reset embedded flags.")
			fmt.Printf("Run: shaktiman index --embed %s\n", projectRoot)
			return nil
		},
	}
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
