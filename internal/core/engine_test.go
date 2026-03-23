package core

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func setupTestEngine(t *testing.T) (*QueryEngine, *storage.Store) {
	t.Helper()
	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	engine := NewQueryEngine(store, t.TempDir())
	return engine, store
}

func seedTestData(t *testing.T, store *storage.Store) {
	t.Helper()
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "src/auth/login.ts", ContentHash: "h1", Mtime: 1.0,
		Language: "typescript", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	_, err = store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "validateToken", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content: "export function validateToken(token: string): boolean { return token.length > 0; }",
			TokenCount: 25},
		{ChunkIndex: 1, SymbolName: "refreshToken", Kind: "function",
			StartLine: 22, EndLine: 45,
			Content: "export async function refreshToken(token: string): Promise<string> { return token; }",
			TokenCount: 30},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	fileID2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "src/utils/hash.ts", ContentHash: "h2", Mtime: 1.0,
		Language: "typescript", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile2: %v", err)
	}

	_, err = store.InsertChunks(ctx, fileID2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "hashPassword", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content: "export function hashPassword(password: string): string { return hash(password); }",
			TokenCount: 20},
		{ChunkIndex: 1, SymbolName: "comparePassword", Kind: "function",
			StartLine: 12, EndLine: 25,
			Content: "export function comparePassword(plain: string, hashed: string): boolean { return true; }",
			TokenCount: 22},
	})
	if err != nil {
		t.Fatalf("InsertChunks2: %v", err)
	}
}

func TestSearch_KeywordLevel(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	results, err := engine.Search(ctx, SearchInput{
		Query:      "validate token",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// Should find validateToken
	found := false
	for _, r := range results {
		if r.SymbolName == "validateToken" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected validateToken in results")
	}
}

func TestSearch_FilesystemFallback(t *testing.T) {
	t.Parallel()

	// Create engine with empty store — should use filesystem fallback
	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	// Use testdata directory as project root for filesystem fallback
	engine := NewQueryEngine(store, "../../testdata/typescript_project")

	results, err := engine.Search(context.Background(), SearchInput{
		Query:      "login",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Filesystem fallback should find TypeScript files
	if len(results) == 0 {
		t.Error("expected filesystem fallback to find files")
	}
}

func TestContext_BudgetFitting(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	pkg, err := engine.Context(ctx, ContextInput{
		Query:        "validate token",
		BudgetTokens: 50,
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if pkg == nil {
		t.Fatal("expected non-nil context package")
	}
	if pkg.TotalTokens > 50 {
		t.Errorf("expected total_tokens <= 50, got %d", pkg.TotalTokens)
	}
	if len(pkg.Chunks) == 0 {
		t.Error("expected at least one chunk in context package")
	}
}

func TestContext_DefaultBudget(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	pkg, err := engine.Context(ctx, ContextInput{
		Query: "password",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if pkg == nil {
		t.Fatal("expected non-nil context package")
	}
	// Default budget is 4096
	if pkg.TotalTokens > 4096 {
		t.Errorf("expected total_tokens <= 4096, got %d", pkg.TotalTokens)
	}
}

func TestDetermineLevel_EmptyIndex(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	level := DetermineLevel(context.Background(), store)
	if level != LevelFilesystem {
		t.Errorf("expected LevelFilesystem for empty index, got %d", level)
	}
}

func TestDetermineLevel_IndexedChunks(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "function x() {}", TokenCount: 5},
	})

	level := DetermineLevel(ctx, store)
	if level != LevelKeyword {
		t.Errorf("expected LevelKeyword with chunks, got %d", level)
	}
}

func TestAssemble_BudgetRespected(t *testing.T) {
	t.Parallel()

	candidates := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9, Path: "a.ts", StartLine: 1, EndLine: 10, TokenCount: 30, Content: "chunk1"},
		{ChunkID: 2, Score: 0.8, Path: "b.ts", StartLine: 1, EndLine: 10, TokenCount: 30, Content: "chunk2"},
		{ChunkID: 3, Score: 0.7, Path: "c.ts", StartLine: 1, EndLine: 10, TokenCount: 30, Content: "chunk3"},
	}

	pkg := Assemble(AssemblerInput{
		Candidates:   candidates,
		BudgetTokens: 50,
	})

	if pkg.TotalTokens > 50 {
		t.Errorf("expected total_tokens <= 50, got %d", pkg.TotalTokens)
	}
	// Should fit at most 1 chunk (30 tokens) since 2 chunks = 60 > 50
	if len(pkg.Chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(pkg.Chunks))
	}
}

func TestAssemble_LineOverlapDedup(t *testing.T) {
	t.Parallel()

	candidates := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9, Path: "a.ts", StartLine: 1, EndLine: 20, TokenCount: 30, Content: "chunk1"},
		{ChunkID: 2, Score: 0.8, Path: "a.ts", StartLine: 5, EndLine: 15, TokenCount: 20, Content: "chunk2"},
		{ChunkID: 3, Score: 0.7, Path: "b.ts", StartLine: 1, EndLine: 10, TokenCount: 20, Content: "chunk3"},
	}

	pkg := Assemble(AssemblerInput{
		Candidates:   candidates,
		BudgetTokens: 200,
	})

	// Chunk 2 has >50% overlap with chunk 1 (lines 5-15 out of 5-15 = 100%),
	// so it should be deduped. Result should be chunk 1 + chunk 3.
	if len(pkg.Chunks) != 2 {
		t.Errorf("expected 2 chunks after dedup, got %d", len(pkg.Chunks))
	}
}

func TestAssemble_EmptyCandidates(t *testing.T) {
	t.Parallel()

	pkg := Assemble(AssemblerInput{
		Candidates:   nil,
		BudgetTokens: 1000,
	})

	if len(pkg.Chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(pkg.Chunks))
	}
	if pkg.TotalTokens != 0 {
		t.Errorf("expected 0 total tokens, got %d", pkg.TotalTokens)
	}
}

// ── Phase 3 Integration Tests ──

// mockEmbedder returns deterministic embeddings for testing.
type mockEmbedder struct {
	dim     int
	vectors map[string][]float32 // preloaded query→vector
}

func newMockEmbedder(dim int) *mockEmbedder {
	return &mockEmbedder{dim: dim, vectors: make(map[string][]float32)}
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if vec, ok := m.vectors[text]; ok {
		return vec, nil
	}
	// Generate deterministic vector from text hash
	vec := make([]float32, m.dim)
	for i := range vec {
		vec[i] = float32(i+len(text)) / float32(m.dim)
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := m.Embed(context.Background(), t)
		if err != nil {
			return nil, err
		}
		vecs[i] = v
	}
	return vecs, nil
}

func TestSearch_SemanticLevel(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	// Set up vector store with embeddings for the 4 seeded chunks
	dim := 4
	vs := vector.NewBruteForceStore(dim)

	// Seed vectors: make chunk 4 (comparePassword) very similar to our query
	// and the rest dissimilar. This tests that semantic search surfaces
	// results that keyword alone wouldn't rank highly.
	vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.1, 0.1}) // validateToken
	vs.Upsert(ctx, 2, []float32{0.1, 0.1, 0.2, 0.1}) // refreshToken
	vs.Upsert(ctx, 3, []float32{0.1, 0.1, 0.1, 0.2}) // hashPassword
	vs.Upsert(ctx, 4, []float32{0.9, 0.9, 0.9, 0.9}) // comparePassword — highly similar to query

	embedder := newMockEmbedder(dim)
	// Pre-set the query embedding to be very close to chunk 4
	embedder.vectors["verify credentials"] = []float32{0.9, 0.8, 0.9, 0.8}

	engine.SetVectorStore(vs, embedder, func() bool { return true })

	results, err := engine.Search(ctx, SearchInput{
		Query:      "verify credentials",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// comparePassword should be boosted by semantic similarity
	found := false
	for _, r := range results {
		if r.SymbolName == "comparePassword" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected comparePassword in results (semantic boost)")
	}
}

func TestSearch_FallbackToKeyword_OnEmbedError(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	// Set up vector store but with an embedder that returns query vectors
	// that won't match anything special. The important thing is the search
	// still succeeds via keyword fallback path.
	dim := 4
	vs := vector.NewBruteForceStore(dim)
	embedder := newMockEmbedder(dim)

	engine.SetVectorStore(vs, embedder, func() bool { return true })

	// Search should still work (keyword + empty vector results merged)
	results, err := engine.Search(ctx, SearchInput{
		Query:      "validate token",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected keyword results even with empty vector store")
	}
}

func TestDetermineLevelFull_Hybrid(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	ctx := context.Background()

	// Seed 10 chunks
	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	for i := 0; i < 10; i++ {
		store.InsertChunks(ctx, fileID, []types.ChunkRecord{
			{ChunkIndex: i, Kind: "function", StartLine: i * 10, EndLine: i*10 + 9,
				Content: "function test() {}", TokenCount: 5},
		})
	}

	tests := []struct {
		name        string
		vectorCount int
		wantLevel   FallbackLevel
	}{
		{"80% ready → hybrid", 8, LevelHybrid},
		{"100% ready → hybrid", 10, LevelHybrid},
		{"50% ready → mixed", 5, LevelMixed},
		{"20% ready → mixed", 2, LevelMixed},
		{"10% ready → keyword", 1, LevelKeyword},
		{"0 vectors → keyword", 0, LevelKeyword},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			level := DetermineLevelFull(ctx, DetermineLevelInput{
				Store:          store,
				VectorCount:    tc.vectorCount,
				EmbeddingReady: true,
			})
			if level != tc.wantLevel {
				t.Errorf("got level %v, want %v", level, tc.wantLevel)
			}
		})
	}
}

func TestDetermineLevelFull_EmbeddingNotReady(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.ts", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "function test() {}", TokenCount: 5},
	})

	// EmbeddingReady=false should force keyword level regardless of vectors
	level := DetermineLevelFull(ctx, DetermineLevelInput{
		Store:          store,
		VectorCount:    100,
		EmbeddingReady: false,
	})
	if level != LevelKeyword {
		t.Errorf("got level %v, want LevelKeyword when embedding not ready", level)
	}
}

func TestMergeResults_Deduplication(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	ctx := context.Background()

	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "funcA", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func A() {}", TokenCount: 5},
		{ChunkIndex: 1, SymbolName: "funcB", Kind: "function",
			StartLine: 12, EndLine: 25, Content: "func B() {}", TokenCount: 5},
	})

	kwResults := []types.ScoredResult{
		{ChunkID: chunkIDs[0], Score: 0.9, Path: "test.go", SymbolName: "funcA"},
	}
	semResults := []types.VectorResult{
		{ChunkID: chunkIDs[0], Score: 0.8}, // duplicate of keyword result
		{ChunkID: chunkIDs[1], Score: 0.7}, // new from semantic
	}

	merged := mergeResults(ctx, store, kwResults, semResults)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged results (deduped), got %d", len(merged))
	}

	// First should be the keyword result (preserved as-is)
	if merged[0].ChunkID != chunkIDs[0] {
		t.Errorf("first result should be keyword ChunkID %d, got %d", chunkIDs[0], merged[0].ChunkID)
	}
	// Second should be hydrated from store
	if merged[1].ChunkID != chunkIDs[1] {
		t.Errorf("second result should be semantic ChunkID %d, got %d", chunkIDs[1], merged[1].ChunkID)
	}
	if merged[1].SymbolName != "funcB" {
		t.Errorf("semantic result should be hydrated with SymbolName funcB, got %q", merged[1].SymbolName)
	}
}

func TestContext_SemanticStrategy(t *testing.T) {
	t.Parallel()
	engine, store := setupTestEngine(t)
	ctx := context.Background()

	seedTestData(t, store)

	dim := 4
	vs := vector.NewBruteForceStore(dim)
	// Seed vectors for all 4 chunks
	vs.Upsert(ctx, 1, []float32{0.5, 0.5, 0.5, 0.5})
	vs.Upsert(ctx, 2, []float32{0.3, 0.3, 0.3, 0.3})
	vs.Upsert(ctx, 3, []float32{0.4, 0.4, 0.4, 0.4})
	vs.Upsert(ctx, 4, []float32{0.6, 0.6, 0.6, 0.6})

	embedder := newMockEmbedder(dim)
	engine.SetVectorStore(vs, embedder, func() bool { return true })

	pkg, err := engine.Context(ctx, ContextInput{
		Query:        "password hashing",
		BudgetTokens: 200,
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}

	if pkg == nil {
		t.Fatal("expected non-nil context package")
	}
	if pkg.Strategy != "hybrid_l0" && pkg.Strategy != "mixed_l0.5" {
		t.Errorf("expected hybrid or mixed strategy with vectors attached, got %q", pkg.Strategy)
	}
	if len(pkg.Chunks) == 0 {
		t.Error("expected at least one chunk in context package")
	}
}

func TestFallbackLevel_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level FallbackLevel
		want  string
	}{
		{LevelHybrid, "hybrid_l0"},
		{LevelMixed, "mixed_l0.5"},
		{LevelKeyword, "keyword_l2"},
		{LevelFilesystem, "filesystem_l3"},
		{FallbackLevel(99), "unknown_99"},
	}

	for _, tc := range tests {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("FallbackLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestFilterByScore(t *testing.T) {
	t.Parallel()

	results := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9},
		{ChunkID: 2, Score: 0.5},
		{ChunkID: 3, Score: 0.1},
		{ChunkID: 4, Score: 0.05},
		{ChunkID: 5, Score: 0.3},
	}

	filtered := filterByScore(results, 0.15)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 results above 0.15, got %d", len(filtered))
	}
	// Check IDs: should be 1, 2, 5
	want := []int64{1, 2, 5}
	for i, w := range want {
		if filtered[i].ChunkID != w {
			t.Errorf("filtered[%d].ChunkID = %d, want %d", i, filtered[i].ChunkID, w)
		}
	}
}

func TestFilterByScore_AllPass(t *testing.T) {
	t.Parallel()
	results := []types.ScoredResult{
		{ChunkID: 1, Score: 0.9},
		{ChunkID: 2, Score: 0.5},
	}
	filtered := filterByScore(results, 0.1)
	if len(filtered) != 2 {
		t.Errorf("expected 2 results, got %d", len(filtered))
	}
}

func TestFilterByScore_NonePass(t *testing.T) {
	t.Parallel()
	results := []types.ScoredResult{
		{ChunkID: 1, Score: 0.1},
		{ChunkID: 2, Score: 0.05},
	}
	filtered := filterByScore(results, 0.5)
	if len(filtered) != 0 {
		t.Errorf("expected 0 results, got %d", len(filtered))
	}
}

func TestFilterByScore_Empty(t *testing.T) {
	t.Parallel()
	filtered := filterByScore(nil, 0.5)
	if len(filtered) != 0 {
		t.Errorf("expected 0 results, got %d", len(filtered))
	}
}

func TestNormalizeBM25(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rank   float64
		wantGT float64
		wantLT float64
	}{
		{"zero rank", 0, -0.1, 0.1},
		{"positive rank", 5, -0.1, 0.1},
		{"negative rank", -25, 0.4, 0.6},
		{"very negative rank", -100, 0.9, 1.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := normalizeBM25(tt.rank)
			if score < tt.wantGT || score > tt.wantLT {
				t.Errorf("normalizeBM25(%v) = %v, want between %v and %v",
					tt.rank, score, tt.wantGT, tt.wantLT)
			}
		})
	}
}
