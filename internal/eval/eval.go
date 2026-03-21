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
	Cases        []Result `json:"cases"`
	AvgRecall    float64  `json:"avg_recall"`
	AvgPrecision float64  `json:"avg_precision"`
	AvgMRR       float64  `json:"avg_mrr"`
}

// EvaluateInput configures an evaluation run.
type EvaluateInput struct {
	Engine *core.QueryEngine
	Cases  []TestCase
	TopK   int
}

// Evaluate runs all test cases and computes aggregate metrics.
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

	return &Summary{
		Cases:        results,
		AvgRecall:    totalRecall / n,
		AvgPrecision: totalPrecision / n,
		AvgMRR:       totalMRR / n,
	}, nil
}

// evaluate computes metrics for a single test case.
func evaluate(tc TestCase, results []types.ScoredResult, topK int) Result {
	// Build set of expected items (files and symbols)
	expected := make(map[string]bool)
	for _, f := range tc.ExpectedFiles {
		expected[f] = true
	}
	for _, s := range tc.ExpectedSymbols {
		expected[s] = true
	}

	var found []string
	var missing []string
	firstRelevantRank := 0
	relevantInTopK := 0

	for rank, r := range results {
		if rank >= topK {
			break
		}

		isRelevant := expected[r.Path] || expected[r.SymbolName]
		if isRelevant {
			found = append(found, fmt.Sprintf("%s:%s", r.Path, r.SymbolName))
			relevantInTopK++
			if firstRelevantRank == 0 {
				firstRelevantRank = rank + 1 // 1-indexed
			}
		}
	}

	// Find missing expected items
	foundSet := make(map[string]bool)
	for _, r := range results {
		foundSet[r.Path] = true
		if r.SymbolName != "" {
			foundSet[r.SymbolName] = true
		}
	}
	for item := range expected {
		if !foundSet[item] {
			missing = append(missing, item)
		}
	}

	totalExpected := len(expected)
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
