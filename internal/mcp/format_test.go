package mcp

import (
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestFormatSearchResults_Empty(t *testing.T) {
	got := formatSearchResults(nil, false)
	if got != "No results found.\n" {
		t.Errorf("expected 'No results found.\\n', got %q", got)
	}
}

func TestFormatSearchResults_Single(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "main",
		Kind:       "function",
		StartLine:  1,
		EndLine:    10,
		Content:    "func main() {\n\tfmt.Println(\"hello\")\n}\n",
		Score:      0.95,
	}}

	got := formatSearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("missing expected header, got:\n%s", got)
	}
	if !strings.Contains(got, "func main()") {
		t.Errorf("missing content, got:\n%s", got)
	}
	// Score should NOT appear without explain
	if strings.Contains(got, "score:") {
		t.Error("score should not appear without explain=true")
	}
}

func TestFormatSearchResults_WithExplain(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "main",
		Kind:       "function",
		StartLine:  1,
		EndLine:    10,
		Content:    "func main() {}\n",
		Score:      0.8512,
	}}

	got := formatSearchResults(results, true)

	if !strings.Contains(got, "score:0.85") {
		t.Errorf("expected score in header, got:\n%s", got)
	}
}

func TestFormatSearchResults_MissingSymbolName(t *testing.T) {
	results := []types.ScoredResult{{
		Path:      "src/main.go",
		Kind:      "block",
		StartLine: 5,
		EndLine:   8,
		Content:   "var x = 1\n",
	}}

	got := formatSearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:5-8 (block) ---") {
		t.Errorf("expected kind-only parenthetical, got:\n%s", got)
	}
}

func TestFormatSearchResults_MissingKindAndSymbol(t *testing.T) {
	results := []types.ScoredResult{{
		Path:      "src/main.go",
		StartLine: 5,
		EndLine:   8,
		Content:   "var x = 1\n",
	}}

	got := formatSearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:5-8 ---") {
		t.Errorf("expected no parenthetical, got:\n%s", got)
	}
}

func TestFormatSearchResults_AdjacentSameFile(t *testing.T) {
	results := []types.ScoredResult{
		{
			Path:       "src/main.go",
			SymbolName: "main",
			Kind:       "function",
			StartLine:  1,
			EndLine:    10,
			Content:    "func main() {}\n",
		},
		{
			Path:       "src/main.go",
			SymbolName: "init",
			Kind:       "function",
			StartLine:  12,
			EndLine:    15,
			Content:    "func init() {}\n",
		},
		{
			Path:       "src/other.go",
			SymbolName: "helper",
			Kind:       "function",
			StartLine:  1,
			EndLine:    5,
			Content:    "func helper() {}\n",
		},
	}

	got := formatSearchResults(results, false)

	// First chunk: full path
	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("expected full path on first chunk, got:\n%s", got)
	}
	// Second chunk: path omitted (same file)
	if !strings.Contains(got, "--- :12-15 (init, function) ---") {
		t.Errorf("expected omitted path on second chunk, got:\n%s", got)
	}
	// Third chunk: different file, full path
	if !strings.Contains(got, "--- src/other.go:1-5 (helper, function) ---") {
		t.Errorf("expected full path on third chunk, got:\n%s", got)
	}
}

func TestFormatSearchResults_EmptyContent(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "stub",
		Kind:       "function",
		StartLine:  1,
		EndLine:    1,
	}}

	got := formatSearchResults(results, false)

	if !strings.Contains(got, "--- src/main.go:1-1 (stub, function) ---") {
		t.Errorf("expected header for empty-content chunk, got:\n%s", got)
	}
}

func TestFormatContextPackage_Nil(t *testing.T) {
	got := formatContextPackage(nil)
	if got != "No context assembled.\n" {
		t.Errorf("expected 'No context assembled.\\n', got %q", got)
	}
}

func TestFormatContextPackage_EmptyChunks(t *testing.T) {
	pkg := &types.ContextPackage{
		Chunks:      nil,
		TotalTokens: 0,
		Strategy:    "keyword_l2",
	}
	got := formatContextPackage(pkg)
	if got != "No context assembled.\n" {
		t.Errorf("expected 'No context assembled.\\n', got %q", got)
	}
}

func TestFormatContextPackage_Normal(t *testing.T) {
	pkg := &types.ContextPackage{
		Chunks: []types.ScoredResult{
			{
				Path:       "src/main.go",
				SymbolName: "main",
				Kind:       "function",
				StartLine:  1,
				EndLine:    10,
				Content:    "func main() {}\n",
				TokenCount: 20,
			},
			{
				Path:       "src/util.go",
				SymbolName: "helper",
				Kind:       "function",
				StartLine:  5,
				EndLine:    15,
				Content:    "func helper() {}\n",
				TokenCount: 25,
			},
		},
		TotalTokens: 45,
		Strategy:    "hybrid_l0",
	}

	got := formatContextPackage(pkg)

	if !strings.HasPrefix(got, "[context] 2 chunks, 45 tokens, strategy: hybrid_l0\n\n") {
		t.Errorf("expected summary line, got:\n%s", got)
	}
	if !strings.Contains(got, "--- src/main.go:1-10 (main, function) ---") {
		t.Errorf("missing first chunk header, got:\n%s", got)
	}
	if !strings.Contains(got, "--- src/util.go:5-15 (helper, function) ---") {
		t.Errorf("missing second chunk header, got:\n%s", got)
	}
}

// ── formatLocateResults tests ──

func TestFormatLocateResults_Empty(t *testing.T) {
	got := formatLocateResults(nil)
	if got != "No results found.\n" {
		t.Errorf("expected 'No results found.\\n', got %q", got)
	}
}

func TestFormatLocateResults_Single(t *testing.T) {
	results := []types.ScoredResult{{
		Path:       "src/main.go",
		SymbolName: "main",
		Kind:       "function",
		StartLine:  1,
		EndLine:    10,
		Score:      0.91,
		TokenCount: 120,
	}}

	got := formatLocateResults(results)

	if !strings.HasPrefix(got, "1 results:\n") {
		t.Errorf("expected count header, got:\n%s", got)
	}
	if !strings.Contains(got, "src/main.go:1-10") {
		t.Errorf("missing path:lines, got:\n%s", got)
	}
	if !strings.Contains(got, "main (function)") {
		t.Errorf("missing symbol info, got:\n%s", got)
	}
	if !strings.Contains(got, "score:0.91") {
		t.Errorf("missing score, got:\n%s", got)
	}
	if !strings.Contains(got, "~120 tokens") {
		t.Errorf("missing token count, got:\n%s", got)
	}
	// Should NOT contain any source code
	if strings.Contains(got, "func ") {
		t.Error("locate mode should not contain source code")
	}
}

func TestFormatLocateResults_Multiple(t *testing.T) {
	results := []types.ScoredResult{
		{Path: "a.go", SymbolName: "Foo", Kind: "function", StartLine: 1, EndLine: 10, Score: 0.9, TokenCount: 50},
		{Path: "b.go", SymbolName: "Bar", Kind: "type", StartLine: 5, EndLine: 20, Score: 0.7, TokenCount: 80},
	}

	got := formatLocateResults(results)

	if !strings.HasPrefix(got, "2 results:\n") {
		t.Errorf("expected '2 results:', got:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 { // header + 2 results
		t.Errorf("expected 3 lines, got %d:\n%s", len(lines), got)
	}
}

func TestFormatLocateResults_MissingFields(t *testing.T) {
	results := []types.ScoredResult{{
		Path:      "x.go",
		StartLine: 1,
		EndLine:   5,
		Score:     0.5,
	}}

	got := formatLocateResults(results)

	if !strings.Contains(got, "x.go:1-5") {
		t.Errorf("missing path:lines, got:\n%s", got)
	}
	if !strings.Contains(got, "score:0.50") {
		t.Errorf("missing score, got:\n%s", got)
	}
	// No symbol or kind — shouldn't have extra spaces/parens
	if strings.Contains(got, "()") {
		t.Error("should not have empty parens")
	}
	// No token count
	if strings.Contains(got, "tokens") {
		t.Error("should not show tokens when TokenCount=0")
	}
}
