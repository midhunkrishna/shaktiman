// Package eval provides a retrieval quality evaluation harness.
// It measures recall@K, precision@K, and MRR against curated test cases.
package eval

import (
	"context"
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// TestCase defines an evaluation query with expected results.
type TestCase struct {
	Query           string   `json:"query"`
	ExpectedFiles   []string `json:"expected_files"`
	ExpectedSymbols []string `json:"expected_symbols"`
	Description     string   `json:"description"`
}

// Result holds evaluation metrics for a single test case.
type Result struct {
	Query     string   `json:"query"`
	Recall    float64  `json:"recall"`
	Precision float64  `json:"precision"`
	MRR       float64  `json:"mrr"`
	Found     []string `json:"found"`
	Missing   []string `json:"missing"`
}

// Summary aggregates evaluation metrics across all test cases.
type Summary struct {
	Cases             []Result      `json:"cases"`
	AvgRecall         float64       `json:"avg_recall"`
	AvgPrecision      float64       `json:"avg_precision"`
	AvgMRR            float64       `json:"avg_mrr"`
	NoiseFalsePosits  []NoiseResult `json:"noise_false_positives,omitempty"`
	NoiseTotalReturns int           `json:"noise_total_returns"`
}

// NoiseResult records the result count for a noise query — a query
// the harness expects to return nothing meaningful. Returning any
// hit is a false-positive signal that ranking is too permissive.
type NoiseResult struct {
	Query     string `json:"query"`
	Returned  int    `json:"returned"`
	TopResult string `json:"top_result,omitempty"`
}

// EvaluateInput configures an evaluation run.
type EvaluateInput struct {
	Engine *core.QueryEngine
	Cases  []TestCase
	TopK   int

	// NoiseQueries are query strings the harness expects to return
	// nothing relevant. Each one is run; the count of returned results
	// is recorded in Summary.NoiseFalsePosits. Useful for catching
	// over-eager ranking that finds matches for nonsense.
	NoiseQueries []string

	// MinMRR, when > 0, makes Evaluate return ErrMRRBelowThreshold if
	// AvgMRR falls below this value. Lets CI fail loudly on retrieval
	// regressions instead of silently shipping degraded ranking.
	MinMRR float64
}

// ErrMRRBelowThreshold is returned by Evaluate when the aggregate MRR
// falls below EvaluateInput.MinMRR. The summary is still populated.
var ErrMRRBelowThreshold = fmt.Errorf("evaluation MRR below threshold")

// Evaluate runs all test cases and computes aggregate metrics. If
// MinMRR is non-zero and the aggregate AvgMRR falls below it,
// Evaluate returns ErrMRRBelowThreshold along with the populated
// summary so callers can still inspect the cases.
func Evaluate(ctx context.Context, input EvaluateInput) (*Summary, error) {
	if input.TopK <= 0 {
		input.TopK = 10
	}

	var results []Result
	var totalRecall, totalPrecision, totalMRR float64

	for _, tc := range input.Cases {
		searchResults, err := input.Engine.Search(ctx, core.SearchInput{
			Query:      tc.Query,
			MaxResults: input.TopK,
		})
		if err != nil {
			return nil, fmt.Errorf("search %q: %w", tc.Query, err)
		}

		r := evaluate(tc, searchResults, input.TopK)
		results = append(results, r)
		totalRecall += r.Recall
		totalPrecision += r.Precision
		totalMRR += r.MRR
	}

	n := float64(len(input.Cases))
	if n == 0 {
		n = 1
	}

	noisePosits, noiseTotal, err := runNoiseQueries(ctx, input.Engine, input.NoiseQueries, input.TopK)
	if err != nil {
		return nil, err
	}

	summary := &Summary{
		Cases:             results,
		AvgRecall:         totalRecall / n,
		AvgPrecision:      totalPrecision / n,
		AvgMRR:            totalMRR / n,
		NoiseFalsePosits:  noisePosits,
		NoiseTotalReturns: noiseTotal,
	}

	if input.MinMRR > 0 && summary.AvgMRR < input.MinMRR {
		return summary, fmt.Errorf("%w: AvgMRR=%.3f < threshold=%.3f",
			ErrMRRBelowThreshold, summary.AvgMRR, input.MinMRR)
	}
	return summary, nil
}

// runNoiseQueries executes each query in queries and records any
// returned results. Returns the list of false-positive entries (only
// queries that actually returned anything) plus the total count.
func runNoiseQueries(ctx context.Context, engine *core.QueryEngine, queries []string, topK int) ([]NoiseResult, int, error) {
	if len(queries) == 0 {
		return nil, 0, nil
	}
	var out []NoiseResult
	total := 0
	for _, q := range queries {
		results, err := engine.Search(ctx, core.SearchInput{Query: q, MaxResults: topK})
		if err != nil {
			return nil, 0, fmt.Errorf("noise search %q: %w", q, err)
		}
		if len(results) == 0 {
			continue
		}
		nr := NoiseResult{Query: q, Returned: len(results)}
		if len(results) > 0 {
			nr.TopResult = fmt.Sprintf("%s:%s", results[0].Path, results[0].SymbolName)
		}
		out = append(out, nr)
		total += len(results)
	}
	return out, total, nil
}

// evaluate computes metrics for a single test case. Files are matched
// strictly against tc.ExpectedFiles by Path, and symbols strictly
// against tc.ExpectedSymbols by SymbolName. Cross-matching (e.g. a
// file path appearing in expected_symbols) used to leak through; the
// stricter check makes a regression in symbol ranking visible even
// when the file ranks above it.
func evaluate(tc TestCase, results []types.ScoredResult, topK int) Result {
	expectedFiles := make(map[string]bool, len(tc.ExpectedFiles))
	for _, f := range tc.ExpectedFiles {
		expectedFiles[f] = true
	}
	expectedSymbols := make(map[string]bool, len(tc.ExpectedSymbols))
	for _, s := range tc.ExpectedSymbols {
		expectedSymbols[s] = true
	}

	var found []string
	var missing []string
	firstRelevantRank := 0
	relevantInTopK := 0

	// Track which expected items were satisfied so we can compute
	// missing without double-counting (a single result may satisfy
	// both a file and a symbol expectation).
	satisfiedFiles := make(map[string]bool)
	satisfiedSymbols := make(map[string]bool)

	for rank, r := range results {
		if rank >= topK {
			break
		}

		fileHit := expectedFiles[r.Path]
		symHit := r.SymbolName != "" && expectedSymbols[r.SymbolName]
		if !fileHit && !symHit {
			continue
		}
		if fileHit {
			satisfiedFiles[r.Path] = true
		}
		if symHit {
			satisfiedSymbols[r.SymbolName] = true
		}

		found = append(found, fmt.Sprintf("%s:%s", r.Path, r.SymbolName))
		relevantInTopK++
		if firstRelevantRank == 0 {
			firstRelevantRank = rank + 1 // 1-indexed
		}
	}

	for f := range expectedFiles {
		if !satisfiedFiles[f] {
			missing = append(missing, f)
		}
	}
	for s := range expectedSymbols {
		if !satisfiedSymbols[s] {
			missing = append(missing, s)
		}
	}

	totalExpected := len(expectedFiles) + len(expectedSymbols)
	if totalExpected == 0 {
		totalExpected = 1
	}

	recall := float64(relevantInTopK) / float64(totalExpected)
	if recall > 1.0 {
		recall = 1.0
	}

	k := topK
	if len(results) < k {
		k = len(results)
	}
	precision := 0.0
	if k > 0 {
		precision = float64(relevantInTopK) / float64(k)
	}

	mrr := 0.0
	if firstRelevantRank > 0 {
		mrr = 1.0 / float64(firstRelevantRank)
	}

	return Result{
		Query:     tc.Query,
		Recall:    recall,
		Precision: precision,
		MRR:       mrr,
		Found:     found,
		Missing:   missing,
	}
}
