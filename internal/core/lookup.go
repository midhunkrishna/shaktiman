package core

import (
	"context"
	"fmt"
	"time"
	"unicode"

	"github.com/shaktimanai/shaktiman/internal/format"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// SymbolLookupResult holds the result of a symbol lookup, including
// pending_edges fallback when no definitions are found.
type SymbolLookupResult struct {
	Definitions  []format.SymbolResult
	ReferencedBy []format.SymbolRef // set only when no definitions found
	Note         string             // set only when ReferencedBy is non-empty
}

// LookupSymbols performs symbol lookup with kind/scope filtering and
// pending_edges fallback for external/unresolved symbols.
func LookupSymbols(ctx context.Context, store types.WriterStore, name, kind string, filter TestFilter) (*SymbolLookupResult, error) {
	syms, err := store.GetSymbolByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("symbol lookup %q: %w", name, err)
	}

	var results []format.SymbolResult
	for _, s := range syms {
		if kind != "" && s.Kind != kind {
			continue
		}
		if filter.ExcludeTests || filter.TestOnly {
			isTest, err := store.GetFileIsTestByID(ctx, s.FileID)
			if err != nil {
				continue
			}
			if filter.ExcludeTests && isTest {
				continue
			}
			if filter.TestOnly && !isTest {
				continue
			}
		}
		path, _ := store.GetFilePathByID(ctx, s.FileID)
		results = append(results, format.SymbolResult{
			Name:       s.Name,
			Kind:       s.Kind,
			Line:       s.Line,
			Signature:  s.Signature,
			Visibility: s.Visibility,
			FilePath:   path,
		})
	}

	result := &SymbolLookupResult{Definitions: results}

	// When the symbol genuinely doesn't exist in the project (not just
	// filtered by kind), check pending_edges for references. External
	// types (e.g. JDK's ExecutorService) are never in the symbols table
	// but may be referenced as imports by project symbols.
	if len(results) == 0 && len(syms) == 0 {
		callers, err := store.PendingEdgeCallersWithKind(ctx, name)
		if err == nil && len(callers) > 0 {
			var refs []format.SymbolRef
			for _, c := range callers {
				sym, err := store.GetSymbolByID(ctx, c.SrcSymbolID)
				if err != nil || sym == nil {
					continue
				}
				if filter.ExcludeTests || filter.TestOnly {
					isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
					if err != nil {
						continue
					}
					if filter.ExcludeTests && isTest {
						continue
					}
					if filter.TestOnly && !isTest {
						continue
					}
				}
				path, _ := store.GetFilePathByID(ctx, sym.FileID)
				refs = append(refs, format.SymbolRef{
					Symbol:        sym.Name,
					Kind:          sym.Kind,
					FilePath:      path,
					Line:          sym.Line,
					Via:           c.Kind,
					QualifiedName: c.DstQualifiedName,
				})
			}
			if len(refs) > 0 {
				result.ReferencedBy = refs
				result.Note = fmt.Sprintf("No definitions for %q in project. Referenced by %d symbol(s). Use 'dependencies direction:callers' or 'search' for more.", name, len(refs))
			}
		}
	}

	return result, nil
}

// LookupDependencies traces callers/callees of a symbol with scope filtering
// and pending_edges fallback.
func LookupDependencies(ctx context.Context, store types.WriterStore, symbol, direction string, depth int, filter TestFilter) ([]format.DepResult, error) {
	syms, err := store.GetSymbolByName(ctx, symbol)
	if err != nil {
		return nil, fmt.Errorf("symbol lookup %q: %w", symbol, err)
	}

	var neighborIDs []int64
	if len(syms) > 0 {
		neighborIDs, err = store.Neighbors(ctx, syms[0].ID, depth, direction)
		if err != nil {
			return nil, fmt.Errorf("graph query %q: %w", symbol, err)
		}
	}

	// When no resolved neighbors found and direction includes incoming,
	// also check pending_edges.
	if len(neighborIDs) == 0 && (direction == "incoming" || direction == "both") {
		pendingIDs, err := store.PendingEdgeCallers(ctx, symbol)
		if err != nil {
			return nil, fmt.Errorf("pending edge lookup %q: %w", symbol, err)
		}
		neighborIDs = append(neighborIDs, pendingIDs...)
	}

	results := make([]format.DepResult, 0)
	for _, nID := range neighborIDs {
		sym, err := store.GetSymbolByID(ctx, nID)
		if err != nil || sym == nil {
			continue
		}
		if filter.ExcludeTests || filter.TestOnly {
			isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
			if err != nil {
				continue
			}
			if filter.ExcludeTests && isTest {
				continue
			}
			if filter.TestOnly && !isTest {
				continue
			}
		}
		path, _ := store.GetFilePathByID(ctx, sym.FileID)
		results = append(results, format.DepResult{
			Name:     sym.Name,
			Kind:     sym.Kind,
			FilePath: path,
			Line:     sym.Line,
		})
	}
	return results, nil
}

// LookupDiffs returns recent file changes with affected symbols, filtered by scope.
func LookupDiffs(ctx context.Context, store types.WriterStore, since time.Time, limit int, filter TestFilter) ([]format.DiffResult, error) {
	diffs, err := store.GetRecentDiffs(ctx, types.RecentDiffsInput{
		Since: since,
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("diff query: %w", err)
	}

	var results []format.DiffResult
	for _, d := range diffs {
		if filter.ExcludeTests || filter.TestOnly {
			isTest, err := store.GetFileIsTestByID(ctx, d.FileID)
			if err != nil {
				continue
			}
			if filter.ExcludeTests && isTest {
				continue
			}
			if filter.TestOnly && !isTest {
				continue
			}
		}
		path, _ := store.GetFilePathByID(ctx, d.FileID)
		dr := format.DiffResult{
			FileID:       d.FileID,
			FilePath:     path,
			ChangeType:   d.ChangeType,
			LinesAdded:   d.LinesAdded,
			LinesRemoved: d.LinesRemoved,
			Timestamp:    d.Timestamp,
		}
		dsyms, _ := store.GetDiffSymbols(ctx, d.ID)
		for _, ds := range dsyms {
			dr.Symbols = append(dr.Symbols, ds.SymbolName+" ("+ds.ChangeType+")")
		}
		results = append(results, dr)
	}
	return results, nil
}

// SummaryResult holds codebase summary statistics.
type SummaryResult struct {
	TotalFiles   int            `json:"total_files"`
	TotalChunks  int            `json:"total_chunks"`
	TotalSymbols int            `json:"total_symbols"`
	Languages    map[string]int `json:"languages"`
	EmbeddingPct float64        `json:"embedding_pct"`
	ParseErrors  int            `json:"parse_errors"`
	StaleFiles   int            `json:"stale_files"`
}

// GetSummary returns codebase summary statistics.
func GetSummary(ctx context.Context, store types.WriterStore, vs types.VectorStore) (*SummaryResult, error) {
	stats, err := store.GetIndexStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("stats query: %w", err)
	}

	vectorCount := 0
	if vs != nil {
		vectorCount, _ = vs.Count(ctx)
	}
	embeddingPct := 0.0
	if stats.TotalChunks > 0 {
		embeddingPct = float64(vectorCount) / float64(stats.TotalChunks) * 100
	}

	return &SummaryResult{
		TotalFiles:   stats.TotalFiles,
		TotalChunks:  stats.TotalChunks,
		TotalSymbols: stats.TotalSymbols,
		Languages:    stats.Languages,
		EmbeddingPct: embeddingPct,
		ParseErrors:  stats.ParseErrors,
		StaleFiles:   stats.StaleFiles,
	}, nil
}

// EnrichmentStatusResult holds enrichment/embedding progress.
type EnrichmentStatusResult struct {
	TotalChunks    int     `json:"total_chunks"`
	EmbeddedChunks int     `json:"embedded_chunks"`
	EmbeddingPct   float64 `json:"embedding_pct"`
	PendingJobs    int     `json:"pending_jobs"`
	CircuitState   string  `json:"circuit_state"`
	TotalFiles     int     `json:"total_files"`
	TotalSymbols   int     `json:"total_symbols"`
}

// EnrichmentStatusInput configures enrichment status retrieval.
type EnrichmentStatusInput struct {
	Store       types.WriterStore
	VectorStore types.VectorStore // nil ok
	PendingFn   func() int       // nil → 0
	CircuitFn   func() string    // nil → "n/a"
}

// isIdentifierQuery returns true when query looks like a single code identifier:
// no whitespace, contains only letters/digits/underscores, has at least one letter.
// Matches camelCase, PascalCase, snake_case, SCREAMING_SNAKE_CASE.
func isIdentifierQuery(query string) bool {
	if len(query) < 2 {
		return false
	}
	hasLetter := false
	for _, r := range query {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return hasLetter
}

// SymbolExactSearch looks up chunks by exact symbol name.
// Returns nil (not an error) when no symbols match, so callers fall through gracefully.
// Tries case-sensitive match first, then case-insensitive fallback.
// Score is always 1.0 — exact symbol matches are maximum relevance.
func SymbolExactSearch(ctx context.Context, store types.MetadataStore, query string, filter TestFilter) ([]types.ScoredResult, error) {
	// Case-sensitive first (cheaper, more precise)
	syms, err := store.GetSymbolByName(ctx, query)
	if err != nil {
		return nil, err
	}
	// Case-insensitive fallback
	if len(syms) == 0 {
		syms, err = store.GetSymbolByNameCI(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	if len(syms) == 0 {
		return nil, nil
	}

	// Collect unique chunk IDs respecting test filter
	type symEntry struct {
		sym  types.SymbolRecord
		path string
	}
	seen := make(map[int64]bool, len(syms))
	var entries []symEntry

	for _, sym := range syms {
		if sym.ChunkID == 0 || seen[sym.ChunkID] {
			continue
		}
		isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
		if err != nil {
			continue
		}
		if filter.ExcludeTests && isTest {
			continue
		}
		if filter.TestOnly && !isTest {
			continue
		}
		path, err := store.GetFilePathByID(ctx, sym.FileID)
		if err != nil {
			continue
		}
		seen[sym.ChunkID] = true
		entries = append(entries, symEntry{sym: sym, path: path})
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Batch hydration path (1 query instead of N)
	chunkIDs := make([]int64, len(entries))
	for i, e := range entries {
		chunkIDs[i] = e.sym.ChunkID
	}

	if bs, ok := store.(types.BatchMetadataStore); ok {
		hydrated, err := bs.BatchHydrateChunks(ctx, chunkIDs)
		if err == nil {
			hydratedMap := make(map[int64]types.HydratedChunk, len(hydrated))
			for _, h := range hydrated {
				hydratedMap[h.ChunkID] = h
			}
			results := make([]types.ScoredResult, 0, len(entries))
			for _, e := range entries {
				h, ok := hydratedMap[e.sym.ChunkID]
				if !ok {
					continue
				}
				results = append(results, types.ScoredResult{
					ChunkID:    h.ChunkID,
					Score:      1.0,
					Path:       h.Path,
					SymbolName: e.sym.Name,
					Kind:       h.Kind,
					StartLine:  h.StartLine,
					EndLine:    h.EndLine,
					Content:    h.Content,
					TokenCount: h.TokenCount,
				})
			}
			return results, nil
		}
		// fall through to per-item on batch error
	}

	// Per-item fallback
	results := make([]types.ScoredResult, 0, len(entries))
	for _, e := range entries {
		chunk, err := store.GetChunkByID(ctx, e.sym.ChunkID)
		if err != nil || chunk == nil {
			continue
		}
		results = append(results, types.ScoredResult{
			ChunkID:    chunk.ID,
			Score:      1.0,
			Path:       e.path,
			SymbolName: e.sym.Name,
			Kind:       chunk.Kind,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Content:    chunk.Content,
			TokenCount: chunk.TokenCount,
		})
	}
	return results, nil
}

// GetEnrichmentStatus returns enrichment and embedding progress.
func GetEnrichmentStatus(ctx context.Context, input EnrichmentStatusInput) (*EnrichmentStatusResult, error) {
	stats, err := input.Store.GetIndexStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("stats query: %w", err)
	}

	vectorCount := 0
	if input.VectorStore != nil {
		vectorCount, _ = input.VectorStore.Count(ctx)
	}
	readiness := 0.0
	if stats.TotalChunks > 0 {
		readiness = float64(vectorCount) / float64(stats.TotalChunks)
	}

	pending := 0
	if input.PendingFn != nil {
		pending = input.PendingFn()
	}
	circuitState := "n/a"
	if input.CircuitFn != nil {
		circuitState = input.CircuitFn()
	}

	return &EnrichmentStatusResult{
		TotalChunks:    stats.TotalChunks,
		EmbeddedChunks: vectorCount,
		EmbeddingPct:   readiness * 100,
		PendingJobs:    pending,
		CircuitState:   circuitState,
		TotalFiles:     stats.TotalFiles,
		TotalSymbols:   stats.TotalSymbols,
	}, nil
}
