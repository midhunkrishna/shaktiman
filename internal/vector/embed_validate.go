package vector

import (
	"log/slog"
	"math"
)

// sanitizeEmbeddings filters out vectors that would corrupt downstream
// similarity search: all-zero vectors (produced by some embedders on
// whitespace-only or degenerate inputs), NaN/Inf components (model or
// transport errors surfaced silently), and dimension mismatches (which
// would panic bruteforce.Search on a shorter stored vector or produce
// nonsense for a longer one).
//
// Returns the filtered chunk-id and vector slices, and a counter of
// rejected vectors by reason. The returned slices are fresh allocations
// and preserve the order of accepted entries.
//
// expectDim == 0 disables the dim check (useful when the caller doesn't
// know the store's dim yet, e.g. first-time upsert).
func sanitizeEmbeddings(chunkIDs []int64, vectors [][]float32, expectDim int, log *slog.Logger) ([]int64, [][]float32, embedReject) {
	var r embedReject
	outIDs := make([]int64, 0, len(chunkIDs))
	outVecs := make([][]float32, 0, len(vectors))

	n := len(vectors)
	if len(chunkIDs) < n {
		n = len(chunkIDs)
	}

	for i := 0; i < n; i++ {
		v := vectors[i]
		id := chunkIDs[i]

		if len(v) == 0 {
			r.empty++
			continue
		}
		if expectDim > 0 && len(v) != expectDim {
			r.dimMismatch++
			if log != nil {
				log.Warn("embedding rejected: dim mismatch",
					"chunk_id", id, "got", len(v), "want", expectDim)
			}
			continue
		}

		reason := scanVectorHealth(v)
		switch reason {
		case vecOK:
			outIDs = append(outIDs, id)
			outVecs = append(outVecs, v)
		case vecZero:
			r.zero++
		case vecNaN:
			r.naN++
			if log != nil {
				log.Warn("embedding rejected: NaN component", "chunk_id", id)
			}
		case vecInf:
			r.inf++
			if log != nil {
				log.Warn("embedding rejected: Inf component", "chunk_id", id)
			}
		}
	}

	return outIDs, outVecs, r
}

type embedReject struct {
	zero        int
	naN         int
	inf         int
	empty       int
	dimMismatch int
}

func (r embedReject) total() int {
	return r.zero + r.naN + r.inf + r.empty + r.dimMismatch
}

type vecHealth int

const (
	vecOK vecHealth = iota
	vecZero
	vecNaN
	vecInf
)

// scanVectorHealth returns the first health-reason encountered on v.
// An all-zero vector is reported as vecZero even if it contains no
// NaN/Inf, because it would cause downstream cosine similarity to
// degenerate to 0 across every comparison.
func scanVectorHealth(v []float32) vecHealth {
	allZero := true
	for _, f := range v {
		f64 := float64(f)
		if math.IsNaN(f64) {
			return vecNaN
		}
		if math.IsInf(f64, 0) {
			return vecInf
		}
		if f != 0 {
			allZero = false
		}
	}
	if allZero {
		return vecZero
	}
	return vecOK
}
