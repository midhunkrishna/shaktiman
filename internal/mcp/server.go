package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(engine *core.QueryEngine, store *storage.Store, vs *vector.BruteForceStore, ew *vector.EmbedWorker) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.2.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	// Register tools
	s.AddTool(searchToolDef(), searchHandler(engine))
	s.AddTool(contextToolDef(), contextHandler(engine))
	s.AddTool(symbolsToolDef(), symbolsHandler(store))
	s.AddTool(dependenciesToolDef(), dependenciesHandler(store))
	s.AddTool(diffToolDef(), diffHandler(store))
	s.AddTool(enrichmentStatusToolDef(), enrichmentStatusHandler(store, vs, ew))

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(store))

	return s
}
