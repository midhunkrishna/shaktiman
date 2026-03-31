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
			`Ranked code discovery. Narrows a large codebase to the most relevant files for a query.
Use to decide what to Read — avoids spending context on files that turn out irrelevant.
Best for conceptual queries: "error handling", "auth flow", "database setup".
Default "locate" mode returns compact pointers (~12 tokens/result). Use mode="full" for inline source.
Excludes test files by default. Use scope="test" for test files only.
For exact strings or regex, use Grep.`),
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
		mcpsdk.WithString("scope",
			mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
			mcpsdk.Enum("impl", "test", "all"),
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	)
}

// scopeToFilter converts a scope string ("impl", "test", "all") to core.TestFilter flags.
func scopeToFilter(scope string) (excludeTests, testOnly bool) {
	switch scope {
	case "test":
		return false, true
	case "all":
		return false, false
	default: // "impl"
		return true, false
	}
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
		scope := req.GetString("scope", "impl")
		excludeTests, testOnly := scopeToFilter(scope)

		// Over-fetch when path or scope filter is set to compensate for post-filtering.
		engineMax := maxResults
		if pathFilter != "" {
			engineMax = maxResults * 3
			if engineMax > 200 {
				engineMax = 200
			}
		}

		results, err := engine.Search(ctx, core.SearchInput{
			Query:        query,
			MaxResults:   engineMax,
			Explain:      explain,
			MinScore:     minScore,
			ExcludeTests: excludeTests,
			TestOnly:     testOnly,
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
			`Cross-file understanding within a token budget.
Instead of reading many files to understand a topic, returns only the relevant chunks — ranked, deduplicated, fitted to your budget.
Use smaller budgets (1024-2048) for focused queries. Default budget: %d tokens.
Excludes test files by default. Use scope="test" for test files only.
For reading a specific known file, use Read.`, cfg.ContextBudgetTokens)),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("What you need context for"),
		),
		mcpsdk.WithNumber("budget_tokens",
			mcpsdk.Description(fmt.Sprintf("Token budget (256-32768, default %d)", cfg.ContextBudgetTokens)),
		),
		mcpsdk.WithString("scope",
			mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
			mcpsdk.Enum("impl", "test", "all"),
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

		scope := req.GetString("scope", "impl")
		excludeTests, testOnly := scopeToFilter(scope)

		pkg, err := engine.Context(ctx, core.ContextInput{
			Query:        query,
			BudgetTokens: budget,
			ExcludeTests: excludeTests,
			TestOnly:     testOnly,
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
			`Find where a function, type, or class is defined by exact name.
Returns file path, line number, signature, and visibility — without reading the whole file.
Excludes test files by default. Use scope="test" for test files only.
For finding every mention of a string (not just definitions), use Grep.`),
		mcpsdk.WithString("name",
			mcpsdk.Required(),
			mcpsdk.Description("Symbol name to search for"),
		),
		mcpsdk.WithString("kind",
			mcpsdk.Description("Optional kind filter: function, class, method, type, interface, variable"),
		),
		mcpsdk.WithString("scope",
			mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
			mcpsdk.Enum("impl", "test", "all"),
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
		scope := req.GetString("scope", "impl")
		excludeTests, testOnly := scopeToFilter(scope)

		var results []format.SymbolResult
		for _, s := range syms {
			if kindFilter != "" && s.Kind != kindFilter {
				continue
			}
			if excludeTests || testOnly {
				isTest, err := store.GetFileIsTestByID(ctx, s.FileID)
				if err != nil {
					continue
				}
				if excludeTests && isTest {
					continue
				}
				if testOnly && !isTest {
					continue
				}
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

		// When the symbol genuinely doesn't exist in the project (not just
		// filtered by kind), check pending_edges for references. External
		// types (e.g. JDK's ExecutorService) are never in the symbols table
		// but may be referenced as imports by project symbols.
		if len(results) == 0 && len(syms) == 0 {
			callers, err := store.PendingEdgeCallersWithKind(ctx, name)
			if err == nil && len(callers) > 0 {
				var refs []format.SymbolRef
				for _, c := range callers {
					sym, err := store.GetSymbolByID(ctx, c.SrcSymbolID)
					if err != nil || sym == nil {
						continue
					}
					if excludeTests || testOnly {
						isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
						if err != nil {
							continue
						}
						if excludeTests && isTest {
							continue
						}
						if testOnly && !isTest {
							continue
						}
					}
					path, _ := store.GetFilePathByID(ctx, sym.FileID)
					refs = append(refs, format.SymbolRef{
						Symbol:        sym.Name,
						Kind:          sym.Kind,
						FilePath:      path,
						Line:          sym.Line,
						Via:           c.Kind,
						QualifiedName: c.DstQualifiedName,
					})
				}
				if len(refs) > 0 {
					enriched := format.SymbolsWithRefs{
						Definitions:  results,
						ReferencedBy: refs,
						Note:         fmt.Sprintf("No definitions for %q in project. Referenced by %d symbol(s). Use 'dependencies direction:callers' or 'search' for more.", name, len(refs)),
					}
					data, err := json.Marshal(enriched)
					if err != nil {
						return mcpsdk.NewToolResultError(sanitizeError("marshal results: ", err)), nil
					}
					return withResultCount(mcpsdk.NewToolResultText(string(data)), len(refs)), nil
				}
			}
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
			`Trace callers or callees of a symbol across the codebase in one call.
Replaces multiple rounds of searching and reading to follow call chains.
Excludes test files by default. Use scope="test" for test files only.
No equivalent in built-in tools.`),
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
		mcpsdk.WithString("scope",
			mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
			mcpsdk.Enum("impl", "test", "all"),
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
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("symbol lookup failed: ", err)), nil
		}

		var neighborIDs []int64
		if len(syms) > 0 {
			symbolID := syms[0].ID
			neighborIDs, err = store.Neighbors(ctx, symbolID, depth, direction)
			if err != nil {
				return mcpsdk.NewToolResultError(sanitizeError("graph query failed: ", err)), nil
			}
		}

		// When no resolved neighbors found and direction includes incoming,
		// also check pending_edges. This covers two cases:
		// 1. Symbol not defined in project (external type like ExecutorService)
		// 2. Symbol exists but edges are still pending (incremental indexing)
		if len(neighborIDs) == 0 && (direction == "incoming" || direction == "both") {
			pendingIDs, err := store.PendingEdgeCallers(ctx, symbolName)
			if err != nil {
				return mcpsdk.NewToolResultError(sanitizeError("pending edge lookup failed: ", err)), nil
			}
			neighborIDs = append(neighborIDs, pendingIDs...)
		}

		if len(neighborIDs) == 0 {
			return withResultCount(mcpsdk.NewToolResultText("[]"), 0), nil
		}

		scope := req.GetString("scope", "impl")
		excludeTests, testOnly := scopeToFilter(scope)

		var results []format.DepResult
		for _, nID := range neighborIDs {
			sym, err := store.GetSymbolByID(ctx, nID)
			if err != nil || sym == nil {
				continue
			}
			if excludeTests || testOnly {
				isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
				if err != nil {
					continue
				}
				if excludeTests && isTest {
					continue
				}
				if testOnly && !isTest {
					continue
				}
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
Complements git log with structured, symbol-level change tracking.
Excludes test files by default. Use scope="test" for test files only.
Use to see which definitions were affected by recent changes.`),
		mcpsdk.WithString("since",
			mcpsdk.Description("Time window, e.g. '24h', '1h', '30m' (default: 24h)"),
		),
		mcpsdk.WithNumber("limit",
			mcpsdk.Description("Maximum diffs to return (default: 50)"),
		),
		mcpsdk.WithString("scope",
			mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
			mcpsdk.Enum("impl", "test", "all"),
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

		scope := req.GetString("scope", "impl")
		excludeTests, testOnly := scopeToFilter(scope)

		var results []format.DiffResult
		for _, d := range diffs {
			if excludeTests || testOnly {
				isTest, err := store.GetFileIsTestByID(ctx, d.FileID)
				if err != nil {
					continue
				}
				if excludeTests && isTest {
					continue
				}
				if testOnly && !isTest {
					continue
				}
			}
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
			`Quick codebase snapshot: file count, languages, symbol count, and index health.
Start here to orient in an unfamiliar codebase — gives structure at a glance without reading files.`),
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
