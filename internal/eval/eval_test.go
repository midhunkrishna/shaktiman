package eval

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func setupEvalEngine(t *testing.T) *core.QueryEngine {
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

	return core.NewQueryEngine(store, t.TempDir())
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
