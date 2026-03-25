# Shaktiman: Scalability & Enhancement Plan

> Multi-phased plan for scaling shaktiman to large codebases (16K+ files, 141K+ chunks).
> Derived from real-world Kubernetes indexing failure analysis and adversarial review.

---

## 0. Problem Statement

Indexing Kubernetes (12,867 Go files, 141,320 chunks, 133,953 symbols) exposed critical failures:

| Issue | Severity | Detail |
|---|---|---|
| 99.3% embedding loss | CRITICAL | 140,300 of 141,320 embed jobs dropped. Queue cap=1000, all jobs submitted in ~3ms burst. |
| Silent Ollama failure | HIGH | When Ollama is down, user sees `"Embedded: 0 chunks"` with no error. |
| No progress feedback | MEDIUM | No output between start and completion for multi-minute operations. |
| Memory ceiling | MEDIUM | BruteForceStore at 141K vectors = ~870MB peak (snapshot doubling). |
| Writer backpressure noise | LOW | 38 "channel full" warnings. Working as designed but noisy. |

---

## 1. Current Architecture

```
  CLI: shaktiman index --embed <root>
    │
    ▼
  daemon.New(cfg)
    ├── SQLite (WAL, 1 writer conn, 4 reader conns)
    ├── BruteForceStore (in-memory map[int64][]float32, RWMutex)
    ├── OllamaClient (HTTP, 30s timeout, no health check on init)
    └── EmbedWorker (chan EmbedJob cap=1000, batchSz=32)
    │
    ▼
  daemon.IndexProject(ctx)
    ├── ScanRepo → file list
    ├── EnrichmentPipeline.IndexAll (N workers, tree-sitter)
    │     └── WriterManager.ch (cap=500, blocking) → SQLite
    ├── Print stats
    │
    ▼ (if --embed)
  daemon.EmbedProject(ctx)
    ├── queueEmbeddings() ← BOTTLENECK
    │     ├── store.GetChunksNeedingEmbedding() ← loads ALL 141K into memory
    │     ├── filter by vectorStore.Has() (reconciliation)
    │     └── embedWorker.SubmitBatch() ← non-blocking, DROPS when full
    ├── embedWorker.Run() in goroutine
    ├── embedWorker.WaitIdle()
    └── SaveToDisk
```

### Why It Breaks at Scale

1. `SubmitBatch` iterates 141K jobs in a tight loop, each doing `select { case ch <- job: | default: DROP }`.
2. Channel fills after 1000 jobs. Remaining 140K dropped in 3ms.
3. `MarkChunksEmbedded` only checks current batch (32 IDs) against file total. Files with >32 chunks never reach `'complete'` status → re-embed on every restart.
4. 5-minute `SaveToDisk` interval creates crash window: DB says complete, vectors lost.

---

## Constraints (All Phases)

1. **Do not change MCP behavior.** The core MCP tools (`search`, `symbols`, `dependencies`, `context`, `diff`, `summary`, `enrichment_status`) must continue to produce identical results. All changes are internal pipeline improvements.
2. **Each phase ends with a quality gate** — adversarial-analyst, architect, and code-reviewer agents review the implementation. Findings are fixed only after explicit user approval.
3. **Tests are first-class deliverables**, not afterthoughts. Every code change ships with unit tests, integration tests, coverage analysis, and performance benchmarks.

---

## Phase 1: Fix the Embedding Pipeline (Must Fix)

> Goal: Make embedding reliable for any codebase size. Zero data loss.

### 1.1 DB-Cursor Embed Worker (`RunFromDB`)

Replace fire-and-forget channel submission with pull-based DB cursor.

```
  PROPOSED EMBEDDING FLOW

  EmbedWorker.RunFromDB(ctx, source, onProgress)
    │
    ├── total = store.CountChunksNeedingEmbedding()
    │
    └── loop:
          page = store.GetEmbedPage(lastID, pageSize=256)
          if len(page) == 0: break
          │
          for batch of 32 in page:
          │  if !circuitBreaker.Allow():
          │    sleep(backoff)      ← DO NOT advance cursor
          │    retry same batch
          │  │
          │  vectors = ollama.EmbedBatch(texts)
          │  vectorStore.UpsertBatch(ids, vectors)
          │  store.MarkChunksEmbedded(ids)
          │  onProgress(embedded, total)
          │
          lastID = page[len(page)-1].ChunkID
```

**New types and methods:**

```go
// internal/vector/embedding.go
type EmbedSource interface {
    GetEmbedPage(ctx context.Context, afterID int64, limit int) ([]EmbedJob, error)
    MarkChunksEmbedded(ctx context.Context, chunkIDs []int64) error
    CountChunksNeedingEmbedding(ctx context.Context) (int, error)
}

type EmbedProgress struct {
    Embedded int
    Total    int
}

func (w *EmbedWorker) RunFromDB(ctx context.Context, source EmbedSource, onProgress func(EmbedProgress)) error
```

**Files to modify:**

| File | Change |
|---|---|
| `internal/vector/embedding.go` | Add `RunFromDB`, `EmbedSource`, `EmbedProgress` |
| `internal/storage/metadata.go` | Add `GetEmbedPage(ctx, afterID, limit)`, `CountChunksNeedingEmbedding(ctx)` |
| `internal/daemon/daemon.go` | New `embedFromDB()` method; update `EmbedProject` and `Start` to use it |

**Design decisions:**
- Page size 256 (8 batches of 32). ~128KB memory per page.
- Cursor is `WHERE c.id > ? ORDER BY c.id LIMIT ?` — efficient PK index scan.
- Circuit breaker rejection → sleep and retry same batch. Never advance cursor on failure.
- `RunFromDB` is one-shot (runs to completion). Watcher handles incremental via existing channel path.

### 1.2 Fix `MarkChunksEmbedded` for Cumulative Tracking

Current bug: checks only current batch's chunk IDs against file total. A file with 50 chunks processed in batches of 32 never reaches `'complete'`.

**Fix:** Add `embedded INTEGER DEFAULT 0` column to chunks table. Mark individual chunks as embedded. Update file status based on `COUNT(*) WHERE file_id = ? AND embedded = 0`.

| File | Change |
|---|---|
| `internal/storage/metadata.go` | Update `MarkChunksEmbedded` to use per-chunk column |
| `internal/storage/db.go` | Add migration for `embedded` column |

### 1.3 Crash-Safe Vector Persistence

**Problem:** `SaveToDisk` runs every 5 minutes. Crash between `MarkChunksEmbedded` and `SaveToDisk` = DB says complete, vectors gone.

**Two-pronged fix:**

1. **Reduce save interval during active embedding** — 30 seconds instead of 5 minutes when `RunFromDB` is in progress.
2. **Reconcile on startup** — `RunFromDB` checks `vectorStore.Has()` for each chunk before embedding. Skip chunks already in store (idempotent). Re-embed chunks marked complete but missing from store (crash recovery).

| File | Change |
|---|---|
| `internal/daemon/daemon.go` | Parameterize save interval; faster during embedding |
| `internal/vector/embedding.go` | `RunFromDB` checks `Has()` before embedding each batch |

### 1.4 Concurrent RunFromDB + Watcher Safety

The `vectorDeleter` hook on `WriterManager` already cleans up stale vectors when files are re-indexed. Document this invariant. No additional code needed.

### 1.5 Unit Tests

| Test | File | What it verifies |
|---|---|---|
| `TestRunFromDB_AllChunksEmbedded` | `internal/vector/embedding_test.go` | All chunks from mock `EmbedSource` are embedded; progress callback fires for each batch |
| `TestRunFromDB_CircuitBreakerRetry` | `internal/vector/embedding_test.go` | When circuit breaker rejects, cursor does NOT advance; same batch is retried after backoff |
| `TestRunFromDB_ContextCancellation` | `internal/vector/embedding_test.go` | Graceful shutdown: finishes in-flight batch, marks it, returns |
| `TestRunFromDB_EmptySource` | `internal/vector/embedding_test.go` | Zero chunks → immediate return, no error |
| `TestRunFromDB_HasReconciliation` | `internal/vector/embedding_test.go` | Chunks already in vector store are skipped, not re-embedded |
| `TestGetEmbedPage_Pagination` | `internal/storage/metadata_test.go` | Returns correct pages; afterID cursor advances correctly; empty page on completion |
| `TestGetEmbedPage_ConcurrentDeletes` | `internal/storage/metadata_test.go` | Deleted chunks between pages don't cause errors or duplicates |
| `TestCountChunksNeedingEmbedding` | `internal/storage/metadata_test.go` | Returns correct count; updates after `MarkChunksEmbedded` |
| `TestMarkChunksEmbedded_Cumulative` | `internal/storage/metadata_test.go` | File with 50 chunks marked in batches of 32 eventually reaches `'complete'` status |
| `TestMarkChunksEmbedded_PartialBatch` | `internal/storage/metadata_test.go` | Marking 10 of 50 chunks → file stays `'partial'`; marking remaining 40 → `'complete'` |
| `TestEmbeddedColumnMigration` | `internal/storage/db_test.go` | Migration adds column; existing rows have `embedded = 0` |

### 1.6 Integration Tests

| Test | File | What it verifies |
|---|---|---|
| `TestEmbedProject_LargeChunkCount` | `internal/daemon/daemon_integration_test.go` | Index a synthetic project with 5000+ chunks, run `EmbedProject`, verify all chunks embedded with zero drops |
| `TestEmbedProject_OllamaDown` | `internal/daemon/daemon_integration_test.go` | With mock Ollama returning errors, verify circuit breaker opens, `RunFromDB` retries, no chunks permanently skipped |
| `TestEmbedProject_CrashRecovery` | `internal/daemon/daemon_integration_test.go` | Embed 500 chunks, simulate crash (kill before `SaveToDisk`), restart, verify resume from correct cursor position and missing vectors are re-embedded |
| `TestEmbedProject_ConcurrentWatcher` | `internal/daemon/daemon_integration_test.go` | Start `RunFromDB` and simultaneously trigger file re-index via writer; verify no orphan vectors, no panics, correct final state |
| `TestEmbedProject_IncrementalAfterCold` | `internal/daemon/daemon_integration_test.go` | Cold index + embed, then modify 3 files via watcher, verify only new chunks embedded (not full re-embed) |
| `TestMCPSearchUnchanged` | `internal/mcp/mcp_integration_test.go` | After all Phase 1 changes, MCP `search`, `symbols`, `dependencies` tools return identical results to baseline |

### 1.7 Coverage Analysis

After completing 1.5 and 1.6:
1. Run `go test -race -tags sqlite_fts5 -coverprofile=cover.out ./internal/vector/ ./internal/storage/ ./internal/daemon/`
2. Generate HTML report: `go tool cover -html=cover.out`
3. Target: >=80% line coverage for modified functions (`RunFromDB`, `GetEmbedPage`, `MarkChunksEmbedded`, `CountChunksNeedingEmbedding`, `embedFromDB`)
4. Add test cases for any uncovered branches or error paths.

### 1.8 Performance Tests & Invariants

| Benchmark | File | Invariant |
|---|---|---|
| `BenchmarkGetEmbedPage` | `internal/storage/metadata_bench_test.go` | <5ms per page of 256 chunks from a 150K chunk DB |
| `BenchmarkMarkChunksEmbedded` | `internal/storage/metadata_bench_test.go` | <10ms per batch of 32 chunks |
| `BenchmarkCountChunksNeedingEmbedding` | `internal/storage/metadata_bench_test.go` | <50ms on a 150K chunk DB |
| `BenchmarkRunFromDB_Throughput` | `internal/vector/embedding_bench_test.go` | Processes >=100 batches/sec with mock embedder (no Ollama I/O) |
| `BenchmarkRunFromDB_Memory` | `internal/vector/embedding_bench_test.go` | Peak heap allocation <10MB regardless of total chunk count (verify with `testing.B.ReportAllocs`) |

These benchmarks serve as regression guards. If a future change degrades performance beyond the invariant, the benchmark fails.

### 1.9 Quality Gate

After implementation:
1. Run adversarial-analyst agent on the diff.
2. Run architect agent to verify design alignment.
3. Run code-reviewer agent for correctness and regression risk.
4. Present findings to user. Fix only after explicit approval.

---

## Phase 2: CLI UX Enhancements

> Goal: Users always know what's happening during long operations.

### 2.1 Progress Reporting

**Indexing progress:**

Add a callback to `IndexAll`:

```go
// internal/daemon/enrichment.go
type IndexProgress struct {
    Indexed int
    Errors  int
    Total   int
}

// IndexAllInput gains OnProgress func(IndexProgress)
```

**Embedding progress:**

`RunFromDB` already takes `onProgress func(EmbedProgress)`.

**CLI output:**

```
Indexing: 8342/16000 files (52%)    ← \r overwrite on TTY
Indexed: 16000 files | 141320 chunks | 133953 symbols

Embedding: 45000/141320 chunks (31%)    ← \r overwrite on TTY
Embedded: 141320 chunks → .shaktiman/embeddings.bin
```

Use `\r` carriage return for in-place updates when stdout is a TTY. Fall back to periodic log lines when piped.

| File | Change |
|---|---|
| `internal/daemon/enrichment.go` | Add `OnProgress` callback to `IndexAllInput`, call it after each file |
| `cmd/shaktiman/main.go` | Wire progress callbacks, detect TTY, print `\r`-overwriting lines |

### 2.2 Ollama Health Check and Error Reporting

**Problem:** `OllamaClient.Healthy()` exists but is never called. When Ollama is down, user sees `"Embedded: 0 chunks"` with no error.

**Fix:**

1. Before `EmbedProject`, call `OllamaClient.Healthy()`. If unreachable, print a clear error and abort:
   ```
   Error: Ollama is not reachable at http://localhost:11434
   Please start Ollama or configure a different URL in shaktiman.toml
   Indexing completed without embeddings. Run 'shaktiman index --embed' to retry.
   ```

2. During embedding, if circuit breaker opens, report it via progress callback:
   ```
   Warning: Ollama connection lost. Retrying... (attempt 3/10)
   ```

3. After embedding, if embedded count < total, report the gap:
   ```
   Warning: 5000/141320 chunks could not be embedded (Ollama errors).
   Run 'shaktiman index --embed' to retry failed chunks.
   ```

| File | Change |
|---|---|
| `internal/daemon/daemon.go` | Call `Healthy()` before `RunFromDB`; expose Ollama health status |
| `cmd/shaktiman/main.go` | Display health check errors; display warnings for partial embedding |

### 2.3 Reduce Writer Backpressure Log Noise

Change "writer channel full, blocking" from `Warn` to `Debug` in `internal/daemon/writer.go`. This is expected behavior under load, not a problem.

### 2.4 Unit Tests

| Test | File | What it verifies |
|---|---|---|
| `TestIndexProgress_Callback` | `internal/daemon/enrichment_test.go` | `OnProgress` fires after each file with correct `Indexed/Total` counts |
| `TestIndexProgress_NilCallback` | `internal/daemon/enrichment_test.go` | `OnProgress = nil` doesn't panic; falls back to existing log |
| `TestOllamaHealthCheck_Unreachable` | `internal/vector/embedding_test.go` | `Healthy()` returns error when server is down |
| `TestOllamaHealthCheck_Reachable` | `internal/vector/embedding_test.go` | `Healthy()` returns nil when mock server responds |
| `TestEmbedProgress_PartialFailure` | `internal/vector/embedding_test.go` | Progress reports correct embedded vs total when some batches fail |
| `TestWriterLogLevel_Debug` | `internal/daemon/writer_test.go` | "channel full" message logged at Debug, not Warn |

### 2.5 Integration Tests

| Test | File | What it verifies |
|---|---|---|
| `TestCLI_IndexProgress_TTY` | `cmd/shaktiman/main_test.go` | Progress output contains percentage and `\r` when writing to a pseudo-TTY |
| `TestCLI_IndexProgress_Pipe` | `cmd/shaktiman/main_test.go` | Progress output is periodic log lines (no `\r`) when piped |
| `TestCLI_EmbedOllamaDown` | `cmd/shaktiman/main_test.go` | CLI exits with clear error message when Ollama is unreachable; exit code != 0 |
| `TestCLI_EmbedPartialSuccess` | `cmd/shaktiman/main_test.go` | Warning displayed when embedded < total |
| `TestMCPToolsUnchanged` | `internal/mcp/mcp_integration_test.go` | All MCP tools return identical results after Phase 2 changes |

### 2.6 Coverage Analysis

1. Run coverage for `internal/daemon/enrichment.go`, `internal/vector/embedding.go`, `cmd/shaktiman/main.go`.
2. Target: >=80% line coverage for all new progress-reporting code paths.
3. Verify TTY detection branch, pipe branch, nil-callback branch, and partial-failure branch are all covered.

### 2.7 Performance Tests & Invariants

| Benchmark | File | Invariant |
|---|---|---|
| `BenchmarkProgressCallback_Overhead` | `internal/daemon/enrichment_bench_test.go` | Progress callback adds <1% overhead to `IndexAll` wall time (measured as <1μs per callback invocation) |
| `BenchmarkHealthCheck_Latency` | `internal/vector/embedding_bench_test.go` | `Healthy()` completes in <100ms when server is reachable |

### 2.8 Quality Gate

After implementation:
1. Run adversarial-analyst on the diff.
2. Run architect to verify UX decisions align with CLI conventions.
3. Run code-reviewer for correctness.
4. Present findings to user. Fix only after explicit approval.

---

## Phase 3: Scalability — Storage Backend Options

> Goal: Support codebases beyond 100K chunks without memory pressure.
> This phase introduces optional dedicated backends while keeping the current local-first defaults.
> The core MCP tool behavior is unchanged — only the storage layer behind the interfaces is swapped.

### 3.1 The Scaling Problem

| Scale | Chunks | BruteForceStore Memory | Search Latency | Verdict |
|---|---|---|---|---|
| Small (<5K files) | <50K | ~150MB | <30ms | Current design works well |
| Medium (5-15K files) | 50-150K | 150-450MB | 30-100ms | Workable but tight |
| Large (15K+ files) | 150K+ | 450MB+ (870MB peak) | 100ms+ | Needs alternative |
| Monorepo (50K+ files) | 500K+ | 1.5GB+ | 300ms+ | BruteForce unviable |

The two bottlenecks are:
1. **Vector store** — O(n) brute-force scan + full in-memory footprint + snapshot doubling on save.
2. **SQLite single writer** — serialized writes via `MaxOpenConns=1`. Fine for most workloads but becomes a bottleneck when embedding marks and enrichment writes compete.

### 3.2 Option A: Disk-Backed Vector Index (Recommended Default Upgrade)

Replace `BruteForceStore` with a memory-mapped HNSW index using a library like `hnswlib-go`.

**Tradeoffs:**

| Factor | BruteForceStore | HNSW (disk-backed) |
|---|---|---|
| Memory | O(n * dims * 4) | O(n * ~50 bytes) index + mmap |
| Search | O(n) exact | O(log n) approximate (recall ~95-99%) |
| Insert | O(1) | O(log n) amortized |
| Persistence | Manual snapshot (copy entire map) | Built into index structure |
| Crash safety | Snapshot lag | Immediate on disk |
| Complexity | ~200 LOC | ~500 LOC + dependency |
| Concurrency | RWMutex on entire map | Concurrent reads native, serialized writes |

**Implementation:**
- Implement `types.VectorStore` interface with HNSW backend.
- Config toggle: `vector_backend = "brute_force" | "hnsw"` in `shaktiman.toml`.

### 3.3 Option B: External Vector Database (Qdrant)

For monorepo-scale (500K+ chunks), delegate vector storage to an external database.

**Tradeoffs:**

| Factor | In-Process | External (Qdrant) |
|---|---|---|
| Setup | Zero | Requires running Qdrant server |
| Memory | Application heap | External process |
| Search | In-process | Network round-trip (~1-5ms) |
| Scale | ~1M vectors max | 10M+ vectors |
| Persistence | Application manages | Database manages |
| Concurrency | Mutex-based | Server handles |
| Dependency | None | Docker/binary |

**Implementation:**
- Implement `types.VectorStore` interface with Qdrant HTTP client.
- Config: `vector_backend = "qdrant"`, `qdrant_url = "http://localhost:6333"`.
- Eliminates `SaveToDisk`/`LoadFromDisk` (Qdrant handles persistence).
- Parallel writes possible (no mutex, Qdrant handles concurrency).

### 3.4 Option C: PostgreSQL + pgvector (Full External Stack)

Replace both SQLite and BruteForceStore with PostgreSQL + pgvector extension.

**Tradeoffs:**

| Factor | SQLite + BruteForce | PostgreSQL + pgvector |
|---|---|---|
| Setup | Zero | Requires PostgreSQL server + pgvector extension |
| Write concurrency | Single writer conn | Full MVCC, parallel writes |
| Vector search | Separate store | Integrated (`ORDER BY embedding <=> query LIMIT k`) |
| FTS | FTS5 (excellent) | `tsvector` + GIN (comparable) |
| Crash safety | WAL + manual snapshots | WAL + automatic recovery |
| Joins | Chunks + vectors in separate stores | Single query joins chunks, vectors, files |
| Scale | ~150K chunks comfortable | Millions of chunks |
| Operational cost | None | Database administration |

**Implementation:**
- Implement `storage.Store` interface with `pgx` driver.
- Vector column on chunks table: `embedding vector(768)`.
- Config: `database_backend = "postgres"`, `database_url = "postgres://..."`.
- Parallel enrichment writes (remove single-writer constraint).

### 3.5 Recommendation Matrix

| Codebase Size | Recommended Backend | Config |
|---|---|---|
| <50K chunks (most projects) | SQLite + BruteForce (default) | No config needed |
| 50-200K chunks (large projects) | SQLite + HNSW | `vector_backend = "hnsw"` |
| 200K-1M chunks (monorepos) | SQLite + Qdrant | `vector_backend = "qdrant"` |
| 1M+ chunks (mega-monorepos) | PostgreSQL + pgvector | `database_backend = "postgres"` |

### 3.6 Interface Changes for Backend Swappability (Prerequisite)

Current state: `Daemon` holds `*vector.BruteForceStore` (concrete type), not `types.VectorStore` (interface). This blocks backend swapping.

**Required refactoring:**

1. Expand `types.VectorStore` interface:
   ```go
   type VectorStore interface {
       Search(ctx context.Context, query []float32, topK int) ([]VectorResult, error)
       Upsert(ctx context.Context, chunkID int64, vector []float32) error
       UpsertBatch(ctx context.Context, chunkIDs []int64, vectors [][]float32) error
       Delete(ctx context.Context, chunkIDs []int64) error
       Has(ctx context.Context, chunkID int64) (bool, error)
       Count(ctx context.Context) (int, error)
       Close() error
   }
   ```

2. Add optional `VectorPersister` interface for stores that need explicit save:
   ```go
   type VectorPersister interface {
       SaveToDisk(path string) error
       LoadFromDisk(path string) error
   }
   ```

3. Update `Daemon` to hold `types.VectorStore` instead of `*vector.BruteForceStore`. Use type assertion for `VectorPersister` when needed.

| File | Change |
|---|---|
| `internal/types/interfaces.go` | Expand `VectorStore` with `UpsertBatch`, `Has`, `Close` |
| `internal/types/interfaces.go` | Add `VectorPersister` interface |
| `internal/vector/store.go` | Ensure `BruteForceStore` satisfies both interfaces |
| `internal/daemon/daemon.go` | Change field type from `*BruteForceStore` to `types.VectorStore` |
| `internal/vector/embedding.go` | Change `EmbedWorkerInput.Store` from `*BruteForceStore` to `types.VectorStore` |

### 3.7 Unit Tests

| Test | File | What it verifies |
|---|---|---|
| `TestVectorStoreInterface_BruteForce` | `internal/vector/store_test.go` | `BruteForceStore` satisfies expanded `VectorStore` interface (compile-time check) |
| `TestVectorPersister_BruteForce` | `internal/vector/store_test.go` | `BruteForceStore` satisfies `VectorPersister` interface |
| `TestHNSWStore_Search` | `internal/vector/hnsw_test.go` | HNSW returns top-K results with recall >=95% vs brute-force baseline |
| `TestHNSWStore_UpsertBatch` | `internal/vector/hnsw_test.go` | Batch insert of 1000 vectors; all retrievable via `Has()` |
| `TestHNSWStore_Persistence` | `internal/vector/hnsw_test.go` | `Close()` persists; new instance loads all vectors correctly |
| `TestHNSWStore_ConcurrentReadWrite` | `internal/vector/hnsw_test.go` | Concurrent `Search` and `Upsert` with `-race` flag — no races |
| `TestQdrantStore_Search` | `internal/vector/qdrant_test.go` | Mock HTTP server; correct API calls for search |
| `TestQdrantStore_UpsertBatch` | `internal/vector/qdrant_test.go` | Mock HTTP server; correct batch upsert payload |
| `TestQdrantStore_ConnectionError` | `internal/vector/qdrant_test.go` | Returns meaningful error when Qdrant is unreachable |
| `TestDaemon_VectorStoreInterface` | `internal/daemon/daemon_test.go` | Daemon works with any `VectorStore` implementation (BruteForce and mock) |
| `TestPgStore_Search` | `internal/storage/postgres_test.go` | pgvector cosine search returns correct results (requires test DB or mock) |
| `TestPgStore_WriteConcurrency` | `internal/storage/postgres_test.go` | Parallel writes succeed without contention errors |

### 3.8 Integration Tests

| Test | File | What it verifies |
|---|---|---|
| `TestEndToEnd_HNSWBackend` | `integration/hnsw_test.go` | Full index + embed + search cycle with HNSW backend. MCP search results are relevant (recall >=90% vs brute-force baseline). |
| `TestEndToEnd_QdrantBackend` | `integration/qdrant_test.go` | Full cycle with Qdrant. Requires Qdrant container (skip in CI if unavailable). |
| `TestEndToEnd_PostgresBackend` | `integration/postgres_test.go` | Full cycle with PostgreSQL + pgvector. Requires Postgres container (skip if unavailable). |
| `TestBackendSwitch_BruteForceToHNSW` | `integration/migration_test.go` | Index with BruteForce, switch config to HNSW, re-embed, verify search still works. |
| `TestMCPTools_AllBackends` | `integration/mcp_test.go` | All MCP tools return equivalent results across BruteForce, HNSW, and Qdrant backends. |

### 3.9 Coverage Analysis

1. Run coverage for all new backend files and modified daemon code.
2. Target: >=80% for `store.go`, `hnsw.go`, `qdrant.go`, `postgres.go`.
3. Verify error paths (connection failure, timeout, invalid dimensions) are covered.
4. Add fuzz tests for vector serialization/deserialization if coverage shows gaps.

### 3.10 Performance Tests & Invariants

| Benchmark | File | Invariant |
|---|---|---|
| `BenchmarkSearch_BruteForce_10K` | `internal/vector/store_bench_test.go` | <10ms for 10K vectors (baseline) |
| `BenchmarkSearch_BruteForce_100K` | `internal/vector/store_bench_test.go` | <100ms for 100K vectors (baseline) |
| `BenchmarkSearch_HNSW_100K` | `internal/vector/hnsw_bench_test.go` | <5ms for 100K vectors |
| `BenchmarkSearch_HNSW_500K` | `internal/vector/hnsw_bench_test.go` | <10ms for 500K vectors |
| `BenchmarkUpsertBatch_HNSW_1000` | `internal/vector/hnsw_bench_test.go` | <100ms for 1000 vectors |
| `BenchmarkSearch_Qdrant_100K` | `internal/vector/qdrant_bench_test.go` | <20ms for 100K vectors (includes network) |
| `BenchmarkMemory_BruteForce_100K` | `internal/vector/store_bench_test.go` | <350MB heap for 100K vectors at 768 dims |
| `BenchmarkMemory_HNSW_100K` | `internal/vector/hnsw_bench_test.go` | <50MB heap for 100K vectors at 768 dims |
| `BenchmarkSQLiteWrite_Contention` | `internal/storage/db_bench_test.go` | Embedding marks + enrichment writes combined: <50ms p99 per write tx |

### 3.11 Quality Gate

After implementation of each backend (3.6, then 3.2, then 3.3, then 3.4 separately):
1. Run adversarial-analyst on the diff — focus on concurrency, crash safety, data consistency.
2. Run architect to verify interface design and backend fit.
3. Run code-reviewer for correctness, test completeness, regression risk.
4. Present findings to user. Fix only after explicit approval.

---

## Phase 4: Configuration & Documentation

> Goal: Users can configure shaktiman for their scale.

### 4.1 Expanded `shaktiman.toml` Config

```toml
# shaktiman.toml — large repo configuration example

[embedding]
ollama_url = "http://localhost:11434"
model = "nomic-embed-text"
dims = 768
batch_size = 64          # increase for faster GPUs
enabled = true

[storage]
# database_backend = "sqlite"       # default
# database_url = "postgres://..."   # only for postgres backend

[vector]
# backend = "brute_force"   # default: in-memory, suitable for <100K chunks
# backend = "hnsw"          # disk-backed HNSW, suitable for 100K-1M chunks
# backend = "qdrant"        # external Qdrant server, suitable for 1M+ chunks
# qdrant_url = "http://localhost:6333"

[indexing]
# writer_channel_size = 500     # default
# enrichment_workers = 4        # default: GOMAXPROCS/2
# embed_page_size = 256         # default: chunks per DB page during embedding
# embed_save_interval = "30s"   # default during active embedding
```

### 4.2 Documentation

Add a section to README linking to a dedicated large-repo setup guide:

```markdown
## Large Repository Setup

For repositories with 10,000+ files, see [Large Repository Configuration](docs/large-repo-setup.md)
for guidance on backend selection and tuning.
```

The guide (`docs/large-repo-setup.md`) should cover:
- How to assess your codebase size (`shaktiman index <root>` prints stats)
- Backend selection based on chunk count (reference 3.5 recommendation matrix)
- Qdrant setup (Docker one-liner)
- PostgreSQL + pgvector setup
- Tuning batch sizes for GPU throughput
- Memory estimation formulas

### 4.3 Unit Tests

| Test | File | What it verifies |
|---|---|---|
| `TestConfigParse_VectorBackend` | `internal/types/config_test.go` | `shaktiman.toml` with `vector.backend = "hnsw"` parses correctly |
| `TestConfigParse_PostgresURL` | `internal/types/config_test.go` | `database_backend = "postgres"` with URL parses correctly |
| `TestConfigParse_Defaults` | `internal/types/config_test.go` | Missing config fields use correct defaults |
| `TestConfigValidation_InvalidBackend` | `internal/types/config_test.go` | `vector.backend = "invalid"` returns clear error |
| `TestConfigValidation_PostgresNoURL` | `internal/types/config_test.go` | `database_backend = "postgres"` without URL returns clear error |

### 4.4 Integration Tests

| Test | File | What it verifies |
|---|---|---|
| `TestCLI_ConfigFile_Override` | `cmd/shaktiman/main_test.go` | CLI reads `shaktiman.toml` from project root and applies config |
| `TestDaemon_BackendSelection` | `internal/daemon/daemon_integration_test.go` | Config `vector.backend` selects correct `VectorStore` implementation |

### 4.5 Coverage Analysis

1. Run coverage for config parsing and validation code.
2. Target: 100% branch coverage for config validation (every invalid input case).

### 4.6 Quality Gate

After implementation:
1. Run code-reviewer on config parsing and documentation.
2. Run adversarial-analyst to check for config injection, path traversal, or unsafe defaults.
3. Present findings to user. Fix only after explicit approval.

---

## Implementation Order

```
  Phase 1 ──────────────────────────────── Phase 2 ──────── Phase 3 ──────── Phase 4
  (Must Fix)                               (UX)             (Scale)          (Config)

  1.1 RunFromDB (DB cursor)                2.1 Progress     3.6 Interface    4.1 Config
   │                                        reporting        refactoring      4.2 Docs
   ├─ GetEmbedPage                          │                │                4.3 Tests
   ├─ CountChunksNeedingEmbedding           2.2 Ollama       3.2 HNSW
   ├─ Circuit breaker retry (not advance)    health check     backend
   │                                        │                │
  1.2 Fix MarkChunksEmbedded               2.3 Writer log    3.3 Qdrant
   │  (per-chunk embedded column)            noise            backend
   │                                        │                │
  1.3 Crash-safe persistence               2.4-2.7 Tests     3.4 Postgres
   │  (30s save + Has() reconcile)          2.8 Quality gate   backend
   │                                                         │
  1.4 Document watcher safety                                3.7-3.10 Tests
  1.5-1.8 Tests                                              3.11 Quality gate
  1.9 Quality gate
```

**Dependencies:**
- Phase 2 can proceed in parallel with Phase 1 (independent concerns).
- Phase 3.6 (interface refactoring) must precede 3.2-3.4 (backend implementations).
- Phase 3.2 (HNSW) can be done without 3.3/3.4.
- Phase 4 depends on Phase 3 config structure being finalized.

---

## Estimated Scope

| Phase | New LOC | Modified LOC | New Files | Test LOC | Risk |
|---|---|---|---|---|---|
| Phase 1 | ~200 | ~80 | 0 | ~400 | Low |
| Phase 2 | ~100 | ~50 | 0 | ~200 | Low |
| Phase 3.6 | ~30 | ~60 | 0 | ~100 | Medium |
| Phase 3.2 | ~500 | ~20 | 1 | ~400 | Medium |
| Phase 3.3 | ~300 | ~20 | 1 | ~300 | Low |
| Phase 3.4 | ~800 | ~100 | 2 | ~600 | High |
| Phase 4 | ~50 | ~30 | 1 | ~150 | Low |

---

## Open Decisions

| # | Question | Options | Recommendation |
|---|---|---|---|
| 1 | Per-chunk `embedded` column vs. vector store `Has()` check | Column is simpler, `Has()` avoids schema change | Column — cleaner, works with any vector backend |
| 2 | HNSW library choice | `hnswlib-go` (CGo, battle-tested) vs. pure-Go impl | `hnswlib-go` — performance matters for 100K+ vectors |
| 3 | Should Phase 3 backends share the `storage.Store` interface or be separate? | Unified vs. separate | Separate — vector and metadata have different access patterns |
| 4 | TTY detection for progress output | `golang.org/x/term` vs. `os.Stdout.Stat()` | `golang.org/x/term.IsTerminal()` — standard, reliable |

---

## Risks

| Risk | Mitigation |
|---|---|
| HNSW approximate recall <95% for code search | Benchmark on real queries before committing; keep BruteForce as fallback |
| PostgreSQL adds operational burden that deters adoption | Keep as explicit opt-in; default remains SQLite + BruteForce |
| Interface refactoring (Phase 3.6) breaks existing tests | Run full test suite after each interface change; keep backward compatibility |
| Qdrant dependency version churn | Pin to stable version; use HTTP API (more stable than gRPC) |
| Per-chunk `embedded` column migration on existing indexes | Migration adds column with default 0; mark all chunks in existing vector store as embedded=1 in migration |
