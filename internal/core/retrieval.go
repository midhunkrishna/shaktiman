// Package core provides the query engine: keyword retrieval, context assembly, and fallback chain.
package core

import (
	"context"
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// TestFilter controls test file filtering during retrieval.
type TestFilter struct {
	ExcludeTests bool // drop test files from results
	TestOnly     bool // keep only test files
}

// ScopeToFilter converts a scope string ("impl", "test", "all") to TestFilter.
func ScopeToFilter(scope string) TestFilter {
	switch scope {
	case "test":
		return TestFilter{TestOnly: true}
	case "all":
		return TestFilter{}
	default: // "impl"
		return TestFilter{ExcludeTests: true}
	}
}

// KeywordSearch performs FTS5 search and returns hydrated scored results.
// When filter excludes or selects test files, FTS limit is doubled to
// compensate for filtered-out results.
func KeywordSearch(ctx context.Context, store types.MetadataStore, query string, limit int, filter TestFilter) ([]types.ScoredResult, error) {
	ftsLimit := limit
	if filter.ExcludeTests || filter.TestOnly {
		ftsLimit = limit * 2
		if ftsLimit > 400 {
			ftsLimit = 400
		}
	}

	ftsResults, err := store.KeywordSearch(ctx, query, ftsLimit)
	if err != nil {
		return nil, fmt.Errorf("keyword search %q: %w", query, err)
	}

	if len(ftsResults) == 0 {
		return nil, nil
	}

	return hydrateFTSResults(ctx, store, ftsResults, filter)
}

// hydrateFTSResults enriches FTS results with full chunk data.
// Uses batch hydration when the store supports BatchMetadataStore (1 query instead of 3N).
func hydrateFTSResults(ctx context.Context, store types.MetadataStore, ftsResults []types.FTSResult, filter TestFilter) ([]types.ScoredResult, error) {
	if bs, ok := store.(types.BatchMetadataStore); ok {
		return hydrateFTSResultsBatch(ctx, bs, ftsResults, filter)
	}
	return hydrateFTSResultsLegacy(ctx, store, ftsResults, filter)
}

func hydrateFTSResultsBatch(ctx context.Context, store types.BatchMetadataStore, ftsResults []types.FTSResult, filter TestFilter) ([]types.ScoredResult, error) {
	chunkIDs := make([]int64, len(ftsResults))
	for i, fts := range ftsResults {
		chunkIDs[i] = fts.ChunkID
	}

	hydrated, err := store.BatchHydrateChunks(ctx, chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("batch hydrate FTS results: %w", err)
	}

	// Index by chunk ID for score lookup
	hydratedMap := make(map[int64]types.HydratedChunk, len(hydrated))
	for _, h := range hydrated {
		hydratedMap[h.ChunkID] = h
	}

	results := make([]types.ScoredResult, 0, len(ftsResults))
	for _, fts := range ftsResults {
		h, ok := hydratedMap[fts.ChunkID]
		if !ok {
			continue // chunk deleted between FTS search and hydration
		}

		if filter.ExcludeTests && h.IsTest {
			continue
		}
		if filter.TestOnly && !h.IsTest {
			continue
		}

		results = append(results, types.ScoredResult{
			ChunkID:    h.ChunkID,
			Score:      normalizeBM25(fts.Rank),
			Path:       h.Path,
			SymbolName: h.SymbolName,
			Kind:       h.Kind,
			StartLine:  h.StartLine,
			EndLine:    h.EndLine,
			Content:    h.Content,
			TokenCount: h.TokenCount,
		})
	}
	return results, nil
}

func hydrateFTSResultsLegacy(ctx context.Context, store types.MetadataStore, ftsResults []types.FTSResult, filter TestFilter) ([]types.ScoredResult, error) {
	results := make([]types.ScoredResult, 0, len(ftsResults))

	for _, fts := range ftsResults {
		chunk, err := store.GetChunkByID(ctx, fts.ChunkID)
		if err != nil {
			return nil, fmt.Errorf("hydrate chunk %d: %w", fts.ChunkID, err)
		}
		if chunk == nil {
			continue
		}

		if filter.ExcludeTests || filter.TestOnly {
			isTest, err := store.GetFileIsTestByID(ctx, chunk.FileID)
			if err != nil {
				return nil, fmt.Errorf("check is_test for chunk %d: %w", fts.ChunkID, err)
			}
			if filter.ExcludeTests && isTest {
				continue
			}
			if filter.TestOnly && !isTest {
				continue
			}
		}

		path, err := store.GetFilePathByID(ctx, chunk.FileID)
		if err != nil {
			return nil, fmt.Errorf("get file path for chunk %d: %w", fts.ChunkID, err)
		}

		results = append(results, types.ScoredResult{
			ChunkID:    chunk.ID,
			Score:      normalizeBM25(fts.Rank),
			Path:       path,
			SymbolName: chunk.SymbolName,
			Kind:       chunk.Kind,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Content:    chunk.Content,
			TokenCount: chunk.TokenCount,
		})
	}

	return results, nil
}

// normalizeBM25 converts a BM25 rank (negative, lower=better) to a 0-1 score.
func normalizeBM25(rank float64) float64 {
	// BM25 rank from FTS5 is negative; more negative = more relevant.
	// Convert to positive score where higher = more relevant.
	if rank >= 0 {
		return 0
	}
	// Simple normalization: cap at -50 as "maximum relevance"
	score := -rank / 50.0
	if score > 1.0 {
		score = 1.0
	}
	return score
}
