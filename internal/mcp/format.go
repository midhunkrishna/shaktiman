package mcp

import (
	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// formatSearchResults delegates to the shared format package.
func formatSearchResults(results []types.ScoredResult, explain bool) string {
	return format.SearchResults(results, explain)
}

// formatLocateResults delegates to the shared format package.
func formatLocateResults(results []types.ScoredResult) string {
	return format.LocateResults(results)
}

// formatContextPackage delegates to the shared format package.
func formatContextPackage(pkg *types.ContextPackage) string {
	return format.ContextPackage(pkg)
}
