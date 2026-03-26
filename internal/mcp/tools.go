// Package mcp provides the MCP stdio server, tool handlers, and resource handlers.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// searchToolDef defines the MCP search tool schema.
func searchToolDef(cfg types.Config) mcpsdk.Tool {
	return mcpsdk.NewTool("search",
		mcpsdk.WithDescription(
			`Search indexed code by keyword or semantic query.
Use this INSTEAD of Grep for code discovery — returns 10-50x fewer tokens.
Default mode is "locate": returns compact file pointers (~12 tokens per result vs ~500 for Grep).
Supports semantic matching (e.g. "error handling" finds try/catch, recover, Result types).
Use locate to discover relevant files, then Read specific files you need.
Set mode="full" only when you need inline source code.`),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("Search query text"),
		),
		mcpsdk.WithString("mode",
			mcpsdk.Description(fmt.Sprintf(`Result mode: "locate" (headers only) or "full" (with source code). Default: %q`, cfg.SearchDefaultMode)),
			mcpsdk.Enum("locate", "full"),
		),
		mcpsdk.WithNumber("max_results",
			mcpsdk.Description(fmt.Sprintf("Maximum results to return (1-200, default %d)", cfg.SearchMaxResults)),
		),
		mcpsdk.WithNumber("min_score",
			mcpsdk.Description(fmt.Sprintf("Minimum relevance score 0.0-1.0 (default %.2f)", cfg.SearchMinScore)),
		),
		mcpsdk.WithBoolean("explain",
			mcpsdk.Description("Include per-signal score breakdown"),
		),
		mcpsdk.WithString("path",
			mcpsdk.Description("Filter results to this file or directory prefix (e.g. 'internal/mcp/' or 'main.go')"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

// searchHandler returns an MCP tool handler for the search tool.
func searchHandler(engine *core.QueryEngine, cfg types.Config) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: query"), nil
		}

		// Validate query length (AP-5: max 10,000 chars)
		if len(query) > 10000 {
			return mcpsdk.NewToolResultError("query exceeds maximum length of 10,000 characters"), nil
		}

		mode := req.GetString("mode", cfg.SearchDefaultMode)
		if mode != "locate" && mode != "full" {
			return mcpsdk.NewToolResultError("mode must be 'locate' or 'full'"), nil
		}

		maxResults := req.GetInt("max_results", cfg.SearchMaxResults)
		if maxResults < 1 || maxResults > 200 {
			return mcpsdk.NewToolResultError("max_results must be between 1 and 200"), nil
		}

		minScore := req.GetFloat("min_score", cfg.SearchMinScore)
		if minScore < 0.0 || minScore > 1.0 {
			return mcpsdk.NewToolResultError("min_score must be between 0.0 and 1.0"), nil
		}

		explain := req.GetBool("explain", false)
		pathFilter := req.GetString("path", "")

		// Over-fetch when path filter is set to compensate for post-filtering.
		engineMax := maxResults
		if pathFilter != "" {
			engineMax = maxResults * 3
			if engineMax > 200 {
				engineMax = 200
			}
		}

		results, err := engine.Search(ctx, core.SearchInput{
			Query:      query,
			MaxResults: engineMax,
			Explain:    explain,
			MinScore:   minScore,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("search failed: ", err)), nil
		}

		// Apply path prefix filter if specified.
		if pathFilter != "" {
			filtered := results[:0]
			for _, r := range results {
				if strings.HasPrefix(r.Path, pathFilter) {
					filtered = append(filtered, r)
				}
			}
			results = filtered
			if len(results) > maxResults {
				results = results[:maxResults]
			}
		}

		var text string
		if mode == "locate" {
			text = formatLocateResults(results)
		} else {
			text = formatSearchResults(results, explain)
		}

		return withResultCount(mcpsdk.NewToolResultText(text), len(results)), nil
	}
}

// contextToolDef defines the MCP context tool schema.
func contextToolDef(cfg types.Config) mcpsdk.Tool {
	return mcpsdk.NewTool("context",
		mcpsdk.WithDescription(fmt.Sprintf(
			`Assemble a cross-file context overview fitted to a token budget.
Use this INSTEAD of reading multiple files — returns only the relevant chunks, ranked and deduplicated.
Returns ranked code chunks for multi-file understanding within a strict token budget.
Use smaller budgets (1024-2048) for focused queries. Default budget: %d tokens.
For single-file reading, prefer the Read tool instead.`, cfg.ContextBudgetTokens)),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("What you need context for"),
		),
		mcpsdk.WithNumber("budget_tokens",
			mcpsdk.Description(fmt.Sprintf("Token budget (256-32768, default %d)", cfg.ContextBudgetTokens)),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

// contextHandler returns an MCP tool handler for the context tool.
func contextHandler(engine *core.QueryEngine, cfg types.Config) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: query"), nil
		}

		if len(query) > 10000 {
			return mcpsdk.NewToolResultError("query exceeds maximum length of 10,000 characters"), nil
		}

		budget := req.GetInt("budget_tokens", cfg.ContextBudgetTokens)
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

		resultCount := 0
		if pkg != nil {
			resultCount = len(pkg.Chunks)
		}

		return withResultCount(mcpsdk.NewToolResultText(formatContextPackage(pkg)), resultCount), nil
	}
}

// ── symbols tool ──

func symbolsToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("symbols",
		mcpsdk.WithDescription(
			`Look up symbols (functions, types, classes, methods) by exact name.
Use this INSTEAD of Grep for finding definitions — returns structured data with file path, line, signature, and visibility.
More precise than text search: finds the definition, not every mention.`),
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

		var results []format.SymbolResult
		for _, s := range syms {
			if kindFilter != "" && s.Kind != kindFilter {
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

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}
		return withResultCount(mcpsdk.NewToolResultText(string(data)), len(results)), nil
	}
}

// ── dependencies tool ──

func dependenciesToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("dependencies",
		mcpsdk.WithDescription(
			`Show callers/callees of a symbol using the pre-built dependency graph.
No equivalent in built-in tools — traces call chains across files instantly.
Use to understand how a function is used or what it depends on.`),
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

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
		}
		return withResultCount(mcpsdk.NewToolResultText(string(data)), len(results)), nil
	}
}

// ── diff tool ──

func diffToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("diff",
		mcpsdk.WithDescription(
			`Show recent file changes and which symbols were added, modified, or removed.
More structured than git log — returns symbol-level change tracking with timestamps.
Use to understand what changed recently without parsing raw diffs.`),
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
		return withResultCount(mcpsdk.NewToolResultText(string(data)), len(results)), nil
	}
}

// ── enrichment_status tool ──

func enrichmentStatusToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("enrichment_status",
		mcpsdk.WithDescription(
			`Show embedding and indexing progress.
Use to check if semantic search is available and how complete the index is.
Returns chunk counts, embedding percentage, and circuit breaker state.`),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func enrichmentStatusHandler(store *storage.Store, vs types.VectorStore, ew *vector.EmbedWorker) handlerFunc {
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
		return withResultCount(mcpsdk.NewToolResultText(string(data)), 1), nil
	}
}

// ── summary tool ──

func summaryToolDef() mcpsdk.Tool {
	return mcpsdk.NewTool("summary",
		mcpsdk.WithDescription(
			`Show workspace overview: files, languages, symbols, and index health.
Use this to understand codebase structure before searching.
Returns total files, chunks, symbols, language breakdown, and index quality metrics.`),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

func summaryHandler(store *storage.Store, vs types.VectorStore) handlerFunc {
	return func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		stats, err := store.GetIndexStats(ctx)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("stats query failed: ", err)), nil
		}

		vectorCount := 0
		if vs != nil {
			vectorCount, _ = vs.Count(ctx)
		}

		embeddingPct := 0.0
		if stats.TotalChunks > 0 {
			embeddingPct = float64(vectorCount) / float64(stats.TotalChunks) * 100
		}

		type summaryResult struct {
			TotalFiles   int            `json:"total_files"`
			TotalChunks  int            `json:"total_chunks"`
			TotalSymbols int            `json:"total_symbols"`
			Languages    map[string]int `json:"languages"`
			EmbeddingPct float64        `json:"embedding_pct"`
			ParseErrors  int            `json:"parse_errors"`
			StaleFiles   int            `json:"stale_files"`
		}

		result := summaryResult{
			TotalFiles:   stats.TotalFiles,
			TotalChunks:  stats.TotalChunks,
			TotalSymbols: stats.TotalSymbols,
			Languages:    stats.Languages,
			EmbeddingPct: embeddingPct,
			ParseErrors:  stats.ParseErrors,
			StaleFiles:   stats.StaleFiles,
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("marshal summary: ", err)), nil
		}
		return withResultCount(mcpsdk.NewToolResultText(string(data)), 1), nil
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
