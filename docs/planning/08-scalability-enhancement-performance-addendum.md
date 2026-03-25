# Shaktiman: Performance Addendum

> Companion to `08-scalability-enhancement.md`. Covers hot path analysis, measurement strategy,
> and prioritized optimizations for the current codebase (82 files, 699 chunks, 694 symbols).
> All findings are from static analysis unless marked [measured].

---

## 0. Methodology

### Analysis Approach

1. Identified hot paths by tracing request flow from MCP handlers through engine, storage, and vector layers.
2. Classified each finding by severity (S1-S5) using the heuristic in the introduction.
3. For each finding: current code reference, issue, proposed fix with code sketch, expected impact, risk.

### Recommended Profiling Setup

Before implementing any optimization, establish baselines:

```bash
# CPU profile during cold index of a medium project (~1K files)
go test -tags sqlite_fts5 -run='^$' -bench=BenchmarkColdIndex \
  -cpuprofile=cpu.prof -benchtime=1x ./internal/daemon/

# Memory profile during search
go test -tags sqlite_fts5 -run='^$' -bench=BenchmarkSearch \
  -memprofile=mem.prof -benchtime=100x ./internal/core/

# Trace for concurrency analysis
go test -tags sqlite_fts5 -run=TestIntegration -trace=trace.out ./internal/daemon/

# View profiles
go tool pprof -http=:8080 cpu.prof
go tool trace trace.out
```

### Severity Heuristic

| Level | Description |
|-------|-------------|
| S1 | Fundamentally inefficient algorithm or data structure on hot path |
| S2 | Obvious bottleneck with high measurable payoff |
| S3 | Repeated allocation/contention/copying overhead in important path |
| S4 | Moderate inefficiency with limited but worthwhile gain |
| S5 | Speculative micro-optimization |

---

## 1. Current Performance Baseline

### Known Characteristics (from scalability plan analysis)

| Metric | Value | Source |
|--------|-------|--------|
| BruteForceStore at 141K vectors | ~870MB peak | Scalability plan |
| Vector search at 141K | O(n) full scan | `internal/vector/store.go:39-77` |
| Embedding drop rate at scale | 99.3% (140K/141K) | Scalability plan |
| SQLite writer connections | 1 (serialized) | `internal/storage/db.go:73` |
| SQLite reader connections | 4 | `internal/storage/db.go:109` |
| Enrichment workers | GOMAXPROCS/2 | Config default |
| Embed batch size | 32 | `internal/vector/embedding.go:302` |
| Writer channel capacity | 500 | `internal/daemon/writer.go:41-43` |
| Embed worker queue capacity | 1000 | `internal/vector/embedding.go:309` |

### Paths Requiring Measurement

| Path | How to Measure | Why |
|------|---------------|-----|
| Tree-sitter parse time per file | pprof CPU on `Parser.Parse` | Determines if parsing or I/O dominates cold index |
| Token counting overhead | Benchmark `tokenCounter.Count` | tiktoken encoding called per chunk, per merge, per split |
| FTS5 MATCH latency at scale | Benchmark `KeywordSearch` with 100K+ rows | Determines if FTS is a bottleneck for search |
| `computeStructuralScores` DB round-trips | Count queries per `HybridRank` call | N+1 query pattern suspect |
| `SaveToDisk` snapshot copy cost | pprof on `SaveToDisk` at 100K vectors | Full map copy under RLock |
| `filterChanged` at scale | Time with 10K+ files | Sequential DB lookups |

---

## 2. Hot Path Analysis

### 2.1 Search Path (MCP `search` tool -- user-facing latency)

```
searchHandler -> engine.Search -> determineLevel
  -> searchSemantic:
       1. KeywordSearch (FTS5 MATCH + hydrate each result)
       2. embedQuery (cache check or Ollama HTTP)
       3. vectorStore.Search (O(n) brute-force)
       4. mergeResults (hydrate vector-only entries one-by-one)
       5. HybridRank:
            a. ComputeChangeScores (2 batched SQL queries)
            b. computeStructuralScores (N * 3 SQL queries)
       6. filterByScore + recordSession
```

**Critical observation**: `computeStructuralScores` is an N+1 query storm. For each candidate:
- 1 query: `GetChunkByID` (lookup symbol name)
- 1 query: `GetSymbolByName` (resolve symbol ID)
- 1 query: `Neighbors` CTE (BFS)
- N queries: `GetSymbolByID` per neighbor (in `lookupChunkIDsForSymbols`)

With 20 candidates and average 5 neighbors each, that is ~20*(3 + 5) = 160 individual SQL queries per search.

### 2.2 Cold Indexing Path

```
IndexProject -> ScanRepo -> IndexAll
  ScanRepo:
    WalkDir -> ReadFile -> SHA256 per file
  IndexAll:
    filterChanged (N sequential GetFileByPath queries)
    DisableFTSTriggers
    N workers:
      readFileContent -> Parser.Parse -> writer.Submit
    RebuildFTS
    EnableFTSTriggers
  WriterManager.processJob:
    per file: WithWriteTx containing ~8-12 SQL statements
```

### 2.3 Embedding Path

```
EmbedProject -> queueEmbeddings -> embedWorker.Run
  queueEmbeddings:
    GetChunksNeedingEmbedding (loads ALL chunks into memory)
    for each: vectorStore.Has (RLock per check)
  embedWorker.Run:
    batch of 32 -> OllamaClient.EmbedBatch (HTTP)
                -> store.UpsertBatch
                -> onBatchDone -> MarkChunksEmbedded
```

### 2.4 Vector Search Path

```
BruteForceStore.Search:
  RLock entire map
  Allocate results slice of size n (all vectors)
  Compute cosineSimilarity for every vector
  sort.Slice all results
  Allocate output slice
  RUnlock
```

---

## 3. Quick Wins (Low Risk, High Impact)

### QW-1: Eliminate N+1 Queries in `computeStructuralScores` [S1]

**File**: `/Users/minimac/p/shaktiman/internal/core/ranker.go:138-177`

**Issue**: For `maxResults=10` (typical), this function executes 80-160 individual SQL queries. Each query goes through `database/sql` connection pool overhead, SQLite page cache lookups, and Go-to-C bridge via CGo.

**Fix**: Batch the lookups. Replace per-candidate sequential queries with two batched queries.

```go
func computeStructuralScores(ctx context.Context, store types.MetadataStore,
    candidates []types.ScoredResult) map[int64]float64 {
    scores := make(map[int64]float64, len(candidates))
    if len(candidates) == 0 {
        return scores
    }

    // Batch 1: Get all symbol IDs for candidate chunks in one query
    chunkIDs := make([]int64, len(candidates))
    for i, c := range candidates {
        chunkIDs[i] = c.ChunkID
    }
    chunkSymbolMap := store.BatchGetSymbolIDsForChunks(ctx, chunkIDs)

    // Batch 2: Get all neighbors for all symbols in one CTE
    symbolIDs := make([]int64, 0, len(chunkSymbolMap))
    for _, symID := range chunkSymbolMap {
        if symID != 0 {
            symbolIDs = append(symbolIDs, symID)
        }
    }
    neighborMap := store.BatchNeighbors(ctx, symbolIDs, 2)

    // Compute overlaps from in-memory maps
    candidateSet := make(map[int64]bool, len(candidates))
    for _, c := range candidates {
        candidateSet[c.ChunkID] = true
    }
    // ... overlap computation using maps instead of per-item queries
}
```

**Required new store methods**:
- `BatchGetSymbolIDsForChunks(ctx, chunkIDs) map[int64]int64` -- single JOIN query
- `BatchNeighbors(ctx, symbolIDs, depth) map[int64][]int64` -- single CTE with `IN` clause

**Expected impact**: Reduce search-path SQL queries from ~160 to ~4. At ~0.1ms per query, saves ~15ms per search.
**Risk**: Low. New methods are additive; old path remains as fallback.

---

### QW-2: Batch `filterChanged` into Single Query [S2]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/enrichment.go:221-233`

**Issue**: `filterChanged` calls `GetFileByPath` once per file. For Kubernetes (12,867 files), that is 12,867 sequential SELECT queries.

**Fix**: Single batch query using `IN` clause or temp table.

```go
func (ep *EnrichmentPipeline) filterChanged(ctx context.Context,
    files []ScannedFile) ([]ScannedFile, error) {
    // Build path -> hash map
    pathHashes := make(map[string]string, len(files))
    paths := make([]string, len(files))
    for i, f := range files {
        pathHashes[f.Path] = f.ContentHash
        paths[i] = f.Path
    }

    // Single batch query for all existing file hashes
    existing, err := ep.store.BatchGetFileHashes(ctx, paths)
    if err != nil {
        return nil, err
    }

    var changed []ScannedFile
    for _, f := range files {
        if hash, found := existing[f.Path]; !found || hash != f.ContentHash {
            changed = append(changed, f)
        }
    }
    return changed, nil
}
```

**Required new store method**:
- `BatchGetFileHashes(ctx, paths []string) (map[string]string, error)` -- returns path -> content_hash

For very large file lists, chunk the `IN` clause into groups of 500 (SQLite `SQLITE_MAX_VARIABLE_NUMBER` default is 999).

**Expected impact**: Reduce filterChanged from O(n) queries to O(n/500) queries. For 12K files: ~13K queries to ~26.
**Risk**: Low.

---

### QW-3: Avoid Full Snapshot Copy in `SaveToDisk` [S2]

**File**: `/Users/minimac/p/shaktiman/internal/vector/store.go:147-159`

**Issue**: `SaveToDisk` copies every vector slice under RLock. At 141K vectors x 768 dims x 4 bytes = ~412MB of allocations just for the snapshot, doubling peak memory to ~870MB.

**Fix**: Write directly to disk under RLock without the snapshot copy. Since `SaveToDisk` uses atomic rename, readers of the old file are not affected.

```go
func (s *BruteForceStore) SaveToDisk(path string) error {
    start := time.Now()
    if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
        return fmt.Errorf("create embedding dir: %w", err)
    }

    tmp, err := os.CreateTemp(filepath.Dir(path), ".embeddings-*.tmp")
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }
    tmpName := tmp.Name()
    defer os.Remove(tmpName)

    bw := bufio.NewWriterSize(tmp, 1<<20) // 1MB buffer
    h := crc32.NewIEEE()
    w := io.MultiWriter(bw, h)

    s.mu.RLock()
    dim := s.dim
    count := len(s.vectors)

    // Write header
    binary.Write(w, binary.LittleEndian, embMagic)
    binary.Write(w, binary.LittleEndian, uint32(2))
    binary.Write(w, binary.LittleEndian, uint32(dim))
    binary.Write(w, binary.LittleEndian, uint32(count))

    // Write entries directly from map (no copy)
    for id, vec := range s.vectors {
        binary.Write(w, binary.LittleEndian, id)
        binary.Write(w, binary.LittleEndian, vec)
    }
    s.mu.RUnlock()

    // CRC and flush outside lock
    binary.Write(bw, binary.LittleEndian, h.Sum32())
    bw.Flush()
    tmp.Close()
    os.Rename(tmpName, path)
    return nil
}
```

**Tradeoff**: RLock is held longer (disk I/O duration instead of memory copy duration). For buffered writes to local SSD this is typically <1s even at 141K vectors. `UpsertBatch` (which needs write lock) will block during this window. Acceptable for a periodic save operation.

**Expected impact**: Eliminates ~412MB transient allocation. Peak memory drops from ~870MB to ~435MB at 141K vectors.
**Risk**: Medium. RLock held during I/O. Mitigated by buffered writer and local-only file access.

---

### QW-4: Use `container/heap` for Top-K in Vector Search [S2]

**File**: `/Users/minimac/p/shaktiman/internal/vector/store.go:39-77`

**Issue**: `Search` allocates a slice of ALL n scored results, then sorts the entire slice. For 141K vectors, this allocates 141K `scored` structs (~3.3MB) and sorts them (O(n log n)).

**Fix**: Use a min-heap of size K. Only track top-K results, reducing allocation to K structs and time to O(n log K).

```go
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

    // Min-heap of size topK
    h := &scoreHeap{}
    heap.Init(h)
    for id, vec := range s.vectors {
        sim := cosineSimilarity(query, vec)
        if h.Len() < topK {
            heap.Push(h, scored{id, sim})
        } else if sim > (*h)[0].score {
            (*h)[0] = scored{id, sim}
            heap.Fix(h, 0)
        }
    }
    s.mu.RUnlock()

    // Extract results in descending order
    out := make([]types.VectorResult, h.Len())
    for i := len(out) - 1; i >= 0; i-- {
        s := heap.Pop(h).(scored)
        out[i] = types.VectorResult{ChunkID: s.id, Score: s.score}
    }
    return out, nil
}
```

**Expected impact**: Allocation drops from O(n) to O(K). For K=20, n=141K: 141K structs to 20 structs. Sort time: O(n log n) to O(n log K). At 141K: ~17*141K to ~4.3*141K comparisons.
**Risk**: Low. Standard algorithm change.

---

### QW-5: Optimize `cosineSimilarity` with SIMD-Friendly Loop [S3]

**File**: `/Users/minimac/p/shaktiman/internal/vector/store.go:322-336`

**Issue**: The inner loop converts `float32` to `float64` per element, preventing compiler auto-vectorization. This is the innermost hot loop for vector search (called 141K times per search).

**Fix**: Operate in `float32` throughout, convert to `float64` only for the final sqrt. Unroll the loop 4x for SIMD friendliness.

```go
func cosineSimilarity(a, b []float32) float64 {
    var dot, normA, normB float32
    n := len(a)
    i := 0
    // Unrolled loop (compiler hint for SIMD)
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
```

**Expected impact**: 2-4x faster inner loop. With 768-dim vectors and 141K iterations, this saves significant CPU. Measure with `BenchmarkCosineSimilarity`.
**Risk**: Low. Pure computation change. Verify with unit tests for numerical accuracy.

---

### QW-6: Pre-allocate `[]byte` for `readFileContent` [S4]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/enrichment.go:324-333`

**Issue**: `os.ReadFile` allocates a new byte slice per file. During cold indexing of 12K files, this creates 12K allocations. The `Stat` call already provides the file size.

**Fix**: Allocate with known size to avoid `os.ReadFile`'s internal growth.

```go
func readFileContent(path string) ([]byte, error) {
    info, err := os.Stat(path)
    if err != nil {
        return nil, err
    }
    if info.Size() > maxFileSize {
        return nil, fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxFileSize)
    }
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    buf := make([]byte, info.Size())
    _, err = io.ReadFull(f, buf)
    return buf, err
}
```

**Expected impact**: Avoids `os.ReadFile` internal slice growth (doubles capacity). Minor GC pressure reduction.
**Risk**: Very low.

---

### QW-7: Avoid Double File Read in `ScanRepo` [S3]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/scanner.go:158`

**Issue**: `ScanRepo` reads every file into memory to compute SHA-256 hash. Then `enrichFile` reads the same file again. For 12K files, every file is read twice.

**Fix**: Cache the file content from `ScanRepo` in `ScannedFile` or use a content-addressed cache. Alternatively, compute hash from mtime+size (weaker but avoids the read entirely for unchanged files).

```go
// Option A: Add Content field to ScannedFile (simplest)
type ScannedFile struct {
    Path        string
    AbsPath     string
    ContentHash string
    Content     []byte  // file content, read once during scan
    Mtime       float64
    Size        int64
    Language    string
}
```

Then in `enrichFile`, use `file.Content` instead of calling `readFileContent(file.AbsPath)`.

**Expected impact**: Eliminates one full file read per file during cold indexing. For 12K files at average 5KB, saves ~60MB of I/O.
**Risk**: Medium. Increases peak memory during scan (all file contents held in `[]ScannedFile`). For very large repos, this trades I/O for memory. Consider clearing `Content` after parse to limit peak.

---

### QW-8: Use `fmt.Appendf` / `hex.EncodeToString` for Hash [S5]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/enrichment.go:318-320` and `/Users/minimac/p/shaktiman/internal/daemon/scanner.go:179`

**Issue**: `fmt.Sprintf("%x", sha256.Sum256(content))` allocates for the format string. Called once per file.

**Fix**:
```go
import "encoding/hex"

func contentHash(content []byte) string {
    h := sha256.Sum256(content)
    return hex.EncodeToString(h[:])
}
```

**Expected impact**: ~50% fewer allocations per hash. Minor at scale.
**Risk**: None.

---

## 4. Algorithmic Improvements

### ALG-1: Token Counting Is Called Excessively [S2]

**File**: `/Users/minimac/p/shaktiman/internal/parser/chunker.go` (multiple locations)

**Issue**: `p.tokens.Count(content)` calls tiktoken encoding on every chunk, then again during merge (`mergeSmallChunks` re-counts after concatenation), and again during split (`splitLargeChunks` calls `Count` on the `strings.Builder` output at every line boundary). tiktoken encoding is not cheap -- it tokenizes the full string.

Specific call sites in `chunker.go`:
- Line 87: initial chunk creation
- Line 95-96: regular chunkable nodes
- Line 293: `mergeSmallChunks` re-counts merged content
- Line 321: `splitLargeChunks` calls `Count` at every line (O(lines * content_length))

The `splitLargeChunks` inner loop is the worst case: for a 2000-line chunk, it calls `Count` up to 2000 times, each on an increasingly large string. This is O(n^2) in content length.

**Fix for `splitLargeChunks`**:
```go
func (p *Parser) splitLargeChunks(chunks []types.ChunkRecord) []types.ChunkRecord {
    var result []types.ChunkRecord
    for _, c := range chunks {
        if c.TokenCount <= maxChunkTokens {
            result = append(result, c)
            continue
        }
        // Tokenize once, then split on token boundaries
        tokens := p.tokens.enc.Encode(c.Content, nil, nil)
        lines := strings.Split(c.Content, "\n")
        // Track cumulative byte position to line number mapping,
        // split at token count boundaries by finding corresponding line
        // ... (use byte offsets from token spans)
    }
    return result
}
```

A simpler fix: estimate token count from byte length (`len/4` is a reasonable approximation for code), and only call the full tokenizer on the final split chunks.

**Expected impact**: For files with large functions (>1024 tokens), reduces parse time significantly. A 2000-line function currently generates ~2000 tiktoken calls; this reduces it to 1-2.
**Risk**: Medium. Approximation-based splitting may produce slightly different chunk boundaries. Validate with existing tests.

---

### ALG-2: LRU `moveToEnd` / `evictIfNeeded` Is O(n) [S3]

**File**: `/Users/minimac/p/shaktiman/internal/vector/embedding.go:501-508` (EmbedCache) and `/Users/minimac/p/shaktiman/internal/core/session.go:168-176` (SessionStore)

**Issue**: Both `EmbedCache.moveToEnd` and `SessionStore.moveToBack` do linear scans of the `order` slice to find the key. `evictIfNeeded` and `moveToEnd` both manipulate the slice by copying. With 2000-entry SessionStore and frequent Score/RecordBatch calls, this is O(n) per access.

Additionally, `c.order = c.order[1:]` in eviction leaks the underlying array -- the slice header moves forward but the backing array retains all elements.

**Fix**: Replace `[]string` + `map` with `container/list` + `map[string]*list.Element` (standard doubly-linked-list LRU pattern).

```go
type EmbedCache struct {
    mu      sync.Mutex
    entries map[string]*list.Element
    order   *list.List
    maxSize int
}

type cacheEntry struct {
    key string
    vec []float32
}

func (c *EmbedCache) Get(query string) ([]float32, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if elem, ok := c.entries[query]; ok {
        c.order.MoveToBack(elem)
        entry := elem.Value.(*cacheEntry)
        cp := make([]float32, len(entry.vec))
        copy(cp, entry.vec)
        return cp, true
    }
    return nil, false
}
```

**Expected impact**: `moveToEnd`/`moveToBack` drops from O(n) to O(1). Eliminates memory leak from slice header advancement. For SessionStore with 2000 entries called on every search, this removes O(2000) scans.
**Risk**: Low. Standard data structure replacement.

---

### ALG-3: `hydrateFTSResults` Is N+1 [S3]

**File**: `/Users/minimac/p/shaktiman/internal/core/retrieval.go:26-61`

**Issue**: For each FTS result, two separate queries: `GetChunkByID` then `GetFilePathByID`. With `limit=20`, that is 40 queries.

**Fix**: Single batch query joining chunks and files:
```go
func (s *Store) BatchHydrateFTS(ctx context.Context, chunkIDs []int64) ([]HydratedChunk, error) {
    // SELECT c.*, f.path FROM chunks c JOIN files f ON c.file_id = f.id
    // WHERE c.id IN (...)
}
```

**Expected impact**: 40 queries to 1. Saves ~4ms per search.
**Risk**: Low.

---

### ALG-4: `mergeResults` Hydrates One-by-One [S3]

**File**: `/Users/minimac/p/shaktiman/internal/core/engine.go:314-350`

**Issue**: For each semantic-only result not in keyword results, calls `GetChunkByID` + `GetFilePathByID`. With `maxResults*2 = 20` semantic results, worst case 40 queries.

**Fix**: Collect all unhydrated chunk IDs, batch query.

**Expected impact**: Up to 40 queries to 1.
**Risk**: Low.

---

## 5. Memory Optimization

### MEM-1: `GetChunksNeedingEmbedding` Loads All Chunk Content Into Memory [S1]

**File**: `/Users/minimac/p/shaktiman/internal/storage/metadata.go:377-398`

**Issue**: Loads ALL chunks with their full content text for files not yet embedding-complete. At Kubernetes scale (141K chunks, average ~200 bytes content each), this allocates ~28MB of strings in a single query. The scalability plan (Phase 1.1) addresses this with cursor-based pagination.

**Additional concern**: `queueEmbeddings` in `/Users/minimac/p/shaktiman/internal/daemon/daemon.go:198-226` then iterates this list calling `vectorStore.Has()` per chunk (141K RLock/RUnlock cycles).

**Fix**: Already addressed in scalability plan Phase 1.1 (`RunFromDB` with DB cursor). This addendum notes that the `Has()` reconciliation loop should also be batched:

```go
// Instead of per-chunk Has():
func (s *BruteForceStore) HasBatch(_ context.Context, chunkIDs []int64) map[int64]bool {
    s.mu.RLock()
    defer s.mu.RUnlock()
    result := make(map[int64]bool, len(chunkIDs))
    for _, id := range chunkIDs {
        result[id] = s.vectors[id] != nil
    }
    return result
}
```

**Expected impact**: Single lock acquisition instead of 141K.
**Risk**: Low.

---

### MEM-2: `processEnrichmentJob` Allocates Multiple Intermediate Slices [S4]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/writer.go:196-358`

**Issue**: Per file enrichment job: allocates `chunkIDs` slice, `symbolIDs` map, `newSymbolNames` slice, `staleChunkIDs` slice, `diffSymbols` slice, `newSymbolSet` map. For a file with 50 chunks and 30 symbols, this is ~8 allocations per file, or ~100K allocations for 12K files.

**Fix**: Pool or pre-allocate reusable buffers. Since `processJob` runs on a single goroutine (WriterManager), buffers can be reused without synchronization.

```go
type writerBuffers struct {
    chunkIDs     []int64
    staleIDs     []int64
    symbolIDs    map[string]int64
    symbolNames  []string
    diffSymbols  []DiffSymbolEntry
}

// Reuse across processJob calls, reset length but keep capacity
```

**Expected impact**: Reduces GC pressure during cold indexing. ~100K fewer allocations for 12K files.
**Risk**: Low. Must clear maps between uses (`clear()` in Go 1.21+).

---

### MEM-3: `ScanRepo` Holds All File Contents in Memory [S3]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/scanner.go:158`

**Issue**: (Related to QW-7) If QW-7 is implemented (caching content in ScannedFile), all file contents are held simultaneously. For Kubernetes (12K files, ~60MB source), this is manageable but grows with repo size.

**Fix**: If QW-7 is adopted, set `file.Content = nil` after `enrichFile` returns to release content for GC. Alternatively, use mmap for large files.

**Expected impact**: Bounds peak memory to working set rather than full repo.
**Risk**: Low.

---

### MEM-4: `EmbedCache.Get` Always Copies [S5]

**File**: `/Users/minimac/p/shaktiman/internal/vector/embedding.go:464-475`

**Issue**: `Get` copies the 768-element `[]float32` vector (3KB) every time. The copy is defensive (prevents mutation), but the caller (`embedQuery`) never mutates the result.

**Fix**: Return the cached slice directly and document the immutability contract. Or use a `sync.Pool` for the copy buffers.

**Expected impact**: Eliminates one 3KB allocation per cached query embedding lookup. Minor.
**Risk**: Low if callers respect immutability. Medium if future callers mutate.

---

## 6. I/O Optimization

### IO-1: Prepared Statements Not Used in `processEnrichmentJob` [S3]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/writer.go:280-291`

**Issue**: Chunk insertion uses individual `tx.ExecContext` per chunk. For a file with 50 chunks, this compiles 50 identical SQL statements within the same transaction. The standalone `InsertChunks` method in `metadata.go:135-173` correctly uses `tx.PrepareContext`, but the writer's inline version does not.

**Fix**: Use `tx.PrepareContext` for chunk and symbol insertion within `processEnrichmentJob`:

```go
chunkStmt, err := tx.PrepareContext(ctx, `
    INSERT INTO chunks (file_id, chunk_index, symbol_name, kind,
                        start_line, end_line, content, token_count, signature, parse_quality)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
if err != nil {
    return nil, err
}
defer chunkStmt.Close()

for i, c := range job.Chunks {
    res, err := chunkStmt.ExecContext(ctx, ...)
    // ...
}
```

**Expected impact**: SQLite only compiles the statement once per transaction instead of once per row. For 50 chunks per file, saves ~49 VDBE compilations.
**Risk**: Very low.

---

### IO-2: `MarkChunksEmbedded` Does Per-File Chunk Enumeration [S3]

**File**: `/Users/minimac/p/shaktiman/internal/storage/metadata.go:408-473`

**Issue**: For each file touched by the batch, queries all chunk IDs (`SELECT id FROM chunks WHERE file_id = ?`) and checks each against the `embeddedSet`. This is an N+1 within a write transaction.

**Fix**: Use SQL aggregation instead:

```go
func (s *Store) MarkChunksEmbedded(ctx context.Context, chunkIDs []int64) error {
    if len(chunkIDs) == 0 {
        return nil
    }
    return s.db.WithWriteTx(func(tx *sql.Tx) error {
        // Build IN clause
        ph, args := buildInClause(chunkIDs)

        // Single query: for each file, count total chunks and count embedded chunks
        _, err := tx.ExecContext(ctx, fmt.Sprintf(`
            UPDATE files SET embedding_status = CASE
                WHEN (SELECT COUNT(*) FROM chunks WHERE file_id = files.id) =
                     (SELECT COUNT(*) FROM chunks WHERE file_id = files.id AND id IN (%s))
                THEN 'complete'
                ELSE 'partial'
            END
            WHERE id IN (SELECT DISTINCT file_id FROM chunks WHERE id IN (%s))
        `, ph, ph), append(args, args...)...)
        return err
    })
}
```

**Caveat**: This marks files based only on the current batch. The existing bug (from scalability plan 1.2) of not tracking cumulative embedding still applies. This optimization is relevant after Phase 1.2 adds the `embedded` column.

**Expected impact**: Eliminates per-file inner queries. For a batch touching 10 files, removes ~10 queries.
**Risk**: Medium. SQL is more complex. Test thoroughly.

---

### IO-3: SQLite `cache_size` May Be Too Small [S4]

**File**: `/Users/minimac/p/shaktiman/internal/storage/db.go:76`

**Issue**: Writer cache is set to `-8000` (8MB). Reader has no explicit cache_size (SQLite default is 2MB). For large indexes with 141K chunks (the chunks table alone could be 50MB+), the page cache will thrash.

**Fix**: Set reader cache size proportional to expected index size. Also set `mmap_size` for memory-mapped I/O on the reader.

```go
func openReader(path string, inMemory bool) (*sql.DB, error) {
    // ... existing code ...
    db.SetMaxOpenConns(4)
    db.SetMaxIdleConns(4)
    db.SetConnMaxLifetime(0)

    // Increase reader cache for large indexes
    db.Exec("PRAGMA cache_size = -32000") // 32MB
    db.Exec("PRAGMA mmap_size = 268435456") // 256MB mmap
    return db, nil
}
```

**Expected impact**: Fewer page cache misses during FTS and graph queries. Measurable at scale.
**Risk**: Low. Memory increase is bounded (32MB per reader connection, 4 connections = 128MB max).

---

### IO-4: `binary.Write` Is Slow for Vector Persistence [S4]

**File**: `/Users/minimac/p/shaktiman/internal/vector/store.go:194-204`

**Issue**: `binary.Write` uses reflection internally to handle the `interface{}` parameter. For writing `[]float32` vectors, this adds unnecessary overhead per vector.

**Fix**: Use `unsafe` or `encoding/binary` with a direct byte slice conversion:

```go
import "unsafe"

func writeFloat32Slice(w io.Writer, vec []float32) error {
    b := unsafe.Slice((*byte)(unsafe.Pointer(&vec[0])), len(vec)*4)
    _, err := w.Write(b)
    return err
}
```

Or safer: pre-allocate a byte buffer and use `math.Float32bits`:
```go
buf := make([]byte, len(vec)*4)
for i, v := range vec {
    binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
}
w.Write(buf)
```

**Expected impact**: 2-5x faster vector serialization. At 141K vectors, saves seconds during SaveToDisk.
**Risk**: Low for the `math.Float32bits` approach. Medium for `unsafe`.

---

## 7. Concurrency Optimization

### CONC-1: `WriterManager.Submit` Takes Mutex on Every Submit [S3]

**File**: `/Users/minimac/p/shaktiman/internal/daemon/writer.go:92-123`

**Issue**: Every `Submit` call acquires `wm.mu` for the non-blocking channel send attempt. During cold indexing with N worker goroutines, all workers contend on this mutex. The mutex is needed for safe close coordination, but the fast path (channel not full, not closed) pays for it unnecessarily.

**Fix**: Use atomic check for closed state and only acquire mutex on the slow path:

```go
func (wm *WriterManager) Submit(job types.WriteJob) error {
    if wm.closed.Load() {
        return ErrWriterClosed
    }
    // Fast path: try non-blocking send without mutex
    select {
    case wm.ch <- job:
        return nil
    default:
    }
    // Slow path: channel full, need blocking send with close coordination
    wm.mu.Lock()
    if wm.closed.Load() {
        wm.mu.Unlock()
        return ErrWriterClosed
    }
    wm.mu.Unlock()
    // ... existing blocking logic
}
```

**Issue detail**: The current code acquires the mutex even on the happy path to prevent a race between `Submit` and `drain`. The race is: Submit checks `closed` (false), drain sets `closed` (true) and closes channel, Submit sends to closed channel (panic). The fix above is safe because the channel `close` only happens after `wm.producers.Wait()` returns, and Submit is only called by registered producers.

Actually, re-examining: Submit can be called by non-producers (e.g., the sync marker in `IndexAll` line 132). So the mutex is necessary for those cases. The optimization should only apply when `Submit` is called between `AddProducer`/`RemoveProducer`.

**Revised fix**: Add an `UnsafeSubmit` for use within producer contexts where the close race is impossible:

```go
func (wm *WriterManager) SubmitFromProducer(job types.WriteJob) error {
    select {
    case wm.ch <- job:
        return nil
    default:
    }
    // Blocking send -- safe because drain() waits for producers first
    select {
    case wm.ch <- job:
        return nil
    case <-wm.done:
        return ErrWriterClosed
    }
}
```

**Expected impact**: Eliminates mutex contention during cold indexing. With 4 workers submitting concurrently, removes ~4 * fileCount mutex lock/unlock cycles.
**Risk**: Medium. Requires careful analysis of the close ordering invariant.

---

### CONC-2: `EmbedCache` Uses `sync.Mutex` Instead of `sync.RWMutex` [S4]

**File**: `/Users/minimac/p/shaktiman/internal/vector/embedding.go:448-509`

**Issue**: `EmbedCache.Get` acquires an exclusive `sync.Mutex` even though reads could be concurrent. During search, `embedQuery` calls `Get` on the cache; if multiple MCP search requests arrive concurrently, they serialize on the cache mutex.

**Fix**: Use `sync.RWMutex`. `Get` takes RLock, `Put` takes Lock. However, since `Get` calls `moveToEnd` (a write operation), the current design requires exclusive lock. Fix by deferring the LRU update:

```go
func (c *EmbedCache) Get(query string) ([]float32, bool) {
    c.mu.RLock()
    vec, ok := c.entries[query]
    c.mu.RUnlock()
    if ok {
        // Defer the LRU promotion to Put time or a separate goroutine
        // For a 100-entry cache, LRU precision is not critical
        cp := make([]float32, len(vec))
        copy(cp, vec)
        return cp, true
    }
    return nil, false
}
```

With `container/list` from ALG-2, `Get` with RWMutex would still need a write lock for `MoveToBack`. Alternative: use a probabilistic promotion (promote with 50% chance) to reduce write-lock contention.

**Expected impact**: Minor under current load (100-entry cache, single-user MCP). More relevant with concurrent search clients.
**Risk**: Low.

---

### CONC-3: `SessionStore.DecayAllExcept` Iterates All Entries Under Lock [S4]

**File**: `/Users/minimac/p/shaktiman/internal/core/session.go:133-147`

**Issue**: Called after every search. Holds write lock while iterating all 2000 entries. With `computeStructuralScores` also under the search path, this serializes all searches on the session store.

**Fix**: Use an atomic generation counter instead of per-entry decay:

```go
type SessionStore struct {
    mu         sync.RWMutex
    entries    map[string]*sessionEntry
    generation atomic.Int64  // incremented on each search
}

type sessionEntry struct {
    accessCount    int
    lastAccessed   time.Time
    lastGeneration int64  // generation when last seen
}

func (s *SessionStore) Score(filePath string, startLine int) float64 {
    key := sessionKey(filePath, startLine)
    s.mu.RLock()
    e, ok := s.entries[key]
    s.mu.RUnlock()
    if !ok {
        return 0
    }
    queriesSince := int(s.generation.Load() - e.lastGeneration)
    // ... compute score using queriesSince
}

// No more DecayAllExcept -- generation counter handles it
```

**Expected impact**: Eliminates O(n) iteration under write lock on every search. `DecayAllExcept` drops from O(2000) to O(1).
**Risk**: Low. Semantically equivalent.

---

## 8. Benchmark Harness Design

### BH-1: Core Benchmarks (create in existing test files)

```go
// internal/vector/store_bench_test.go
func BenchmarkCosineSimilarity768(b *testing.B) {
    a := make([]float32, 768)
    q := make([]float32, 768)
    for i := range a {
        a[i] = rand.Float32()
        q[i] = rand.Float32()
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        cosineSimilarity(q, a)
    }
}

func BenchmarkBruteForceSearch(b *testing.B) {
    for _, n := range []int{1000, 10000, 50000, 100000} {
        b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
            store := NewBruteForceStore(768)
            for i := 0; i < n; i++ {
                vec := make([]float32, 768)
                for j := range vec { vec[j] = rand.Float32() }
                store.Upsert(context.Background(), int64(i+1), vec)
            }
            query := make([]float32, 768)
            for j := range query { query[j] = rand.Float32() }
            b.ResetTimer()
            b.ReportAllocs()
            for i := 0; i < b.N; i++ {
                store.Search(context.Background(), query, 20)
            }
        })
    }
}

func BenchmarkSaveToDisk(b *testing.B) {
    store := NewBruteForceStore(768)
    for i := 0; i < 50000; i++ {
        vec := make([]float32, 768)
        for j := range vec { vec[j] = rand.Float32() }
        store.Upsert(context.Background(), int64(i+1), vec)
    }
    dir := b.TempDir()
    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        store.SaveToDisk(filepath.Join(dir, "bench.bin"))
    }
}
```

```go
// internal/core/engine_bench_test.go
func BenchmarkHybridRank(b *testing.B) {
    // Setup: in-memory SQLite with 1000 chunks, 500 symbols, 200 edges
    // Benchmark: HybridRank with 20 candidates
    // Measure: total time, allocations
}

func BenchmarkKeywordSearch(b *testing.B) {
    // Setup: in-memory SQLite with FTS5, 10000 chunks
    // Benchmark: KeywordSearch with typical query
}
```

```go
// internal/parser/chunker_bench_test.go
func BenchmarkTokenCount(b *testing.B) {
    tc, _ := newTokenCounter("cl100k_base")
    content := strings.Repeat("func example() {\n\treturn 42\n}\n", 100)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        tc.Count(content)
    }
}

func BenchmarkChunkFile(b *testing.B) {
    p, _ := NewParser()
    defer p.Close()
    content := readTestFile("testdata/large.go") // ~500 lines
    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        p.Parse(context.Background(), ParseInput{
            FilePath: "large.go",
            Content:  content,
            Language: "go",
        })
    }
}
```

```go
// internal/daemon/enrichment_bench_test.go
func BenchmarkFilterChanged(b *testing.B) {
    // Setup: in-memory DB with 5000 files, half changed
    // Benchmark: filterChanged with 5000 ScannedFiles
    // Compare: current (N queries) vs batched
}
```

### BH-2: End-to-End Benchmark

```go
// internal/daemon/daemon_bench_test.go
func BenchmarkColdIndex(b *testing.B) {
    // Setup: create temp directory with N synthetic Go files
    // Benchmark: IndexProject end-to-end
    // Report: files/sec, allocs/file
    for _, n := range []int{100, 500, 1000} {
        b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
            dir := createSyntheticProject(b, n)
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                // ... IndexProject with in-memory DB
            }
        })
    }
}

func BenchmarkSearchE2E(b *testing.B) {
    // Setup: indexed project with 1000 files + embeddings
    // Benchmark: engine.Search with typical query
    // Report: latency, allocations
}
```

---

## 9. Measurement Plan

### Phase A: Establish Baselines (before any changes)

1. **Run all benchmarks** from Section 8 with `benchstat` collection:
   ```bash
   go test -tags sqlite_fts5 -run='^$' -bench=. -count=10 \
     -benchmem ./internal/vector/ ./internal/core/ ./internal/parser/ \
     > baseline.txt
   ```

2. **Profile cold index** on a real medium-size project:
   ```bash
   go test -tags sqlite_fts5 -run='^$' -bench=BenchmarkColdIndex/files=1000 \
     -cpuprofile=cold_index_cpu.prof -memprofile=cold_index_mem.prof \
     -benchtime=1x ./internal/daemon/
   ```

3. **Profile search path** with realistic data:
   ```bash
   go test -tags sqlite_fts5 -run='^$' -bench=BenchmarkSearchE2E \
     -cpuprofile=search_cpu.prof -benchtime=100x ./internal/core/
   ```

4. **Count SQL queries per search** using a query counter wrapper:
   ```go
   type countingDB struct {
       *sql.DB
       count atomic.Int64
   }
   ```

### Phase B: Implement and Measure (per optimization)

For each optimization:
1. Implement on a branch
2. Run the same benchmarks
3. Compare with `benchstat`:
   ```bash
   benchstat baseline.txt optimized.txt
   ```
4. Accept if: statistically significant improvement (p < 0.05), no regression in other benchmarks

### Phase C: Continuous Regression Prevention

Add benchmark invariants to CI:
```bash
# Fail if search latency regresses >20%
go test -tags sqlite_fts5 -bench=BenchmarkSearchE2E -benchtime=10x -count=5 | \
  benchstat -delta-test=utest -
```

---

## 10. Risk Assessment

| Optimization | Risk | Mitigation |
|---|---|---|
| QW-1 (batch structural scores) | Low | New methods additive; old path kept as fallback |
| QW-2 (batch filterChanged) | Low | Chunked IN clause handles SQLite limits |
| QW-3 (no snapshot copy) | Medium | RLock held during I/O; monitor save duration; set buffer size |
| QW-4 (heap for top-K) | Low | Standard algorithm; benchmark confirms correctness |
| QW-5 (SIMD cosine) | Low | Unit test for numerical equivalence |
| QW-7 (avoid double read) | Medium | Peak memory increase; clear content after parse |
| ALG-1 (token counting) | Medium | Approximation may shift chunk boundaries; validate with tests |
| ALG-2 (LRU O(1)) | Low | Standard `container/list` pattern |
| IO-1 (prepared stmts) | Very low | Strictly internal to existing transaction |
| IO-3 (cache_size + mmap) | Low | Bounded memory increase |
| CONC-1 (submit fast path) | Medium | Close ordering invariant must be maintained |
| CONC-3 (generation counter) | Low | Semantically equivalent to current decay |

---

## 11. Priority Matrix

Ordered by impact/effort ratio. "Effort" is implementation + testing time.

| # | Optimization | Severity | Impact | Effort | Ratio | Dependencies |
|---|---|---|---|---|---|---|
| 1 | QW-1: Batch structural scores | S1 | High (15ms/search) | Medium (new store methods) | **High** | None |
| 2 | QW-4: Heap for top-K search | S2 | High (memory + CPU) | Low (50 LOC) | **High** | None |
| 3 | QW-5: SIMD cosine similarity | S3 | Medium (2-4x inner loop) | Low (20 LOC) | **High** | None |
| 4 | ALG-1: Fix O(n^2) token counting | S2 | High (large files) | Medium (refactor split) | **High** | None |
| 5 | QW-2: Batch filterChanged | S2 | High at scale (12K queries to 26) | Low (new store method) | **High** | None |
| 6 | ALG-2: O(1) LRU | S3 | Medium (2000 entries) | Low (standard pattern) | **Medium** | None |
| 7 | IO-1: Prepared statements | S3 | Medium (~49 VDBE skips/file) | Very low (10 LOC) | **Medium** | None |
| 8 | QW-3: Eliminate snapshot copy | S2 | High (412MB at 141K) | Medium (test tradeoff) | **Medium** | None |
| 9 | CONC-3: Generation counter | S4 | Medium (lock contention) | Low (30 LOC) | **Medium** | None |
| 10 | ALG-3: Batch hydrateFTSResults | S3 | Medium (40 queries to 1) | Low (new store method) | **Medium** | None |
| 11 | ALG-4: Batch mergeResults | S3 | Medium (40 queries to 1) | Low (new store method) | **Medium** | ALG-3 |
| 12 | IO-3: SQLite cache_size + mmap | S4 | Medium at scale | Very low (2 lines) | **Medium** | None |
| 13 | QW-7: Avoid double file read | S3 | Medium (60MB I/O at 12K files) | Medium (struct change) | **Medium** | None |
| 14 | MEM-1: Batch Has() check | S1 | Low (already in plan) | Very low (10 LOC) | **Low** (covered by plan) | Phase 1.1 |
| 15 | IO-4: Faster vector serialization | S4 | Medium (large stores) | Low (30 LOC) | **Low** | None |
| 16 | QW-6: Pre-allocate readFileContent | S4 | Low | Very low | **Low** | None |
| 17 | QW-8: hex.EncodeToString | S5 | Negligible | Very low | **Low** | None |
| 18 | CONC-1: Submit fast path | S3 | Medium | Medium (careful analysis) | **Low** | None |
| 19 | CONC-2: EmbedCache RWMutex | S4 | Low (single-user) | Low | **Low** | ALG-2 |
| 20 | MEM-2: Writer buffer pooling | S4 | Low | Medium | **Low** | None |
| 21 | MEM-4: EmbedCache no-copy Get | S5 | Negligible | Very low | **Low** | None |

### Recommended Implementation Order

**Batch 1 (Quick wins, independent, high ROI):**
1. QW-4: Heap for top-K (50 LOC, immediate memory + CPU win)
2. QW-5: SIMD cosine similarity (20 LOC, immediate CPU win)
3. IO-1: Prepared statements in writer (10 LOC, no risk)
4. QW-8: hex.EncodeToString (5 LOC, trivial)
5. IO-3: SQLite cache_size + mmap (2 lines, trivial)

**Batch 2 (New store methods, medium effort):**
6. QW-1: Batch structural scores (biggest search latency win)
7. ALG-3 + ALG-4: Batch hydration queries
8. QW-2: Batch filterChanged

**Batch 3 (Algorithmic, requires care):**
9. ALG-1: Fix O(n^2) token counting
10. ALG-2: O(1) LRU for EmbedCache + SessionStore
11. CONC-3: Generation counter for SessionStore

**Batch 4 (Memory, after Phase 1):**
12. QW-3: Eliminate snapshot copy in SaveToDisk
13. QW-7: Avoid double file read

---

## Relationship to Scalability Plan

| This Addendum | Scalability Plan Phase | Relationship |
|---|---|---|
| QW-1 through QW-8 | Independent | Can be done before, during, or after any phase |
| ALG-1 through ALG-4 | Independent | Additive improvements to current code |
| MEM-1 (batch Has) | Phase 1.1 | Enhances the planned RunFromDB implementation |
| QW-3 (no snapshot) | Phase 1.3 | Reduces crash window impact; compatible with faster save interval |
| IO-3 (cache_size) | Phase 3 | Helps all backends; especially relevant if SQLite stays default |
| QW-4 + QW-5 (vector perf) | Phase 3.2 | Irrelevant if HNSW replaces BruteForceStore; valuable until then |
| CONC-1 (submit) | Phase 1 | Writer path is unchanged in Phase 1; optimization is additive |

All optimizations in this addendum are safe to implement independently of the scalability plan. They improve the current default configuration and remain beneficial even after Phase 3 backend changes (except QW-4/QW-5 which are BruteForceStore-specific).
