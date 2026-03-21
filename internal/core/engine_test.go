package core

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
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
	// Default budget is 8192
	if pkg.TotalTokens > 8192 {
		t.Errorf("expected total_tokens <= 8192, got %d", pkg.TotalTokens)
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
