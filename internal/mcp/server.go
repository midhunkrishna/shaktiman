package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// NewServerInput configures the MCP server. CS-5: >2 args → input struct.
type NewServerInput struct {
	Engine      *core.QueryEngine
	Store       *storage.Store
	VectorStore *vector.BruteForceStore
	EmbedWorker *vector.EmbedWorker
	Recorder    *MetricsRecorder // nil disables metrics persistence
}

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(input NewServerInput) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.2.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	wrap := func(name string, h handlerFunc) handlerFunc {
		return withMetrics(name, input.Recorder, h)
	}

	// Register tools
	s.AddTool(searchToolDef(), wrap("search", searchHandler(input.Engine)))
	s.AddTool(contextToolDef(), wrap("context", contextHandler(input.Engine)))
	s.AddTool(symbolsToolDef(), wrap("symbols", symbolsHandler(input.Store)))
	s.AddTool(dependenciesToolDef(), wrap("dependencies", dependenciesHandler(input.Store)))
	s.AddTool(diffToolDef(), wrap("diff", diffHandler(input.Store)))
	s.AddTool(enrichmentStatusToolDef(), wrap("enrichment_status", enrichmentStatusHandler(input.Store, input.VectorStore, input.EmbedWorker)))

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(input.Store))

	return s
}
