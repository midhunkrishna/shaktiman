// Package core provides the query engine: keyword retrieval, context assembly, and fallback chain.
package core

import (
	"context"
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// KeywordSearch performs FTS5 search and returns hydrated scored results.
func KeywordSearch(ctx context.Context, store *storage.Store, query string, limit int) ([]types.ScoredResult, error) {
	ftsResults, err := store.KeywordSearch(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("keyword search %q: %w", query, err)
	}

	if len(ftsResults) == 0 {
		return nil, nil
	}

	return hydrateFTSResults(ctx, store, ftsResults)
}

// hydrateFTSResults enriches FTS results with full chunk data.
func hydrateFTSResults(ctx context.Context, store *storage.Store, ftsResults []storage.FTSResult) ([]types.ScoredResult, error) {
	results := make([]types.ScoredResult, 0, len(ftsResults))

	for _, fts := range ftsResults {
		chunk, err := store.GetChunkByID(ctx, fts.ChunkID)
		if err != nil {
			return nil, fmt.Errorf("hydrate chunk %d: %w", fts.ChunkID, err)
		}
		if chunk == nil {
			continue // chunk was deleted between FTS search and hydration
		}

		// Get the file path
		path, err := store.GetFilePathByID(ctx, chunk.FileID)
		if err != nil {
			return nil, fmt.Errorf("get file path for chunk %d: %w", fts.ChunkID, err)
		}

		// Normalize BM25 rank to a 0-1 score (BM25 rank is negative, lower=better)
		score := normalizeBM25(fts.Rank)

		results = append(results, types.ScoredResult{
			ChunkID:    chunk.ID,
			Score:      score,
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
