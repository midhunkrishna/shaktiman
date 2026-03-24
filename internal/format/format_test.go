package format

import (
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── SearchResults tests ──

func TestSearchResults_Empty(t *testing.T) {
	got := SearchResults(nil, false)
	if got != "No results found.\n" {
		t.Errorf("expected 'No results found.\\n', got %q", got)
	}
}

func TestSearchResults_Single(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "main",
		Kind:       "function",
		StartLine:  1,
		EndLine:    10,
		Content:    "func main() {\n\tfmt.Println(\"hello\")\n}\n",
		Score:      0.95,
	}}

	got := SearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("missing expected header, got:\n%s", got)
	}
	if !strings.Contains(got, "func main()") {
		t.Errorf("missing content, got:\n%s", got)
	}
	if strings.Contains(got, "score:") {
		t.Error("score should not appear without explain=true")
	}
}

func TestSearchResults_WithExplain(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "main",
		Kind:       "function",
		StartLine:  1,
		EndLine:    10,
		Content:    "func main() {}\n",
		Score:      0.8512,
	}}

	got := SearchResults(results, true)

	if !strings.Contains(got, "score:0.85") {
		t.Errorf("expected score in header, got:\n%s", got)
	}
}

func TestSearchResults_AdjacentSameFile(t *testing.T) {
	results := []types.ScoredResult{
		{
			Path: "src/main.go", SymbolName: "main", Kind: "function",
			StartLine: 1, EndLine: 10, Content: "func main() {}\n",
		},
		{
			Path: "src/main.go", SymbolName: "init", Kind: "function",
			StartLine: 12, EndLine: 15, Content: "func init() {}\n",
		},
		{
			Path: "src/other.go", SymbolName: "helper", Kind: "function",
			StartLine: 1, EndLine: 5, Content: "func helper() {}\n",
		},
	}

	got := SearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("expected full path on first chunk, got:\n%s", got)
	}
	if !strings.Contains(got, "--- :12-15 (init, function) ---") {
		t.Errorf("expected omitted path on second chunk, got:\n%s", got)
	}
	if !strings.Contains(got, "--- src/other.go:1-5 (helper, function) ---") {
		t.Errorf("expected full path on third chunk, got:\n%s", got)
	}
}

// ── LocateResults tests ──

func TestLocateResults_Empty(t *testing.T) {
	got := LocateResults(nil)
	if got != "No results found.\n" {
		t.Errorf("expected 'No results found.\\n', got %q", got)
	}
}

func TestLocateResults_Single(t *testing.T) {
	results := []types.ScoredResult{{
		Path: "src/main.go", SymbolName: "main", Kind: "function",
		StartLine: 1, EndLine: 10, Score: 0.91, TokenCount: 120,
	}}

	got := LocateResults(results)

	if !strings.HasPrefix(got, "1 results:\n") {
		t.Errorf("expected count header, got:\n%s", got)
	}
	if !strings.Contains(got, "src/main.go:1-10") {
		t.Errorf("missing path:lines, got:\n%s", got)
	}
	if !strings.Contains(got, "score:0.91") {
		t.Errorf("missing score, got:\n%s", got)
	}
	if !strings.Contains(got, "~120 tokens") {
		t.Errorf("missing token count, got:\n%s", got)
	}
}

func TestLocateResults_Multiple(t *testing.T) {
	results := []types.ScoredResult{
		{Path: "a.go", SymbolName: "Foo", Kind: "function", StartLine: 1, EndLine: 10, Score: 0.9, TokenCount: 50},
		{Path: "b.go", SymbolName: "Bar", Kind: "type", StartLine: 5, EndLine: 20, Score: 0.7, TokenCount: 80},
	}

	got := LocateResults(results)

	if !strings.HasPrefix(got, "2 results:\n") {
		t.Errorf("expected '2 results:', got:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d:\n%s", len(lines), got)
	}
}

// ── ContextPackage tests ──

func TestContextPackage_Nil(t *testing.T) {
	got := ContextPackage(nil)
	if got != "No context assembled.\n" {
		t.Errorf("expected 'No context assembled.\\n', got %q", got)
	}
}

func TestContextPackage_Normal(t *testing.T) {
	pkg := &types.ContextPackage{
		Chunks: []types.ScoredResult{
			{Path: "src/main.go", SymbolName: "main", Kind: "function",
				StartLine: 1, EndLine: 10, Content: "func main() {}\n", TokenCount: 20},
			{Path: "src/util.go", SymbolName: "helper", Kind: "function",
				StartLine: 5, EndLine: 15, Content: "func helper() {}\n", TokenCount: 25},
		},
		TotalTokens: 45,
		Strategy:    "hybrid_l0",
	}

	got := ContextPackage(pkg)

	if !strings.HasPrefix(got, "[context] 2 chunks, 45 tokens, strategy: hybrid_l0\n\n") {
		t.Errorf("expected summary line, got:\n%s", got)
	}
	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("missing first chunk header, got:\n%s", got)
	}
}

// ── Symbols tests ──

func TestSymbols_Empty(t *testing.T) {
	got := Symbols(nil)
	if got != "No symbols found.\n" {
		t.Errorf("expected 'No symbols found.\\n', got %q", got)
	}
}

func TestSymbols_Single(t *testing.T) {
	results := []SymbolResult{{
		Name: "NewServer", Kind: "function", FilePath: "internal/mcp/server.go",
		Line: 23, Signature: "func NewServer(input NewServerInput) *server.MCPServer", Visibility: "exported",
	}}

	got := Symbols(results)

	if !strings.HasPrefix(got, "1 symbols:\n") {
		t.Errorf("expected count header, got:\n%s", got)
	}
	if !strings.Contains(got, "NewServer") {
		t.Errorf("missing symbol name, got:\n%s", got)
	}
	if !strings.Contains(got, "internal/mcp/server.go:23") {
		t.Errorf("missing file:line, got:\n%s", got)
	}
	if !strings.Contains(got, "[exported]") {
		t.Errorf("missing visibility, got:\n%s", got)
	}
}

// ── Dependencies tests ──

func TestDependencies_Empty(t *testing.T) {
	got := Dependencies(nil)
	if got != "No dependencies found.\n" {
		t.Errorf("expected 'No dependencies found.\\n', got %q", got)
	}
}

func TestDependencies_Single(t *testing.T) {
	results := []DepResult{{
		Name: "searchHandler", Kind: "function", FilePath: "internal/mcp/tools.go", Line: 56,
	}}

	got := Dependencies(results)

	if !strings.Contains(got, "1 dependencies:\n") {
		t.Errorf("expected count header, got:\n%s", got)
	}
	if !strings.Contains(got, "searchHandler  function  internal/mcp/tools.go:56") {
		t.Errorf("missing dep line, got:\n%s", got)
	}
}

// ── Diffs tests ──

func TestDiffs_Empty(t *testing.T) {
	got := Diffs(nil)
	if got != "No diffs found.\n" {
		t.Errorf("expected 'No diffs found.\\n', got %q", got)
	}
}

func TestDiffs_WithSymbols(t *testing.T) {
	results := []DiffResult{{
		FileID: 1, FilePath: "internal/mcp/tools.go", ChangeType: "modified",
		LinesAdded: 12, LinesRemoved: 3, Timestamp: "2026-03-24T10:00:00Z",
		Symbols: []string{"searchHandler (modified)", "newFunc (added)"},
	}}

	got := Diffs(results)

	if !strings.Contains(got, "1 diffs:\n") {
		t.Errorf("expected count header, got:\n%s", got)
	}
	if !strings.Contains(got, "internal/mcp/tools.go  modified  +12 -3") {
		t.Errorf("missing diff line, got:\n%s", got)
	}
	if !strings.Contains(got, "    searchHandler (modified)") {
		t.Errorf("missing symbol line, got:\n%s", got)
	}
}

// ── IndexStats tests ──

func TestIndexStats_Nil(t *testing.T) {
	got := IndexStats(nil)
	if got != "No index stats available.\n" {
		t.Errorf("expected 'No index stats available.\\n', got %q", got)
	}
}

func TestIndexStats_Normal(t *testing.T) {
	stats := &types.IndexStats{
		TotalFiles:   42,
		TotalChunks:  310,
		TotalSymbols: 180,
		ParseErrors:  2,
		StaleFiles:   1,
		Languages:    map[string]int{"go": 30, "python": 12},
	}

	got := IndexStats(stats)

	if !strings.Contains(got, "Files:   42") {
		t.Errorf("missing files, got:\n%s", got)
	}
	if !strings.Contains(got, "Chunks:  310") {
		t.Errorf("missing chunks, got:\n%s", got)
	}
	if !strings.Contains(got, "Languages:") {
		t.Errorf("missing languages header, got:\n%s", got)
	}
}
