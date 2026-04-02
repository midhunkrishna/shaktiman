package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// openStore creates a WriterStore using the registry for the given config.
// The returned closer must be deferred by the caller.
func openStore(cfg types.Config) (types.WriterStore, func() error, error) {
	store, _, closer, err := storage.NewMetadataStore(storage.MetadataStoreConfig{
		Backend:    cfg.DatabaseBackend,
		SQLitePath: cfg.DBPath,
	})
	if err != nil {
		return nil, nil, err
	}
	return store, closer, nil
}

func searchCmd() *cobra.Command {
	var (
		root       string
		maxResults int
		mode       string
		minScore   float64
		explain    bool
		path       string
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
			engine := core.NewQueryEngine(store, root)

			if maxResults == 0 {
				maxResults = cfg.SearchMaxResults
			}
			if minScore == 0 {
				minScore = cfg.SearchMinScore
			}
			if mode == "" {
				mode = cfg.SearchDefaultMode
			}

			// Over-fetch when path filter is set to compensate for post-filtering.
			engineMax := maxResults
			if path != "" {
				engineMax = maxResults * 3
				if engineMax > 200 {
					engineMax = 200
				}
			}

			results, err := engine.Search(context.Background(), core.SearchInput{
				Query:      args[0],
				MaxResults: engineMax,
				Mode:       mode,
				MinScore:   minScore,
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
	return cmd
}

func contextCmd() *cobra.Command {
	var (
		root   string
		budget int
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
			engine := core.NewQueryEngine(store, root)

			if budget == 0 {
				budget = cfg.ContextBudgetTokens
			}

			pkg, err := engine.Context(context.Background(), core.ContextInput{
				Query:        args[0],
				BudgetTokens: budget,
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
	return cmd
}

func symbolsCmd() *cobra.Command {
	var (
		root string
		kind string
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
			ctx := context.Background()

			syms, err := store.GetSymbolByName(ctx, args[0])
			if err != nil {
				return fmt.Errorf("symbol lookup: %w", err)
			}

			var results []format.SymbolResult
			for _, s := range syms {
				if kind != "" && s.Kind != kind {
					continue
				}
				path, _ := store.GetFilePathByID(ctx, s.FileID)
				results = append(results, format.SymbolResult{
					Name:       s.Name,
					Kind:       s.Kind,
					Line:       s.Line,
					Signature:  s.Signature,
					Visibility: s.Visibility,
					FilePath:   path,
				})
			}

			if outputFormat == "text" {
				fmt.Print(format.Symbols(results))
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
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by symbol kind (function, class, method, type, interface, variable)")
	return cmd
}

func depsCmd() *cobra.Command {
	var (
		root      string
		direction string
		depth     int
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
			ctx := context.Background()

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

			syms, err := store.GetSymbolByName(ctx, args[0])
			if err != nil || len(syms) == 0 {
				if outputFormat == "text" {
					fmt.Print(format.Dependencies(nil))
				} else {
					fmt.Println("[]")
				}
				return nil
			}

			neighborIDs, err := store.Neighbors(ctx, syms[0].ID, depth, dir)
			if err != nil {
				return fmt.Errorf("graph query: %w", err)
			}

			var results []format.DepResult
			for _, nID := range neighborIDs {
				sym, err := store.GetSymbolByID(ctx, nID)
				if err != nil || sym == nil {
					continue
				}
				path, _ := store.GetFilePathByID(ctx, sym.FileID)
				results = append(results, format.DepResult{
					Name:     sym.Name,
					Kind:     sym.Kind,
					FilePath: path,
					Line:     sym.Line,
				})
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
	return cmd
}

func diffCmd() *cobra.Command {
	var (
		root  string
		since string
		limit int
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
			ctx := context.Background()

			duration, err := time.ParseDuration(since)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", since)
			}
			if duration > 720*time.Hour {
				duration = 720 * time.Hour
			}

			sinceTime := time.Now().Add(-duration)
			diffs, err := store.GetRecentDiffs(ctx, types.RecentDiffsInput{
				Since: sinceTime,
				Limit: limit,
			})
			if err != nil {
				return fmt.Errorf("diff query: %w", err)
			}

			var results []format.DiffResult
			for _, d := range diffs {
				path, _ := store.GetFilePathByID(ctx, d.FileID)
				dr := format.DiffResult{
					FileID:       d.FileID,
					FilePath:     path,
					ChangeType:   d.ChangeType,
					LinesAdded:   d.LinesAdded,
					LinesRemoved: d.LinesRemoved,
					Timestamp:    d.Timestamp,
				}
				dsyms, _ := store.GetDiffSymbols(ctx, d.ID)
				for _, ds := range dsyms {
					dr.Symbols = append(dr.Symbols, ds.SymbolName+" ("+ds.ChangeType+")")
				}
				results = append(results, dr)
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
			stats, err := store.GetIndexStats(context.Background())
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}

			if outputFormat == "text" {
				fmt.Print(format.IndexStats(stats))
				return nil
			}

			data, err := json.MarshalIndent(stats, "", "  ")
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
