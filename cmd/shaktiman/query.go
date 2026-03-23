package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func searchCmd() *cobra.Command {
	var (
		root       string
		maxResults int
		mode       string
		minScore   float64
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
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

			results, err := engine.Search(context.Background(), core.SearchInput{
				Query:      args[0],
				MaxResults: maxResults,
				Mode:       mode,
				MinScore:   minScore,
			})
			if err != nil {
				return fmt.Errorf("search: %w", err)
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
			ctx := context.Background()

			syms, err := store.GetSymbolByName(ctx, args[0])
			if err != nil {
				return fmt.Errorf("symbol lookup: %w", err)
			}

			type symbolResult struct {
				Name       string `json:"name"`
				Kind       string `json:"kind"`
				Line       int    `json:"line"`
				Signature  string `json:"signature,omitempty"`
				Visibility string `json:"visibility"`
				FilePath   string `json:"file_path"`
			}

			var results []symbolResult
			for _, s := range syms {
				if kind != "" && s.Kind != kind {
					continue
				}
				path, _ := store.GetFilePathByID(ctx, s.FileID)
				results = append(results, symbolResult{
					Name:       s.Name,
					Kind:       s.Kind,
					Line:       s.Line,
					Signature:  s.Signature,
					Visibility: s.Visibility,
					FilePath:   path,
				})
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
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
				fmt.Println("[]")
				return nil
			}

			neighborIDs, err := store.Neighbors(ctx, syms[0].ID, depth, dir)
			if err != nil {
				return fmt.Errorf("graph query: %w", err)
			}

			type depResult struct {
				Name     string `json:"name"`
				Kind     string `json:"kind"`
				FilePath string `json:"file_path"`
				Line     int    `json:"line"`
			}

			var results []depResult
			for _, nID := range neighborIDs {
				sym, err := store.GetSymbolByID(ctx, nID)
				if err != nil || sym == nil {
					continue
				}
				path, _ := store.GetFilePathByID(ctx, sym.FileID)
				results = append(results, depResult{
					Name:     sym.Name,
					Kind:     sym.Kind,
					FilePath: path,
					Line:     sym.Line,
				})
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
			ctx := context.Background()

			duration, err := time.ParseDuration(since)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", since)
			}
			if duration > 720*time.Hour {
				duration = 720 * time.Hour
			}

			sinceTime := time.Now().Add(-duration)
			diffs, err := store.GetRecentDiffs(ctx, storage.RecentDiffsInput{
				Since: sinceTime,
				Limit: limit,
			})
			if err != nil {
				return fmt.Errorf("diff query: %w", err)
			}

			type diffResult struct {
				FileID       int64    `json:"file_id"`
				FilePath     string   `json:"file_path"`
				ChangeType   string   `json:"change_type"`
				LinesAdded   int      `json:"lines_added"`
				LinesRemoved int      `json:"lines_removed"`
				Timestamp    string   `json:"timestamp"`
				Symbols      []string `json:"affected_symbols,omitempty"`
			}

			var results []diffResult
			for _, d := range diffs {
				path, _ := store.GetFilePathByID(ctx, d.FileID)
				dr := diffResult{
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

			db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer db.Close()

			store := storage.NewStore(db)
			stats, err := store.GetIndexStats(context.Background())
			if err != nil {
				return fmt.Errorf("stats: %w", err)
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
