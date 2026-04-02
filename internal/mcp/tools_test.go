//go:build sqlite_fts5

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
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

// ── Helpers ──

// makeToolRequest builds a CallToolRequest with the given arguments.
func makeToolRequest(args map[string]any) mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Arguments: args,
		},
	}
}

// setupStore creates a minimal in-memory store with no test data.
func setupStore(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return storage.NewStore(db)
}

// setupStoreWithData creates a store and seeds files, chunks, symbols, and returns both.
func setupStoreWithData(t *testing.T) (*storage.Store, *storage.DB) {
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

	f1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/mcp/server.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	chunkIDs, err := store.InsertChunks(ctx, f1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "NewServer", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content:    "func NewServer() *Server { return &Server{} }",
			TokenCount: 15},
		{ChunkIndex: 1, SymbolName: "handleRequest", Kind: "function",
			StartLine: 21, EndLine: 40,
			Content:    "func handleRequest(r *Request) {}",
			TokenCount: 10},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	_, err = store.InsertSymbols(ctx, f1, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "NewServer", Kind: "function", Line: 1,
			Signature: "func NewServer() *Server", Visibility: "exported", IsExported: true},
		{ChunkID: chunkIDs[1], Name: "handleRequest", Kind: "function", Line: 21,
			Signature: "func handleRequest(r *Request)", Visibility: "private", IsExported: false},
	})
	if err != nil {
		t.Fatalf("InsertSymbols: %v", err)
	}

	return store, db
}

// setupContextHandler creates a contextHandler backed by an in-memory store.
func setupContextHandler(t *testing.T) handlerFunc {
	t.Helper()
	store := setupStore(t)
	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())
	return contextHandler(engine, cfg)
}

// setupSymbolsHandler creates a symbolsHandler backed by a seeded store.
func setupSymbolsHandler(t *testing.T) handlerFunc {
	t.Helper()
	store, _ := setupStoreWithData(t)
	return symbolsHandler(store)
}

// setupDependenciesHandler creates a dependenciesHandler backed by a store
// with two symbols and an edge from NewServer -> handleRequest.
func setupDependenciesHandler(t *testing.T) handlerFunc {
	t.Helper()
	store, db := setupStoreWithData(t)
	ctx := context.Background()

	// Get symbol IDs so we can create edges.
	syms, err := store.GetSymbolByName(ctx, "NewServer")
	if err != nil || len(syms) == 0 {
		t.Fatalf("GetSymbolByName NewServer: %v", err)
	}
	srcID := syms[0].ID
	srcFileID := syms[0].FileID

	syms2, err := store.GetSymbolByName(ctx, "handleRequest")
	if err != nil || len(syms2) == 0 {
		t.Fatalf("GetSymbolByName handleRequest: %v", err)
	}
	dstID := syms2[0].ID

	// Insert edge within a write transaction.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, storage.SqliteTxHandle{Tx: tx}, srcFileID, []types.EdgeRecord{
			{SrcSymbolName: "NewServer", DstSymbolName: "handleRequest", Kind: "calls", FileID: srcFileID},
		}, map[string]int64{
			"NewServer":     srcID,
			"handleRequest": dstID,
		}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	return dependenciesHandler(store)
}

// setupDiffHandler creates a diffHandler backed by a store with seeded diff_log data.
func setupDiffHandler(t *testing.T) handlerFunc {
	t.Helper()
	store, db := setupStoreWithData(t)
	ctx := context.Background()

	// Get a valid file ID.
	syms, err := store.GetSymbolByName(ctx, "NewServer")
	if err != nil || len(syms) == 0 {
		t.Fatalf("need seeded symbol: %v", err)
	}
	fileID := syms[0].FileID

	// Insert a diff_log entry.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		diffID, err := store.InsertDiffLog(ctx, storage.SqliteTxHandle{Tx: tx}, storage.DiffLogEntry{
			FileID:       fileID,
			ChangeType:   "modify",
			LinesAdded:   10,
			LinesRemoved: 3,
			HashBefore:   "aaa",
			HashAfter:    "bbb",
		})
		if err != nil {
			return err
		}
		return store.InsertDiffSymbols(ctx, storage.SqliteTxHandle{Tx: tx}, diffID, []storage.DiffSymbolEntry{
			{SymbolName: "NewServer", ChangeType: "modified"},
		})
	})
	if err != nil {
		t.Fatalf("seed diff data: %v", err)
	}

	return diffHandler(store)
}

// ── Search handler validation tests ──

func TestSearchHandler_MissingQuery(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing query")
	}
}

func TestSearchHandler_QueryTooLong(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query": strings.Repeat("x", 10001),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for query too long")
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "10,000") {
		t.Errorf("expected error about 10000 chars, got: %s", text)
	}
}

func TestSearchHandler_InvalidMode(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query": "test",
		"mode":  "invalid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for invalid mode")
	}
}

func TestSearchHandler_MaxResultsTooLow(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":       "test",
		"max_results": float64(0),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for max_results=0")
	}
}

func TestSearchHandler_MaxResultsTooHigh(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":       "test",
		"max_results": float64(201),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for max_results=201")
	}
}

func TestSearchHandler_MinScoreTooLow(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":     "test",
		"min_score": float64(-0.1),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for min_score < 0")
	}
}

func TestSearchHandler_MinScoreTooHigh(t *testing.T) {
	t.Parallel()
	handler := setupSearchHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":     "test",
		"min_score": float64(1.1),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for min_score > 1.0")
	}
}

// ── Context handler tests ──

func TestContextHandler_MissingQuery(t *testing.T) {
	t.Parallel()
	handler := setupContextHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing query")
	}
}

func TestContextHandler_QueryTooLong(t *testing.T) {
	t.Parallel()
	handler := setupContextHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query": strings.Repeat("x", 10001),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for query too long")
	}
}

func TestContextHandler_BudgetTooLow(t *testing.T) {
	t.Parallel()
	handler := setupContextHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":         "test",
		"budget_tokens": float64(100),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for budget < 256")
	}
}

func TestContextHandler_Success(t *testing.T) {
	t.Parallel()
	handler := setupContextHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"query":         "server",
		"budget_tokens": float64(4096),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
}

// ── Symbols handler tests ──

func TestSymbolsHandler_MissingName(t *testing.T) {
	t.Parallel()
	handler := setupSymbolsHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing name")
	}
}

func TestSymbolsHandler_Found(t *testing.T) {
	t.Parallel()
	handler := setupSymbolsHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name": "NewServer",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "NewServer") {
		t.Errorf("expected NewServer in results, got: %s", text)
	}
}

func TestSymbolsHandler_NotFound(t *testing.T) {
	t.Parallel()
	handler := setupSymbolsHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name": "NonexistentSymbol",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	// Empty JSON array for no results.
	if text != "null" && text != "[]" {
		t.Errorf("expected null or [] for not found, got: %s", text)
	}
}

func TestSymbolsHandler_KindFilter(t *testing.T) {
	t.Parallel()
	handler := setupSymbolsHandler(t)
	// NewServer is kind="function"; filter by "type" should exclude it.
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name": "NewServer",
		"kind": "type",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if text != "null" && text != "[]" {
		t.Errorf("expected no results with kind=type filter, got: %s", text)
	}
}

// ── Dependencies handler tests ──

func TestDependenciesHandler_MissingSymbol(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing symbol")
	}
}

func TestDependenciesHandler_InvalidDirection(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "sideways",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for invalid direction")
	}
}

func TestDependenciesHandler_DepthBounds(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	// Depth 0 should fail.
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol": "NewServer",
		"depth":  float64(0),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for depth=0")
	}

	// Depth 6 should also fail.
	result, err = handler(context.Background(), makeToolRequest(map[string]any{
		"symbol": "NewServer",
		"depth":  float64(6),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for depth=6")
	}
}

func TestDependenciesHandler_NotFound(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol": "NonexistentFunc",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if text != "[]" {
		t.Errorf("expected [] for unknown symbol, got: %s", text)
	}
}

func TestDependenciesHandler_Found(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	// NewServer calls handleRequest; direction=callees should find it.
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "callees",
		"depth":     float64(1),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "handleRequest") {
		t.Errorf("expected handleRequest in callees, got: %s", text)
	}
}

// ── Diff handler tests ──

func TestDiffHandler_InvalidDuration(t *testing.T) {
	t.Parallel()
	handler := setupDiffHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"since": "not-a-duration",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for invalid duration")
	}
}

func TestDiffHandler_EmptyResults(t *testing.T) {
	t.Parallel()
	// Use a fresh store with no diffs.
	store := setupStore(t)
	handler := diffHandler(store)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"since": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if text != "null" && text != "[]" {
		t.Errorf("expected null or [] for empty diffs, got: %s", text)
	}
}

func TestDiffHandler_WithResults(t *testing.T) {
	t.Parallel()
	handler := setupDiffHandler(t)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"since": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "modify") {
		t.Errorf("expected 'modify' in diff results, got: %s", text)
	}
	if !strings.Contains(text, "NewServer") {
		t.Errorf("expected 'NewServer' symbol in diff results, got: %s", text)
	}
}

// ── Enrichment status handler tests ──

func TestEnrichmentStatusHandler_NilVsNilEw(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	handler := enrichmentStatusHandler(store, nil, nil)
	result, err := handler(context.Background(), mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if status["circuit_state"] != "disabled" {
		t.Errorf("circuit_state = %v, want disabled", status["circuit_state"])
	}
}

// ── Summary handler tests ──

func TestSummaryHandler_NilVectorStore(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	handler := summaryHandler(store, nil)
	result, err := handler(context.Background(), mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	var summary map[string]any
	if err := json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if summary["embedding_pct"] != float64(0) {
		t.Errorf("embedding_pct = %v, want 0", summary["embedding_pct"])
	}
}

// ── Workspace summary resource handler tests ──

func TestWorkspaceSummaryHandler_ReturnsJSON(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	handler := workspaceSummaryHandler(store)
	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "shaktiman://workspace/summary"
	contents, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 resource content, got %d", len(contents))
	}
	tc, ok := contents[0].(mcpsdk.TextResourceContents)
	if !ok {
		t.Fatal("expected TextResourceContents")
	}
	if tc.MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", tc.MIMEType)
	}
	var stats map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &stats); err != nil {
		t.Fatalf("invalid JSON in resource: %v", err)
	}
}

// ── sanitizeError tests ──

func TestSanitizeError_Short(t *testing.T) {
	t.Parallel()
	msg := sanitizeError("prefix: ", fmt.Errorf("short error"))
	if msg != "prefix: short error" {
		t.Errorf("got %q, want %q", msg, "prefix: short error")
	}
}

func TestSanitizeError_Long(t *testing.T) {
	t.Parallel()
	longErr := fmt.Errorf("%s", strings.Repeat("x", 300))
	msg := sanitizeError("prefix: ", longErr)
	if len(msg) > len("prefix: ")+200+3 {
		t.Errorf("expected truncation, got len=%d", len(msg))
	}
	if !strings.HasSuffix(msg, "...") {
		t.Errorf("expected ... suffix, got %q", msg)
	}
}

// ── ToolDef + NewServer coverage tests ──

func TestToolDefs_DoNotPanic(t *testing.T) {
	t.Parallel()
	cfg := types.DefaultConfig(t.TempDir())

	tools := []mcpsdk.Tool{
		searchToolDef(cfg),
		contextToolDef(cfg),
		symbolsToolDef(),
		dependenciesToolDef(),
		diffToolDef(),
		enrichmentStatusToolDef(),
		summaryToolDef(),
	}
	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool definition has empty name")
		}
	}

	// Also exercise the resource definition.
	res := workspaceSummaryDef()
	if res.URI == "" {
		t.Error("workspace summary resource has empty URI")
	}
}

func TestNewServer_DoesNotPanic(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())

	srv := NewServer(NewServerInput{
		Engine: engine,
		Store:  store,
		Config: cfg,
	})
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

// ── enrichmentStatusHandler with non-nil vector store ──

func TestEnrichmentStatusHandler_WithVectorStore(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "func test() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	vs := vector.NewBruteForceStore(4)
	handler := enrichmentStatusHandler(store, vs, nil)
	result, err := handler(ctx, makeToolRequest(nil))
	if err != nil {
		t.Fatalf("enrichmentStatusHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}

	var status struct {
		TotalChunks    int     `json:"total_chunks"`
		EmbeddedChunks int     `json:"embedded_chunks"`
		EmbeddingPct   float64 `json:"embedding_pct"`
		CircuitState   string  `json:"circuit_state"`
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if status.TotalChunks != 1 {
		t.Errorf("total_chunks = %d, want 1", status.TotalChunks)
	}
	if status.EmbeddedChunks != 0 {
		t.Errorf("embedded_chunks = %d, want 0", status.EmbeddedChunks)
	}
	if status.CircuitState != "disabled" {
		t.Errorf("circuit_state = %q, want %q", status.CircuitState, "disabled")
	}
}

// ── summaryHandler with non-nil vector store ──

func TestSummaryHandler_WithVectorStore(t *testing.T) {
	t.Parallel()
	store := setupStore(t)
	ctx := context.Background()

	fileID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "test.go", ContentHash: "h", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	_, err = store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", StartLine: 1, EndLine: 10,
			Content: "func test() {}", TokenCount: 5},
	})
	if err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	vs := vector.NewBruteForceStore(4)
	handler := summaryHandler(store, vs)
	result, err := handler(ctx, makeToolRequest(nil))
	if err != nil {
		t.Fatalf("summaryHandler: %v", err)
	}

	var summary struct {
		TotalFiles   int     `json:"total_files"`
		TotalChunks  int     `json:"total_chunks"`
		EmbeddingPct float64 `json:"embedding_pct"`
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if err := json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.TotalFiles != 1 {
		t.Errorf("total_files = %d, want 1", summary.TotalFiles)
	}
	if summary.EmbeddingPct != 0 {
		t.Errorf("embedding_pct = %f, want 0", summary.EmbeddingPct)
	}
}

// ── Additional symbolsHandler coverage ──

// ── Additional dependenciesHandler coverage ──

func TestDependenciesHandler_BothDirection(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "both",
	}))
	if err != nil {
		t.Fatalf("dependenciesHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestDependenciesHandler_CallerDirection(t *testing.T) {
	t.Parallel()
	handler := setupDependenciesHandler(t)
	ctx := context.Background()

	result, err := handler(ctx, makeToolRequest(map[string]any{
		"symbol":    "handleRequest",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("dependenciesHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "NewServer") {
		t.Errorf("expected NewServer in callers of handleRequest, got: %s", text)
	}
}

// ── Additional diffHandler coverage ──

func TestDiffHandler_LimitClamping(t *testing.T) {
	t.Parallel()
	handler := setupDiffHandler(t)
	ctx := context.Background()

	// limit=0 should be clamped to 50 (no error).
	result, err := handler(ctx, makeToolRequest(map[string]any{
		"limit": float64(0),
	}))
	if err != nil {
		t.Fatalf("diffHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected no error for clamped limit, got error")
	}
}

func TestDiffHandler_LargeDurationClamped(t *testing.T) {
	t.Parallel()
	handler := setupDiffHandler(t)
	ctx := context.Background()

	// since > 720h should be clamped (no error).
	result, err := handler(ctx, makeToolRequest(map[string]any{
		"since": "9000h",
	}))
	if err != nil {
		t.Fatalf("diffHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected no error for clamped duration, got error")
	}
}

func TestDiffHandler_LargeLimitClamped(t *testing.T) {
	t.Parallel()
	handler := setupDiffHandler(t)
	ctx := context.Background()

	// limit > 500 should be clamped to 50 (no error).
	result, err := handler(ctx, makeToolRequest(map[string]any{
		"limit": float64(999),
	}))
	if err != nil {
		t.Fatalf("diffHandler: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected no error for clamped limit, got error")
	}
}

// ── Pending edge fallback tests ──

// setupHandlersWithPendingEdge creates store+handlers with a pending edge:
// NewServer --imports--> "ExternalLib" (unresolved, lives in pending_edges).
func setupHandlersWithPendingEdge(t *testing.T) (*storage.Store, handlerFunc, handlerFunc) {
	t.Helper()
	store, db := setupStoreWithData(t)
	ctx := context.Background()

	syms, err := store.GetSymbolByName(ctx, "NewServer")
	if err != nil || len(syms) == 0 {
		t.Fatalf("GetSymbolByName NewServer: %v", err)
	}
	srcID := syms[0].ID
	srcFileID := syms[0].FileID

	// Insert edge where dst is unresolvable → goes to pending_edges.
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		return store.InsertEdges(ctx, storage.SqliteTxHandle{Tx: tx}, srcFileID, []types.EdgeRecord{
			{SrcSymbolName: "NewServer", DstSymbolName: "ExternalLib", Kind: "imports"},
		}, map[string]int64{
			"NewServer": srcID,
		}, "")
	})
	if err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	return store, symbolsHandler(store), dependenciesHandler(store)
}

func TestSymbolsHandler_PendingEdgeFallback(t *testing.T) {
	t.Parallel()
	_, handler, _ := setupHandlersWithPendingEdge(t)

	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name": "ExternalLib",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "referenced_by") {
		t.Errorf("expected referenced_by in response, got: %s", text)
	}
	if !strings.Contains(text, "NewServer") {
		t.Errorf("expected NewServer as referencing symbol, got: %s", text)
	}
}

func TestDependenciesHandler_PendingEdgeFallback(t *testing.T) {
	t.Parallel()
	_, _, handler := setupHandlersWithPendingEdge(t)

	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "ExternalLib",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "NewServer") {
		t.Errorf("expected NewServer in callers of ExternalLib, got: %s", text)
	}
}

func TestDependenciesHandler_PendingEdgeCalleesReturnsEmpty(t *testing.T) {
	t.Parallel()
	_, _, handler := setupHandlersWithPendingEdge(t)

	// direction=callees for an unknown symbol should return empty
	// (pending edges only have caller info, not callee info).
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "ExternalLib",
		"direction": "callees",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if text != "[]" {
		t.Errorf("expected [] for callees of unknown symbol, got: %s", text)
	}
}

// TestDependenciesHandler_FoundSymbolWithPendingEdges tests the case where a
// symbol exists in the symbols table but has no resolved edges — only pending
// edges. The handler should still return the pending callers.
func TestDependenciesHandler_FoundSymbolWithPendingEdges(t *testing.T) {
	t.Parallel()

	store, db := setupStoreWithData(t)
	ctx := context.Background()

	// NewServer exists in the symbols table (from setupStoreWithData).
	// Insert a pending edge where NewServer is the SOURCE and "ExternalLib"
	// is an unresolved destination. Then query for callers of "NewServer" —
	// NewServer has no incoming resolved edges, but it IS a known symbol.
	// Meanwhile, also insert a pending edge where some OTHER symbol imports
	// "NewServer" as an unresolved name — simulating incremental indexing
	// where the caller file was indexed before NewServer's file.
	syms, err := store.GetSymbolByName(ctx, "handleRequest")
	if err != nil || len(syms) == 0 {
		t.Fatalf("GetSymbolByName handleRequest: %v", err)
	}
	handleID := syms[0].ID

	// handleRequest --imports--> NewServer (pending, because we insert with
	// "NewServer" as dst_symbol_name but don't resolve it to an edge).
	err = db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO pending_edges (src_symbol_id, dst_symbol_name, kind) VALUES (?, ?, ?)",
			handleID, "NewServer", "imports")
		return err
	})
	if err != nil {
		t.Fatalf("insert pending edge: %v", err)
	}

	handler := dependenciesHandler(store)

	// NewServer IS in the symbols table, Neighbors returns empty (no resolved
	// incoming edges), but pending_edges has handleRequest -> NewServer.
	result, err := handler(ctx, makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcpsdk.TextContent).Text)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "handleRequest") {
		t.Errorf("expected handleRequest in callers of NewServer (via pending edges), got: %s", text)
	}
}

// ── Scope filtering tests ──

// setupStoreWithTestFiles creates a store with both test and impl files,
// including symbols and edges, for testing scope filtering.
func setupStoreWithTestFiles(t *testing.T) *storage.Store {
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

	// Impl file
	implID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/mcp/server.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full", IsTest: false,
	})
	if err != nil {
		t.Fatalf("UpsertFile impl: %v", err)
	}
	implChunkIDs, err := store.InsertChunks(ctx, implID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "NewServer", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content: "func NewServer() *Server { return &Server{} }", TokenCount: 15},
	})
	if err != nil {
		t.Fatalf("InsertChunks impl: %v", err)
	}
	if _, err := store.InsertSymbols(ctx, implID, []types.SymbolRecord{
		{ChunkID: implChunkIDs[0], Name: "NewServer", Kind: "function", Line: 1, Signature: "()", Visibility: "exported"},
	}); err != nil {
		t.Fatalf("InsertSymbols impl: %v", err)
	}

	// Test file
	testID, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "internal/mcp/server_test.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full", IsTest: true,
	})
	if err != nil {
		t.Fatalf("UpsertFile test: %v", err)
	}
	testChunkIDs, err := store.InsertChunks(ctx, testID, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "TestNewServer", Kind: "function",
			StartLine: 1, EndLine: 15,
			Content: "func TestNewServer(t *testing.T) { NewServer() }", TokenCount: 12},
	})
	if err != nil {
		t.Fatalf("InsertChunks test: %v", err)
	}
	if _, err := store.InsertSymbols(ctx, testID, []types.SymbolRecord{
		{ChunkID: testChunkIDs[0], Name: "TestNewServer", Kind: "function", Line: 1, Signature: "(t *testing.T)", Visibility: "exported"},
	}); err != nil {
		t.Fatalf("InsertSymbols test: %v", err)
	}

	// Resolve edge: TestNewServer -> NewServer
	implSyms, err := store.GetSymbolByName(ctx, "NewServer")
	if err != nil || len(implSyms) == 0 {
		t.Fatalf("GetSymbolByName NewServer: err=%v, len=%d", err, len(implSyms))
	}
	testSyms, err := store.GetSymbolByName(ctx, "TestNewServer")
	if err != nil || len(testSyms) == 0 {
		t.Fatalf("GetSymbolByName TestNewServer: err=%v, len=%d", err, len(testSyms))
	}
	if err := store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(storage.SqliteTxHandle).Tx
		_, err := tx.ExecContext(ctx,
			"INSERT INTO edges (src_symbol_id, dst_symbol_id, kind, file_id) VALUES (?, ?, 'calls', ?)",
			testSyms[0].ID, implSyms[0].ID, testID)
		return err
	}); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	return store
}

func TestSearchHandler_Scope_DefaultExcludesTests(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())
	cfg.SearchMinScore = 0 // disable score threshold for test
	handler := searchHandler(engine, cfg)

	result, err := handler(context.Background(), makeSearchRequest(map[string]any{
		"query": "NewServer",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if strings.Contains(text, "server_test.go") {
		t.Errorf("default scope should exclude test files, got: %s", text)
	}
	if !strings.Contains(text, "server.go") {
		t.Errorf("default scope should include impl files, got: %s", text)
	}
}

func TestSearchHandler_Scope_Test(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())
	cfg.SearchMinScore = 0
	handler := searchHandler(engine, cfg)

	result, err := handler(context.Background(), makeSearchRequest(map[string]any{
		"query": "NewServer",
		"scope": "test",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	// Check that impl file (without _test) is NOT present — but test file IS
	if strings.Contains(text, "internal/mcp/server.go") && !strings.Contains(text, "_test.go") {
		t.Errorf("scope=test should exclude impl files, got: %s", text)
	}
	if !strings.Contains(text, "server_test.go") {
		t.Errorf("scope=test should include test files, got: %s", text)
	}
}

func TestSearchHandler_Scope_All(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	engine := core.NewQueryEngine(store, t.TempDir())
	cfg := types.DefaultConfig(t.TempDir())
	cfg.SearchMinScore = 0
	handler := searchHandler(engine, cfg)

	result, err := handler(context.Background(), makeSearchRequest(map[string]any{
		"query": "NewServer",
		"scope": "all",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "server.go") {
		t.Errorf("scope=all should include impl files, got: %s", text)
	}
	if !strings.Contains(text, "server_test.go") {
		t.Errorf("scope=all should include test files, got: %s", text)
	}
}

func TestSymbolsHandler_Scope_DefaultExcludesTests(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	handler := symbolsHandler(store)

	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name": "TestNewServer",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	// Default scope is impl — TestNewServer is in a test file, should be excluded
	text := result.Content[0].(mcpsdk.TextContent).Text
	if text != "null" && text != "[]" {
		var syms []json.RawMessage
		if json.Unmarshal([]byte(text), &syms) == nil && len(syms) > 0 {
			t.Errorf("default scope should exclude test symbols, got: %s", text)
		}
	}
}

func TestSymbolsHandler_Scope_Test(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	handler := symbolsHandler(store)

	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"name":  "TestNewServer",
		"scope": "test",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "TestNewServer") {
		t.Errorf("scope=test should include test symbols, got: %s", text)
	}
}

func TestDependenciesHandler_Scope_DefaultExcludesTests(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	handler := dependenciesHandler(store)

	// NewServer callers should exclude TestNewServer (in test file)
	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if strings.Contains(text, "TestNewServer") {
		t.Errorf("default scope should exclude test callers, got: %s", text)
	}
}

func TestDependenciesHandler_Scope_All(t *testing.T) {
	t.Parallel()
	store := setupStoreWithTestFiles(t)
	handler := dependenciesHandler(store)

	result, err := handler(context.Background(), makeToolRequest(map[string]any{
		"symbol":    "NewServer",
		"direction": "callers",
		"scope":     "all",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := result.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "TestNewServer") {
		t.Errorf("scope=all should include test callers, got: %s", text)
	}
}

// Ensure server import is used.
var _ server.ResourceHandlerFunc
