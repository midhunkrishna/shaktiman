package mcp

import (
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// serverInstructions is returned to the client during MCP initialize.
// It tells the LLM how shaktiman reduces context usage during exploration.
const serverInstructions = `Shaktiman is a pre-built code index that reduces context usage during codebase exploration.

Instead of searching and reading many files to find what's relevant, use shaktiman to narrow down first:

- summary → orient in an unfamiliar codebase (size, languages, health) without reading files
- search → find the most relevant files for a topic, then Read only those files
- context → understand a topic across files within a token budget, instead of reading many files
- symbols → find a definition by exact name without reading the whole file
- dependencies → trace a call chain in one call instead of searching and reading repeatedly

All tools exclude test files by default. Use scope="test" when you need test code.
Use Grep for exact string or regex matching. Use Glob for finding files by name.`

// NewServerInput configures the MCP server. CS-5: >2 args → input struct.
type NewServerInput struct {
	Engine      *core.QueryEngine
	Store       types.WriterStore
	VectorStore types.VectorStore
	EmbedWorker *vector.EmbedWorker
	Recorder    *MetricsRecorder // nil disables metrics persistence
	Config      types.Config     // MCP tool configuration
}

// toolEntry binds a tool's name to its definition and handler factories.
// Each tool name lives in exactly one place; adding a new tool means
// appending one entry. Optional gate disables registration based on config
// (context tool is gated on cfg.ContextEnabled).
type toolEntry struct {
	name  string
	gate  func(types.Config) bool // nil → always enabled
	build func(NewServerInput) (mcpsdk.Tool, handlerFunc)
}

// toolRegistry returns the canonical list of MCP tools shaktiman exposes.
// Order is preserved in registration so clients see tools in a stable order.
func toolRegistry() []toolEntry {
	return []toolEntry{
		{name: "search", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return searchToolDef(in.Config), searchHandler(in.Engine, in.Config)
		}},
		{
			name: "context",
			gate: func(c types.Config) bool { return c.ContextEnabled },
			build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
				return contextToolDef(in.Config), contextHandler(in.Engine, in.Config)
			},
		},
		{name: "symbols", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return symbolsToolDef(), symbolsHandler(in.Store)
		}},
		{name: "dependencies", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return dependenciesToolDef(), dependenciesHandler(in.Store)
		}},
		{name: "diff", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return diffToolDef(), diffHandler(in.Store)
		}},
		{name: "enrichment_status", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return enrichmentStatusToolDef(), enrichmentStatusHandler(in.Store, in.VectorStore, in.EmbedWorker)
		}},
		{name: "summary", build: func(in NewServerInput) (mcpsdk.Tool, handlerFunc) {
			return summaryToolDef(), summaryHandler(in.Store, in.VectorStore)
		}},
	}
}

// NewServer creates a configured MCP server with tools and resources registered.
func NewServer(input NewServerInput) *server.MCPServer {
	s := server.NewMCPServer(
		"shaktiman",
		"0.9.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithInstructions(serverInstructions),
	)

	for _, e := range toolRegistry() {
		if e.gate != nil && !e.gate(input.Config) {
			continue
		}
		def, h := e.build(input)
		s.AddTool(def, withMetrics(e.name, input.Recorder, h))
	}

	// Register resources
	s.AddResource(workspaceSummaryDef(), workspaceSummaryHandler(input.Store))

	return s
}
