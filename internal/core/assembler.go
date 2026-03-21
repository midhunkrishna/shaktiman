package core

import (
	"sort"
	"strings"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// AssemblerInput configures context assembly.
type AssemblerInput struct {
	Candidates   []types.ScoredResult
	BudgetTokens int
}

// Assemble performs budget-fitted greedy packing of ranked chunks.
// Algorithm (CA-5 simplified for Phase 1):
//  1. Sort by score descending
//  2. Skip chunks with >50% line overlap with already-selected
//  3. Add chunks that fit within remaining budget
//  4. If first chunk exceeds budget, truncate to fit (never empty result)
func Assemble(input AssemblerInput) *types.ContextPackage {
	candidates := make([]types.ScoredResult, len(input.Candidates))
	copy(candidates, input.Candidates)

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	var selected []types.ScoredResult
	remaining := input.BudgetTokens

	for _, c := range candidates {
		if remaining <= 0 {
			break
		}

		// Skip chunks with >50% line overlap with already-selected
		if hasLineOverlap(c, selected) {
			continue
		}

		if c.TokenCount <= remaining {
			selected = append(selected, c)
			remaining -= c.TokenCount
		} else if len(selected) == 0 {
			// First chunk exceeds budget — truncate to fit
			truncated := truncateChunk(c, input.BudgetTokens)
			selected = append(selected, truncated)
			remaining = 0
		}
		// Otherwise skip oversized chunk
	}

	totalTokens := 0
	for _, s := range selected {
		totalTokens += s.TokenCount
	}

	return &types.ContextPackage{
		Chunks:      selected,
		TotalTokens: totalTokens,
		Strategy:    "keyword_l2",
	}
}

// hasLineOverlap checks if a candidate has >50% line overlap with any selected chunk.
func hasLineOverlap(candidate types.ScoredResult, selected []types.ScoredResult) bool {
	if candidate.Path == "" {
		return false
	}

	candLines := candidate.EndLine - candidate.StartLine + 1
	if candLines <= 0 {
		return false
	}

	for _, s := range selected {
		if s.Path != candidate.Path {
			continue
		}

		// Calculate overlap
		overlapStart := max(candidate.StartLine, s.StartLine)
		overlapEnd := min(candidate.EndLine, s.EndLine)
		if overlapStart > overlapEnd {
			continue
		}

		overlap := overlapEnd - overlapStart + 1
		if float64(overlap)/float64(candLines) > 0.5 {
			return true
		}
	}
	return false
}

// truncateChunk truncates chunk content to fit within the token budget.
func truncateChunk(chunk types.ScoredResult, budget int) types.ScoredResult {
	lines := strings.Split(chunk.Content, "\n")
	var sb strings.Builder
	tokensSoFar := 0

	for _, line := range lines {
		// Rough estimate: each line is ~(len/4) tokens
		lineTokens := len(line)/4 + 1
		if tokensSoFar+lineTokens > budget && sb.Len() > 0 {
			break
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
		tokensSoFar += lineTokens
	}

	truncated := chunk
	truncated.Content = sb.String()
	truncated.TokenCount = tokensSoFar
	return truncated
}
