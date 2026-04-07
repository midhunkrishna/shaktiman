// Package bruteforce provides in-memory vector storage and similarity search.
package bruteforce

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
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

// scored holds a vector ID and its similarity score.
type scored struct {
	id    int64
	score float64
}

// scoreHeap is a min-heap of scored entries (lowest score on top).
type scoreHeap []scored

func (h scoreHeap) Len() int            { return len(h) }
func (h scoreHeap) Less(i, j int) bool   { return h[i].score < h[j].score }
func (h scoreHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *scoreHeap) Push(x any)          { *h = append(*h, x.(scored)) }
func (h *scoreHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// Search returns the topK most similar vectors by cosine similarity.
// Uses a min-heap to avoid allocating and sorting all N results.
func (s *BruteForceStore) Search(_ context.Context, query []float32, topK int) ([]types.VectorResult, error) {
	if topK <= 0 {
		return nil, nil
	}
	if len(query) != s.dim {
		return nil, fmt.Errorf("query dim %d != store dim %d", len(query), s.dim)
	}

	s.mu.RLock()
	n := len(s.vectors)
	if n == 0 {
		s.mu.RUnlock()
		return nil, nil
	}

	// Min-heap of size topK: keep the highest-scoring entries.
	k := topK
	if k > n {
		k = n
	}
	h := make(scoreHeap, 0, k)
	for id, vec := range s.vectors {
		sim := cosineSimilarity(query, vec)
		if h.Len() < topK {
			heap.Push(&h, scored{id, sim})
		} else if sim > h[0].score {
			h[0] = scored{id, sim}
			heap.Fix(&h, 0)
		}
	}
	s.mu.RUnlock()

	// Extract results in descending order (highest score first).
	out := make([]types.VectorResult, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		s := heap.Pop(&h).(scored)
		out[i] = types.VectorResult{ChunkID: s.id, Score: s.score}
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
func (s *BruteForceStore) Has(_ context.Context, chunkID int64) (bool, error) {
	s.mu.RLock()
	_, ok := s.vectors[chunkID]
	s.mu.RUnlock()
	return ok, nil
}

// Close is a no-op for the in-memory brute-force store.
func (s *BruteForceStore) Close() error {
	return nil
}

// Healthy always returns true for the in-memory brute-force store.
func (s *BruteForceStore) Healthy(_ context.Context) bool {
	return true
}

// Dim returns the vector dimensionality.
func (s *BruteForceStore) Dim() int {
	return s.dim
}

// persistence format v2: magic(4) + version(4) + dim(4) + count(4) + entries(id:8 + vec:dim*4) + crc32(4)
var embMagic = [4]byte{'E', 'M', 'B', 'V'}

// SaveToDisk persists all vectors to a binary file using atomic replace.
// Writes format v2 with CRC32 integrity footer.
// Snapshots vectors under RLock (fast copy), then serializes to disk without
// holding any lock. Uses math.Float32bits encoding (no reflection overhead).
func (s *BruteForceStore) SaveToDisk(path string) error {
	start := time.Now()

	// Snapshot under lock — release before disk I/O to avoid blocking writers.
	// Go's RWMutex is writer-preferring: a waiting writer blocks new readers too,
	// so holding RLock during disk writes would stall both Upsert and Search.
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

	bw := bufio.NewWriterSize(tmp, 1<<20) // 1MB buffer
	h := crc32.NewIEEE()
	w := io.MultiWriter(bw, h) // write to both buffer and hasher

	// Header (direct byte encoding — avoids binary.Write reflection overhead)
	var hdr [16]byte
	copy(hdr[:4], embMagic[:])
	binary.LittleEndian.PutUint32(hdr[4:8], 2)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(dim))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(snapshot)))
	if _, err := w.Write(hdr[:]); err != nil {
		tmp.Close()
		return fmt.Errorf("write header: %w", err)
	}

	// Entries (direct byte encoding — avoids binary.Write reflection per vector)
	entryBuf := make([]byte, 8+dim*4)
	for id, vec := range snapshot {
		binary.LittleEndian.PutUint64(entryBuf[:8], uint64(id))
		for j, v := range vec {
			binary.LittleEndian.PutUint32(entryBuf[8+j*4:], math.Float32bits(v))
		}
		if _, err := w.Write(entryBuf); err != nil {
			tmp.Close()
			return fmt.Errorf("write entry: %w", err)
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

	// Read entries (direct byte decoding — avoids binary.Read reflection per vector)
	entryBuf := make([]byte, 8+int(dim)*4)
	for i := uint32(0); i < count; i++ {
		if _, err := io.ReadFull(r, entryBuf); err != nil {
			return fmt.Errorf("read entry %d: %w", i, err)
		}
		id := int64(binary.LittleEndian.Uint64(entryBuf[:8]))
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = math.Float32frombits(binary.LittleEndian.Uint32(entryBuf[8+j*4:]))
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
// Uses float32 accumulation with 4x unrolling for SIMD-friendly codegen.
func cosineSimilarity(a, b []float32) float64 {
	var dot, normA, normB float32
	n := len(a)
	i := 0
	for ; i+3 < n; i += 4 {
		a0, a1, a2, a3 := a[i], a[i+1], a[i+2], a[i+3]
		b0, b1, b2, b3 := b[i], b[i+1], b[i+2], b[i+3]
		dot += a0*b0 + a1*b1 + a2*b2 + a3*b3
		normA += a0*a0 + a1*a1 + a2*a2 + a3*a3
		normB += b0*b0 + b1*b1 + b2*b2 + b3*b3
	}
	for ; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float64(dot) / (math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
}
