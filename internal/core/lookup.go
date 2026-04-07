package core

import (
	"context"
	"fmt"
	"time"

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
