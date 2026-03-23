package mcp

import (
	"fmt"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// chunkHeader builds a single chunk header line like:
// --- path/to/file.go:33-53 (NewServer, function) ---
func chunkHeader(r types.ScoredResult, showPath bool, explain bool) string {
	var b strings.Builder
	b.WriteString("--- ")

	if showPath {
		b.WriteString(r.Path)
	}
	fmt.Fprintf(&b, ":%d-%d", r.StartLine, r.EndLine)

	var parts []string
	if r.SymbolName != "" {
		parts = append(parts, r.SymbolName)
	}
	if r.Kind != "" {
		parts = append(parts, r.Kind)
	}
	if explain {
		parts = append(parts, fmt.Sprintf("score:%.2f", r.Score))
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}

	b.WriteString(" ---")
	return b.String()
}

// formatSearchResults renders search results as plain text with lightweight headers.
// When explain is true, scores are included in the header line.
func formatSearchResults(results []types.ScoredResult, explain bool) string {
	if len(results) == 0 {
		return "No results found.\n"
	}

	var b strings.Builder
	lastPath := ""

	for i, r := range results {
		showPath := r.Path != lastPath
		b.WriteString(chunkHeader(r, showPath, explain))
		b.WriteByte('\n')

		if r.Content != "" {
			b.WriteString(r.Content)
			// Ensure content ends with newline
			if r.Content[len(r.Content)-1] != '\n' {
				b.WriteByte('\n')
			}
		}

		// Blank line between chunks
		if i < len(results)-1 {
			b.WriteByte('\n')
		}

		lastPath = r.Path
	}

	return b.String()
}

// formatLocateResults renders search results as compact one-line-per-result headers
// without source code content. Use for locate mode where Claude Code only needs
// file locations for subsequent Read calls.
func formatLocateResults(results []types.ScoredResult) string {
	if len(results) == 0 {
		return "No results found.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d results:\n", len(results))

	for _, r := range results {
		b.WriteString(r.Path)
		fmt.Fprintf(&b, ":%d-%d", r.StartLine, r.EndLine)

		if r.SymbolName != "" {
			fmt.Fprintf(&b, "  %s", r.SymbolName)
		}
		if r.Kind != "" {
			fmt.Fprintf(&b, " (%s)", r.Kind)
		}
		fmt.Fprintf(&b, "  score:%.2f", r.Score)

		if r.TokenCount > 0 {
			fmt.Fprintf(&b, "  ~%d tokens", r.TokenCount)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// formatContextPackage renders a context package as plain text with a summary header
// followed by formatted chunks.
func formatContextPackage(pkg *types.ContextPackage) string {
	if pkg == nil || len(pkg.Chunks) == 0 {
		return "No context assembled.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[context] %d chunks, %d tokens, strategy: %s\n\n",
		len(pkg.Chunks), pkg.TotalTokens, pkg.Strategy)
	b.WriteString(formatSearchResults(pkg.Chunks, false))
	return b.String()
}
