package hnsw

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/midhunkrishna/hnswgo"
	"github.com/shaktimanai/shaktiman/internal/types"
)

const (
	defaultM              = 16
	defaultEfConstruction = 200
	defaultMaxElements    = 100_000
)

// StoreInput configures a new Store.
type StoreInput struct {
	Dim            int
	M              int    // max connections per node; 0 = default 16
	EfConstruction int    // build-time accuracy; 0 = default 200
	MaxElements    uint64 // initial capacity; 0 = default 100_000
}

// Store is a vector store backed by hnswlib via CGo.
// Provides O(log n) approximate nearest-neighbor search.
// Thread-safe: hnswgo.HnswIndex has internal RWMutex.
type Store struct {
	index *hnswgo.HnswIndex
	dim   int
	max   uint64
}

// NewStore creates an empty HNSW-backed vector store.
func NewStore(input StoreInput) (*Store, error) {
	m := input.M
	if m == 0 {
		m = defaultM
	}
	ef := input.EfConstruction
	if ef == 0 {
		ef = defaultEfConstruction
	}
	maxEl := input.MaxElements
	if maxEl == 0 {
		maxEl = defaultMaxElements
	}

	idx, err := hnswgo.New(input.Dim, m, ef, 100, maxEl, hnswgo.Cosine, false)
	if err != nil {
		return nil, fmt.Errorf("create hnsw index: %w", err)
	}

	return &Store{
		index: idx,
		dim:   input.Dim,
		max:   maxEl,
	}, nil
}

// Search returns the topK most similar vectors by cosine similarity.
func (s *Store) Search(_ context.Context, query []float32, topK int) ([]types.VectorResult, error) {
	if len(query) != s.dim {
		return nil, fmt.Errorf("query dim %d != store dim %d", len(query), s.dim)
	}

	count, err := s.index.GetCurrentCount()
	if err != nil {
		return nil, fmt.Errorf("get count: %w", err)
	}
	if count == 0 {
		return nil, nil
	}

	// Clamp topK to current count — hnswlib errors if topK > count.
	if uint64(topK) > count {
		topK = int(count)
	}

	results, err := s.index.SearchKNN([][]float32{query}, topK, 1)
	if err != nil {
		// hnswlib may fail when too many elements are soft-deleted relative
		// to topK (it can't fill the result set). Return empty in that case.
		if strings.Contains(err.Error(), "Cannot return the results") {
			return nil, nil
		}
		return nil, fmt.Errorf("hnsw search: %w", err)
	}

	if len(results) == 0 || len(results[0]) == 0 {
		return nil, nil
	}

	out := make([]types.VectorResult, len(results[0]))
	for i, r := range results[0] {
		// hnswlib cosine distance = 1 - cosine_similarity
		// Convert back: similarity = 1.0 - distance
		out[i] = types.VectorResult{
			ChunkID: int64(r.Label),
			Score:   float64(1.0 - r.Distance),
		}
	}
	return out, nil
}

// Upsert inserts or replaces a vector for the given chunk ID.
func (s *Store) Upsert(_ context.Context, chunkID int64, vector []float32) error {
	if len(vector) != s.dim {
		return fmt.Errorf("vector dim %d != store dim %d", len(vector), s.dim)
	}

	if err := s.ensureCapacity(1); err != nil {
		return err
	}

	return s.index.AddPoints([][]float32{vector}, []uint64{uint64(chunkID)}, 1, false)
}

// UpsertBatch inserts multiple vectors in a single call.
func (s *Store) UpsertBatch(_ context.Context, chunkIDs []int64, vectors [][]float32) error {
	if len(chunkIDs) != len(vectors) {
		return fmt.Errorf("chunkIDs len %d != vectors len %d", len(chunkIDs), len(vectors))
	}
	if len(chunkIDs) == 0 {
		return nil
	}

	for i, v := range vectors {
		if len(v) != s.dim {
			return fmt.Errorf("vector[%d] dim %d != store dim %d", i, len(v), s.dim)
		}
	}

	if err := s.ensureCapacity(uint64(len(chunkIDs))); err != nil {
		return err
	}

	labels := make([]uint64, len(chunkIDs))
	for i, id := range chunkIDs {
		labels[i] = uint64(id)
	}

	return s.index.AddPoints(vectors, labels, runtime.NumCPU(), false)
}

// Delete soft-deletes vectors via MarkDeleted.
func (s *Store) Delete(_ context.Context, chunkIDs []int64) error {
	for _, id := range chunkIDs {
		if err := s.index.MarkDeleted(uint64(id)); err != nil {
			// hnswlib errors if label not found — skip silently for idempotent deletes
			slog.Debug("hnsw mark deleted", "id", id, "err", err)
		}
	}
	return nil
}

// Has returns true if a vector exists for the given chunk ID.
func (s *Store) Has(_ context.Context, chunkID int64) (bool, error) {
	_, err := s.index.GetDataByLabel(uint64(chunkID))
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Count returns the number of stored vectors.
func (s *Store) Count(_ context.Context) (int, error) {
	c, err := s.index.GetCurrentCount()
	if err != nil {
		return 0, fmt.Errorf("get count: %w", err)
	}
	return int(c), nil
}

// Close releases C-side resources.
func (s *Store) Close() error {
	s.index.Free()
	return nil
}

// Healthy always returns true for the in-process HNSW store.
func (s *Store) Healthy(_ context.Context) bool {
	return true
}

// Dim returns the vector dimensionality.
func (s *Store) Dim() int {
	return s.dim
}

// SaveToDisk persists the HNSW index using atomic rename.
func (s *Store) SaveToDisk(path string) error {
	start := time.Now()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := s.index.Save(tmp); err != nil {
		return fmt.Errorf("save hnsw index: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomic rename: %w", err)
	}

	count, _ := s.index.GetCurrentCount()
	slog.Info("hnsw index saved",
		"path", path, "count", count,
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// LoadFromDisk loads an HNSW index from disk.
// Returns nil if the file does not exist (fresh start).
func (s *Store) LoadFromDisk(path string) error {
	start := time.Now()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	// Free old index before replacing
	if s.index != nil {
		s.index.Free()
	}

	idx, err := hnswgo.Load(path, hnswgo.Cosine, s.dim, s.max, false)
	if err != nil {
		// Re-create empty index so the store remains usable
		newIdx, newErr := hnswgo.New(s.dim, defaultM, defaultEfConstruction, 100, s.max, hnswgo.Cosine, false)
		if newErr == nil {
			s.index = newIdx
		}
		return fmt.Errorf("load hnsw index: %w", err)
	}
	s.index = idx

	count, _ := s.index.GetCurrentCount()

	// Update max to match loaded index capacity
	maxEl, err := s.index.GetMaxElements()
	if err == nil && maxEl > s.max {
		s.max = maxEl
	}

	slog.Info("hnsw index loaded",
		"path", path, "count", count,
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// ensureCapacity auto-grows the index if needed.
func (s *Store) ensureCapacity(needed uint64) error {
	count, err := s.index.GetCurrentCount()
	if err != nil {
		return fmt.Errorf("get count for capacity check: %w", err)
	}

	if count+needed <= s.max {
		return nil
	}

	newMax := s.max * 2
	if newMax < count+needed {
		newMax = count + needed
	}

	if err := s.index.ResizeIndex(newMax); err != nil {
		return fmt.Errorf("resize hnsw index: %w", err)
	}
	s.max = newMax
	return nil
}
