// Package mcp provides the MCP stdio server, tool handlers, and resource handlers.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// readonlyToolOpts is the standard annotation block used by every
// MCP tool shaktiman exposes. Tools are read-only, non-destructive,
// and idempotent; centralising the options here means one new tool
// can't accidentally drop one of these annotations.
func readonlyToolOpts() []mcpsdk.ToolOption {
	return []mcpsdk.ToolOption{
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
	}
}

// scopeOption returns the standard `scope` parameter declaration used
// by every tool that accepts a scope filter. Pulls the enum values
// from AllScopes() so the description text and validator can never
// drift apart.
func scopeOption() mcpsdk.ToolOption {
	return mcpsdk.WithString("scope",
		mcpsdk.Description(`Result scope: "impl" (default, excludes test files), "test" (test files only), or "all"`),
		mcpsdk.Enum(AllScopes()...),
	)
}

// buildTool assembles a tool definition with name, description, and
// the standard read-only/non-destructive/idempotent annotations.
// Caller-supplied opts are appended after the description so per-tool
// parameter declarations slot in naturally.
func buildTool(name, description string, opts ...mcpsdk.ToolOption) mcpsdk.Tool {
	out := make([]mcpsdk.ToolOption, 0, len(opts)+4)
	out = append(out, mcpsdk.WithDescription(description))
	out = append(out, opts...)
	out = append(out, readonlyToolOpts()...)
	return mcpsdk.NewTool(name, out...)
}

// searchToolDef defines the MCP search tool schema.
func searchToolDef(cfg types.Config) mcpsdk.Tool {
	return buildTool("search",
		`Ranked code discovery. Narrows a large codebase to the most relevant files for a query.
Use to decide what to Read — avoids spending context on files that turn out irrelevant.
Best for conceptual queries: "error handling", "auth flow", "database setup".
Default "locate" mode returns compact pointers (~12 tokens/result). Use mode="full" for inline source.
Excludes test files by default. Use scope="test" for test files only.
For exact strings or regex, use Grep.`,
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("Search query text"),
		),
		mcpsdk.WithString("mode",
			mcpsdk.Description(fmt.Sprintf(`Result mode: "locate" (headers only) or "full" (with source code). Default: %q`, cfg.SearchDefaultMode)),
			mcpsdk.Enum(string(ModeLocate), string(ModeFull)),
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
		scopeOption(),
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
		filter := core.ScopeToFilter(req.GetString("scope", "impl"))

		results, err := engine.Search(ctx, core.SearchInput{
			Query:        query,
			MaxResults:   core.SearchOverfetchLimit(maxResults, pathFilter != ""),
			Explain:      explain,
			MinScore:     minScore,
			ExcludeTests: filter.ExcludeTests,
			TestOnly:     filter.TestOnly,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("search failed: ", err)), nil
		}

		results = core.FilterByPathPrefix(results, pathFilter, maxResults)

		var text string
		if mode == "locate" {
			text = formatLocateResults(results)
		} else {
			text = formatSearchResults(results, explain)
		}

		return withResultCount(ctx, mcpsdk.NewToolResultText(text), len(results)), nil
	}
}

// contextToolDef defines the MCP context tool schema.
func contextToolDef(cfg types.Config) mcpsdk.Tool {
	return buildTool("context",
		fmt.Sprintf(`Cross-file understanding within a token budget.
Instead of reading many files to understand a topic, returns only the relevant chunks — ranked, deduplicated, fitted to your budget.
Use smaller budgets (1024-2048) for focused queries. Default budget: %d tokens.
Excludes test files by default. Use scope="test" for test files only.
For reading a specific known file, use Read.`, cfg.ContextBudgetTokens),
		mcpsdk.WithString("query",
			mcpsdk.Required(),
			mcpsdk.Description("What you need context for"),
		),
		mcpsdk.WithNumber("budget_tokens",
			mcpsdk.Description(fmt.Sprintf("Token budget (256-32768, default %d)", cfg.ContextBudgetTokens)),
		),
		scopeOption(),
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

		filter := core.ScopeToFilter(req.GetString("scope", "impl"))

		pkg, err := engine.Context(ctx, core.ContextInput{
			Query:        query,
			BudgetTokens: budget,
			ExcludeTests: filter.ExcludeTests,
			TestOnly:     filter.TestOnly,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("context assembly failed: ", err)), nil
		}

		resultCount := 0
		if pkg != nil {
			resultCount = len(pkg.Chunks)
		}

		return withResultCount(ctx, mcpsdk.NewToolResultText(formatContextPackage(pkg)), resultCount), nil
	}
}

// ── symbols tool ──

func symbolsToolDef() mcpsdk.Tool {
	return buildTool("symbols",
		`Find where a function, type, or class is defined by exact name.
Returns file path, line number, signature, and visibility — without reading the whole file.
Excludes test files by default. Use scope="test" for test files only.
For finding every mention of a string (not just definitions), use Grep.`,
		mcpsdk.WithString("name",
			mcpsdk.Required(),
			mcpsdk.Description("Symbol name to search for"),
		),
		mcpsdk.WithString("kind",
			mcpsdk.Description("Optional kind filter: function, class, method, type, interface, variable"),
		),
		scopeOption(),
	)
}

func symbolsHandler(store types.WriterStore) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: name"), nil
		}

		kind := req.GetString("kind", "")
		filter := core.ScopeToFilter(req.GetString("scope", "impl"))

		result, err := core.LookupSymbols(ctx, store, name, kind, filter)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("symbol lookup failed: ", err)), nil
		}

		if len(result.ReferencedBy) > 0 {
			enriched := format.SymbolsWithRefs{
				Definitions:  result.Definitions,
				ReferencedBy: result.ReferencedBy,
				Note:         result.Note,
			}
			return jsonResult(ctx, enriched, len(result.ReferencedBy), "marshal results: "), nil
		}
		return jsonResult(ctx, result.Definitions, len(result.Definitions), "marshal results: "), nil
	}
}

// ── dependencies tool ──

func dependenciesToolDef() mcpsdk.Tool {
	return buildTool("dependencies",
		`Trace callers or callees of a symbol across the codebase in one call.
Replaces multiple rounds of searching and reading to follow call chains.
Excludes test files by default. Use scope="test" for test files only.
No equivalent in built-in tools.`,
		mcpsdk.WithString("symbol",
			mcpsdk.Required(),
			mcpsdk.Description("Symbol name to find dependencies for"),
		),
		mcpsdk.WithString("direction",
			mcpsdk.Description("Direction: callers, callees, or both (default: both)"),
			mcpsdk.Enum(AllDirections()...),
		),
		mcpsdk.WithNumber("depth",
			mcpsdk.Description("BFS depth 1-5 (default 2)"),
		),
		scopeOption(),
	)
}

func dependenciesHandler(store types.WriterStore) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		symbolName, err := req.RequireString("symbol")
		if err != nil {
			return mcpsdk.NewToolResultError("missing required parameter: symbol"), nil
		}

		dirRaw := req.GetString("direction", string(DirectionBoth))
		direction, ok := core.NormalizeDirection(dirRaw)
		if !ok {
			return mcpsdk.NewToolResultError("direction must be callers, callees, or both"), nil
		}

		depth := req.GetInt("depth", 2)
		if depth < 1 || depth > 5 {
			return mcpsdk.NewToolResultError("depth must be between 1 and 5"), nil
		}

		filter := core.ScopeToFilter(req.GetString("scope", "impl"))

		results, err := core.LookupDependencies(ctx, store, symbolName, direction, depth, filter)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("dependency lookup failed: ", err)), nil
		}
		return jsonResult(ctx, results, len(results), "marshal results: "), nil
	}
}

// ── diff tool ──

func diffToolDef() mcpsdk.Tool {
	return buildTool("diff",
		`Show recent file changes and which symbols were added, modified, or removed.
Complements git log with structured, symbol-level change tracking.
Excludes test files by default. Use scope="test" for test files only.
Use to see which definitions were affected by recent changes.`,
		mcpsdk.WithString("since",
			mcpsdk.Description("Time window, e.g. '24h', '1h', '30m' (default: 24h)"),
		),
		mcpsdk.WithNumber("limit",
			mcpsdk.Description("Maximum diffs to return (default: 50)"),
		),
		scopeOption(),
	)
}

func diffHandler(store types.WriterStore) handlerFunc {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		sinceStr := req.GetString("since", "24h")
		duration, err := core.ValidateDiffWindow(sinceStr)
		if err != nil {
			return mcpsdk.NewToolResultError("invalid since: " + err.Error()), nil
		}

		limit := req.GetInt("limit", 50)
		if err := core.ValidateDiffLimit(limit); err != nil {
			return mcpsdk.NewToolResultError("invalid limit: " + err.Error()), nil
		}

		filter := core.ScopeToFilter(req.GetString("scope", "impl"))
		since := time.Now().Add(-duration)

		results, err := core.LookupDiffs(ctx, store, since, limit, filter)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("diff query failed: ", err)), nil
		}
		return jsonResult(ctx, results, len(results), "marshal results: "), nil
	}
}

// ── enrichment_status tool ──

func enrichmentStatusToolDef() mcpsdk.Tool {
	return buildTool("enrichment_status",
		`Show embedding and indexing progress.
Use to check if semantic search is available and how complete the index is.
Returns chunk counts, embedding percentage, and circuit breaker state.`)
}

func enrichmentStatusHandler(store types.WriterStore, vs types.VectorStore, ew *vector.EmbedWorker) handlerFunc {
	return func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		pendingFn := func() int { return 0 }
		circuitFn := func() string { return "disabled" }
		if ew != nil {
			pendingFn = ew.Pending
			circuitFn = func() string {
				switch ew.CircuitBreaker().State() {
				case vector.StateClosed:
					return "closed"
				case vector.StateOpen:
					return "open"
				case vector.StateHalfOpen:
					return "half_open"
				default:
					return "disabled"
				}
			}
		}

		result, err := core.GetEnrichmentStatus(ctx, core.EnrichmentStatusInput{
			Store:       store,
			VectorStore: vs,
			PendingFn:   pendingFn,
			CircuitFn:   circuitFn,
		})
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("stats query failed: ", err)), nil
		}
		return jsonResult(ctx, result, 1, "marshal status: "), nil
	}
}

// ── summary tool ──

func summaryToolDef() mcpsdk.Tool {
	return buildTool("summary",
		`Quick codebase snapshot: file count, languages, symbol count, and index health.
Start here to orient in an unfamiliar codebase — gives structure at a glance without reading files.`)
}

func summaryHandler(store types.WriterStore, vs types.VectorStore) handlerFunc {
	return func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		result, err := core.GetSummary(ctx, store, vs)
		if err != nil {
			return mcpsdk.NewToolResultError(sanitizeError("summary failed: ", err)), nil
		}
		return jsonResult(ctx, result, 1, "marshal summary: "), nil
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

// jsonResult marshals payload and wraps it as a successful MCP tool result
// tagged with resultCount for metrics. On marshal failure it returns a
// sanitized error result with marshalLabel as the prefix. Most JSON-returning
// handlers end with this exact pattern, so funnelling it here keeps the
// per-handler tail to a single line.
func jsonResult(ctx context.Context, payload any, resultCount int, marshalLabel string) *mcpsdk.CallToolResult {
	data, err := json.Marshal(payload)
	if err != nil {
		return mcpsdk.NewToolResultError(sanitizeError(marshalLabel, err))
	}
	return withResultCount(ctx, mcpsdk.NewToolResultText(string(data)), resultCount)
}
