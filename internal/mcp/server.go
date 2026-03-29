package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// NewServerInput configures the MCP server. CS-5: >2 args → input struct.
type NewServerInput struct {
	Engine      *core.QueryEngine
	Store       *storage.Store
	VectorStore types.VectorStore
	EmbedWorker *vector.EmbedWorker
	Recorder    *MetricsRecorder // nil disables metrics persistence
	Config      types.Config     // MCP tool configuration
}

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(input NewServerInput) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.6.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	cfg := input.Config

	wrap := func(name string, h handlerFunc) handlerFunc {
		return withMetrics(name, input.Recorder, h)
	}

	// Register tools
	s.AddTool(searchToolDef(cfg), wrap("search", searchHandler(input.Engine, cfg)))
	if cfg.ContextEnabled {
		s.AddTool(contextToolDef(cfg), wrap("context", contextHandler(input.Engine, cfg)))
	}
	s.AddTool(symbolsToolDef(), wrap("symbols", symbolsHandler(input.Store)))
	s.AddTool(dependenciesToolDef(), wrap("dependencies", dependenciesHandler(input.Store)))
	s.AddTool(diffToolDef(), wrap("diff", diffHandler(input.Store)))
	s.AddTool(enrichmentStatusToolDef(), wrap("enrichment_status", enrichmentStatusHandler(input.Store, input.VectorStore, input.EmbedWorker)))
	s.AddTool(summaryToolDef(), wrap("summary", summaryHandler(input.Store, input.VectorStore)))

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(input.Store))

	return s
}
