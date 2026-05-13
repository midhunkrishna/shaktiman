// Package format provides shared text formatters for search, context,
// symbol, dependency, diff, and stats results. Used by both the CLI
// and the MCP server.
package format

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

// SearchResults renders search results as plain text with lightweight headers.
// When explain is true, scores are included in the header line.
func SearchResults(results []types.ScoredResult, explain bool) string {
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
			if r.Content[len(r.Content)-1] != '\n' {
				b.WriteByte('\n')
			}
		}

		if i < len(results)-1 {
			b.WriteByte('\n')
		}

		lastPath = r.Path
	}

	return b.String()
}

// listSection assembles a list-style section: a single "N <label>:\n"
// header followed by rows produced by `row` for each item, or emptyMsg
// (with a trailing newline) when items is empty. Four list-style
// formatters used to repeat this exact shape — empty guard, count
// header, write loop — with only the label and row body differing.
func listSection[T any](items []T, emptyMsg, label string, row func(b *strings.Builder, item T)) string {
	if len(items) == 0 {
		return emptyMsg + "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s:\n", len(items), label)
	for _, item := range items {
		row(&b, item)
	}
	return b.String()
}

// LocateResults renders search results as compact one-line-per-result headers
// without source code content.
func LocateResults(results []types.ScoredResult) string {
	return listSection(results, "No results found.", "results", func(b *strings.Builder, r types.ScoredResult) {
		b.WriteString(r.Path)
		fmt.Fprintf(b, ":%d-%d", r.StartLine, r.EndLine)
		if r.SymbolName != "" {
			fmt.Fprintf(b, "  %s", r.SymbolName)
		}
		if r.Kind != "" {
			fmt.Fprintf(b, " (%s)", r.Kind)
		}
		fmt.Fprintf(b, "  score:%.2f", r.Score)
		if r.TokenCount > 0 {
			fmt.Fprintf(b, "  ~%d tokens", r.TokenCount)
		}
		b.WriteByte('\n')
	})
}

// ContextPackage renders a context package as plain text with a summary header
// followed by formatted chunks.
func ContextPackage(pkg *types.ContextPackage) string {
	if pkg == nil || len(pkg.Chunks) == 0 {
		return "No context assembled.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[context] %d chunks, %d tokens, strategy: %s\n\n",
		len(pkg.Chunks), pkg.TotalTokens, pkg.Strategy)
	b.WriteString(SearchResults(pkg.Chunks, false))
	return b.String()
}

// Symbols renders symbol lookup results as plain text, one line per symbol.
func Symbols(results []SymbolResult) string {
	return listSection(results, "No symbols found.", "symbols", func(b *strings.Builder, s SymbolResult) {
		fmt.Fprintf(b, "  %s  %s  %s:%d", s.Name, s.Kind, s.FilePath, s.Line)
		if s.Signature != "" {
			fmt.Fprintf(b, "  %s", s.Signature)
		}
		if s.Visibility != "" {
			fmt.Fprintf(b, "  [%s]", s.Visibility)
		}
		b.WriteByte('\n')
	})
}

// Dependencies renders dependency results as plain text, one line per dep.
func Dependencies(results []DepResult) string {
	return listSection(results, "No dependencies found.", "dependencies", func(b *strings.Builder, d DepResult) {
		fmt.Fprintf(b, "  %s  %s  %s:%d\n", d.Name, d.Kind, d.FilePath, d.Line)
	})
}

// Diffs renders diff results as plain text with affected symbols indented below.
func Diffs(results []DiffResult) string {
	return listSection(results, "No diffs found.", "diffs", func(b *strings.Builder, d DiffResult) {
		fmt.Fprintf(b, "  %s  %s  +%d -%d  %s\n",
			d.FilePath, d.ChangeType, d.LinesAdded, d.LinesRemoved, d.Timestamp)
		for _, sym := range d.Symbols {
			fmt.Fprintf(b, "    %s\n", sym)
		}
	})
}

// IndexStats renders index statistics as plain text.
func IndexStats(stats *types.IndexStats) string {
	if stats == nil {
		return "No index stats available.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Files:   %d\n", stats.TotalFiles)
	fmt.Fprintf(&b, "Chunks:  %d\n", stats.TotalChunks)
	fmt.Fprintf(&b, "Symbols: %d\n", stats.TotalSymbols)
	fmt.Fprintf(&b, "Errors:  %d\n", stats.ParseErrors)
	fmt.Fprintf(&b, "Stale:   %d\n", stats.StaleFiles)

	if len(stats.Languages) > 0 {
		b.WriteString("Languages:\n")
		for lang, count := range stats.Languages {
			fmt.Fprintf(&b, "  %s: %d\n", lang, count)
		}
	}

	return b.String()
}
