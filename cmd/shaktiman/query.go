package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/backends"
	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// openStore creates a WriterStore using the backends package.
// The returned closer must be deferred by the caller.
func openStore(cfg types.Config) (types.WriterStore, func() error, error) {
	b, err := backends.OpenMetadataOnly(cfg)
	if err != nil {
		return nil, nil, err
	}
	return b.Store, b.Close, nil
}

// openEngine creates a QueryEngine with vector store + embedder when available.
// Read-only: loads existing embeddings from disk, does not start embed worker.
// Falls back to keyword-only if embeddings are unavailable.
func openEngine(cfg types.Config, store types.WriterStore, root string) (*core.QueryEngine, types.VectorStore) {
	engine := core.NewQueryEngine(store, root, cfg.EmbedQueryPrefix)

	if !cfg.EmbedEnabled {
		return engine, nil
	}

	vs, err := vector.NewVectorStore(vector.VectorStoreConfigFrom(cfg, store))
	if err != nil {
		slog.Warn("vector store unavailable, using keyword search", "err", err)
		return engine, nil
	}

	// Load persisted embeddings (brute_force/hnsw use disk files)
	if p, ok := vs.(types.VectorPersister); ok {
		if err := p.LoadFromDisk(backends.EmbeddingsPath(cfg)); err != nil {
			slog.Info("no embeddings on disk, using keyword search", "path", backends.EmbeddingsPath(cfg))
			return engine, vs
		}
	}

	client := vector.NewOllamaClient(vector.OllamaClientInput{
		BaseURL: cfg.OllamaURL,
		Model:   cfg.EmbeddingModel,
	})

	// Check Ollama availability for query embedding
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !client.Healthy(ctx) {
		slog.Warn("Ollama not available, falling back to keyword search", "url", cfg.OllamaURL)
		return engine, vs
	}

	engine.SetVectorStore(vs, client, func() bool { return true })
	return engine, vs
}

func searchCmd() *cobra.Command {
	var (
		root       string
		maxResults int
		mode       string
		minScore   float64
		explain    bool
		path       string
		scope      string
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search indexed code by keyword or semantic query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()
			engine, _ := openEngine(cfg, store, root)

			if maxResults == 0 {
				maxResults = cfg.SearchMaxResults
			}
			if minScore == 0 {
				minScore = cfg.SearchMinScore
			}
			if mode == "" {
				mode = cfg.SearchDefaultMode
			}

			filter := core.ScopeToFilter(scope)

			// Over-fetch when path filter is set to compensate for post-filtering.
			engineMax := maxResults
			if path != "" {
				engineMax = maxResults * 3
				if engineMax > 200 {
					engineMax = 200
				}
			}

			results, err := engine.Search(context.Background(), core.SearchInput{
				Query:        args[0],
				MaxResults:   engineMax,
				Explain:      explain,
				Mode:         mode,
				MinScore:     minScore,
				ExcludeTests: filter.ExcludeTests,
				TestOnly:     filter.TestOnly,
			})
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}

			// Apply path prefix filter if specified.
			if path != "" {
				filtered := results[:0]
				for _, r := range results {
					if strings.HasPrefix(r.Path, path) {
						filtered = append(filtered, r)
					}
				}
				results = filtered
				if len(results) > maxResults {
					results = results[:maxResults]
				}
			}

			if outputFormat == "text" {
				if mode == "locate" {
					fmt.Print(format.LocateResults(results))
				} else {
					fmt.Print(format.SearchResults(results, explain))
				}
				return nil
			}

			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().IntVar(&maxResults, "max", 0, "Maximum results (0 = use config default)")
	cmd.Flags().StringVar(&mode, "mode", "", "Result mode: locate or full (empty = use config default)")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "Minimum relevance score (0 = use config default)")
	cmd.Flags().BoolVar(&explain, "explain", false, "Include score breakdown (text format only)")
	cmd.Flags().StringVar(&path, "path", "", "Filter results to file or directory prefix")
	cmd.Flags().StringVar(&scope, "scope", "impl", `Result scope: "impl", "test", or "all"`)
	return cmd
}

func contextCmd() *cobra.Command {
	var (
		root   string
		budget int
		scope  string
	)
	cmd := &cobra.Command{
		Use:   "context <query>",
		Short: "Assemble cross-file context for a query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()
			engine, _ := openEngine(cfg, store, root)

			if budget == 0 {
				budget = cfg.ContextBudgetTokens
			}

			filter := core.ScopeToFilter(scope)

			pkg, err := engine.Context(context.Background(), core.ContextInput{
				Query:        args[0],
				BudgetTokens: budget,
				ExcludeTests: filter.ExcludeTests,
				TestOnly:     filter.TestOnly,
			})
			if err != nil {
				return fmt.Errorf("context: %w", err)
			}

			if outputFormat == "text" {
				fmt.Print(format.ContextPackage(pkg))
				return nil
			}

			data, err := json.MarshalIndent(pkg, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().IntVar(&budget, "budget", 0, "Token budget (0 = use config default)")
	cmd.Flags().StringVar(&scope, "scope", "impl", `Result scope: "impl", "test", or "all"`)
	return cmd
}

func symbolsCmd() *cobra.Command {
	var (
		root  string
		kind  string
		scope string
	)
	cmd := &cobra.Command{
		Use:   "symbols <name>",
		Short: "Look up symbols by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			filter := core.ScopeToFilter(scope)
			result, err := core.LookupSymbols(context.Background(), store, args[0], kind, filter)
			if err != nil {
				return fmt.Errorf("symbol lookup: %w", err)
			}

			if outputFormat == "text" {
				fmt.Print(format.Symbols(result.Definitions))
				return nil
			}

			// Return enriched response when pending_edges fallback triggered
			if len(result.ReferencedBy) > 0 {
				data, err := json.MarshalIndent(format.SymbolsWithRefs{
					Definitions:  result.Definitions,
					ReferencedBy: result.ReferencedBy,
					Note:         result.Note,
				}, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal: %w", err)
				}
				fmt.Println(string(data))
				return nil
			}

			data, err := json.MarshalIndent(result.Definitions, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by symbol kind (function, class, method, type, interface, variable)")
	cmd.Flags().StringVar(&scope, "scope", "impl", `Result scope: "impl", "test", or "all"`)
	return cmd
}

func depsCmd() *cobra.Command {
	var (
		root      string
		direction string
		depth     int
		scope     string
	)
	cmd := &cobra.Command{
		Use:   "deps <symbol>",
		Short: "Show callers/callees of a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			dir := direction
			switch dir {
			case "callers":
				dir = "incoming"
			case "callees":
				dir = "outgoing"
			case "both":
				// keep as-is
			default:
				return fmt.Errorf("direction must be callers, callees, or both")
			}

			filter := core.ScopeToFilter(scope)
			results, err := core.LookupDependencies(context.Background(), store, args[0], dir, depth, filter)
			if err != nil {
				return fmt.Errorf("dependency lookup: %w", err)
			}

			if outputFormat == "text" {
				fmt.Print(format.Dependencies(results))
				return nil
			}

			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().StringVar(&direction, "direction", "both", "Direction: callers, callees, or both")
	cmd.Flags().IntVar(&depth, "depth", 2, "BFS depth (1-5)")
	cmd.Flags().StringVar(&scope, "scope", "impl", `Result scope: "impl", "test", or "all"`)
	return cmd
}

func diffCmd() *cobra.Command {
	var (
		root  string
		since string
		limit int
		scope string
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show recent file changes and affected symbols",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			duration, err := time.ParseDuration(since)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", since)
			}
			if duration > 720*time.Hour {
				duration = 720 * time.Hour
			}

			filter := core.ScopeToFilter(scope)
			sinceTime := time.Now().Add(-duration)

			results, err := core.LookupDiffs(context.Background(), store, sinceTime, limit, filter)
			if err != nil {
				return fmt.Errorf("diff query: %w", err)
			}

			if outputFormat == "text" {
				fmt.Print(format.Diffs(results))
				return nil
			}

			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().StringVar(&since, "since", "24h", "Time window (e.g. 24h, 1h, 30m)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum diffs to return")
	cmd.Flags().StringVar(&scope, "scope", "impl", `Result scope: "impl", "test", or "all"`)
	return cmd
}

func enrichmentStatusCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "enrichment-status",
		Short: "Show indexing stats and enrichment progress",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			_, vs := openEngine(cfg, store, root)

			result, err := core.GetEnrichmentStatus(context.Background(), core.EnrichmentStatusInput{
				Store:       store,
				VectorStore: vs,
			})
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}

			if outputFormat == "text" {
				stats, _ := store.GetIndexStats(context.Background())
				fmt.Print(format.IndexStats(stats))
				return nil
			}

			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	return cmd
}

func summaryCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Show codebase summary: files, languages, symbols, embedding %",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := types.DefaultConfig(root)
			cfg, err := types.LoadConfigFromFile(cfg)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			store, closer, err := openStore(cfg)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closer()

			_, vs := openEngine(cfg, store, root)

			result, err := core.GetSummary(context.Background(), store, vs)
			if err != nil {
				return fmt.Errorf("summary: %w", err)
			}

			if outputFormat == "text" {
				stats, _ := store.GetIndexStats(context.Background())
				fmt.Print(format.IndexStats(stats))
				return nil
			}

			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	return cmd
}
