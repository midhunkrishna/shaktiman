// Package mcp provides the MCP stdio server, tool handlers, and resource handlers.
package mcp

import (
	"context"
	"encoding/json"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// searchToolDef defines the MCP search tool schema.
func searchToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("search",
		mcpsdk.WithDescription("Search indexed code by keyword query. Returns ranked code chunks matching the query."),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("Search query text"),
		),
		mcpsdk.WithNumber("max_results",
			mcpsdk.Description("Maximum results to return (1-200, default 50)"),
		),
		mcpsdk.WithBoolean("explain",
			mcpsdk.Description("Include per-signal score breakdown"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

// searchHandler returns an MCP tool handler for the search tool.
func searchHandler(engine *core.QueryEngine) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: query"), nil
		}

		// Validate query length (AP-5: max 10,000 chars)
		if len(query) > 10000 {
			return mcpsdk.NewToolResultError("query exceeds maximum length of 10,000 characters"), nil
		}

		maxResults := req.GetInt("max_results", 50)
		if maxResults < 1 || maxResults > 200 {
			return mcpsdk.NewToolResultError("max_results must be between 1 and 200"), nil
		}

		explain := req.GetBool("explain", false)

		results, err := engine.Search(ctx, core.SearchInput{
			Query:      query,
			MaxResults: maxResults,
			Explain:    explain,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("search failed: ", err)), nil
		}

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}

		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// contextToolDef defines the MCP context tool schema.
func contextToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("context",
		mcpsdk.WithDescription("Assemble relevant code context for a task, fitted to a token budget. Returns ranked and deduplicated code chunks."),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("What you need context for"),
		),
		mcpsdk.WithNumber("budget_tokens",
			mcpsdk.Description("Token budget (256-32768, default 8192)"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

// contextHandler returns an MCP tool handler for the context tool.
func contextHandler(engine *core.QueryEngine) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: query"), nil
		}

		if len(query) > 10000 {
			return mcpsdk.NewToolResultError("query exceeds maximum length of 10,000 characters"), nil
		}

		budget := req.GetInt("budget_tokens", 8192)
		if budget < 256 || budget > 32768 {
			return mcpsdk.NewToolResultError("budget_tokens must be between 256 and 32768"), nil
		}

		pkg, err := engine.Context(ctx, core.ContextInput{
			Query:        query,
			BudgetTokens: budget,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("context assembly failed: ", err)), nil
		}

		data, err := json.Marshal(pkg)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal context: ", err)), nil
		}

		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// ── symbols tool ──

func symbolsToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("symbols",
		mcpsdk.WithDescription("Look up symbols by name. Returns matching symbols with file path, line, signature, and visibility."),
		mcpsdk.WithString("name",
			mcpsdk.Required(),
			mcpsdk.Description("Symbol name to search for"),
		),
		mcpsdk.WithString("kind",
			mcpsdk.Description("Optional kind filter: function, class, method, type, interface, variable"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func symbolsHandler(store *storage.Store) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: name"), nil
		}

		syms, err := store.GetSymbolByName(ctx, name)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("symbol lookup failed: ", err)), nil
		}

		kindFilter := req.GetString("kind", "")

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
			if kindFilter != "" && s.Kind != kindFilter {
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

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}
		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// ── dependencies tool ──

func dependenciesToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("dependencies",
		mcpsdk.WithDescription("Show callers/callees of a symbol using the dependency graph."),
		mcpsdk.WithString("symbol",
			mcpsdk.Required(),
			mcpsdk.Description("Symbol name to find dependencies for"),
		),
		mcpsdk.WithString("direction",
			mcpsdk.Description("Direction: callers, callees, or both (default: both)"),
		),
		mcpsdk.WithNumber("depth",
			mcpsdk.Description("BFS depth 1-5 (default 2)"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func dependenciesHandler(store *storage.Store) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		symbolName, err := req.RequireString("symbol")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: symbol"), nil
		}

		direction := req.GetString("direction", "both")
		if direction == "callers" {
			direction = "incoming"
		} else if direction == "callees" {
			direction = "outgoing"
		}
		if direction != "incoming" && direction != "outgoing" && direction != "both" {
			return mcpsdk.NewToolResultError("direction must be callers, callees, or both"), nil
		}

		depth := req.GetInt("depth", 2)
		if depth < 1 || depth > 5 {
			return mcpsdk.NewToolResultError("depth must be between 1 and 5"), nil
		}

		// Find the symbol
		syms, err := store.GetSymbolByName(ctx, symbolName)
		if err != nil || len(syms) == 0 {
			return mcpsdk.NewToolResultText("[]"), nil
		}

		symbolID := syms[0].ID
		neighborIDs, err := store.Neighbors(ctx, symbolID, depth, direction)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("graph query failed: ", err)), nil
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

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}
		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// ── diff tool ──

func diffToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("diff",
		mcpsdk.WithDescription("Show recent file changes and affected symbols."),
		mcpsdk.WithString("since",
			mcpsdk.Description("Time window, e.g. '24h', '1h', '30m' (default: 24h)"),
		),
		mcpsdk.WithNumber("limit",
			mcpsdk.Description("Maximum diffs to return (default: 50)"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func diffHandler(store *storage.Store) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		sinceStr := req.GetString("since", "24h")
		duration, err := time.ParseDuration(sinceStr)
		if err != nil {
			return mcpsdk.NewToolResultError("invalid duration: " + sinceStr), nil
		}
		if duration > 720*time.Hour {
			duration = 720 * time.Hour
		}

		limit := req.GetInt("limit", 50)
		if limit < 1 || limit > 500 {
			limit = 50
		}

		since := time.Now().Add(-duration)
		diffs, err := store.GetRecentDiffs(ctx, storage.RecentDiffsInput{
			Since: since,
			Limit: limit,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("diff query failed: ", err)), nil
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

			// Get affected symbols
			dsyms, _ := store.GetDiffSymbols(ctx, d.ID)
			for _, ds := range dsyms {
				dr.Symbols = append(dr.Symbols, ds.SymbolName+" ("+ds.ChangeType+")")
			}
			results = append(results, dr)
		}

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}
		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// ── enrichment_status tool ──

func enrichmentStatusToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("enrichment_status",
		mcpsdk.WithDescription("Show embedding and indexing progress."),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func enrichmentStatusHandler(store *storage.Store, vs *vector.BruteForceStore, ew *vector.EmbedWorker) handlerFunc {
	return func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		stats, err := store.GetIndexStats(ctx)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("stats query failed: ", err)), nil
		}

		vectorCount := 0
		if vs != nil {
			vectorCount, _ = vs.Count(ctx)
		}

		readiness := 0.0
		if stats.TotalChunks > 0 {
			readiness = float64(vectorCount) / float64(stats.TotalChunks)
		}

		cbState := "disabled"
		pending := 0
		if ew != nil {
			pending = ew.Pending()
			switch ew.CircuitBreaker().State() {
			case vector.StateClosed:
				cbState = "closed"
			case vector.StateOpen:
				cbState = "open"
			case vector.StateHalfOpen:
				cbState = "half_open"
			case vector.StateDisabled:
				cbState = "disabled"
			}
		}

		type statusResult struct {
			TotalChunks    int     `json:"total_chunks"`
			EmbeddedChunks int     `json:"embedded_chunks"`
			EmbeddingPct   float64 `json:"embedding_pct"`
			PendingJobs    int     `json:"pending_jobs"`
			CircuitState   string  `json:"circuit_state"`
			TotalFiles     int     `json:"total_files"`
			TotalSymbols   int     `json:"total_symbols"`
		}

		result := statusResult{
			TotalChunks:    stats.TotalChunks,
			EmbeddedChunks: vectorCount,
			EmbeddingPct:   readiness * 100,
			PendingJobs:    pending,
			CircuitState:   cbState,
			TotalFiles:     stats.TotalFiles,
			TotalSymbols:   stats.TotalSymbols,
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal status: ", err)), nil
		}
		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// handlerFunc matches the MCP server.ToolHandlerFunc signature.
type handlerFunc = func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)

// sanitizeError truncates error messages for MCP responses (defense in depth).
func sanitizeError(prefix string, err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return prefix + msg
}
