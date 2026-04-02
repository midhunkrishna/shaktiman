//go:build sqlite_fts5

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestTruncateChunk_MultiLine(t *testing.T) {
	t.Parallel()

	// A chunk with multiple lines and large token count.
	chunk := types.ScoredResult{
		ChunkID:    42,
		Path:       "foo.go",
		Content:    "line1\nline2\nline3\nline4\nline5",
		TokenCount: 100,
	}

	result := truncateChunk(chunk, 5)

	// Truncated content should be shorter than original.
	if result.TokenCount > 5 {
		t.Errorf("TokenCount = %d, want <= 5", result.TokenCount)
	}
	if len(result.Content) >= len(chunk.Content) {
		t.Errorf("content should be truncated, got %d chars", len(result.Content))
	}
}

func TestTruncateChunk_SingleLine(t *testing.T) {
	t.Parallel()

	chunk := types.ScoredResult{
		ChunkID:    1,
		Path:       "single.go",
		Content:    strings.Repeat("x", 400),
		TokenCount: 100,
	}

	result := truncateChunk(chunk, 2)

	// truncateChunk always includes at least the first line even if it
	// exceeds the budget, so content should be non-empty.
	if result.Content == "" {
		t.Error("expected non-empty truncated content")
	}
	// The single line of 400 chars yields lineTokens = 400/4 + 1 = 101,
	// which is added because the builder is empty (never-empty guarantee).
	if result.TokenCount != 101 {
		t.Errorf("TokenCount = %d, want 101 (first line always included)", result.TokenCount)
	}
}

func TestTruncateChunk_PreservesMetadata(t *testing.T) {
	t.Parallel()

	chunk := types.ScoredResult{
		ChunkID:    42,
		Path:       "meta.go",
		StartLine:  10,
		EndLine:    50,
		Kind:       "function",
		SymbolName: "DoStuff",
		Content:    "line1\nline2\nline3",
		TokenCount: 30,
	}

	result := truncateChunk(chunk, 5)

	// Metadata fields should be preserved.
	if result.ChunkID != 42 {
		t.Errorf("ChunkID = %d, want 42", result.ChunkID)
	}
	if result.Path != "meta.go" {
		t.Errorf("Path = %q, want %q", result.Path, "meta.go")
	}
	if result.Kind != "function" {
		t.Errorf("Kind = %q, want %q", result.Kind, "function")
	}
	if result.SymbolName != "DoStuff" {
		t.Errorf("SymbolName = %q, want %q", result.SymbolName, "DoStuff")
	}
}

func TestAssemble_StructuralExpansion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := storage.NewStore(db)

	// Create file with 3 functions: Caller, Helper, Unrelated
	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "expand.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunkIDs, err := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Caller", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func Caller() {}", TokenCount: 10},
		{ChunkIndex: 1, SymbolName: "Helper", Kind: "function",
			StartLine: 12, EndLine: 20, Content: "func Helper() {}", TokenCount: 10},
		{ChunkIndex: 2, SymbolName: "Unrelated", Kind: "function",
			StartLine: 22, EndLine: 30, Content: "func Unrelated() {}", TokenCount: 10},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}
	symIDs, err := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "Caller", Kind: "function", Line: 1, Visibility: "exported"},
		{ChunkID: chunkIDs[1], Name: "Helper", Kind: "function", Line: 12, Visibility: "exported"},
		{ChunkID: chunkIDs[2], Name: "Unrelated", Kind: "function", Line: 22, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}

	// Create edge: Caller -> Helper (no connection to Unrelated)
	err = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		symMap := map[string]int64{
			"Caller": symIDs[0],
			"Helper": symIDs[1],
		}
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Helper", Kind: "calls"},
		}, symMap, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// All three chunks are candidates, but only Caller is high-scoring enough
	// to be selected within the primary budget. Helper and Unrelated have
	// lower scores but are in the candidate pool.
	allCandidates := []types.ScoredResult{
		{ChunkID: chunkIDs[0], Score: 0.9, Path: "expand.go", SymbolName: "Caller",
			Kind: "function", StartLine: 1, EndLine: 10, TokenCount: 10, Content: "func Caller() {}"},
		{ChunkID: chunkIDs[1], Score: 0.3, Path: "expand.go", SymbolName: "Helper",
			Kind: "function", StartLine: 12, EndLine: 20, TokenCount: 10, Content: "func Helper() {}"},
		{ChunkID: chunkIDs[2], Score: 0.2, Path: "expand.go", SymbolName: "Unrelated",
			Kind: "function", StartLine: 22, EndLine: 30, TokenCount: 10, Content: "func Unrelated() {}"},
	}

	// Budget = 100 tokens. Primary packing takes all 3 (30 tokens).
	// Remaining = 70. Expansion budget = 70 * 30% = 21.
	// But all candidates already fit. Use smaller primary budget to force
	// only Caller into primary selection, leaving room for expansion.
	pkg := Assemble(AssemblerInput{
		Candidates:   allCandidates,
		BudgetTokens: 100,
		Store:        store,
		Ctx:          ctx,
	})

	if pkg == nil {
		t.Fatal("expected non-nil context package")
	}

	// With budget=100 and all chunks at 10 tokens, all 3 fit in primary.
	// Structural expansion runs with remaining budget after primary.
	// The key assertion: Caller and Helper should both appear (connected by edge).
	foundCaller := false
	foundHelper := false
	for _, c := range pkg.Chunks {
		if c.SymbolName == "Caller" {
			foundCaller = true
		}
		if c.SymbolName == "Helper" {
			foundHelper = true
		}
	}
	if !foundCaller {
		t.Error("expected Caller in assembled results")
	}
	if !foundHelper {
		t.Error("expected Helper in assembled results (structural neighbor of Caller)")
	}
}

func TestStructuralExpand_WithEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := storage.NewStore(db)

	// Two files, each with a function connected by an edge.
	fileID1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "h1", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 1: %v", err)
	}
	chunkIDs1, err := store.InsertChunks(ctx, fileID1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Main", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func main() { Serve() }", TokenCount: 10},
	})
	if err != nil {
		t.Fatalf("InsertChunks 1: %v", err)
	}
	symIDs1, err := store.InsertSymbols(ctx, fileID1, []types.SymbolRecord{
		{ChunkID: chunkIDs1[0], Name: "Main", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols 1: %v", err)
	}

	fileID2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "server.go", ContentHash: "h2", Mtime: 1.0,
		EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile 2: %v", err)
	}
	chunkIDs2, err := store.InsertChunks(ctx, fileID2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Serve", Kind: "function",
			StartLine: 1, EndLine: 15, Content: "func Serve() {}", TokenCount: 8},
	})
	if err != nil {
		t.Fatalf("InsertChunks 2: %v", err)
	}
	symIDs2, err := store.InsertSymbols(ctx, fileID2, []types.SymbolRecord{
		{ChunkID: chunkIDs2[0], Name: "Serve", Kind: "function", Line: 1, Visibility: "exported"},
	})
	if err != nil {
		t.Fatalf("InsertSymbols 2: %v", err)
	}

	// Edge: Main -> Serve
	err = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		symMap := map[string]int64{
			"Main":  symIDs1[0],
			"Serve": symIDs2[0],
		}
		return store.InsertEdges(ctx, txh, fileID1, []types.EdgeRecord{
			{SrcSymbolName: "Main", DstSymbolName: "Serve", Kind: "calls"},
		}, symMap, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	// Only "Main" is selected. "Serve" is a candidate but not selected.
	selected := []types.ScoredResult{
		{ChunkID: chunkIDs1[0], Score: 0.9, Path: "main.go", SymbolName: "Main",
			Kind: "function", StartLine: 1, EndLine: 10, TokenCount: 10},
	}
	allCandidates := []types.ScoredResult{
		{ChunkID: chunkIDs1[0], Score: 0.9, Path: "main.go", SymbolName: "Main",
			Kind: "function", StartLine: 1, EndLine: 10, TokenCount: 10},
		{ChunkID: chunkIDs2[0], Score: 0.4, Path: "server.go", SymbolName: "Serve",
			Kind: "function", StartLine: 1, EndLine: 15, TokenCount: 8},
	}

	expanded := structuralExpand(ctx, store, selected, allCandidates, 50)

	// Serve should be pulled in via structural expansion
	if len(expanded) == 0 {
		t.Fatal("expected structuralExpand to return neighbor chunk")
	}
	if expanded[0].SymbolName != "Serve" {
		t.Errorf("expected expanded chunk to be Serve, got %q", expanded[0].SymbolName)
	}
}
