// Package mcp provides the MCP stdio server, tool handlers, and resource handlers.
package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/core"
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
			return mcpsdk.NewToolResultError("search failed: " + err.Error()), nil
		}

		data, err := json.Marshal(results)
		if err != nil {
			return mcpsdk.NewToolResultError("marshal results: " + err.Error()), nil
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
			return mcpsdk.NewToolResultError("context assembly failed: " + err.Error()), nil
		}

		data, err := json.Marshal(pkg)
		if err != nil {
			return mcpsdk.NewToolResultError("marshal context: " + err.Error()), nil
		}

		return mcpsdk.NewToolResultText(string(data)), nil
	}
}

// handlerFunc matches the MCP server.ToolHandlerFunc signature.
type handlerFunc = func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)
