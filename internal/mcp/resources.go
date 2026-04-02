package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// workspaceSummaryDef defines the workspace summary MCP resource.
func workspaceSummaryDef() mcpsdk.Resource {
	return mcpsdk.NewResource(
		"shaktiman://workspace/summary",
		"Workspace Summary",
		mcpsdk.WithResourceDescription("Overview of indexed codebase: files, symbols, languages, health"),
		mcpsdk.WithMIMEType("application/json"),
	)
}

// workspaceSummaryHandler returns a resource handler for the workspace summary.
func workspaceSummaryHandler(store types.WriterStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error) {
		stats, err := store.GetIndexStats(ctx)
		if err != nil {
			return nil, err
		}

		data, err := json.Marshal(stats)
		if err != nil {
			return nil, err
		}

		return []mcpsdk.ResourceContents{
			mcpsdk.TextResourceContents{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}
