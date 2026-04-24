package vector

import (
	"math"
	"testing"
)

func TestSanitizeEmbeddings_Valid(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2, 3}
	vecs := [][]float32{
		{0.1, 0.2, 0.3},
		{-0.5, 0.5, 0.7},
		{0.9, 0.1, 0.05},
	}
	outIDs, outVecs, rej := sanitizeEmbeddings(ids, vecs, 3, nil)

	if len(outIDs) != 3 || len(outVecs) != 3 {
		t.Fatalf("expected 3 accepted; got %d/%d", len(outIDs), len(outVecs))
	}
	if rej.total() != 0 {
		t.Errorf("expected 0 rejections, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DropsZeroVector(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2}
	vecs := [][]float32{
		{0.1, 0.2, 0.3},
		{0, 0, 0},
	}
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 3, nil)
	if len(outIDs) != 1 || outIDs[0] != 1 {
		t.Errorf("expected [1]; got %v", outIDs)
	}
	if rej.zero != 1 {
		t.Errorf("expected zero=1, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DropsNaN(t *testing.T) {
	t.Parallel()

	nan := float32(math.NaN())
	ids := []int64{1, 2}
	vecs := [][]float32{
		{0.1, nan, 0.3},
		{0.5, 0.5, 0.5},
	}
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 3, nil)
	if len(outIDs) != 1 || outIDs[0] != 2 {
		t.Errorf("expected [2]; got %v", outIDs)
	}
	if rej.naN != 1 {
		t.Errorf("expected naN=1, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DropsInf(t *testing.T) {
	t.Parallel()

	inf := float32(math.Inf(1))
	ids := []int64{1}
	vecs := [][]float32{{0.1, inf, 0.3}}
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 3, nil)
	if len(outIDs) != 0 {
		t.Errorf("expected 0 accepted; got %v", outIDs)
	}
	if rej.inf != 1 {
		t.Errorf("expected inf=1, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DropsDimMismatch(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2}
	vecs := [][]float32{
		{0.1, 0.2}, // dim=2, expect=3
		{0.1, 0.2, 0.3},
	}
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 3, nil)
	if len(outIDs) != 1 || outIDs[0] != 2 {
		t.Errorf("expected [2]; got %v", outIDs)
	}
	if rej.dimMismatch != 1 {
		t.Errorf("expected dimMismatch=1, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DropsEmpty(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2}
	vecs := [][]float32{
		nil,
		{0.1, 0.2, 0.3},
	}
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 3, nil)
	if len(outIDs) != 1 || outIDs[0] != 2 {
		t.Errorf("expected [2]; got %v", outIDs)
	}
	if rej.empty != 1 {
		t.Errorf("expected empty=1, got %+v", rej)
	}
}

func TestSanitizeEmbeddings_DisableDimCheck(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2}
	vecs := [][]float32{
		{0.1, 0.2}, // dim=2
		{0.1, 0.2, 0.3}, // dim=3
	}
	// expectDim=0 disables dim check, so both survive.
	outIDs, _, rej := sanitizeEmbeddings(ids, vecs, 0, nil)
	if len(outIDs) != 2 {
		t.Errorf("expected both; got %v", outIDs)
	}
	if rej.dimMismatch != 0 {
		t.Errorf("expected dimMismatch=0, got %+v", rej)
	}
}
