package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
)

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(engine *core.QueryEngine, store *storage.Store) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	// Register tools
	s.AddTool(searchToolDef(), searchHandler(engine))
	s.AddTool(contextToolDef(), contextHandler(engine))

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(store))

	return s
}
