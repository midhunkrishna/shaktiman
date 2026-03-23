package core

import (
	"context"
	"sort"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// RankWeights controls signal blending for hybrid ranking.
type RankWeights struct {
	Semantic   float64 // default 0.40
	Structural float64 // default 0.20
	Change     float64 // default 0.15
	Session    float64 // default 0.15
	Keyword    float64 // default 0.10
}

// DefaultRankWeights returns the full 5-signal weights.
func DefaultRankWeights() RankWeights {
	return RankWeights{
		Semantic:   0.40,
		Structural: 0.20,
		Change:     0.15,
		Session:    0.15,
		Keyword:    0.10,
	}
}

// HybridRankInput configures hybrid ranking.
type HybridRankInput struct {
	Candidates      []types.ScoredResult
	Store           types.MetadataStore
	Weights         RankWeights
	SemanticScores  map[int64]float64 // chunkID → cosine similarity [0,1]
	SemanticReady   bool              // true if semantic scores are available
}

// HybridRank re-ranks candidates using up to 5 signals:
// semantic, structural, change, session, keyword.
// Missing signals have their weight redistributed proportionally.
func HybridRank(ctx context.Context, input HybridRankInput) []types.ScoredResult {
	if len(input.Candidates) == 0 {
		return input.Candidates
	}

	// Determine available signals and redistribute weights
	w := redistributeWeights(input.Weights, input.SemanticReady)

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
		sessionScore := 0.0 // Phase 4: working set LRU

		var semanticScore float64
		if input.SemanticReady && input.SemanticScores != nil {
			semanticScore = input.SemanticScores[results[i].ChunkID]
		}

		results[i].Score = w.Semantic*semanticScore +
			w.Structural*structScore +
			w.Change*changeScore +
			w.Session*sessionScore +
			w.Keyword*keywordScore
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// redistributeWeights proportionally distributes the weight of unavailable
// signals to the remaining available ones.
func redistributeWeights(base RankWeights, semanticReady bool) RankWeights {
	w := base

	var unavailable float64
	if !semanticReady {
		unavailable += w.Semantic
		w.Semantic = 0
	}
	// Session is not yet available (Phase 4)
	unavailable += w.Session
	w.Session = 0

	if unavailable == 0 {
		return w
	}

	remaining := w.Keyword + w.Structural + w.Change + w.Semantic
	if remaining == 0 {
		return w
	}

	factor := (remaining + unavailable) / remaining
	w.Keyword *= factor
	w.Structural *= factor
	w.Change *= factor
	if semanticReady {
		w.Semantic *= factor
	}

	return w
}

// NormalizeCosineSimilarity maps cosine similarity from [-1,1] to [0,1].
func NormalizeCosineSimilarity(sim float64) float64 {
	return (sim + 1.0) / 2.0
}

// computeStructuralScores computes structural boost for candidates.
// A candidate gets boosted if other high-scoring candidates are BFS-reachable.
func computeStructuralScores(ctx context.Context, store types.MetadataStore, candidates []types.ScoredResult) map[int64]float64 {
	scores := make(map[int64]float64, len(candidates))

	candidateChunks := make(map[int64]bool, len(candidates))
	for _, c := range candidates {
		candidateChunks[c.ChunkID] = true
	}

	for _, c := range candidates {
		if c.ChunkID == 0 {
			continue
		}

		symbolID := lookupSymbolForChunk(ctx, store, c.ChunkID)
		if symbolID == 0 {
			continue
		}

		neighbors, err := store.Neighbors(ctx, symbolID, 2, "both")
		if err != nil {
			continue
		}

		neighborChunks := lookupChunkIDsForSymbols(ctx, store, neighbors)
		overlap := 0
		for _, ncID := range neighborChunks {
			if candidateChunks[ncID] && ncID != c.ChunkID {
				overlap++
			}
		}

		if overlap > 0 {
			scores[c.ChunkID] = float64(overlap) / float64(overlap+1)
		}
	}

	return scores
}

// lookupSymbolForChunk finds a symbol ID associated with a chunk.
func lookupSymbolForChunk(ctx context.Context, store types.MetadataStore, chunkID int64) int64 {
	chunk, err := store.GetChunkByID(ctx, chunkID)
	if err != nil || chunk == nil || chunk.SymbolName == "" {
		return 0
	}

	syms, err := store.GetSymbolByName(ctx, chunk.SymbolName)
	if err != nil || len(syms) == 0 {
		return 0
	}

	for _, s := range syms {
		if s.FileID == chunk.FileID {
			return s.ID
		}
	}
	return syms[0].ID
}

// lookupChunkIDsForSymbols maps symbol IDs to their chunk IDs.
func lookupChunkIDsForSymbols(ctx context.Context, store types.MetadataStore, symbolIDs []int64) []int64 {
	var chunkIDs []int64
	for _, symID := range symbolIDs {
		sym, err := store.GetSymbolByID(ctx, symID)
		if err != nil || sym == nil {
			continue
		}
		chunkIDs = append(chunkIDs, sym.ChunkID)
	}
	return chunkIDs
}
