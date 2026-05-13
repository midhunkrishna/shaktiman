// Package core provides the query engine: keyword retrieval, context assembly, and fallback chain.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// Active reports whether the filter will actually drop anything. Callers
// short-circuit the per-file lookup when both flags are off.
func (f TestFilter) Active() bool { return f.ExcludeTests || f.TestOnly }

// AcceptIsTest returns whether a row whose underlying file has the given
// is_test flag passes the filter.
func (f TestFilter) AcceptIsTest(isTest bool) bool {
	if f.ExcludeTests && isTest {
		return false
	}
	if f.TestOnly && !isTest {
		return false
	}
	return true
}

// fileIsTestLookup is the subset of MetadataStore that AcceptByFileID
// needs. Declaring it locally lets every caller (WriterStore,
// MetadataStore, BatchMetadataStore — they all expose GetFileIsTestByID)
// pass through without the helper bolting onto any one of them.
type fileIsTestLookup interface {
	GetFileIsTestByID(ctx context.Context, fileID int64) (bool, error)
}

// AcceptByFileID checks whether a row whose backing file has the given
// fileID passes the filter, performing the lookup only when the filter is
// active. Errors from the lookup propagate to the caller — different
// retrieval paths handle them differently (some drop the row, some
// propagate the failure), so this helper does not impose a policy.
func (f TestFilter) AcceptByFileID(ctx context.Context, store fileIsTestLookup, fileID int64) (bool, error) {
	if !f.Active() {
		return true, nil
	}
	isTest, err := store.GetFileIsTestByID(ctx, fileID)
	if err != nil {
		return false, err
	}
	return f.AcceptIsTest(isTest), nil
}

// scoredFromHydrated builds a ScoredResult from a HydratedChunk with the
// given score. Five retrieval paths used to set this field-for-field by
// hand; funnelling through one constructor prevents drift when ScoredResult
// gains a new field.
func scoredFromHydrated(h types.HydratedChunk, score float64) types.ScoredResult {
	return types.ScoredResult{
		ChunkID:    h.ChunkID,
		FileID:     h.FileID,
		Score:      score,
		Path:       h.Path,
		SymbolName: h.SymbolName,
		Kind:       h.Kind,
		StartLine:  h.StartLine,
		EndLine:    h.EndLine,
		Content:    h.Content,
		TokenCount: h.TokenCount,
	}
}

// scoredFromChunk builds a ScoredResult from a ChunkRecord. Path is
// passed separately because the chunk record does not carry it.
func scoredFromChunk(c *types.ChunkRecord, path string, score float64) types.ScoredResult {
	return types.ScoredResult{
		ChunkID:    c.ID,
		FileID:     c.FileID,
		Score:      score,
		Path:       path,
		SymbolName: c.SymbolName,
		Kind:       c.Kind,
		StartLine:  c.StartLine,
		EndLine:    c.EndLine,
		Content:    c.Content,
		TokenCount: c.TokenCount,
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

		if !filter.AcceptIsTest(h.IsTest) {
			continue
		}
		results = append(results, scoredFromHydrated(h, normalizeBM25(fts.Rank)))
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

		ok, err := filter.AcceptByFileID(ctx, store, chunk.FileID)
		if err != nil {
			return nil, fmt.Errorf("check is_test for chunk %d: %w", fts.ChunkID, err)
		}
		if !ok {
			continue
		}

		path, err := store.GetFilePathByID(ctx, chunk.FileID)
		if err != nil {
			return nil, fmt.Errorf("get file path for chunk %d: %w", fts.ChunkID, err)
		}

		results = append(results, scoredFromChunk(chunk, path, normalizeBM25(fts.Rank)))
	}

	return results, nil
}

// ExpandSplitSiblings reconstitutes split method fragments into complete methods.
// When a chunk is a fragment of a larger method (detected by the presence of
// sibling chunks with the same file_id, symbol_name, and kind), all siblings
// are merged into a single ScoredResult with joined content.
// Uses batch queries when the store supports BatchMetadataStore.
func ExpandSplitSiblings(ctx context.Context, store types.MetadataStore, results []types.ScoredResult) []types.ScoredResult {
	if len(results) == 0 {
		return results
	}

	if bs, ok := store.(types.BatchMetadataStore); ok {
		return expandSiblingsBatch(ctx, bs, results)
	}
	return expandSiblingsLegacy(ctx, store, results)
}

func expandSiblingsBatch(ctx context.Context, store types.BatchMetadataStore, results []types.ScoredResult) []types.ScoredResult {
	// Collect unique sibling keys from results. FileID is carried through
	// from hydration, so no per-chunk DB lookups are needed.
	keySet := make(map[string]types.SiblingKey)
	for _, r := range results {
		if r.SymbolName == "" || r.Kind == "header" || r.FileID == 0 {
			continue
		}
		k := types.SiblingKey{FileID: r.FileID, SymbolName: r.SymbolName, Kind: r.Kind}
		keySet[k.String()] = k
	}

	if len(keySet) == 0 {
		return results
	}

	keys := make([]types.SiblingKey, 0, len(keySet))
	for _, k := range keySet {
		keys = append(keys, k)
	}

	siblingMap, err := store.BatchGetSiblingChunks(ctx, keys)
	if err != nil {
		return results // degrade gracefully
	}

	return mergeSiblings(results, siblingMap)
}

func expandSiblingsLegacy(ctx context.Context, store types.MetadataStore, results []types.ScoredResult) []types.ScoredResult {
	// Build sibling map using per-key queries. FileID is carried through
	// from hydration, so no per-chunk DB lookups are needed.
	siblingMap := make(map[string][]types.HydratedChunk)

	for _, r := range results {
		if r.SymbolName == "" || r.Kind == "header" || r.FileID == 0 {
			continue
		}
		k := types.SiblingKey{FileID: r.FileID, SymbolName: r.SymbolName, Kind: r.Kind}
		if _, seen := siblingMap[k.String()]; seen {
			continue
		}
		siblings, err := store.GetSiblingChunks(ctx, r.FileID, r.SymbolName, r.Kind)
		if err != nil || len(siblings) <= 1 {
			continue // not a split fragment
		}
		var hydrated []types.HydratedChunk
		for _, s := range siblings {
			hydrated = append(hydrated, types.HydratedChunk{
				ChunkID:    s.ID,
				FileID:     s.FileID,
				Path:       r.Path,
				SymbolName: s.SymbolName,
				Kind:       s.Kind,
				StartLine:  s.StartLine,
				EndLine:    s.EndLine,
				Content:    s.Content,
				TokenCount: s.TokenCount,
			})
		}
		siblingMap[k.String()] = hydrated
	}

	return mergeSiblings(results, siblingMap)
}

// mergeSiblings takes the original results and a map of sibling groups,
// and replaces individual fragments with merged whole-method results.
func mergeSiblings(results []types.ScoredResult, siblingMap map[string][]types.HydratedChunk) []types.ScoredResult {
	if len(siblingMap) == 0 {
		return results
	}

	// Build a set of chunk IDs that are part of any sibling group.
	siblingChunkIDs := make(map[int64]string) // chunkID → siblingKey
	for key, siblings := range siblingMap {
		for _, s := range siblings {
			siblingChunkIDs[s.ChunkID] = key
		}
	}

	// Track which sibling groups we've already emitted a merged result for.
	emitted := make(map[string]bool)
	var merged []types.ScoredResult

	for _, r := range results {
		key, isSibling := siblingChunkIDs[r.ChunkID]
		if !isSibling {
			merged = append(merged, r)
			continue
		}
		if emitted[key] {
			continue // already emitted merged version, skip this fragment
		}
		emitted[key] = true

		// Build merged result from all siblings.
		// Sort by StartLine so the joined content reflects source
		// order. siblingMap is built from the storage layer which
		// doesn't guarantee any particular order, and unsorted joins
		// produced merged chunks where method bodies were stitched
		// out of sequence — visible to the LLM as scrambled code.
		siblings := append([]types.HydratedChunk(nil), siblingMap[key]...)
		sort.Slice(siblings, func(i, j int) bool {
			return siblings[i].StartLine < siblings[j].StartLine
		})

		var contentParts []string
		totalTokens := 0
		bestScore := r.Score
		startLine := siblings[0].StartLine
		endLine := siblings[0].EndLine

		for _, s := range siblings {
			contentParts = append(contentParts, s.Content)
			totalTokens += s.TokenCount
			if s.StartLine < startLine {
				startLine = s.StartLine
			}
			if s.EndLine > endLine {
				endLine = s.EndLine
			}
		}

		// Check if any other result fragments have a higher score.
		for _, other := range results {
			if otherKey, ok := siblingChunkIDs[other.ChunkID]; ok && otherKey == key {
				if other.Score > bestScore {
					bestScore = other.Score
				}
			}
		}

		merged = append(merged, types.ScoredResult{
			ChunkID:    r.ChunkID,
			Score:      bestScore,
			Path:       r.Path,
			SymbolName: r.SymbolName,
			Kind:       r.Kind,
			StartLine:  startLine,
			EndLine:    endLine,
			Content:    strings.Join(contentParts, "\n"),
			TokenCount: totalTokens,
		})
	}

	return merged
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
