# Shaktiman: Component Design Specification

> Detailed design of all major components, their interfaces, communication protocols,
> and partial enrichment strategies.
> Derived from [03-architecture-v3.md](./03-architecture-v3.md) and [addendum](./03-architecture-v3-addendum.md).

---

## Table of Contents

1. [Component Map](#1-component-map)
2. [C1: Agent Interface (MCP Server + CLI)](#2-c1-agent-interface)
3. [C2: Shaktiman Core (Query Engine)](#3-c2-shaktiman-core)
4. [C3: Graph DB Module](#4-c3-graph-db-module)
5. [C4: Vector DB / Embedding Module](#5-c4-vector-db--embedding-module)
6. [C5: Metadata Store Module](#6-c5-metadata-store-module)
7. [C6: Diff Store Module](#7-c6-diff-store-module)
8. [C7: Enrichment Pipeline](#8-c7-enrichment-pipeline)
9. [C8: Writer Thread](#9-c8-writer-thread)
10. [C9: Filesystem / Git Monitor](#10-c9-filesystem--git-monitor)
11. [Communication Protocols](#11-communication-protocols)
12. [Partial Enrichment & Lazy Processing](#12-partial-enrichment--lazy-processing)
13. [Trust, Speed & Token Efficiency Annotations](#13-trust-speed--token-efficiency)

---

## 1. Component Map

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   ┌──────────────────────────────────────────────────────────────┐              │
│   │  C1: AGENT INTERFACE                                         │              │
│   │  ┌─────────────────────┐  ┌────────────────┐                │              │
│   │  │    MCP Server       │  │      CLI       │                │              │
│   │  │  tools / resources  │  │  commands      │                │              │
│   │  │  prompts / notifs   │  │                │                │              │
│   │  └─────────┬───────────┘  └───────┬────────┘                │              │
│   └────────────┼──────────────────────┼─────────────────────────┘              │
│                │                      │                                         │
│                ▼                      ▼                                         │
│   ┌──────────────────────────────────────────────────────────────┐              │
│   │  C2: SHAKTIMAN CORE (Query Engine)                           │              │
│   │  ┌────────────┐ ┌────────────┐ ┌──────────┐ ┌────────────┐ │              │
│   │  │Query Router│→│Retrieval   │→│Hybrid    │→│Context     │ │              │
│   │  │            │ │Engine      │ │Ranker    │ │Assembler   │ │              │
│   │  └────────────┘ └─────┬──────┘ └──────────┘ └────────────┘ │              │
│   └───────────────────────┼─────────────────────────────────────┘              │
│                           │                                                     │
│           ┌───────────────┼───────────────┬──────────────┐                      │
│           ▼               ▼               ▼              ▼                      │
│   ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐          │
│   │  C3: Graph   │ │  C4: Vector  │ │  C5: Meta-   │ │  C6: Diff    │          │
│   │  DB Module   │ │  DB Module   │ │  data Store  │ │  Store       │          │
│   │              │ │              │ │              │ │              │          │
│   │  CSR + delta │ │  sqlite-vec  │ │  files/chunks│ │  diff_log    │          │
│   │  + SQLite    │ │  + embed wkr │ │  symbols/FTS │ │  diff_symbols│          │
│   └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘          │
│           │               │               │              │                      │
│           └───────────────┼───────────────┼──────────────┘                      │
│                           ▼               ▼                                     │
│                    ┌──────────────────────────────┐                              │
│                    │  C8: WRITER THREAD           │                              │
│                    │  priority queue + serialized │                              │
│                    │  SQLite writes               │                              │
│                    └──────────────┬───────────────┘                              │
│                                  │                                               │
│                           writes to all stores                                   │
│                                                                                 │
│   ┌──────────────────────────────────────────────────────────────┐              │
│   │  C7: ENRICHMENT PIPELINE                                     │              │
│   │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐       │              │
│   │  │Tree-sitter│ │Symbol   │ │Chunk     │ │Dep       │       │              │
│   │  │Parser    │→│Extractor│ │Splitter  │ │Extractor │       │              │
│   │  └──────────┘ └──────────┘ └──────────┘ └──────────┘       │              │
│   │                     │             │           │              │              │
│   │                     └─────────────┼───────────┘              │              │
│   │                                   ▼                          │              │
│   │                          ┌──────────────┐                   │              │
│   │                          │ Diff Engine  │                   │              │
│   │                          └──────────────┘                   │              │
│   └──────────────────────────────────────────────────────────────┘              │
│                                  ▲                                               │
│                                  │ file change events                            │
│   ┌──────────────────────────────┴───────────────────────────────┐              │
│   │  C9: FILESYSTEM / GIT MONITOR                                │              │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │              │
│   │  │ File Watcher │  │   Change     │  │  Periodic    │      │              │
│   │  │ (fsevents)   │  │  Detector    │  │  Scanner     │      │              │
│   │  └──────────────┘  └──────────────┘  └──────────────┘      │              │
│   └──────────────────────────────────────────────────────────────┘              │
│                                                                                 │
│   Cross-cutting: Session Store (in-memory LRU + periodic SQLite flush)          │
│   Cross-cutting: File Enrichment Mutex (per-file lock map)                      │
│   Cross-cutting: Config Manager (.shaktiman/config.json)                        │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. C1: Agent Interface

### Purpose
The front door. Translates MCP protocol and CLI commands into internal Query Engine calls, and translates results back into token-efficient response formats. Manages push mode (resources, notifications, prompts).

### Inputs

| Source | Input | Format |
|---|---|---|
| Claude Code / MCP clients | Tool calls: `search`, `context`, `symbols`, `dependencies`, `diff`, `summary` | MCP JSON-RPC over stdio |
| Claude Code / MCP clients | Resource reads: `shaktiman://context/active`, `shaktiman://workspace/summary` | MCP resource protocol |
| Claude Code / MCP clients | Prompt requests: `task-start` | MCP prompt protocol |
| Developer | CLI commands: `init`, `query`, `status`, `diff`, `reindex`, `config`, `inspect` | Shell argv |
| Internal (C8/C9) | State change events (for push mode notifications) | In-process channel |

### Outputs

| Target | Output | Format |
|---|---|---|
| MCP clients | `ContextPackage` (ranked chunks + metadata + budget accounting) | MCP JSON-RPC response |
| MCP clients | Resource content (pre-assembled context) | MCP resource response |
| MCP clients | `context/changed` notification | MCP notification |
| Developer | Formatted text (tables, trees, progress bars) | stdout |
| C2 (Core) | `QueryRequest` | In-process function call |
| Session Store | `AccessEvent` (log tool calls for session tracking) | In-process channel |

### Internal Responsibilities

```
MCP SERVER:
  1. Parse MCP JSON-RPC messages
  2. Validate tool call parameters (budget bounds, file paths)
  3. Translate MCP tool calls → QueryRequest structs
  4. Call C2 (Shaktiman Core) synchronously
  5. Serialize ContextPackage → MCP JSON-RPC response
  6. Emit AccessEvent to Session Store (async, fire-and-forget)

RESOURCE MANAGER (push mode):
  7. Maintain cached active resource content (ContextPackage)
  8. Listen for resource update triggers (from C9 watcher, session shifts)
  9. Re-assemble resource content (debounced: 500ms after last trigger)
  10. Detect branch switches (>20 file changes in <2s)
  11. Send context/changed notifications (debounced: 3s min interval)

CLI:
  12. Parse argv, dispatch to same internal functions as MCP
  13. Format output for terminal (colors, tables, progress)
  14. Handle interactive commands (reindex with progress bar)
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C2: Shaktiman Core | Query execution | Sync (blocks on response) |
| Session Store | Log access events | Async (fire-and-forget channel) |
| C9: Monitor | Listen for state changes (push mode) | Async (event channel) |
| Config Manager | Read config (budgets, tokenizer) | Sync (startup + on-demand) |

### Performance Considerations

```
• MCP connection: persistent stdio, no reconnect overhead
• Resource cache: pre-assembled, served in <1ms on read
• Resource re-assembly: debounced 500ms, runs in background thread
• Notification debounce: 3s — prevents agent from making rapid redundant reads
• CLI output: streamed, not buffered — progress visible during cold index
```

### Failure Handling

```
• MCP parse error → return JSON-RPC error response (code: -32700)
• Invalid tool params → return JSON-RPC error with descriptive message
• C2 query timeout → return partial results with enrichment_level: "degraded"
• Resource assembly failure → serve last cached version, log warning
• CLI command failure → stderr message + exit code 1
```

**Token efficiency:** Response format uses compact metadata (~12 tokens/chunk). Budget accounting included in every response so the agent knows exactly how much context it received.

**Developer trust:** Every response includes `strategy` field ("hybrid_l0", "structural", "degraded") and `enrichment_level` so the agent and developer can see exactly what quality of results they got.

---

## 3. C2: Shaktiman Core (Query Engine)

### Purpose
The brain. Routes queries to the right strategy, fans out to index stores in parallel, normalizes and ranks results, and assembles budget-fitted context packages. This is the **hot path** — every millisecond counts.

### Inputs

| Source | Input | Format |
|---|---|---|
| C1 (Interface) | `QueryRequest` | In-process struct |
| C7 (Enrichment) | `EnrichmentResult` (for query-time enrichment) | In-process struct |

### Outputs

| Target | Output | Format |
|---|---|---|
| C1 (Interface) | `ContextPackage` | In-process struct |
| C7 (Enrichment) | `EnrichmentTrigger` (query-time enrichment) | In-process function call |
| C4 (Vector) | Priority embedding bump | Async channel message |

### Internal Responsibilities

#### Sub-component: Query Router

```
RESPONSIBILITIES:
  1. Inspect index readiness flags:
     • metadata_ready: bool    (C5 has files/chunks/symbols)
     • graph_ready: CsrState   (BUILDING | READY)
     • embeddings_pct: f32     (0.0 - 1.0)
     • fts_ready: bool         (FTS5 index built)

  2. Select retrieval strategy:
     if embeddings_pct > 0.8 && graph_ready == READY     → Level 0: FULL_HYBRID
     if embeddings_pct > 0.2 && metadata_ready            → Level 0.5: MIXED
     if metadata_ready && (graph_ready || fts_ready)      → Level 1: STRUCTURAL_KW
     if fts_ready                                          → Level 2: KEYWORD_ONLY
     else                                                  → Level 3: FILESYSTEM_PASS

  3. Check query-time enrichment triggers:
     • Target file unindexed? → trigger sync enrichment (single-file, 80ms budget)
     • Target file stale? → trigger sync re-enrichment
     • Constraints: per-file mutex, no recursion, 80ms max

  4. Pass strategy + query to Retrieval Engine
```

#### Sub-component: Retrieval Engine

```
RESPONSIBILITIES:
  1. Embed query text (via C4 embedding model, cached in LRU)
  2. Fan out to index stores IN PARALLEL:
     ┌──▶ C4: Vector search      → Vec<(ChunkId, f32)>     cosine scores
     ├──▶ C3: Graph BFS          → Map<ChunkId, u8>        BFS distances
     ├──▶ C5: FTS5 search        → Vec<(ChunkId, f32)>     BM25 scores
     ├──▶ C6: Diff score query   → Map<ChunkId, f32>       change scores
     └──▶ Session Store: lookup  → Map<ChunkId, f32>       session scores
  3. Collect all results, merge into Vec<CandidateChunk>
  4. Each CandidateChunk has 5 raw sub-scores + chunk metadata
  5. Pass to Hybrid Ranker
```

#### Sub-component: Hybrid Ranker

```
RESPONSIBILITIES:
  1. NORMALIZE each raw score to [0.0, 1.0]:
     • semantic:    (cosine + 1.0) / 2.0
     • structural:  1.0 / (1.0 + bfs_distance as f32)
     • keyword:     min(bm25 / percentile_95_bm25, 1.0)
     • change:      recency_decay(ts) × min(magnitude / 50.0, 1.0)
     • session:     min(access_count / 5.0, 1.0) × 0.9^queries_since_last_hit

  2. APPLY WEIGHTS:
     score = 0.40 × sem + 0.20 × struct + 0.15 × change + 0.15 × session + 0.10 × kw

  3. WEIGHT REDISTRIBUTION (when signals unavailable):
     Embeddings down: [0.00, 0.30, 0.20, 0.15, 0.35]
     Graph down:      [0.50, 0.00, 0.15, 0.15, 0.20]
     No diffs:        [0.40, 0.20, 0.00, 0.25, 0.15]

  4. PARSE QUALITY PENALTY:
     if chunk.parse_quality == Partial: score *= 0.5

  5. Sort descending by final score
```

#### Sub-component: Context Assembler

```
RESPONSIBILITIES:
  1. COMPUTE effective budget:
     effective = budget × 0.95    # safety margin for tokenizer variance

  2. PRIMARY SELECTION:
     expansion_budget = effective × 0.30
     primary_budget   = effective × 0.70

     for chunk in ranked_chunks:
       if line_overlap(chunk, selected) > 0.50 → skip
       if chunk.token_count > remaining_primary → skip
       add chunk; subtract tokens

  3. STRUCTURAL EXPANSION (capped):
     for chunk in selected:
       neighbors = C3.bfs(chunk.symbol_id, depth=2, max_results=5)
       for neighbor in neighbors:
         if neighbor.token_count > remaining_expansion → skip
         if neighbor already selected → skip
         add neighbor; subtract from expansion_budget

  4. METADATA ATTACHMENT:
     for chunk in package:
       attach {
         path, symbol_name, start_line, end_line,
         score, last_modified, change_summary, parse_quality
       }

  5. RETURN ContextPackage {
       chunks, budget_used, budget_limit, effective_limit,
       strategy, enrichment_level, tokenizer: "cl100k_base"
     }
```

### Performance Considerations

```
LATENCY BUDGET (p95 < 200ms):
  Query Router:       2-5ms    (flag checks + enrichment trigger decision)
  Query embed:        3-8ms    (LRU cache hit: 0ms; miss: ~8ms)
  Parallel fan-out:   35-60ms  (max of 5 parallel store reads)
  Normalize + rank:   3-7ms    (pure math)
  Assemble:           10-25ms  (pre-computed token counts, greedy pack)
  TOTAL:              53-105ms

  LRU caches:
    • Query embedding cache: 100 entries, ~3MB
    • BM25 percentile cache: per-query, no persistence needed
```

### Failure Handling

```
• Vector store timeout → exclude semantic score, redistribute weights
• Graph BFS timeout → exclude structural score, redistribute weights
• FTS5 error → exclude keyword score, redistribute weights
• All stores fail → Level 3 filesystem passthrough
• Query-time enrichment timeout (>80ms) → serve without enrichment, queue async
• Zero results → return empty package with hint: "No matching code found.
  Try broader query or check `shaktiman status` for index readiness."
```

**Token efficiency:** Every result is budget-fitted. The agent never receives more tokens than it asked for. Pre-computed token counts eliminate runtime tokenization. Expansion is capped at 30% to prevent structural bloat.

**Speed:** Parallel fan-out to stores means latency = max(individual store latency), not sum. LRU caching for query embeddings avoids redundant model calls.

---

## 4. C3: Graph DB Module

### Purpose
Stores and traverses the directed dependency graph of code relationships. Provides fast BFS for structural scoring. The graph captures how code connects: imports, calls, type references, extends, implements, exports.

### Inputs

| Source | Input | Format |
|---|---|---|
| C8 (Writer Thread) | `EdgeBatch` (new/deleted edges from enrichment) | In-process struct |
| C2 (Core) | `BfsRequest` (symbol_id, max_depth, max_results) | In-process function call |
| C2 (Core) | `NeighborRequest` (callers/callees of a symbol) | In-process function call |

### Outputs

| Target | Output | Format |
|---|---|---|
| C2 (Core) | `BfsResult` (Map<SymbolId, depth>) | In-process return value |
| C2 (Core) | `NeighborList` (Vec of symbol_id + edge_kind) | In-process return value |
| C1 (Interface) | Dependencies response (for `dependencies()` tool) | Via C2 |

### Internal Responsibilities

```
DATA STRUCTURES:

  1. CSR (Compressed Sparse Row) — immutable, read-optimized
     • offsets: Vec<u32>   — one per node, pointing into edges array
     • edges: Vec<(u32, u8)>  — (dest_node_id, edge_kind_enum)
     • Built from SQLite edges table during compaction
     • Swapped atomically via Arc pointer swap

  2. Delta Buffer — append-only, lock-free
     • additions: Vec<(u32, u32, u8)>  — (src, dst, kind)
     • deletions: HashSet<(u32, u32)>  — (src, dst)
     • Version counter (atomic u64), incremented on each append
     • Cleared on compaction

  3. SQLite edge table — persistent, write-through
     • All edge mutations go through Writer Thread (C8)
     • Source of truth for CSR rebuilds

OPERATIONS:

  bfs(start: SymbolId, max_depth: u8, max_results: usize) → BfsResult:
    if csr_state == READY:
      result = csr.bfs(start, max_depth)
      result = apply_delta(result, delta_buffer)   # add new, remove deleted
      return result.truncate(max_results)
    else: # BUILDING
      return sqlite_bfs(start, min(max_depth, 2))  # depth-limited fallback

  neighbors(symbol: SymbolId, direction: In|Out|Both) → NeighborList:
    similar to bfs(depth=1) but returns edge kinds

  update(batch: EdgeBatch):
    # Called by Writer Thread AFTER SQLite commit
    for (src, dst, kind) in batch.additions:
      delta_buffer.add(src, dst, kind)
    for (src, dst) in batch.deletions:
      delta_buffer.delete(src, dst)
    write_version.increment()

  compact():
    # Background thread, triggered when delta_buffer.len() > 5000 OR every 60s idle
    new_csr = build_csr_from_sqlite()   # 50-120ms at 1M lines
    atomic_swap(csr, new_csr)
    delta_buffer.clear()
    csr_state = READY

STATE MACHINE:
  BUILDING → READY (on initial build or compaction complete)
  READY → READY (normal operation with delta buffer)
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C8 (Writer Thread) | Receives edge updates after SQLite commit | Sync (called by Writer Thread) |
| C5 (Metadata Store) | Symbol ID resolution | Sync (function call) |
| SQLite | Persistent edge storage | Via C8 (Writer Thread) |

### Performance Considerations

```
CSR BFS:
  • Sequential memory access (cache-friendly)
  • 100K lines: ~3ms for depth-3 BFS
  • 1M lines: ~12ms for depth-3 BFS
  • Delta buffer scan: <0.1ms for <1000 entries

SQLite BFS fallback (BUILDING state):
  • Recursive CTE, depth ≤ 2
  • 1M lines: ~30-80ms (acceptable for brief startup window)

Memory:
  100K lines: ~600KB (CSR) + <60KB (delta buffer)
  1M lines:   ~17MB (CSR) + <60KB (delta buffer)
  2M lines:   ~42MB (CSR) + <60KB (delta buffer)

Compaction:
  Non-blocking — queries use old CSR + delta during rebuild
  Atomic pointer swap — no reader locks needed
```

### Failure Handling

```
• CSR build fails (corrupted edges table):
  → Stay in BUILDING state, serve SQLite BFS
  → Log error, suggest `shaktiman reindex`

• Delta buffer grows beyond 10K entries (compaction stuck):
  → Force compaction, log warning
  → If compaction still fails → clear delta buffer, serve SQLite BFS

• SQLite BFS timeout (>100ms):
  → Return partial result with depth reduced to 1
  → Log slow query for diagnostics
```

**Trust:** Version counter ensures queries never silently read stale graph data. Fallback to SQLite BFS during startup is correct, just slower.

---

## 5. C4: Vector DB / Embedding Module

> **Note (FD-3):** References to "sqlite-vec" in this section are superseded. The default implementation is **brute-force in-process** (`[]float32` + cosine similarity). The `VectorStore` interface remains unchanged. See architecture doc FD-3.

### Purpose
Two responsibilities: (1) store and search chunk embeddings for semantic similarity, and (2) manage the async embedding pipeline that generates embeddings in the background. Separated behind a `VectorStore` trait interface so the storage backend can be swapped.

### Inputs

| Source | Input | Format |
|---|---|---|
| C2 (Core) | `VectorSearchRequest` (query_vector, top_k) | In-process function call |
| C2 (Core) | `EmbedTextRequest` (query text to embed) | In-process function call |
| C8 (Writer Thread) | `EmbeddingWrite` (chunk_id, vector) | In-process via Writer Thread |
| C7 (Enrichment) | `EmbedQueueEntry` (chunk_id, content, priority) | Async channel |
| C2 (Core) | `PriorityBump` (chunk_ids to promote to P1) | Async channel |

### Outputs

| Target | Output | Format |
|---|---|---|
| C2 (Core) | `Vec<(ChunkId, f32)>` — top-K chunk IDs with cosine scores | In-process return |
| C2 (Core) | `Vec<f32>` — query embedding vector | In-process return |
| C8 (Writer Thread) | `WriteJob::EmbeddingInsert` | Async channel |

### Internal Responsibilities

```
VECTOR STORE (trait interface):

  trait VectorStore {
    fn search(&self, query: &[f32], top_k: usize) -> Vec<(ChunkId, f32)>;
    fn insert(&self, chunk_id: ChunkId, vector: &[f32]);
    fn delete(&self, chunk_id: ChunkId);
    fn invalidate_all(&self);
    fn count(&self) -> usize;
    fn model_id(&self) -> &str;
  }

  Primary implementation: SqliteVecStore
    • Uses sqlite-vec extension in .shaktiman/index.db
    • Reads via reader connection pool (WAL — no blocking)
    • Writes via Writer Thread

EMBEDDING MODEL (trait interface):

  trait EmbeddingModel {
    fn embed_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>>;
    fn embed_single(&self, text: &str) -> Result<Vec<f32>>;
    fn model_id(&self) -> &str;
    fn dimensions(&self) -> usize;
  }

  Primary implementation: OllamaEmbedder
    • HTTP calls to Ollama API (localhost:11434)
    • Model: nomic-embed-text (768 dimensions)
    • Batch size: 32 texts per API call

EMBEDDING WORKER (background thread):

  Priority queue:
    P1: Query-time bumped chunks (serve next query better)
    P2: Recently modified files (current session)
    P3: Working set files
    P4: All remaining un-embedded chunks (FIFO)

  Processing loop:
    while !shutdown:
      batch = dequeue(max=32)
      if batch.is_empty():
        sleep(500ms)       # idle — nothing to embed
        continue

      match circuit_breaker.state:
        DISABLED → sleep(60s); continue
        OPEN → if cooldown_elapsed: set HALF_OPEN; else sleep(1s); continue
        HALF_OPEN → try single batch; success→CLOSED, fail→OPEN
        CLOSED → process normally

      result = embed_model.embed_batch(batch.texts, timeout=30s)
      match result:
        Ok(vectors) →
          for (chunk_id, vector) in zip(batch.ids, vectors):
            writer_thread.enqueue(WriteJob::EmbeddingInsert { chunk_id, vector }, P2)
          circuit_breaker.record_success()
        Err(timeout | connection) →
          circuit_breaker.record_failure()
          requeue(batch, same_priority)

      // CPU throttle
      if system_load > 0.8: sleep(500ms)
      else: sleep(100ms)    # yield CPU between batches

MODEL CHANGE DETECTION:

  on_startup():
    stored_model = config.get("embedding_model_id")
    current_model = embed_model.model_id()
    if stored_model != current_model:
      log_warn("Embedding model changed: {stored_model} → {current_model}")
      vector_store.invalidate_all()
      config.set("embedding_model_id", current_model)
      requeue_all_chunks_at_p4()
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C2 (Core) | Search and embed queries | Sync (function call) |
| C8 (Writer Thread) | Write new embeddings to SQLite | Async (enqueue) |
| C7 (Enrichment) | Receive chunk IDs for embedding | Async (channel) |
| Ollama (external) | Generate embeddings | Async HTTP (with timeout) |
| Config Manager | Model ID tracking | Sync (read on startup) |

### Performance Considerations

```
Query embedding:
  • LRU cache: 100 entries (~3MB for 768-dim vectors)
  • Cache hit: <0.1ms; cache miss: ~5-8ms (Ollama call)

Vector search:
  • sqlite-vec at 10K chunks: ~20ms
  • sqlite-vec at 50K chunks: ~50ms
  • sqlite-vec at 100K chunks: ~80ms (approaching limit)
  • If >100K chunks: swap to usearch implementation (same trait)

Embedding throughput:
  • Ollama nomic-embed-text: ~200 chunks/minute on CPU, ~2000/minute on GPU
  • 100K-line codebase (~10K chunks): 50 min CPU / 5 min GPU
  • 1M-line codebase (~75K chunks): 6 hours CPU / 37 min GPU

Circuit breaker prevents wasted work when Ollama is down.
```

### Failure Handling

```
• Ollama not running at startup:
  → Circuit breaker starts OPEN → after 3 cycles → DISABLED
  → System operates at Level 1 (structural + keyword)
  → Log: "Embedding disabled. Run `shaktiman config set embedding.enabled true`"

• Ollama timeout (>30s):
  → Circuit breaker records failure
  → Batch re-queued at same priority
  → After 3 failures → OPEN (5 min cooldown)

• Model produces wrong dimensions:
  → Detect on first batch: actual dims != config.embedding_dimensions
  → Invalidate all, update config, re-embed from scratch
  → Log error

• sqlite-vec query error:
  → Return empty results, exclude semantic score from ranking
  → Weight redistribution kicks in
```

**Token efficiency:** Embedding happens in the background — never blocks the query path. The agent gets structural/keyword results immediately while semantic search improves asynchronously.

---

## 6. C5: Metadata Store Module

### Purpose
Central catalog of all indexed files, code chunks, and symbol definitions. Provides FTS5 full-text search for keyword matching. This is the foundational data layer — all other stores reference entities defined here.

### Inputs

| Source | Input | Format |
|---|---|---|
| C8 (Writer Thread) | `FileRecord`, `ChunkBatch`, `SymbolBatch` | In-process via WriteJob |
| C2 (Core) | FTS5 search queries | SQL via reader connection |
| C2 (Core) | Chunk/symbol lookups by ID | SQL via reader connection |
| C7 (Enrichment) | Readiness checks (is file indexed? is it stale?) | Sync function call |

### Outputs

| Target | Output | Format |
|---|---|---|
| C2 (Core) | `Vec<(ChunkId, f32)>` — FTS5/BM25 results | In-process return |
| C2 (Core) | `ChunkRecord` — full chunk content + metadata | In-process return |
| C7 (Enrichment) | `IndexStatus` — for a given file: indexed/stale/missing | In-process return |
| C3 (Graph) | `SymbolId` lookups for graph node identity | In-process return |

### Internal Responsibilities

```
SCHEMA (SQLite tables):

  schema_version (version INTEGER, applied_at TEXT)

  files (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE NOT NULL,
    content_hash TEXT NOT NULL,
    mtime REAL NOT NULL,
    size INTEGER,
    language TEXT,
    indexed_at TEXT,
    embedding_status TEXT DEFAULT 'pending'    -- 'pending'|'partial'|'complete'
  )

  chunks (
    id INTEGER PRIMARY KEY,
    file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
    parent_chunk_id INTEGER REFERENCES chunks(id),
    symbol_name TEXT,
    kind TEXT,         -- 'function'|'class'|'method'|'type'|'interface'|'header'
    start_line INTEGER,
    end_line INTEGER,
    content TEXT NOT NULL,
    token_count INTEGER NOT NULL,   -- pre-computed via cl100k_base
    signature TEXT,
    parse_quality TEXT DEFAULT 'full'   -- 'full'|'partial'
  )

  symbols (
    id INTEGER PRIMARY KEY,
    chunk_id INTEGER REFERENCES chunks(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    file_id INTEGER REFERENCES files(id),
    line INTEGER,
    signature TEXT,
    visibility TEXT    -- 'public'|'private'|'internal'
  )

  CREATE VIRTUAL TABLE chunks_fts USING fts5(
    content, symbol_name, content=chunks, content_rowid=id
  );

  CREATE INDEX idx_files_path ON files(path);
  CREATE INDEX idx_chunks_file ON chunks(file_id);
  CREATE INDEX idx_symbols_name ON symbols(name);
  CREATE INDEX idx_symbols_file ON symbols(file_id);

OPERATIONS:

  fts_search(query: &str, limit: usize) → Vec<(ChunkId, f32)>:
    SELECT rowid, rank FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY rank LIMIT ?

  get_chunk(id: ChunkId) → ChunkRecord:
    SELECT * FROM chunks WHERE id = ?

  get_file_status(path: &str) → IndexStatus:
    SELECT content_hash, indexed_at, embedding_status FROM files WHERE path = ?
    → compare with current file hash → return Indexed|Stale|Missing

  get_chunks_for_file(file_id: FileId) → Vec<ChunkRecord>:
    SELECT * FROM chunks WHERE file_id = ? ORDER BY start_line

  get_embedding_readiness() → f32:
    SELECT COUNT(*) FILTER (WHERE embedding_status != 'pending') * 1.0 / COUNT(*)
    FROM files WHERE indexed_at IS NOT NULL
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C8 (Writer Thread) | All writes go through Writer Thread | Async (enqueued) |
| SQLite reader pool | All reads use pooled read connections | Sync (query) |
| C3 (Graph) | Symbol IDs for graph node identity | Sync (lookup) |
| C7 (Enrichment) | Stale/missing detection for enrichment triggers | Sync (lookup) |

### Performance Considerations

```
FTS5 search:
  • 100K lines: ~4ms
  • 1M lines: ~8ms
  • FTS5 rank() function provides BM25 scores directly

Chunk lookup by ID:
  • O(1) with PRIMARY KEY index: <0.1ms

File status check:
  • O(1) with idx_files_path index: <0.1ms

FTS5 during cold index:
  • DISABLED during cold index (no content sync)
  • Single FTS5 rebuild after cold index: 2-5s at 1M lines
  • During cold index, keyword search returns empty → graceful degradation

Token counting:
  • Pre-computed at chunk creation time using cl100k_base tokenizer
  • Stored in chunks.token_count — never recomputed at query time
  • Safety margin (95%) applied in Context Assembler, not here

Schema migrations:
  • schema_version table tracks current version
  • On startup: compare schema_version with code version
  • Run migration scripts sequentially (ALTER TABLE, CREATE INDEX, etc.)
  • Major version change → prompt for `shaktiman reindex`
```

### Failure Handling

```
• FTS5 corruption:
  → Rebuild: INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild');
  → During rebuild: keyword search returns empty, weight redistribution handles

• File hash mismatch (detected by enrichment):
  → Mark file as stale, trigger re-enrichment
  → Serve stale chunks (flagged) until re-enrichment completes

• Schema version mismatch:
  → Run migrations for minor versions
  → For major versions: prompt `shaktiman reindex`
  → Never silently drop data
```

**Token efficiency:** Token counts pre-computed at index time — zero runtime tokenization cost. FTS5 provides BM25 scores directly — no post-processing needed.

---

## 7. C6: Diff Store Module

### Purpose
Tracks what changed, when, and which symbols were affected. Provides the change signal for ranking (recently modified code is more relevant) and powers the `diff()` MCP tool. Unlike git, this tracks symbol-level changes, not just line-level.

### Inputs

| Source | Input | Format |
|---|---|---|
| C7 (Enrichment/Diff Engine) | `DiffRecord` (file change + affected symbols) | Via C8 Writer Thread |
| C2 (Core) | `DiffScoreQuery` (chunk_ids → change scores) | Sync function call |
| C1 (Interface) | `DiffQuery` (scope, since) — for `diff()` MCP tool | Via C2 |

### Outputs

| Target | Output | Format |
|---|---|---|
| C2 (Core) | `Map<ChunkId, f32>` — change scores per chunk | In-process return |
| C1 (Interface) | `DiffReport` — human/agent-readable change summary | Via C2 |

### Internal Responsibilities

```
SCHEMA:

  diff_log (
    id INTEGER PRIMARY KEY,
    file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
    timestamp TEXT NOT NULL,                -- ISO8601
    change_type TEXT NOT NULL,              -- 'add'|'modify'|'delete'|'rename'
    lines_added INTEGER DEFAULT 0,
    lines_removed INTEGER DEFAULT 0,
    hash_before TEXT,
    hash_after TEXT
  )
  CREATE INDEX idx_difflog_file_ts ON diff_log(file_id, timestamp);
  CREATE INDEX idx_difflog_ts ON diff_log(timestamp);

  diff_symbols (
    id INTEGER PRIMARY KEY,
    diff_id INTEGER REFERENCES diff_log(id) ON DELETE CASCADE,
    symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    symbol_name TEXT NOT NULL,              -- preserved even if symbol deleted
    change_type TEXT NOT NULL               -- 'added'|'modified'|'removed'|'signature_changed'
  )
  CREATE INDEX idx_diffsym_symbol ON diff_symbols(symbol_id);
  CREATE INDEX idx_diffsym_diff ON diff_symbols(diff_id);

OPERATIONS:

  compute_change_scores(chunk_ids: &[ChunkId], now: Timestamp) → Map<ChunkId, f32>:
    // For each chunk, find the most recent diff affecting its symbol
    // Score = recency_decay(time_since_change) × min(magnitude / 50.0, 1.0)
    // recency_decay = exp(-0.05 × hours_since_change)

    SELECT c.id, MAX(
      EXP(-0.05 * (julianday(?) - julianday(dl.timestamp)) * 24)
      * MIN((dl.lines_added + dl.lines_removed) / 50.0, 1.0)
    )
    FROM chunks c
    JOIN diff_symbols ds ON ds.symbol_id IN (SELECT id FROM symbols WHERE chunk_id = c.id)
    JOIN diff_log dl ON dl.id = ds.diff_id
    WHERE c.id IN (?)
    GROUP BY c.id

  query_diffs(scope: &str, since: Timestamp) → DiffReport:
    // For the diff() MCP tool
    SELECT dl.*, ds.symbol_name, ds.change_type as sym_change,
           f.path
    FROM diff_log dl
    JOIN files f ON f.id = dl.file_id
    LEFT JOIN diff_symbols ds ON ds.diff_id = dl.id
    WHERE f.path LIKE ? AND dl.timestamp > ?
    ORDER BY dl.timestamp DESC

  prune_old_diffs(max_age_days: u32):
    DELETE FROM diff_log WHERE timestamp < datetime('now', '-' || ? || ' days')
    -- Runs on startup and every 6 hours during idle
    -- Default: 30 days retention
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C8 (Writer Thread) | All writes via Writer Thread | Async (enqueued) |
| C5 (Metadata Store) | Symbol/chunk cross-references | Sync (JOIN in SQL) |
| SQLite reader pool | All reads | Sync (query) |

### Performance Considerations

```
Change score query:
  • Batch query for all candidate chunk_ids in one SQL call
  • With composite indexes: ~4ms at 100K lines, ~8ms at 1M lines
  • Computed at query time (not pre-cached) — diffs are time-dependent

Diff tool query:
  • Scope-filtered, timestamp-filtered — fast with indexes
  • Returns at most 100 results (paginated)

Storage:
  • ~1KB per diff_log entry
  • 30-day retention, ~100 changes/day = ~3000 entries = ~3MB
  • Heavy development: ~1000 changes/day = ~30K entries = ~30MB
  • Pruning keeps storage bounded
```

### Failure Handling

```
• diff_symbols references deleted symbol (ON DELETE SET NULL):
  → symbol_name TEXT preserved as denormalized copy
  → Change score still computable from diff_log data alone

• Diff computation fails (file unreadable):
  → Skip diff record, log warning
  → File still gets re-indexed (just no diff tracking for this change)
  → Next successful change will produce a diff
```

**Token efficiency:** The `diff()` tool lets the agent ask "what changed?" instead of re-reading files to discover changes. A diff summary is ~50 tokens vs ~2000 tokens for re-reading the file.

**Developer trust:** Change summaries include symbol-level detail ("validateToken: signature changed") not just file-level ("middleware.ts modified").

---

## 8. C7: Enrichment Pipeline

### Purpose
Transforms raw source files into indexed, chunked, analyzed data. This is the ingestion engine that feeds all index stores. It runs both in the background (watcher-triggered) and on-demand (query-time enrichment triggers).

### Inputs

| Source | Input | Format |
|---|---|---|
| C9 (Monitor) | `FileChangeEvent` (path, change_type) | Async channel |
| C2 (Core) | `EnrichmentTrigger` (path, is_query_time, budget_ms) | Sync function call |
| Cold index job | `ColdIndexBatch` (list of file paths) | In-process |

### Outputs

| Target | Output | Format |
|---|---|---|
| C8 (Writer Thread) | `WriteJob::EnrichmentResult` | Async channel (enqueue) |
| C4 (Vector) | `EmbedQueueEntry` (chunk_id, content, priority) | Async channel |
| C2 (Core) | `EnrichmentResult` (for query-time: immediate, not via Writer Thread) | In-process return |

### Internal Responsibilities

#### Sub-component: Tree-sitter Parser

```
RESPONSIBILITIES:
  1. Detect language from file extension (or config override)
  2. Load appropriate tree-sitter grammar
  3. Parse source file → AST
  4. Count ERROR nodes in AST:
     • 0 errors → parse_quality = Full
     • 1-3 errors → parse_quality = Partial (extract what we can)
     • >3 errors OR >50% ERROR nodes → parse_quality = Error (keep previous index)
  5. Run tree-sitter query files (.scm) for language-specific extraction

LANGUAGE SUPPORT (via .scm query files):
  Each language has 3 query files in .shaktiman/queries/<lang>/:
    • symbols.scm    — extract function/class/method/type definitions
    • imports.scm    — extract import/export statements
    • calls.scm      — extract function call sites

  Adding a new language (NFR-14):
    1. Add tree-sitter grammar to dependency
    2. Write 3 .scm query files
    3. Register language in config
    — No code changes needed
```

#### Sub-component: Symbol Extractor

```
RESPONSIBILITIES:
  1. Run symbols.scm query against AST
  2. For each match: extract name, kind, line, signature, visibility
  3. Produce Vec<SymbolRecord>
```

#### Sub-component: Chunk Splitter

```
RESPONSIBILITIES:
  1. Split AST into semantic chunks at symbol boundaries
  2. Chunking strategy:
     • Primary: one chunk per top-level symbol (function, class, type)
     • Nested: class methods → own chunks, parent_chunk_id set to class chunk
     • Header: top-level code (imports, constants) → file-header chunk
     • Oversized: chunks > 1024 tokens → split at logical sub-boundaries
     • Minimum: chunks < 20 tokens → merge with adjacent
  3. Pre-compute token count for each chunk (cl100k_base)
  4. Produce Vec<ChunkRecord>
```

#### Sub-component: Dep Extractor

```
RESPONSIBILITIES:
  1. Run imports.scm and calls.scm queries against AST
  2. Resolve import targets to symbol IDs (best-effort, file-path based)
  3. Resolve call targets to symbol IDs (best-effort, name + scope matching)
  4. Produce Vec<EdgeRecord> with edge kinds:
     imports, calls, type_ref, extends, implements, exports
```

#### Sub-component: Diff Engine

```
RESPONSIBILITIES:
  Runs AFTER parser + extractors complete (SF-1).

  1. Get previous chunk content from Metadata Store (C5)
  2. Compute line-level diff (Myers or patience algorithm)
  3. Map changed line ranges to:
     a. OLD symbol IDs (from previous index)
     b. NEW symbol IDs (from current extraction)
  4. Produce DiffRecord:
     • diff_log entry: file_id, change_type, lines_added, lines_removed
     • diff_symbols entries: symbol_id, symbol_name, change_type

  If file is new (no previous content):
    • change_type = 'add'
    • All symbols marked as 'added'
    • No line-level diff needed

  If file is deleted:
    • change_type = 'delete'
    • All previous symbols marked as 'removed'
```

#### Orchestration: Enrichment Flow

```
enrich_file(path: &str, is_query_time: bool) → EnrichmentResult:

  1. ACQUIRE per-file mutex
     • Watcher path: try_lock() — if held, skip (caught by periodic scan)
     • Query-time path: lock(timeout=50ms) — if held, wait or fail

  2. READ file content from filesystem

  3. PARSE (tree-sitter)
     if parse_quality == Error:
       RELEASE mutex
       return PreviousIndex    # keep old data, mark for retry

  4. PARALLEL EXTRACT (spawn 3 tasks, join all):
     ├── symbols = symbol_extractor.extract(ast)
     ├── chunks  = chunk_splitter.split(ast, file_content)
     └── edges   = dep_extractor.extract(ast)

  5. DIFF ENGINE (sequential, after extract):
     diff = diff_engine.compute(path, chunks, symbols, previous_index)

  6. PACKAGE into immutable WriteJob:
     job = WriteJob::Enrichment {
       file_record, chunks, symbols, edges, diff,
       stale_chunk_ids,         # chunks to invalidate embeddings for
       embedding_queue_entries   # new chunks to embed
     }

  7. RELEASE per-file mutex

  8. ENQUEUE to Writer Thread:
     if is_query_time: writer_thread.enqueue(job, P0)
     else: writer_thread.enqueue(job, P2)

  9. ENQUEUE to Embedding Worker:
     for chunk in new_chunks:
       embed_queue.send(EmbedQueueEntry {
         chunk_id, content, priority: if is_query_time { P1 } else { P2 }
       })

  10. RETURN EnrichmentResult { chunks, symbols, edges }
      (for query-time: caller gets results immediately, doesn't wait for Writer Thread)
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C5 (Metadata Store) | Read previous index for diff computation | Sync (reader connection) |
| C8 (Writer Thread) | Enqueue write jobs | Async (channel) |
| C4 (Embedding Worker) | Enqueue embed jobs | Async (channel) |
| Tree-sitter runtime | Parse source files | Sync (CPU-bound) |
| File Enrichment Mutex | Per-file locking | Sync (lock/try_lock) |

### Performance Considerations

```
Single file enrichment:
  Parse:    15-40ms (depends on file size)
  Extract:  10-20ms (3 extractors in parallel → max of individual times)
  Diff:     5-15ms (line-level diff + symbol mapping)
  Package:  <1ms
  TOTAL:    ~35-75ms (within 80ms query-time budget for most files)

Worker pool:
  • 4 workers total during normal operation
  • 3 workers for cold index + 1 reserved for query-time triggers
  • Each worker processes one file at a time

Cold index:
  • Files batched in groups of 200
  • Priority ordering: recently modified → entry points → config → rest
  • Progressive: each batch committed → files immediately queryable
```

### Failure Handling

```
• Tree-sitter grammar not found for language:
  → Skip file, log warning
  → File excluded from index (but accessible via filesystem passthrough)

• Parse timeout (binary file misidentified):
  → Watchdog timer: 5s max parse time
  → Release mutex, mark file as 'unparseable' in config
  → File excluded from future indexing until config changed

• Extractor produces zero symbols:
  → Create single 'header' chunk with full file content
  → Normal indexing otherwise (chunk will be searchable via FTS5)

• Diff computation fails:
  → Skip diff record, proceed with enrichment
  → Chunks/symbols still indexed, just no diff tracking for this change
```

**Speed:** Parallel extraction (3 extractors) means total time ≈ max(individual) not sum. Mutex decoupled from Writer Thread (addendum A5) means enrichment results available in ~55ms.

---

## 9. C8: Writer Thread

### Purpose
Single serialized writer to SQLite. Eliminates write contention under WAL mode. All mutations to all stores flow through this thread. Priority lanes ensure query-time writes aren't blocked by bulk operations.

### Inputs

| Source | Input | Format |
|---|---|---|
| C7 (Enrichment) | `WriteJob::Enrichment` | Async priority channel |
| C4 (Embedding Worker) | `WriteJob::EmbeddingInsert` | Async priority channel |
| Session Store | `WriteJob::SessionUpdate` | Async priority channel |
| C7 (Cold Index) | `WriteJob::ColdIndexBatch` | Async priority channel |
| FTS5 rebuild | `WriteJob::FtsRebuild` | Async priority channel |

### Outputs

| Target | Output | Side-effect |
|---|---|---|
| SQLite DB | Committed transactions | Durable state |
| C3 (Graph) | `EdgeBatch` update signal | CSR delta buffer append |
| Write version | Atomic u64 increment | Consistency marker |

### Internal Responsibilities

```
PRIORITY QUEUE:

  P0: Query-time enrichment writes    (must drain first, <5ms each)
  P1: Session / working set updates   (small, frequent)
  P2: Watcher-triggered enrichment    (normal operation)
  P3: Cold index batch writes         (large, interruptible)

  Back-pressure: if total queue depth > 1000, P3 pauses until depth < 500

PROCESSING LOOP:

  writer_loop():
    connection = open_sqlite_write_connection()
    connection.execute("PRAGMA journal_mode = WAL")
    connection.execute("PRAGMA synchronous = NORMAL")
    connection.execute("PRAGMA cache_size = -8000")  # 8MB

    while !shutdown:
      job = priority_queue.dequeue()    # blocks until job available

      match job:
        WriteJob::Enrichment { file, chunks, symbols, edges, diff, stale_ids, .. } →
          begin_transaction()
          upsert_file(file)
          delete_old_chunks(file.id)
          insert_chunks(chunks)
          insert_symbols(symbols)
          upsert_edges(edges)
          insert_diff(diff)
          invalidate_embeddings(stale_ids)
          update_fts5(chunks)      # skip during cold index (A11)
          commit_transaction()

          // Post-commit side-effects:
          graph_module.update(EdgeBatch { additions, deletions })
          write_version.fetch_add(1)

        WriteJob::EmbeddingInsert { chunk_id, vector } →
          vector_store.insert(chunk_id, vector)
          update_file_embedding_status(chunk_id)
          write_version.fetch_add(1)

        WriteJob::SessionUpdate { access_events, decay_flush } →
          insert_access_log(access_events)
          update_working_set(decay_flush)

        WriteJob::ColdIndexBatch { jobs } →
          begin_transaction()
          for job in jobs:
            # same as Enrichment but batched
          commit_transaction()
          graph_module.update(batch_edges)
          write_version.fetch_add(1)

        WriteJob::FtsRebuild →
          execute("INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild')")

BURST COALESCING:

  When dequeuing:
    • If multiple P2 jobs for the same file_id are queued → keep only the latest
    • Branch switch: watcher emits debounced batch → single P2 job with all files
    • Reduces queue depth during rapid file changes

DISK SPACE CHECK:

  Before large transactions (ColdIndexBatch, FtsRebuild):
    available = check_disk_space(.shaktiman/)
    if available < 100MB:
      log_warn("Low disk space. Pausing writes.")
      pause until space available or user intervention
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| SQLite | Write connection (exclusive) | Sync (within Writer Thread) |
| C3 (Graph) | Post-commit CSR delta update | Sync (called after commit) |
| All other components | Receive write jobs via priority channel | Async (enqueue side) |

### Performance Considerations

```
Transaction timing:
  P0 single-file enrichment:  3-8ms
  P1 session update batch:    1-3ms
  P2 single-file enrichment:  5-15ms
  P3 cold index (200 files):  30-50ms (without FTS5 sync)
  Embedding insert:           1-2ms

Throughput:
  Sustained: ~100-200 writes/second
  P0 reserved capacity: always drained first, never starved

WAL checkpointing:
  Auto-checkpoint every 1000 pages (~4MB WAL)
  Checkpoint is transparent to readers
```

### Failure Handling

```
• SQLite write error (SQLITE_BUSY):
  → WAL mode should prevent this (single writer)
  → If occurs: retry once after 10ms, then log error and drop job

• Disk full during transaction:
  → SQLite rolls back automatically
  → Writer Thread pauses, logs error
  → Resumes when disk space available

• Writer Thread crash:
  → SQLite WAL ensures committed transactions survive
  → Un-committed transaction rolled back automatically
  → On restart: write_version reset, CSR rebuilt, embedding queue rebuilt from status flags
```

---

## 10. C9: Filesystem / Git Monitor

### Purpose
Watches the project directory for file changes in real-time. Filters noise (gitignored files, non-source files, unchanged files). Detects branch switches. Feeds change events to the Enrichment Pipeline.

### Inputs

| Source | Input | Format |
|---|---|---|
| OS filesystem | fsevents (macOS) / inotify (Linux) | OS-native events |
| Timer | Periodic full scan trigger (every 5 min) | Internal timer |
| Config | `.gitignore`, `.shaktimanignore` patterns | File reads |
| C5 (Metadata Store) | File hashes for staleness detection | SQL query |

### Outputs

| Target | Output | Format |
|---|---|---|
| C7 (Enrichment) | `FileChangeEvent` | Async channel |
| C1 (Interface) | Branch switch signal (for push mode) | Async channel |

### Internal Responsibilities

```
FILE WATCHER:

  1. Register recursive watch on project directory
  2. Filter incoming events:
     • Skip if matches .gitignore pattern
     • Skip if matches .shaktimanignore pattern
     • Skip if not a recognized source file extension
     • Skip if path is inside .shaktiman/ directory
  3. Debounce: coalesce events for same file within 200ms
  4. Emit FileChangeEvent { path, change_type, timestamp }

CHANGE DETECTOR:

  5. For each event, check if file actually changed:
     • Compare mtime with stored mtime
     • If mtime changed: compute content hash, compare with stored hash
     • If hash unchanged → skip (spurious event, e.g., touch without modify)
     • If hash changed → emit as genuine change
  6. Determine change_type:
     • File exists + not in index → 'add'
     • File exists + in index + hash differs → 'modify'
     • File doesn't exist + was in index → 'delete'
     • Path changed + content hash matches old file → 'rename'

BRANCH SWITCH DETECTION:

  7. Count file change events within a 2-second window
  8. If > 20 events in 2 seconds → classify as branch switch
  9. Emit BranchSwitchSignal to Interface (for push mode resource update)
  10. Batch all events into single FileChangeBatch

PERIODIC FULL SCAN:

  11. Every 5 minutes: walk project directory
  12. Compare all files against stored hashes
  13. Emit FileChangeEvent for any mismatches
  14. Catches: missed fsevents, external tool modifications, git operations
```

### Dependencies

| Component | Interaction | Sync/Async |
|---|---|---|
| C5 (Metadata Store) | File hash lookups for change detection | Sync (reader connection) |
| C7 (Enrichment) | Emit change events | Async (channel) |
| C1 (Interface) | Emit branch switch signal | Async (channel) |
| Config Manager | Ignore patterns | Sync (read on startup + file watch on config change) |

### Performance Considerations

```
Watcher overhead:
  • fsevents: ~1-2MB memory, negligible CPU
  • Debounce: 200ms window reduces event storms
  • Filter: .gitignore check is O(patterns × path_segments)

Periodic scan:
  • 100K lines (~3000 files): ~2s to walk + stat all
  • 1M lines (~10000 files): ~5s to walk + stat all
  • Only computes hashes for files with changed mtime
  • Runs in background, low priority

Branch switch:
  • Detection: O(1) counter check
  • Batch emission: reduces 500 individual events to 1 batch event
  • Watcher debounce already coalesces — branch switch adds semantic labeling
```

### Failure Handling

```
• fsevents/inotify unavailable:
  → Fall back to polling mode (scan every 30 seconds)
  → Log warning: "Filesystem events unavailable. Using polling."

• Watch descriptor limit exceeded (inotify):
  → Watch only top-level directories
  → Increase scan frequency to every 2 minutes
  → Log: "Too many directories. Consider adding exclusions to .shaktimanignore"

• File disappeared between event and read:
  → Treat as delete event, proceed normally
  → Race condition handled gracefully

• Permission denied on file:
  → Skip file, log warning
  → File excluded from index
```

**Speed:** Debouncing + batching ensures the enrichment pipeline isn't overwhelmed. Branch switch detection prevents 500 individual enrichment jobs — they go as one batch.

---

## 11. Communication Protocols

All communication is in-process (single process, multi-threaded). No network protocols between components. The only network protocol is MCP (stdio) for the agent interface and HTTP for Ollama.

### 11.1 Message Types

```
// ─── Events (C9 → C7) ───

FileChangeEvent {
  path: String,
  change_type: Add | Modify | Delete | Rename { from: String },
  timestamp: Timestamp,
  is_branch_switch: bool,
}

FileChangeBatch {
  events: Vec<FileChangeEvent>,
  is_branch_switch: bool,
}

// ─── Enrichment (C7 → C8) ───

WriteJob {
  priority: P0 | P1 | P2 | P3,
  payload: WritePayload,
}

WritePayload::Enrichment {
  file_record: FileRecord {
    path: String,
    content_hash: String,
    mtime: f64,
    size: u64,
    language: String,
  },
  chunks: Vec<ChunkRecord {
    symbol_name: Option<String>,
    kind: ChunkKind,
    start_line: u32,
    end_line: u32,
    content: String,
    token_count: u32,
    signature: Option<String>,
    parse_quality: ParseQuality,
    parent_chunk_id: Option<ChunkId>,
  }>,
  symbols: Vec<SymbolRecord {
    name: String,
    kind: SymbolKind,
    line: u32,
    signature: Option<String>,
    visibility: Visibility,
  }>,
  edges: Vec<EdgeRecord {
    src_symbol_name: String,
    dst_symbol_name: String,
    kind: EdgeKind,
  }>,
  diff: Option<DiffRecord {
    change_type: ChangeType,
    lines_added: u32,
    lines_removed: u32,
    hash_before: Option<String>,
    hash_after: String,
    affected_symbols: Vec<AffectedSymbol {
      symbol_name: String,
      change_type: SymbolChangeType,
    }>,
  }>,
  stale_chunk_ids: Vec<ChunkId>,
}

WritePayload::EmbeddingInsert {
  chunk_id: ChunkId,
  vector: Vec<f32>,
}

WritePayload::SessionUpdate {
  access_events: Vec<AccessEvent {
    session_id: String,
    chunk_id: ChunkId,
    operation: AccessOp,
    timestamp: Timestamp,
  }>,
  decay_flush: Option<Vec<(ChunkId, u32)>>,    // queries_since_last_hit updates
}

WritePayload::ColdIndexBatch {
  enrichments: Vec<WritePayload::Enrichment>,   // up to 200 per batch
}

WritePayload::FtsRebuild {}

// ─── Queries (C1 → C2) ───

QueryRequest {
  kind: QueryKind,
  budget: Option<u32>,        // token budget, default 8192
}

QueryKind::Search {
  query: String,
}

QueryKind::Context {
  files: Vec<String>,
  task: Option<String>,
}

QueryKind::Symbols {
  file: String,
}

QueryKind::Dependencies {
  symbol: String,
  direction: In | Out | Both,
}

QueryKind::Diff {
  scope: String,              // file path or directory prefix
  since: Option<Timestamp>,
}

QueryKind::Summary {
  scope: String,              // "project" | module path | file path
}

// ─── Responses (C2 → C1) ───

ContextPackage {
  chunks: Vec<ContextChunk {
    path: String,
    symbol_name: Option<String>,
    start_line: u32,
    end_line: u32,
    content: String,
    score: f32,
    last_modified: Option<Timestamp>,
    change_summary: Option<String>,
    parse_quality: ParseQuality,
  }>,
  budget_used: u32,
  budget_limit: u32,
  effective_limit: u32,
  strategy: Strategy,
  enrichment_level: EnrichmentLevel,
  tokenizer: String,
}

Strategy: FullHybrid | Mixed | StructuralKw | KeywordOnly | FilesystemPass
EnrichmentLevel: Full | Partial | Degraded

// ─── Embedding (C7 → C4) ───

EmbedQueueEntry {
  chunk_id: ChunkId,
  content: String,
  priority: EmbedPriority,    // P1 | P2 | P3 | P4
}
```

### 11.2 Channel Types

```
ASYNC CHANNELS (bounded, multi-producer single-consumer):

  file_change_tx/rx:       C9 → C7           capacity: 10,000 events
  write_job_tx/rx:         C7,C4,Sess → C8   capacity: 5,000 jobs (priority queue)
  embed_queue_tx/rx:       C7,C2 → C4        capacity: 100,000 entries (priority queue)
  branch_switch_tx/rx:     C9 → C1           capacity: 10 events
  resource_update_tx/rx:   C8,C9 → C1        capacity: 100 events

SYNC CALLS (direct function invocation):

  C1 → C2:  query(QueryRequest) → ContextPackage     (blocks caller)
  C2 → C3:  bfs(SymbolId, depth, limit) → BfsResult  (blocks caller)
  C2 → C4:  search(query_vec, top_k) → results       (blocks caller)
  C2 → C5:  fts_search(query, limit) → results       (blocks caller)
  C2 → C6:  change_scores(chunk_ids) → scores         (blocks caller)
  C2 → C7:  enrich_file(path, is_query_time) → result (blocks caller, ≤80ms)
  C7 → C5:  get_file_status(path) → IndexStatus       (blocks caller)
```

### 11.3 Example Message Flows

#### Flow 1: File Change → Enrichment → Write

```
C9 (Watcher)          C7 (Enrichment)           C8 (Writer Thread)
    │                      │                          │
    │ FileChangeEvent      │                          │
    │ { path: "src/auth/   │                          │
    │   rateLimit.ts",     │                          │
    │   change_type:       │                          │
    │     Modify,          │                          │
    │   timestamp: T1 }    │                          │
    │─────────────────────▶│                          │
    │                      │                          │
    │                      │ acquire mutex             │
    │                      │ parse + extract            │
    │                      │   (~55ms)                 │
    │                      │ diff engine               │
    │                      │ release mutex             │
    │                      │                          │
    │                      │ WriteJob { P2,            │
    │                      │   Enrichment {            │
    │                      │     file, chunks,         │
    │                      │     symbols, edges,       │
    │                      │     diff, stale_ids       │
    │                      │ }}                        │
    │                      │─────────────────────────▶│
    │                      │                          │
    │                      │                          │ begin txn
    │                      │                          │ upsert file
    │                      │                          │ insert chunks
    │                      │                          │ insert symbols
    │                      │                          │ upsert edges
    │                      │                          │ insert diff
    │                      │                          │ update FTS5
    │                      │                          │ commit txn
    │                      │                          │ update CSR delta
    │                      │                          │ increment version
    │                      │                          │
```

#### Flow 2: Agent Query → Response

```
Agent            C1 (MCP)         C2 (Core)              Stores
  │                │                  │                      │
  │ search(        │                  │                      │
  │  "rate limit") │                  │                      │
  │───────────────▶│                  │                      │
  │                │ QueryRequest     │                      │
  │                │ { Search {       │                      │
  │                │   query: "rate   │                      │
  │                │   limit" },      │                      │
  │                │   budget: 8192 } │                      │
  │                │─────────────────▶│                      │
  │                │                  │                      │
  │                │                  │ Router: strategy=L0   │
  │                │                  │ embed query (~5ms)    │
  │                │                  │                      │
  │                │                  │ PARALLEL fan-out:     │
  │                │                  │──▶ C4.search()        │
  │                │                  │──▶ C3.bfs()           │
  │                │                  │──▶ C5.fts_search()    │
  │                │                  │──▶ C6.change_scores() │
  │                │                  │──▶ Session.lookup()   │
  │                │                  │◀── all scores return  │
  │                │                  │                      │
  │                │                  │ Ranker: normalize     │
  │                │                  │   + weight + sort     │
  │                │                  │                      │
  │                │                  │ Assembler: dedup      │
  │                │                  │   + budget fit        │
  │                │                  │   + expand + meta     │
  │                │                  │                      │
  │                │ ContextPackage   │                      │
  │                │◀─────────────────│                      │
  │                │                  │                      │
  │ { chunks: [..],│                  │                      │
  │   budget_used: │                  │                      │
  │     5800,      │                  │                      │
  │   strategy:    │                  │                      │
  │     "hybrid_l0"│                  │                      │
  │ }              │                  │                      │
  │◀───────────────│                  │                      │
```

---

## 12. Partial Enrichment & Lazy Processing

Every component is designed to function with incomplete data. The system progressively improves — never blocks on missing enrichment.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                   ENRICHMENT STATE PROGRESSION                             │
│                                                                             │
│  State 0: EMPTY                                                            │
│    Available: nothing                                                       │
│    Query strategy: Level 3 (filesystem passthrough)                        │
│    Trigger: cold index starts                                               │
│                                                                             │
│  State 1: PARTIAL METADATA (during cold index)                             │
│    Available: some files indexed (chunks, symbols, edges) — progressively   │
│    Query strategy: Level 1-2 for indexed files, Level 3 for rest           │
│    FTS5: disabled (no keyword search yet)                                   │
│    Graph: CSR BUILDING (SQLite BFS fallback)                               │
│    Trigger: cold index completes                                            │
│                                                                             │
│  State 2: FULL METADATA (cold index complete, embeddings pending)          │
│    Available: all files indexed, FTS5 rebuilt, CSR READY                    │
│    Query strategy: Level 1 (structural + keyword)                          │
│    Embeddings: 0% → progressing                                             │
│    Trigger: embedding progress crosses 80%                                  │
│                                                                             │
│  State 3: HYBRID (embeddings >80%)                                         │
│    Available: full metadata + most embeddings + graph + FTS5                │
│    Query strategy: Level 0 (full hybrid) or Level 0.5 (mixed)              │
│    Trigger: embeddings reach 100%                                           │
│                                                                             │
│  State 4: FULLY ENRICHED                                                   │
│    Available: everything                                                    │
│    Query strategy: Level 0 (full hybrid)                                   │
│    Ongoing: incremental updates maintain this state                        │
│                                                                             │
│  INVARIANT: The system is queryable at EVERY state. Quality improves.      │
│  INVARIANT: No query ever blocks waiting for enrichment to complete.       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Per-component lazy/deferred behavior:**

| Component | What's Deferred | When It's Needed | Trigger |
|---|---|---|---|
| C4 (Vector) | Embedding generation | Only when query uses semantic search | Background worker; query-time P1 bump |
| C3 (Graph) | CSR construction | Only when query uses structural scoring | Startup build; periodic compaction |
| C5 (Metadata) | FTS5 index | Only when query uses keyword search | Rebuilt once after cold index |
| C6 (Diff) | Historical diffs | Only when query uses change scoring or `diff()` tool | Computed during enrichment (post-parse) |
| C7 (Enrichment) | Full file index | Only when file is queried | Query-time trigger (single-file, 80ms) |
| C9 (Monitor) | Missed events | Only if fsevents drops events | 5-minute periodic full scan |

---

## 13. Trust, Speed & Token Efficiency

### Where Developer Trust Is Preserved

```
TRANSPARENCY:
  • Every ContextPackage includes strategy and enrichment_level
    → Agent knows if results are "full hybrid" or "degraded"
  • Parse quality flag on every chunk
    → Agent knows if chunk came from a partial (error-containing) AST
  • Change summaries on every chunk
    → Agent sees what recently changed without re-reading
  • Budget accounting in every response
    → Agent knows exactly how much context it received

CORRECTNESS:
  • WAL mode + serialized Writer Thread → no torn reads
  • Version counter → no stale graph data served silently
  • File mutex → no duplicate enrichment producing inconsistent results
  • Content hash verification → index always matches actual file content
  • Periodic full scan → self-healing for missed events

GRACEFUL DEGRADATION:
  • System never returns an error when data is partial
  • It returns best-available results with honest metadata about quality
  • Every fallback level is a correct answer — just less precise
```

### Where Speed Is Preserved

```
HOT PATH OPTIMIZATIONS:
  • Parallel fan-out to 5 stores → latency = max, not sum
  • LRU cache for query embeddings → <0.1ms on cache hit
  • Pre-computed token counts → zero runtime tokenization
  • CSR graph → cache-friendly sequential BFS (<15ms at 1M lines)
  • In-memory session LRU → <1ms access
  • Read connection pool → no reader contention (WAL mode)

BACKGROUND WORK:
  • Embedding: async, never blocks queries
  • CSR compaction: background thread, atomic pointer swap
  • FTS5 rebuild: async after cold index
  • Session decay flush: batched every 30s
  • Resource re-assembly (push mode): debounced 500ms

BOUNDED BUDGETS:
  • Query-time enrichment: 80ms max
  • File mutex wait: 50ms timeout
  • Embedding batch: 30s timeout
  • BFS depth: capped at 3 (CSR) or 2 (SQLite fallback)
  • Expansion: max 5 neighbors per chunk, 30% of budget
```

### Where Token Efficiency Is Preserved

```
RETRIEVAL PRECISION:
  • Chunk-level (not file-level) retrieval → ~300 tokens vs ~2000
  • 5-signal ranking → most relevant chunks first
  • Budget hard-cap with 95% safety margin → never over-deliver
  • Line-range dedup → no redundant content

METADATA COMPRESSION:
  • ~12 tokens per chunk metadata (4% overhead on ~300 token chunk)
  • Change summaries: ~10-20 tokens (vs ~2000 to re-read file)
  • Strategy/enrichment_level: 2 tokens (agent can trust quality without investigating)

CALL REDUCTION:
  • Single search() → replaces 8-15 grep/read cycles
  • dependencies() → replaces manual usage grep
  • diff() → replaces git log + manual reads
  • Push mode resources → agent gets context without even asking

BUDGET ALLOCATION:
  • Primary chunks: 70% of budget (highest-relevance full content)
  • Structural expansion: 30% of budget (callers/callees/type defs)
  • If expansion yields nothing useful, budget returns to primary selection
```

---

## Status

**Component Design** — Complete. Awaiting critique validation and confirmation before Step 5 (implementation planning / code).
