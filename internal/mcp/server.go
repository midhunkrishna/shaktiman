package mcp

import (
	"context"
	"log/slog"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// withLogging wraps an MCP tool handler with request/response logging.
func withLogging(name string, h handlerFunc) handlerFunc {
	logger := slog.Default().With("component", "mcp")
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		start := time.Now()
		result, err := h(ctx, req)
		isErr := err != nil || (result != nil && result.IsError)
		logger.Info("tool call",
			"tool", name,
			"duration_ms", time.Since(start).Milliseconds(),
			"is_error", isErr,
		)
		return result, err
	}
}

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(engine *core.QueryEngine, store *storage.Store, vs *vector.BruteForceStore, ew *vector.EmbedWorker) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.2.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	// Register tools
	s.AddTool(searchToolDef(), withLogging("search", searchHandler(engine)))
	s.AddTool(contextToolDef(), withLogging("context", contextHandler(engine)))
	s.AddTool(symbolsToolDef(), withLogging("symbols", symbolsHandler(store)))
	s.AddTool(dependenciesToolDef(), withLogging("dependencies", dependenciesHandler(store)))
	s.AddTool(diffToolDef(), withLogging("diff", diffHandler(store)))
	s.AddTool(enrichmentStatusToolDef(), withLogging("enrichment_status", enrichmentStatusHandler(store, vs, ew)))

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(store))

	return s
}
