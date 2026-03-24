//go:build sqlite_fts5

package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// setupSearchHandler creates a searchHandler backed by an in-memory store with
// seeded test data across multiple directories.
func setupSearchHandler(t *testing.T) handlerFunc {
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
	ctx := context.Background()

	// Seed files in different directories for path filter testing.
	f1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/mcp/server.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, f1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "NewServer", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content:    "func NewServer() *Server { return &Server{} }",
			TokenCount: 15},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	f2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/mcp/tools.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, f2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "searchHandler", Kind: "function",
			StartLine: 1, EndLine: 30,
			Content:    "func searchHandler(engine *core.QueryEngine) handlerFunc { return nil }",
			TokenCount: 20},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	f3, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/storage/metadata.go", ContentHash: "h3", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, f3, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "NewStore", Kind: "function",
			StartLine: 1, EndLine: 15,
			Content:    "func NewStore(db *DB) *Store { return &Store{db: db} }",
			TokenCount: 18},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	f4, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "cmd/shaktimand/main.go", ContentHash: "h4", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, f4, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "main", Kind: "function",
			StartLine: 1, EndLine: 10,
			Content:    "func main() { server := NewServer(); server.Run() }",
			TokenCount: 12},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())
	return searchHandler(engine, cfg)
}

// makeSearchRequest builds a CallToolRequest for the search handler.
func makeSearchRequest(args map[string]any) mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Name:      "search",
			Arguments: args,
		},
	}
}

func TestSearchHandler_PathFilter_ScopesToDirectory(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query": "func",
		"path":  "internal/mcp/",
		"mode":  "locate",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text

	// All results should be in internal/mcp/
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, line := range lines[1:] { // skip count header
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "internal/mcp/") {
			t.Errorf("result outside path filter: %s", line)
		}
	}

	// Should NOT contain results from other directories
	if strings.Contains(text, "internal/storage/") {
		t.Error("should not contain internal/storage/ results")
	}
	if strings.Contains(text, "cmd/") {
		t.Error("should not contain cmd/ results")
	}
}

func TestSearchHandler_PathFilter_ExactFile(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query":     "NewServer",
		"path":      "internal/mcp/server.go",
		"mode":      "locate",
		"min_score": float64(0),
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text

	if !strings.Contains(text, "internal/mcp/server.go") {
		t.Errorf("expected server.go in results, got: %s", text)
	}
	if strings.Contains(text, "tools.go") {
		t.Error("should not contain tools.go when filtering to server.go")
	}
}

func TestSearchHandler_PathFilter_Empty_ReturnsAll(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	// Without path filter — should return results from any directory.
	// Use min_score=0 to ensure all chunks are returned regardless of BM25 rank.
	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query":     "func",
		"mode":      "locate",
		"min_score": float64(0),
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text

	if strings.Contains(text, "No results found") {
		t.Fatal("expected results without path filter")
	}

	// Without a path filter, results may come from any directory.
	// Verify we got more than one result (seeded 4 chunks total).
	lines := strings.Split(strings.TrimSpace(text), "\n")
	resultLines := 0
	for _, line := range lines[1:] {
		if line != "" {
			resultLines++
		}
	}
	if resultLines < 2 {
		t.Errorf("expected multiple results without path filter, got %d", resultLines)
	}
}

func TestSearchHandler_PathFilter_NoMatch(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query": "func",
		"path":  "nonexistent/directory/",
		"mode":  "locate",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "No results found") {
		t.Errorf("expected no results for nonexistent path, got: %s", text)
	}
}

func TestSearchHandler_PathFilter_FullMode(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query":     "NewServer",
		"path":      "internal/mcp/",
		"mode":      "full",
		"min_score": float64(0),
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text

	// Full mode should include source code
	if strings.Contains(text, "No results found") {
		t.Fatal("expected results")
	}

	// Full mode should include actual source content
	if !strings.Contains(text, "func NewServer()") {
		t.Errorf("full mode should include source code, got: %s", text)
	}

	// Should NOT contain results from storage directory
	if strings.Contains(text, "internal/storage/") {
		t.Error("path filter should exclude storage results in full mode")
	}
}

func TestSearchHandler_PathFilter_RespectsMaxResults(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeSearchRequest(map[string]any{
		"query":       "func",
		"path":        "internal/",
		"mode":        "locate",
		"max_results": float64(1), // JSON numbers are float64
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}

	text := result.Content[0].(mcpsdk.TextContent).Text

	// Count result lines (skip header "N results:")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	resultLines := 0
	for _, line := range lines[1:] {
		if line != "" {
			resultLines++
		}
	}

	if resultLines > 1 {
		t.Errorf("expected at most 1 result with max_results=1, got %d", resultLines)
	}
}
