// Package vector provides in-process vector storage and similarity search.
package vector

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// BruteForceStore is an in-memory vector store using O(n) cosine scan.
// Thread-safe via RWMutex. Suitable for ≤100K vectors (~225MB at 768 dims).
type BruteForceStore struct {
	mu      sync.RWMutex
	vectors map[int64][]float32
	dim     int
}

// NewBruteForceStore creates an empty store with the given vector dimensionality.
func NewBruteForceStore(dim int) *BruteForceStore {
	return &BruteForceStore{
		vectors: make(map[int64][]float32),
		dim:     dim,
	}
}

// Search returns the topK most similar vectors by cosine similarity.
func (s *BruteForceStore) Search(_ context.Context, query []float32, topK int) ([]types.VectorResult, error) {
	if len(query) != s.dim {
		return nil, fmt.Errorf("query dim %d != store dim %d", len(query), s.dim)
	}

	s.mu.RLock()
	n := len(s.vectors)
	if n == 0 {
		s.mu.RUnlock()
		return nil, nil
	}

	type scored struct {
		id    int64
		score float64
	}
	results := make([]scored, 0, n)
	for id, vec := range s.vectors {
		sim := cosineSimilarity(query, vec)
		results = append(results, scored{id, sim})
	}
	s.mu.RUnlock()

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topK > len(results) {
		topK = len(results)
	}
	out := make([]types.VectorResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = types.VectorResult{
			ChunkID: results[i].id,
			Score:   results[i].score,
		}
	}
	return out, nil
}

// Upsert inserts or replaces a vector for the given chunk ID.
func (s *BruteForceStore) Upsert(_ context.Context, chunkID int64, vector []float32) error {
	if len(vector) != s.dim {
		return fmt.Errorf("vector dim %d != store dim %d", len(vector), s.dim)
	}
	s.mu.Lock()
	s.vectors[chunkID] = vector
	s.mu.Unlock()
	return nil
}

// UpsertBatch inserts multiple vectors in a single lock acquisition.
// All dimensions are validated before any writes to prevent partial updates.
func (s *BruteForceStore) UpsertBatch(_ context.Context, chunkIDs []int64, vectors [][]float32) error {
	if len(chunkIDs) != len(vectors) {
		return fmt.Errorf("chunkIDs len %d != vectors len %d", len(chunkIDs), len(vectors))
	}
	// Pre-validate all dimensions before acquiring write lock
	for i, v := range vectors {
		if len(v) != s.dim {
			return fmt.Errorf("vector[%d] dim %d != store dim %d", i, len(v), s.dim)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, id := range chunkIDs {
		s.vectors[id] = vectors[i]
	}
	return nil
}

// Delete removes vectors for the given chunk IDs.
func (s *BruteForceStore) Delete(_ context.Context, chunkIDs []int64) error {
	s.mu.Lock()
	for _, id := range chunkIDs {
		delete(s.vectors, id)
	}
	s.mu.Unlock()
	return nil
}

// Count returns the number of stored vectors.
func (s *BruteForceStore) Count(_ context.Context) (int, error) {
	s.mu.RLock()
	n := len(s.vectors)
	s.mu.RUnlock()
	return n, nil
}

// Has returns true if a vector exists for the given chunk ID.
func (s *BruteForceStore) Has(_ context.Context, chunkID int64) bool {
	s.mu.RLock()
	_, ok := s.vectors[chunkID]
	s.mu.RUnlock()
	return ok
}

// Dim returns the vector dimensionality.
func (s *BruteForceStore) Dim() int {
	return s.dim
}

// persistence format v2: magic(4) + version(4) + dim(4) + count(4) + entries(id:8 + vec:dim*4) + crc32(4)
var embMagic = [4]byte{'E', 'M', 'B', 'V'}

// SaveToDisk persists all vectors to a binary file using atomic replace.
// Writes format v2 with CRC32 integrity footer.
// Snapshots vectors under RLock, then releases before disk I/O.
func (s *BruteForceStore) SaveToDisk(path string) error {
	start := time.Now()

	// Snapshot under lock — release before disk I/O to avoid blocking UpsertBatch
	s.mu.RLock()
	snapshot := make(map[int64][]float32, len(s.vectors))
	dim := s.dim
	for id, vec := range s.vectors {
		cp := make([]float32, len(vec))
		copy(cp, vec)
		snapshot[id] = cp
	}
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create embedding dir: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".embeddings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // clean up on failure

	bw := bufio.NewWriter(tmp)
	h := crc32.NewIEEE()
	w := io.MultiWriter(bw, h) // write to both buffer and hasher

	// Header
	if err := binary.Write(w, binary.LittleEndian, embMagic); err != nil {
		tmp.Close()
		return fmt.Errorf("write magic: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(2)); err != nil {
		tmp.Close()
		return fmt.Errorf("write version: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(dim)); err != nil {
		tmp.Close()
		return fmt.Errorf("write dim: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(snapshot))); err != nil {
		tmp.Close()
		return fmt.Errorf("write count: %w", err)
	}

	// Entries (from snapshot, no lock held)
	for id, vec := range snapshot {
		if err := binary.Write(w, binary.LittleEndian, id); err != nil {
			tmp.Close()
			return fmt.Errorf("write id: %w", err)
		}
		if err := binary.Write(w, binary.LittleEndian, vec); err != nil {
			tmp.Close()
			return fmt.Errorf("write vector: %w", err)
		}
	}

	// CRC32 footer (written only to buffer, not hasher)
	if err := binary.Write(bw, binary.LittleEndian, h.Sum32()); err != nil {
		tmp.Close()
		return fmt.Errorf("write crc32: %w", err)
	}

	if err := bw.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	slog.Info("embeddings saved to disk",
		"path", path, "count", len(snapshot),
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// Safety bounds for persistence file headers.
const (
	maxDim         = 4096
	maxVectorCount = 2_000_000
)

// LoadFromDisk loads vectors from a binary persistence file.
// Returns nil if the file does not exist (fresh start).
// Supports v1 (no checksum) and v2 (CRC32 integrity check).
func (s *BruteForceStore) LoadFromDisk(path string) error {
	start := time.Now()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open embeddings file: %w", err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	h := crc32.NewIEEE()
	r := io.TeeReader(br, h) // feeds all reads into CRC hasher

	var magic [4]byte
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return fmt.Errorf("read magic: %w", err)
	}
	if magic != embMagic {
		return fmt.Errorf("invalid embedding file magic: %q", magic)
	}

	var version, dim, count uint32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if version != 1 && version != 2 {
		return fmt.Errorf("unsupported embedding file version: %d", version)
	}
	if err := binary.Read(r, binary.LittleEndian, &dim); err != nil {
		return fmt.Errorf("read dim: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return fmt.Errorf("read count: %w", err)
	}

	// Validate bounds to prevent OOM from crafted files
	if dim > maxDim {
		return fmt.Errorf("embedding dim %d exceeds max %d", dim, maxDim)
	}
	if count > maxVectorCount {
		return fmt.Errorf("embedding count %d exceeds max %d", count, maxVectorCount)
	}
	// Validate dim matches expected (if store was initialized with a specific dim)
	if s.dim > 0 && int(dim) != s.dim {
		return fmt.Errorf("embedding file dim %d != expected %d (model changed?)", dim, s.dim)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.dim = int(dim)
	s.vectors = make(map[int64][]float32, count)

	for i := uint32(0); i < count; i++ {
		var id int64
		if err := binary.Read(r, binary.LittleEndian, &id); err != nil {
			return fmt.Errorf("read id at entry %d: %w", i, err)
		}
		vec := make([]float32, dim)
		if err := binary.Read(r, binary.LittleEndian, &vec); err != nil {
			return fmt.Errorf("read vector at entry %d: %w", i, err)
		}
		s.vectors[id] = vec
	}

	// v2: verify CRC32 integrity
	if version == 2 {
		computed := h.Sum32()
		var stored uint32
		// Read CRC from underlying reader (NOT through TeeReader)
		if err := binary.Read(br, binary.LittleEndian, &stored); err != nil {
			return fmt.Errorf("read crc32: %w", err)
		}
		if computed != stored {
			return fmt.Errorf("embedding file CRC32 mismatch: computed %08x, stored %08x", computed, stored)
		}
	}

	slog.Info("embeddings loaded from disk",
		"path", path, "count", len(s.vectors),
		"duration_ms", time.Since(start).Milliseconds())
	return nil
}

// cosineSimilarity computes cosine similarity between two vectors.
// Returns a value in [-1, 1]. Normalized to [0, 1] by callers when needed.
func cosineSimilarity(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
