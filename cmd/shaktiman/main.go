// Command shaktiman is the CLI for Shaktiman.
// It provides direct access to indexing, search, and status without MCP.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/daemon"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	slog.SetDefault(logger)

	rootCmd := &cobra.Command{
		Use:   "shaktiman",
		Short: "Shaktiman code indexing and retrieval CLI",
	}

	rootCmd.AddCommand(indexCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(contextCmd())
	rootCmd.AddCommand(symbolsCmd())
	rootCmd.AddCommand(depsCmd())
	rootCmd.AddCommand(diffCmd())
	rootCmd.AddCommand(enrichmentStatusCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func indexCmd() *cobra.Command {
	var embed bool
	cmd := &cobra.Command{
		Use:   "index <project-root>",
		Short: "Index a project directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			cfg := types.DefaultConfig(projectRoot)

			d, err := daemon.New(cfg)
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}

			ctx := context.Background()
			if err := d.IndexProject(ctx); err != nil {
				return fmt.Errorf("index: %w", err)
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
				count, err := d.EmbedProject(ctx)
				if err != nil {
					return fmt.Errorf("embed: %w", err)
				}
				fmt.Printf("Embedded: %d chunks → %s\n", count, cfg.EmbeddingsPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&embed, "embed", false, "Also generate embeddings (requires Ollama)")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <project-root>",
		Short: "Show index status for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot := args[0]
			cfg := types.DefaultConfig(projectRoot)

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
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
