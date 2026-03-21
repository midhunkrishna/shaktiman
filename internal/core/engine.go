package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

// QueryEngine orchestrates search and context assembly.
type QueryEngine struct {
	store       *storage.Store
	projectRoot string
	logger      *slog.Logger
	vectorStore *vector.BruteForceStore // nil if embeddings disabled
	embedder    types.Embedder          // nil if embeddings disabled
	embedCache  *vector.EmbedCache
	embedReady  func() bool // checks if embedding service is available
}

// NewQueryEngine creates an engine backed by the given store.
func NewQueryEngine(store *storage.Store, projectRoot string) *QueryEngine {
	return &QueryEngine{
		store:       store,
		projectRoot: projectRoot,
		logger:      slog.Default().With("component", "engine"),
		embedCache:  vector.NewEmbedCache(100),
	}
}

// SetVectorStore attaches a vector store and embedder for semantic search.
// readyFn reports whether the embedding service is available (circuit breaker check).
func (e *QueryEngine) SetVectorStore(vs *vector.BruteForceStore, embedder types.Embedder, readyFn func() bool) {
	e.vectorStore = vs
	e.embedder = embedder
	e.embedReady = readyFn
}

// SearchInput configures a search operation.
type SearchInput struct {
	Query      string
	MaxResults int
	Explain    bool
}

// ContextInput configures a context assembly operation.
type ContextInput struct {
	Query        string
	BudgetTokens int
}

// Search executes a search and returns scored results using the best available level.
func (e *QueryEngine) Search(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
	if input.MaxResults <= 0 {
		input.MaxResults = 50
	}

	level := e.determineLevel(ctx)

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
		input.BudgetTokens = 8192
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
func (e *QueryEngine) Store() *storage.Store {
	return e.store
}

// VectorStore returns the vector store, or nil if unavailable.
func (e *QueryEngine) VectorStore() *vector.BruteForceStore {
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
	// Get keyword candidates
	kwResults, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults*2)
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
	merged := mergeResults(ctx, e.store, kwResults, semResults)

	// Apply hybrid ranking
	ranked := HybridRank(ctx, HybridRankInput{
		Candidates:     merged,
		Store:          e.store,
		Weights:        DefaultRankWeights(),
		SemanticScores: semanticScores,
		SemanticReady:  true,
	})

	if len(ranked) > input.MaxResults {
		ranked = ranked[:input.MaxResults]
	}
	return ranked, nil
}

// searchKeyword runs keyword-only search with structural + change signals.
func (e *QueryEngine) searchKeyword(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
	results, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults)
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
	})
	return results, nil
}

func (e *QueryEngine) contextSemantic(ctx context.Context, input ContextInput, level FallbackLevel) (*types.ContextPackage, error) {
	results, err := e.searchSemantic(ctx, SearchInput{
		Query:      input.Query,
		MaxResults: 200,
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
	results, err := KeywordSearch(ctx, e.store, input.Query, 200)
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
func (e *QueryEngine) embedQuery(ctx context.Context, query string) ([]float32, error) {
	if vec, ok := e.embedCache.Get(query); ok {
		return vec, nil
	}
	vec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	e.embedCache.Put(query, vec)
	return vec, nil
}

// mergeResults creates a union of keyword and semantic results, hydrating vector-only entries.
func mergeResults(ctx context.Context, store *storage.Store, kwResults []types.ScoredResult, semResults []types.VectorResult) []types.ScoredResult {
	seen := make(map[int64]bool, len(kwResults)+len(semResults))
	merged := make([]types.ScoredResult, 0, len(kwResults)+len(semResults))

	for _, r := range kwResults {
		seen[r.ChunkID] = true
		merged = append(merged, r)
	}

	for _, sr := range semResults {
		if seen[sr.ChunkID] {
			continue
		}
		seen[sr.ChunkID] = true

		// Hydrate from store
		chunk, err := store.GetChunkByID(ctx, sr.ChunkID)
		if err != nil || chunk == nil {
			continue
		}
		path, _ := store.GetFilePathByID(ctx, chunk.FileID)

		merged = append(merged, types.ScoredResult{
			ChunkID:    chunk.ID,
			Score:      0, // will be set by ranker
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
