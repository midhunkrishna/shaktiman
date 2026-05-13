package vector

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckBatchLengths(t *testing.T) {
	t.Parallel()
	if err := CheckBatchLengths(3, 3); err != nil {
		t.Errorf("equal lengths should pass: %v", err)
	}
	err := CheckBatchLengths(3, 4)
	if err == nil {
		t.Fatal("unequal lengths must error")
	}
	// Wording is part of the contract — backends prefix this and the
	// existing tests grep for "chunkIDs len" in error chains.
	if !strings.Contains(err.Error(), "chunkIDs len 3") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckBatchDims_PassThrough(t *testing.T) {
	t.Parallel()
	if err := CheckBatchDims(2, [][]float32{{1, 2, 3}, {4, 5, 6}}, 3); err != nil {
		t.Errorf("matching dims should pass: %v", err)
	}
}

func TestCheckBatchDims_RejectsMismatch(t *testing.T) {
	t.Parallel()
	err := CheckBatchDims(2, [][]float32{{1, 2, 3}, {4, 5}}, 3)
	if err == nil {
		t.Fatal("dim mismatch must error")
	}
	if !strings.Contains(err.Error(), "vector[1] dim 2 != store dim 3") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckBatchDims_LengthMismatchTakesPriority(t *testing.T) {
	t.Parallel()
	err := CheckBatchDims(3, [][]float32{{1, 2, 3}, {4, 5, 6}}, 3)
	if err == nil || !strings.Contains(err.Error(), "chunkIDs len 3") {
		t.Errorf("length error must be reported first, got %v", err)
	}
}

func TestForEachBatch_SlicesEvenly(t *testing.T) {
	t.Parallel()
	var got [][2]int
	err := ForEachBatch(7, 3, func(start, end int) error {
		got = append(got, [2]int{start, end})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{0, 3}, {3, 6}, {6, 7}}
	if len(got) != len(want) {
		t.Fatalf("windows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestForEachBatch_EmptyAndDegenerate(t *testing.T) {
	t.Parallel()
	calls := 0
	count := func(_, _ int) error { calls++; return nil }

	if err := ForEachBatch(0, 10, count); err != nil || calls != 0 {
		t.Errorf("n=0 must not invoke fn: err=%v calls=%d", err, calls)
	}
	if err := ForEachBatch(5, 0, count); err != nil || calls != 0 {
		t.Errorf("batchSize=0 must not invoke fn: err=%v calls=%d", err, calls)
	}
	if err := ForEachBatch(5, -1, count); err != nil || calls != 0 {
		t.Errorf("batchSize<0 must not invoke fn: err=%v calls=%d", err, calls)
	}
}

func TestForEachBatch_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("batch failed")
	calls := 0
	err := ForEachBatch(10, 3, func(_, _ int) error {
		calls++
		if calls == 2 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel, got %v", err)
	}
	if calls != 2 {
		t.Errorf("ForEachBatch must short-circuit on error: got %d calls", calls)
	}
}
