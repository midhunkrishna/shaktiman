package eval

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/testutil"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func setupEvalEngine(t *testing.T) *core.QueryEngine {
	t.Helper()
	store := testutil.NewTestWriterStore(t)

	// Seed test data
	ctx := context.Background()
	fid1, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "auth/login.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if _, err := store.InsertChunks(ctx, fid1, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "Login", Kind: "function",
			StartLine: 1, EndLine: 20,
			Content: "func Login(user string, pass string) error { return nil }",
			TokenCount: 20},
	}); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	fid2, err := store.UpsertFile(ctx, &types.FileRecord{
		Path: "auth/token.go", ContentHash: "h2", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if _, err := store.InsertChunks(ctx, fid2, []types.ChunkRecord{
		{ChunkIndex: 0, SymbolName: "ValidateToken", Kind: "function",
			StartLine: 1, EndLine: 15,
			Content: "func ValidateToken(token string) bool { return len(token) > 0 }",
			TokenCount: 18},
	}); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	return core.NewQueryEngine(store, t.TempDir(), "")
}

func TestEvaluate_BasicMetrics(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases: []TestCase{
			{
				Query:         "login authentication",
				ExpectedFiles: []string{"auth/login.go"},
				Description:   "should find login file",
			},
		},
		TopK: 10,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if len(summary.Cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(summary.Cases))
	}

	// We expect the FTS search to find auth/login.go
	r := summary.Cases[0]
	if r.Query != "login authentication" {
		t.Errorf("Query = %q, want %q", r.Query, "login authentication")
	}
}

func TestEvaluate_EmptyCases(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases:  []TestCase{},
		TopK:   10,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if len(summary.Cases) != 0 {
		t.Errorf("expected 0 cases, got %d", len(summary.Cases))
	}
	if summary.AvgRecall != 0 || summary.AvgPrecision != 0 || summary.AvgMRR != 0 {
		t.Errorf("expected zero averages for empty cases, got recall=%.2f prec=%.2f mrr=%.2f",
			summary.AvgRecall, summary.AvgPrecision, summary.AvgMRR)
	}
}

func TestEvaluate_DefaultTopK(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	// TopK=0 should default to 10
	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases: []TestCase{
			{Query: "token", ExpectedSymbols: []string{"ValidateToken"}},
		},
		TopK: 0,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if len(summary.Cases) != 1 {
		t.Fatalf("expected 1 case, got %d", len(summary.Cases))
	}
}

func TestEvaluate_NoResults(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases: []TestCase{
			{
				Query:         "xyznonexistent",
				ExpectedFiles: []string{"nonexistent.go"},
				Description:   "should find nothing",
			},
		},
		TopK: 10,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	r := summary.Cases[0]
	if r.Recall != 0 {
		t.Errorf("Recall = %f, want 0", r.Recall)
	}
	if r.Precision != 0 {
		t.Errorf("Precision = %f, want 0", r.Precision)
	}
	if r.MRR != 0 {
		t.Errorf("MRR = %f, want 0", r.MRR)
	}
	if len(r.Missing) == 0 {
		t.Error("expected missing items for no-results case")
	}
}

func TestEvaluate_MultipleCases(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases: []TestCase{
			{Query: "login", ExpectedFiles: []string{"auth/login.go"}},
			{Query: "token validate", ExpectedSymbols: []string{"ValidateToken"}},
		},
		TopK: 10,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if len(summary.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(summary.Cases))
	}
}

func TestEvaluate_FoundAndMissing(t *testing.T) {
	t.Parallel()

	// Test the evaluate() helper directly with controlled inputs
	tc := TestCase{
		Query:           "test",
		ExpectedFiles:   []string{"found.go", "missing.go"},
		ExpectedSymbols: []string{"FoundFunc"},
	}

	results := []types.ScoredResult{
		{Path: "found.go", SymbolName: "FoundFunc", Score: 0.9},
		{Path: "other.go", SymbolName: "OtherFunc", Score: 0.5},
	}

	r := evaluate(tc, results, 10)

	// One result is relevant (found.go:FoundFunc), 3 expected items → recall = 1/3
	if r.Recall < 0.3 || r.Recall > 0.4 {
		t.Errorf("Recall = %f, expected ~0.333", r.Recall)
	}
	if r.MRR != 1.0 {
		t.Errorf("MRR = %f, want 1.0 (first result is relevant)", r.MRR)
	}
	if len(r.Missing) == 0 {
		t.Error("expected missing.go in Missing list")
	}
	if len(r.Found) == 0 {
		t.Error("expected items in Found list")
	}
}

// TestEvaluate_NoiseQueries confirms that returning hits for a noise
// query is recorded as a false positive in the summary so a future
// ranking regression that starts matching nonsense is observable.
func TestEvaluate_NoiseQueries(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	// "login" matches our seeded auth/login.go. The harness records
	// any non-empty result set as a false-positive entry.
	summary, err := Evaluate(ctx, EvaluateInput{
		Engine:       engine,
		Cases:        []TestCase{},
		TopK:         5,
		NoiseQueries: []string{"login"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if summary.NoiseTotalReturns == 0 {
		t.Error("expected noise query 'login' to surface false positives")
	}
	if len(summary.NoiseFalsePosits) != 1 {
		t.Errorf("expected 1 noise hit, got %d", len(summary.NoiseFalsePosits))
	}
}

// TestEvaluate_MinMRRThreshold guards the regression-floor wiring.
// A high MinMRR against a setup with no relevant results must error
// while still returning the populated summary.
func TestEvaluate_MinMRRThreshold(t *testing.T) {
	t.Parallel()

	engine := setupEvalEngine(t)
	ctx := context.Background()

	summary, err := Evaluate(ctx, EvaluateInput{
		Engine: engine,
		Cases: []TestCase{
			{Query: "totally-unrelated-query-xyz", ExpectedFiles: []string{"nope.go"}},
		},
		TopK:   10,
		MinMRR: 0.9,
	})
	if err == nil {
		t.Fatal("expected ErrMRRBelowThreshold")
	}
	if summary == nil {
		t.Fatal("expected summary to be returned alongside threshold error")
	}
	if summary.AvgMRR >= 0.9 {
		t.Errorf("avgMRR = %f, expected below threshold for the test to be meaningful", summary.AvgMRR)
	}
}

// TestEvaluate_StrictSymbolMatch verifies the new contract: a result
// whose Path matches an entry in expected_symbols (but whose
// SymbolName does not) no longer counts as relevant. This catches the
// historical bug where path-vs-symbol leakage hid symbol-ranking
// regressions.
func TestEvaluate_StrictSymbolMatch(t *testing.T) {
	t.Parallel()

	tc := TestCase{
		Query:           "x",
		ExpectedSymbols: []string{"WantedSymbol"},
	}
	results := []types.ScoredResult{
		// Path matches symbol-name string but SymbolName doesn't.
		{Path: "WantedSymbol", SymbolName: "Other", Score: 0.9},
	}
	r := evaluate(tc, results, 10)
	if r.MRR != 0 {
		t.Errorf("MRR = %f, want 0 (no genuine symbol match)", r.MRR)
	}
	if len(r.Missing) != 1 {
		t.Errorf("expected 1 missing symbol, got %v", r.Missing)
	}
}
