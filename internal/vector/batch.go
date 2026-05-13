package vector

import "fmt"

// CheckBatchLengths returns an error when chunkIDs and vectors disagree on
// length. Every backend's UpsertBatch guards this the same way; pulling it
// here keeps the wording stable and eliminates 4 copies.
func CheckBatchLengths(chunkIDsLen, vectorsLen int) error {
	if chunkIDsLen != vectorsLen {
		return fmt.Errorf("chunkIDs len %d != vectors len %d", chunkIDsLen, vectorsLen)
	}
	return nil
}

// CheckBatchDims runs CheckBatchLengths and additionally verifies that
// every vector has exactly expectDim components. Used by in-memory
// backends (brute force, HNSW) that cannot rely on a server-side dim
// check; remote backends (pgvector, qdrant) let the server reject
// mismatched dimensions.
func CheckBatchDims(chunkIDsLen int, vectors [][]float32, expectDim int) error {
	if err := CheckBatchLengths(chunkIDsLen, len(vectors)); err != nil {
		return err
	}
	for i, v := range vectors {
		if len(v) != expectDim {
			return fmt.Errorf("vector[%d] dim %d != store dim %d", i, len(v), expectDim)
		}
	}
	return nil
}

// ForEachBatch invokes fn for each [start, end) window of [0, n) bounded
// by batchSize. Returns the first non-nil error from fn. When n == 0 or
// batchSize <= 0 the function returns nil without invoking fn.
//
// Remote vector backends (pgvector, qdrant) use this to slice UpsertBatch
// and Delete payloads into chunks the server will accept; the previous
// inline `for i := 0; i < n; i += maxBatchSize { end := i + maxBatchSize;
// if end > n { end = n } … }` pattern lived in four call sites.
func ForEachBatch(n, batchSize int, fn func(start, end int) error) error {
	if n <= 0 || batchSize <= 0 {
		return nil
	}
	for start := 0; start < n; start += batchSize {
		end := start + batchSize
		if end > n {
			end = n
		}
		if err := fn(start, end); err != nil {
			return err
		}
	}
	return nil
}
