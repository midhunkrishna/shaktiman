package core

import (
	"context"
	"sort"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// RankWeights controls signal blending for hybrid ranking.
type RankWeights struct {
	Keyword    float64 // default 0.5
	Structural float64 // default 0.3
	Change     float64 // default 0.2
}

// DefaultRankWeights returns the default weights.
func DefaultRankWeights() RankWeights {
	return RankWeights{
		Keyword:    0.5,
		Structural: 0.3,
		Change:     0.2,
	}
}

// HybridRankInput configures hybrid ranking.
type HybridRankInput struct {
	Candidates []types.ScoredResult
	Store      *storage.Store
	Weights    RankWeights
}

// HybridRank re-ranks candidates using 3 signals: keyword, structural, change.
func HybridRank(ctx context.Context, input HybridRankInput) []types.ScoredResult {
	if len(input.Candidates) == 0 {
		return input.Candidates
	}

	w := input.Weights

	// Collect chunk IDs for batch queries
	chunkIDs := make([]int64, len(input.Candidates))
	for i, c := range input.Candidates {
		chunkIDs[i] = c.ChunkID
	}

	// Compute change scores
	changeScores, _ := input.Store.ComputeChangeScores(ctx, chunkIDs)

	// Compute structural scores via BFS neighbor overlap
	structScores := computeStructuralScores(ctx, input.Store, input.Candidates)

	// Blend signals
	results := make([]types.ScoredResult, len(input.Candidates))
	copy(results, input.Candidates)

	for i := range results {
		keywordScore := results[i].Score
		structScore := structScores[results[i].ChunkID]
		changeScore := changeScores[results[i].ChunkID]

		results[i].Score = w.Keyword*keywordScore + w.Structural*structScore + w.Change*changeScore
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// computeStructuralScores computes structural boost for candidates.
// A candidate gets boosted if other high-scoring candidates are BFS-reachable.
func computeStructuralScores(ctx context.Context, store *storage.Store, candidates []types.ScoredResult) map[int64]float64 {
	scores := make(map[int64]float64, len(candidates))

	// Build set of candidate chunk IDs for overlap detection
	candidateChunks := make(map[int64]bool, len(candidates))
	for _, c := range candidates {
		candidateChunks[c.ChunkID] = true
	}

	// For each candidate, find its symbol and check BFS neighbors
	for _, c := range candidates {
		if c.ChunkID == 0 {
			continue
		}

		// Look up symbol for this chunk
		symbolID := lookupSymbolForChunk(ctx, store, c.ChunkID)
		if symbolID == 0 {
			continue
		}

		// BFS depth 2
		neighbors, err := store.Neighbors(ctx, symbolID, 2, "both")
		if err != nil {
			continue
		}

		// Check how many neighbors map to other candidate chunks
		neighborChunks := lookupChunkIDsForSymbols(ctx, store, neighbors)
		overlap := 0
		for _, ncID := range neighborChunks {
			if candidateChunks[ncID] && ncID != c.ChunkID {
				overlap++
			}
		}

		if overlap > 0 {
			// Score inversely proportional to distance, capped at 1.0
			scores[c.ChunkID] = float64(overlap) / float64(overlap+1)
		}
	}

	return scores
}

// lookupSymbolForChunk finds a symbol ID associated with a chunk.
func lookupSymbolForChunk(ctx context.Context, store *storage.Store, chunkID int64) int64 {
	chunk, err := store.GetChunkByID(ctx, chunkID)
	if err != nil || chunk == nil || chunk.SymbolName == "" {
		return 0
	}

	syms, err := store.GetSymbolByName(ctx, chunk.SymbolName)
	if err != nil || len(syms) == 0 {
		return 0
	}

	// Prefer symbol from same file
	for _, s := range syms {
		if s.FileID == chunk.FileID {
			return s.ID
		}
	}
	return syms[0].ID
}

// lookupChunkIDsForSymbols maps symbol IDs to their chunk IDs.
func lookupChunkIDsForSymbols(ctx context.Context, store *storage.Store, symbolIDs []int64) []int64 {
	var chunkIDs []int64
	for _, symID := range symbolIDs {
		syms, err := store.GetSymbolByID(ctx, symID)
		if err != nil || syms == nil {
			continue
		}
		chunkIDs = append(chunkIDs, syms.ChunkID)
	}
	return chunkIDs
}
