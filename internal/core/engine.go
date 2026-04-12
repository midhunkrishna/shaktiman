package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// QueryEngine orchestrates search and context assembly.
type QueryEngine struct {
	store        types.MetadataStore
	projectRoot  string
	logger       *slog.Logger
	vectorStore  types.VectorStore // nil if embeddings disabled
	embedder     types.Embedder    // nil if embeddings disabled
	embedCache   *vector.EmbedCache
	embedReady   func() bool   // checks if embedding service is available
	sessionStore *SessionStore // nil if session scoring disabled
	queryPrefix  string        // prepended to query text before embedding (e.g. "search_query: ")
}

// NewQueryEngine creates an engine backed by the given store.
// queryPrefix is prepended to query text before embedding; use "" for models that don't require task prefixes.
// For nomic-embed-text use "search_query: ".
func NewQueryEngine(store types.MetadataStore, projectRoot string, queryPrefix string) *QueryEngine {
	return &QueryEngine{
		store:       store,
		projectRoot: projectRoot,
		logger:      slog.Default().With("component", "engine"),
		embedCache:  vector.NewEmbedCache(100),
		queryPrefix: queryPrefix,
	}
}

// SetVectorStore attaches a vector store and embedder for semantic search.
// readyFn reports whether the embedding service is available (circuit breaker check).
func (e *QueryEngine) SetVectorStore(vs types.VectorStore, embedder types.Embedder, readyFn func() bool) {
	e.vectorStore = vs
	e.embedder = embedder
	e.embedReady = readyFn
}

// SetSessionStore attaches a session store for session-aware ranking.
func (e *QueryEngine) SetSessionStore(ss *SessionStore) {
	e.sessionStore = ss
}

// SearchInput configures a search operation.
type SearchInput struct {
	Query        string
	MaxResults   int
	Explain      bool
	Mode         string  // "locate" or "full"; consumed by formatting layer only
	MinScore     float64 // 0.0-1.0; results below this score are dropped post-ranking
	ExcludeTests bool    // filter out test files from results
	TestOnly     bool    // return only test file results
}

// ContextInput configures a context assembly operation.
type ContextInput struct {
	Query        string
	BudgetTokens int
	ExcludeTests bool // filter out test files from context chunks
	TestOnly     bool // include only test file chunks
}

// Search executes a search and returns scored results using the best available level.
func (e *QueryEngine) Search(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
	start := time.Now()
	if input.MaxResults <= 0 {
		input.MaxResults = 10
	}

	level := e.determineLevel(ctx)
	e.logger.Info("search", "strategy", level.String(), "query_len", len(input.Query))
	defer func() {
		e.logger.Info("search completed",
			"strategy", level.String(),
			"duration_ms", time.Since(start).Milliseconds())
	}()

	switch level {
	case LevelHybrid, LevelMixed:
		return e.searchSemantic(ctx, input, level)
	case LevelKeyword:
		return e.searchKeyword(ctx, input)
	case LevelFilesystem:
		pkg, err := FilesystemFallback(ctx, e.projectRoot, input.Query, 4096)
		if err != nil {
			return nil, fmt.Errorf("filesystem fallback: %w", err)
		}
		return pkg.Chunks, nil
	default:
		return nil, fmt.Errorf("unknown fallback level: %d", level)
	}
}

// Context assembles a budget-fitted context package for the given query.
func (e *QueryEngine) Context(ctx context.Context, input ContextInput) (*types.ContextPackage, error) {
	if input.BudgetTokens <= 0 {
		input.BudgetTokens = 4096
	}

	level := e.determineLevel(ctx)

	switch level {
	case LevelHybrid, LevelMixed:
		return e.contextSemantic(ctx, input, level)
	case LevelKeyword:
		return e.contextKeyword(ctx, input)
	case LevelFilesystem:
		return FilesystemFallback(ctx, e.projectRoot, input.Query, input.BudgetTokens)
	default:
		return nil, fmt.Errorf("unknown fallback level: %d", level)
	}
}

// Store returns the underlying store for direct access.
func (e *QueryEngine) Store() types.MetadataStore {
	return e.store
}

// VectorStore returns the vector store, or nil if unavailable.
func (e *QueryEngine) VectorStore() types.VectorStore {
	return e.vectorStore
}

func (e *QueryEngine) determineLevel(ctx context.Context) FallbackLevel {
	if e.vectorStore == nil || e.embedder == nil {
		return DetermineLevel(ctx, e.store)
	}

	embeddingReady := e.embedReady != nil && e.embedReady()
	vc, _ := e.vectorStore.Count(ctx)
	return DetermineLevelFull(ctx, DetermineLevelInput{
		Store:          e.store,
		VectorCount:    vc,
		EmbeddingReady: embeddingReady,
	})
}

// searchSemantic runs hybrid search with semantic + keyword candidates.
func (e *QueryEngine) searchSemantic(ctx context.Context, input SearchInput, level FallbackLevel) ([]types.ScoredResult, error) {
	filter := TestFilter{ExcludeTests: input.ExcludeTests, TestOnly: input.TestOnly}

	// Get keyword candidates
	kwResults, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults*2, filter)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}

	// Get query embedding
	queryVec, err := e.embedQuery(ctx, input.Query)
	if err != nil {
		e.logger.Warn("query embedding failed, falling back to keyword", "err", err)
		return e.searchKeyword(ctx, input)
	}

	// Get semantic candidates
	semResults, err := e.vectorStore.Search(ctx, queryVec, input.MaxResults*2)
	if err != nil {
		e.logger.Warn("vector search failed, falling back to keyword", "err", err)
		return e.searchKeyword(ctx, input)
	}

	// Build semantic score map
	semanticScores := make(map[int64]float64, len(semResults))
	for _, sr := range semResults {
		semanticScores[sr.ChunkID] = NormalizeCosineSimilarity(sr.Score)
	}

	// Merge keyword + semantic candidates (union)
	merged := mergeResults(ctx, e.store, kwResults, semResults, filter)

	// Apply hybrid ranking
	ranked := HybridRank(ctx, HybridRankInput{
		Candidates:     merged,
		Store:          e.store,
		Weights:        DefaultRankWeights(),
		SemanticScores: semanticScores,
		SemanticReady:  true,
		SessionScorer:  e.sessionScorer(),
	})

	if len(ranked) > input.MaxResults {
		ranked = ranked[:input.MaxResults]
	}
	if input.MinScore > 0 {
		ranked = filterByScore(ranked, input.MinScore)
	}
	e.recordSession(ranked)
	return ranked, nil
}

// searchKeyword runs keyword-only search with structural + change signals.
func (e *QueryEngine) searchKeyword(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
	filter := TestFilter{ExcludeTests: input.ExcludeTests, TestOnly: input.TestOnly}
	results, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults, filter)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}

	if len(results) == 0 {
		pkg, err := FilesystemFallback(ctx, e.projectRoot, input.Query, 4096)
		if err != nil {
			return nil, fmt.Errorf("filesystem fallback: %w", err)
		}
		return pkg.Chunks, nil
	}

	results = HybridRank(ctx, HybridRankInput{
		Candidates:    results,
		Store:         e.store,
		Weights:       DefaultRankWeights(),
		SemanticReady: false,
		SessionScorer: e.sessionScorer(),
	})
	if input.MinScore > 0 {
		results = filterByScore(results, input.MinScore)
	}
	e.recordSession(results)
	return results, nil
}

func (e *QueryEngine) contextSemantic(ctx context.Context, input ContextInput, level FallbackLevel) (*types.ContextPackage, error) {
	results, err := e.searchSemantic(ctx, SearchInput{
		Query:        input.Query,
		MaxResults:   200,
		ExcludeTests: input.ExcludeTests,
		TestOnly:     input.TestOnly,
	}, level)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return FilesystemFallback(ctx, e.projectRoot, input.Query, input.BudgetTokens)
	}

	pkg := Assemble(AssemblerInput{
		Candidates:   results,
		BudgetTokens: input.BudgetTokens,
		Store:        e.store,
		Ctx:          ctx,
	})
	pkg.Strategy = level.String()
	return pkg, nil
}

func (e *QueryEngine) contextKeyword(ctx context.Context, input ContextInput) (*types.ContextPackage, error) {
	filter := TestFilter{ExcludeTests: input.ExcludeTests, TestOnly: input.TestOnly}
	results, err := KeywordSearch(ctx, e.store, input.Query, 200, filter)
	if err != nil {
		return nil, fmt.Errorf("keyword search for context: %w", err)
	}
	if len(results) == 0 {
		return FilesystemFallback(ctx, e.projectRoot, input.Query, input.BudgetTokens)
	}

	results = HybridRank(ctx, HybridRankInput{
		Candidates:    results,
		Store:         e.store,
		Weights:       DefaultRankWeights(),
		SemanticReady: false,
		SessionScorer: e.sessionScorer(),
	})

	pkg := Assemble(AssemblerInput{
		Candidates:   results,
		BudgetTokens: input.BudgetTokens,
		Store:        e.store,
		Ctx:          ctx,
	})
	pkg.Strategy = LevelKeyword.String()
	return pkg, nil
}

// embedQuery produces or retrieves a cached embedding for the query text.
// queryPrefix is prepended before embedding and used as the cache key so that
// a prefix change (e.g. switching models) does not serve stale cached vectors.
func (e *QueryEngine) embedQuery(ctx context.Context, query string) ([]float32, error) {
	prefixed := e.queryPrefix + query
	if vec, ok := e.embedCache.Get(prefixed); ok {
		return vec, nil
	}
	vec, err := e.embedder.Embed(ctx, prefixed)
	if err != nil {
		return nil, err
	}
	e.embedCache.Put(prefixed, vec)
	return vec, nil
}

// sessionScorer returns the session scorer if available, or nil.
func (e *QueryEngine) sessionScorer() types.SessionScorer {
	if e.sessionStore == nil {
		return nil
	}
	return e.sessionStore
}

// recordSession records search results in the session store and decays non-hits.
func (e *QueryEngine) recordSession(results []types.ScoredResult) {
	if e.sessionStore == nil {
		return
	}
	hits := make([]SessionHit, len(results))
	for i, r := range results {
		hits[i] = SessionHit{FilePath: r.Path, StartLine: r.StartLine}
	}
	e.sessionStore.RecordBatch(hits)
	e.sessionStore.DecayAllExcept(hits)
}

// filterByScore removes results with Score below the threshold (in-place).
func filterByScore(results []types.ScoredResult, minScore float64) []types.ScoredResult {
	n := 0
	for _, r := range results {
		if r.Score >= minScore {
			results[n] = r
			n++
		}
	}
	return results[:n]
}

// mergeResults creates a union of keyword and semantic results, hydrating vector-only entries.
// The filter is applied to vector-only results during hydration (keyword results are already filtered).
// Uses batch hydration when the store supports BatchMetadataStore.
func mergeResults(ctx context.Context, store types.MetadataStore, kwResults []types.ScoredResult, semResults []types.VectorResult, filter TestFilter) []types.ScoredResult {
	seen := make(map[int64]bool, len(kwResults)+len(semResults))
	merged := make([]types.ScoredResult, 0, len(kwResults)+len(semResults))

	for _, r := range kwResults {
		seen[r.ChunkID] = true
		merged = append(merged, r)
	}

	// Collect unhydrated semantic-only chunk IDs
	var needHydrate []int64
	for _, sr := range semResults {
		if !seen[sr.ChunkID] {
			seen[sr.ChunkID] = true
			needHydrate = append(needHydrate, sr.ChunkID)
		}
	}

	if len(needHydrate) == 0 {
		return merged
	}

	// Batch path: 1 query instead of 3N
	if bs, ok := store.(types.BatchMetadataStore); ok {
		hydrated, err := bs.BatchHydrateChunks(ctx, needHydrate)
		if err == nil {
			for _, h := range hydrated {
				if filter.ExcludeTests && h.IsTest {
					continue
				}
				if filter.TestOnly && !h.IsTest {
					continue
				}
				merged = append(merged, types.ScoredResult{
					ChunkID:    h.ChunkID,
					Score:      0, // will be set by ranker
					Path:       h.Path,
					SymbolName: h.SymbolName,
					Kind:       h.Kind,
					StartLine:  h.StartLine,
					EndLine:    h.EndLine,
					Content:    h.Content,
					TokenCount: h.TokenCount,
				})
			}
			return merged
		}
		// fall through to legacy on error
	}

	// Legacy per-item path
	for _, id := range needHydrate {
		chunk, err := store.GetChunkByID(ctx, id)
		if err != nil || chunk == nil {
			continue
		}

		if filter.ExcludeTests || filter.TestOnly {
			isTest, err := store.GetFileIsTestByID(ctx, chunk.FileID)
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

		path, _ := store.GetFilePathByID(ctx, chunk.FileID)

		merged = append(merged, types.ScoredResult{
			ChunkID:    chunk.ID,
			Score:      0,
			Path:       path,
			SymbolName: chunk.SymbolName,
			Kind:       chunk.Kind,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Content:    chunk.Content,
			TokenCount: chunk.TokenCount,
		})
	}

	return merged
}
