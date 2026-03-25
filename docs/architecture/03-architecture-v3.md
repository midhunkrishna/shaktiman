# Shaktiman: Architecture v3 (Final)

> Local-first, high-performance code context system for coding agents.
> Supersedes v2. Incorporates all critique findings (MF-1→8, SF-1→10).
> Designed for codebases up to 1M+ lines.

---

## 1. Design Decisions (Resolved)

> **Note (FD-3):** References to "sqlite-vec" throughout this document are superseded. The default vector store is now **brute-force in-process** (`[]float32` + cosine similarity). See Final Implementation Decisions (FD-3) at the end of this document.

| Question | Decision | Rationale |
|---|---|---|
| FR-14 push mode | **Hard requirement** | Concrete design via MCP resources + notifications |
| Max codebase size | **1M+ lines** | CSR graph format, progressive cold index, vector store interface |
| Vector store fallback | **Pluggable interface** | sqlite-vec primary; interface allows usearch/lancedb swap without re-architecture |
| Ranking weights | **Ship defaults only** | 5-signal defaults; `RankingStrategy` interface for future pluggability |
| Query-time enrichment | **Single-file, budget-capped** | 80ms max sync budget, no recursive follow |
| Tokenizer | **cl100k_base with 95% safety margin** | Best Claude approximation; configurable |

---

## 2. System Overview

```
╔═══════════════════════════════════════════════════════════════════════════════╗
║                               CONSUMERS                                      ║
║                                                                               ║
║   ┌───────────────┐        ┌───────────────┐        ┌───────────────┐         ║
║   │  Claude Code  │        │   Other MCP   │        │   Developer   │         ║
║   │    (Agent)    │        │    Clients    │        │     (CLI)     │         ║
║   └───────┬───────┘        └───────┬───────┘        └───────┬───────┘         ║
║           │ MCP                    │ MCP                    │ direct           ║
╚═══════════┼════════════════════════┼════════════════════════┼═════════════════╝
            │                        │                        │
            ▼                        ▼                        ▼
╔═══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 1 — INTERFACE                                                         ║
║                                                                               ║
║   ┌──────────────────────────────────────────┐   ┌─────────────────────┐      ║
║   │            MCP Server                    │   │   CLI Interface    │      ║
║   │                                          │   │                     │      ║
║   │  TOOLS:                                  │   │  shaktiman init     │      ║
║   │   search(query, budget?)                 │   │  shaktiman query    │      ║
║   │   context(files, task, budget?)          │   │  shaktiman status   │      ║
║   │   symbols(file)                          │   │  shaktiman diff     │      ║
║   │   dependencies(symbol)                   │   │  shaktiman reindex  │      ║
║   │   diff(scope, since?)                    │   │  shaktiman config   │      ║
║   │   summary(scope)                         │   │  shaktiman inspect  │      ║
║   │                                          │   │                     │      ║
║   │  RESOURCES (FR-14 push mode):            │   │                     │      ║
║   │   shaktiman://context/active             │   │                     │      ║
║   │   shaktiman://workspace/summary          │   │                     │      ║
║   │                                          │   │                     │      ║
║   │  PROMPTS:                                │   │                     │      ║
║   │   task-start(task_description)           │   │                     │      ║
║   │                                          │   │                     │      ║
║   │  NOTIFICATIONS:                          │   │                     │      ║
║   │   context/changed (on working set shift) │   │                     │      ║
║   │                                          │   │                     │      ║
║   └──────────────────┬───────────────────────┘   └──────────┬──────────┘      ║
║                      │                                      │                 ║
╚══════════════════════┼══════════════════════════════════════┼═════════════════╝
                       │                                      │
                       ▼                                      ▼
╔═══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 2 — QUERY ENGINE                                                      ║
║                                                                               ║
║   ┌─────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ║
║   │   Query     │──▶│  Retrieval   │──▶│   Hybrid     │──▶│   Context    │   ║
║   │   Router    │   │   Engine     │   │   Ranker     │   │  Assembler   │   ║
║   │             │   │              │   │              │   │              │   ║
║   │ • strategy  │   │ • parallel   │   │ • normalize  │   │ • dedup      │   ║
║   │   select    │   │   fan-out    │   │ • weight     │   │ • budget fit │   ║
║   │ • fallback  │   │   to 5      │   │ • merge      │   │ • expand     │   ║
║   │   chain     │   │   stores    │   │              │   │   (capped)   │   ║
║   │ • enrich    │   │              │   │              │   │ • meta       │   ║
║   │   trigger   │   │              │   │              │   │   attach     │   ║
║   │   (budgeted)│   │              │   │              │   │              │   ║
║   └─────────────┘   └──────────────┘   └──────────────┘   └──────────────┘   ║
║                            │                                                  ║
║              ┌─────────────┼──────────────┬──────────────┐                    ║
║              ▼             ▼              ▼              ▼                    ║
║        ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐               ║
║        │ Semantic │  │Structural│  │  Change  │  │ Keyword  │               ║
║        │  Score   │  │  Score   │  │  Score   │  │  Score   │               ║
║        │ (vector) │  │ (graph)  │  │ (diff +  │  │ (FTS5)   │               ║
║        │          │  │          │  │  session)│  │          │               ║
║        └──────────┘  └──────────┘  └──────────┘  └──────────┘               ║
║                                                                               ║
╚═══════════════════════════════════════════════════════════════════════════════╝
                       │
                       ▼
╔═══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 3 — INDEX STORES                                                      ║
║                                                                               ║
║   ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐        ║
║   │   Metadata   │ │    Graph     │ │   Vector     │ │    Diff      │        ║
║   │    Store     │ │    Store     │ │    Store     │ │    Store     │        ║
║   │              │ │              │ │              │ │              │        ║
║   │ files        │ │ CSR format   │ │ sqlite-vec   │ │ diff_log     │        ║
║   │ chunks       │ │ in-memory    │ │ (pluggable   │ │ diff_symbols │        ║
║   │ symbols      │ │ + SQLite     │ │  interface:  │ │ change       │        ║
║   │ token counts │ │ persist      │ │  VectorStore)│ │ summaries    │        ║
║   │ FTS5 index   │ │              │ │              │ │              │        ║
║   │ parse quality│ │ ~42MB at 1M  │ │              │ │              │        ║
║   │              │ │ lines        │ │              │ │              │        ║
║   └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────┬───────┘        ║
║          │                │                │               │                 ║
║   ┌──────┴────────────────┴────────────────┴───────────────┴──────┐          ║
║   │                      Session Store                            │          ║
║   │        access_log (TTL 30d, max 100K rows)                   │          ║
║   │        working_set · exploration decay                        │          ║
║   └──────────────────────────────────────────────────────────────┘          ║
║                                                                               ║
╚═══════════════════════════════════════════════════════════════════════════════╝
                       │
                       ▼
╔═══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 4 — ENRICHMENT PIPELINE (background, event-driven)                    ║
║                                                                               ║
║   ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌─────────────────────────┐   ║
║   │  File    │──▶│  Change  │──▶│ Tree-    │──▶│  Parallel Extractors   │   ║
║   │  Watcher │   │ Detector │   │ sitter   │   │  (symbol+chunk+dep)    │   ║
║   └──────────┘   └──────────┘   │ Parser   │   └────────────┬────────────┘   ║
║                                 └──────────┘                │                ║
║                                                  ┌──────────┴──────────┐     ║
║                                                  ▼                     ▼     ║
║                                           ┌────────────┐    ┌──────────────┐ ║
║                                           │ Diff Engine│    │   Writer     │ ║
║                                           │ (post-     │    │   Thread     │ ║
║                                           │  parse)    │    │  (serialized)│ ║
║                                           └─────┬──────┘    └──────┬───────┘ ║
║                                                 │                  │         ║
║                                                 └────────┬─────────┘         ║
║                                                          ▼                   ║
║                                           ┌──────────────────────────┐       ║
║                                           │    Embedding Worker      │       ║
║                                           │  (async, circuit-broken) │       ║
║                                           └──────────────────────────┘       ║
║                                                                               ║
╚═══════════════════════════════════════════════════════════════════════════════╝
                       │
                       ▼
╔═══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 5 — STORAGE                                                           ║
║                                                                               ║
║   ┌─────────────────────────────────────────────────────────────────────┐     ║
║   │                SQLite Database (WAL mode)                           │     ║
║   │                .shaktiman/index.db                                  │     ║
║   │                                                                     │     ║
║   │  schema_version │ files │ chunks │ symbols │ edges                  │     ║
║   │  embeddings (sqlite-vec) │ diff_log │ diff_symbols                 │     ║
║   │  access_log │ working_set │ config │ chunks_fts (FTS5)            │     ║
║   │                                                                     │     ║
║   │  Access pattern:                                                    │     ║
║   │    • Reads: N concurrent connections (WAL allows)                   │     ║
║   │    • Writes: single Writer Thread (serialized queue)                │     ║
║   │                                                                     │     ║
║   └─────────────────────────────────────────────────────────────────────┘     ║
║                                                                               ║
╚═══════════════════════════════════════════════════════════════════════════════╝
```

---

## 3. Component Specifications

### 3.1 Interface Layer

#### MCP Server

| Attribute      | Detail |
|---|---|
| **Purpose**    | Primary interface for agents. Exposes tools, resources, prompts, notifications. |
| **Inputs**     | MCP protocol messages (tool calls, resource reads, prompt requests). |
| **Outputs**    | Token-budgeted context packages, resource content, notifications. |
| **Dependencies** | Query Engine, Session Store. |

**MCP Tools:**

| Tool | Purpose | Replaces |
|---|---|---|
| `search(query, budget?)` | Hybrid 5-signal search | 5-15 grep/glob/read cycles |
| `context(files, task, budget?)` | Full task context from active files | Manual codebase exploration |
| `symbols(file)` | Symbol list with signatures | Reading entire files |
| `dependencies(symbol)` | Callers, callees, importers | Manual grep for usages |
| `diff(scope, since?)` | What changed, which symbols affected | git log + manual reads |
| `summary(scope)` | Project/module/file orientation | Reading dozens of files |

**MCP Resources (FR-14 Push Mode):**

| Resource URI | Content | Update Trigger |
|---|---|---|
| `shaktiman://context/active` | Pre-assembled context for the current working set — top chunks the agent is likely to need based on session history + recent diffs. Budget: 4096 tokens. | Updated on: file save, working set shift (>3 new chunks accessed), every 60s idle. |
| `shaktiman://workspace/summary` | Project structure summary: modules, key entry points, recent change hotspots. Budget: 2048 tokens. | Updated on: cold index complete, significant structural change. |

**MCP Prompts:**

| Prompt | Parameters | Behavior |
|---|---|---|
| `task-start` | `task_description: string` | Assembles context package relevant to the task description. Returns ranked chunks + workspace summary + recent diffs in relevant areas. Budget: 8192 tokens. |

**MCP Notifications:**

| Notification | When | Payload |
|---|---|---|
| `context/changed` | Working set shifts significantly (>30% new chunks vs last notification). | `{ changed_files: [...], new_symbols: [...], hint: "auth module updated" }` |

**Push Mode Flow:**

```
Agent starts new task
    │
    ├──▶ Reads resource: shaktiman://context/active
    │    └── Gets: pre-assembled working set context (4K tokens)
    │
    ├──▶ Calls prompt: task-start("Fix rate limiting bug")
    │    └── Gets: task-relevant context + workspace summary (8K tokens)
    │
    │    ... agent works ...
    │
    ├──▶ Developer saves file
    │    └── Shaktiman re-indexes, updates active resource
    │
    ├──▶ Working set shifts significantly
    │    └── Shaktiman sends notification: context/changed
    │        └── Agent reads updated resource if interested
    │
    └── Agent never needed to search — context arrived proactively
```

#### CLI Interface

| Attribute      | Detail |
|---|---|
| **Purpose**    | Developer-facing: debugging, index management, status checks. |
| **Inputs**     | Shell commands. |
| **Outputs**    | Formatted text to stdout. |

**Commands:** `init`, `query`, `status`, `diff`, `reindex`, `config`, `inspect`, `mcp-config`.

---

### 3.2 Query Engine

#### Query Router

| Attribute      | Detail |
|---|---|
| **Purpose**    | Select retrieval strategy. Trigger bounded on-demand enrichment. |
| **Inputs**     | Parsed query + index readiness flags. |
| **Outputs**    | Strategy to Retrieval Engine. Side-effect: enrichment triggers (budget-capped). |
| **Dependencies** | Index Stores (status), Enrichment Pipeline (trigger), File Enrichment Mutex. |

**Fallback chain:**

```
Level 0:   FULL HYBRID
           All 5 signals. Embeddings >80% ready.

Level 0.5: MIXED
           Hybrid for embedded chunks + structural+kw for unembedded.
           TRIGGER: priority-embed hit chunks.

Level 1:   STRUCTURAL + KEYWORD
           Symbol index + FTS5 + graph + diff scores. No semantic.
           TRIGGER: priority-embed hit chunks.

Level 2:   KEYWORD ONLY
           FTS5 on chunk content. No graph, no semantic.

Level 3:   FILESYSTEM PASSTHROUGH
           Raw file reads, chunked by lines. Budget-fitted.
           For queries with no file hints: return project directory tree as orientation.
           TRIGGER: immediate index for touched files.
```

**Query-time enrichment rules (MF-4, MF-5):**

```
CONSTRAINTS:
  • Single-file only — never recursively follow imports
  • Total sync enrichment budget per query: 80ms max
  • Per-file enrichment mutex — if file is already being enriched,
    wait on existing enrichment (with 50ms timeout) instead of duplicating
  • If budget exhausted: serve best-available, queue remainder as Priority 1 async

TRIGGERS:
  1. MISSING EMBEDDINGS: bump chunks to Priority 1 in embedding queue (async, no block)
  2. UNINDEXED FILE: sync index if file < 2000 lines AND within 80ms budget
     else async index + return raw file content
  3. STALE INDEX: sync re-index if within 80ms budget
  4. MISSING DEPS: async dep extraction, serve partial graph now
```

#### Retrieval Engine

| Attribute      | Detail |
|---|---|
| **Purpose**    | Fan out to stores in parallel, collect candidate chunks with sub-scores. |
| **Inputs**     | Query + strategy + active context. |
| **Outputs**    | Candidate chunks with per-signal raw scores. |
| **Dependencies** | All Index Stores (parallel read connections via WAL). |

#### Hybrid Ranker (MF-2 addressed)

| Attribute      | Detail |
|---|---|
| **Purpose**    | Normalize sub-scores, apply weights, produce final ranking. |
| **Inputs**     | Candidates with raw sub-scores. |
| **Outputs**    | Ranked candidate list with final scores in [0, 1]. |
| **Dependencies** | None (pure computation). |

**Normalization (MF-2):**

```
Each raw score is normalized to [0, 1] before weighting:

  semantic:    norm = (cosine + 1) / 2
  structural:  norm = 1 / (1 + bfs_distance)         # depth 0 = 1.0, depth 3 = 0.25
  change:      norm = recency_decay(ts) × min(magnitude / M, 1.0)
                      where recency_decay(ts) = exp(-λ × hours_since_change), λ = 0.05
                      where M = 50 (line change threshold for max score)
  session:     norm = min(access_count / A, 1.0) × recency_decay(last_access)
                      where A = 5 (access count for max score)
                      EXPLORATION DECAY (SF-9): score *= 0.9^(queries_since_last_hit)
  keyword:     norm = min(bm25_score / percentile_95, 1.0)
                      where percentile_95 is computed per-query from result set
```

**Scoring formula:**

```
score(chunk) = 0.40 × semantic_norm
             + 0.20 × structural_norm
             + 0.15 × change_norm
             + 0.15 × session_norm
             + 0.10 × keyword_norm

WEIGHT REDISTRIBUTION (when signals unavailable):
  Embeddings down:  kw += 0.25, struct += 0.10, change += 0.05     → [0.00, 0.30, 0.20, 0.15, 0.35]
  Graph down:       sem += 0.10, kw += 0.10                        → [0.50, 0.00, 0.15, 0.15, 0.20]
  No diff history:  session += 0.10, kw += 0.05                    → [0.40, 0.20, 0.00, 0.25, 0.15]
  Multiple down:    redistribute proportionally among remaining signals
```

**Parse quality penalty (SF-3):**

```
If chunk.parse_quality == 'partial':
  final_score *= 0.5    # demote chunks from error-containing ASTs
```

#### Context Assembler (SF-4, SF-6 addressed)

| Attribute      | Detail |
|---|---|
| **Purpose**    | Pack ranked chunks into a token-budgeted context package. |
| **Inputs**     | Ranked chunks + budget (default 8192, effective = budget × 0.95 safety margin). |
| **Outputs**    | Context package: chunks + metadata + budget accounting. |
| **Dependencies** | Pre-computed token counts (cl100k_base tokenizer). |

**Algorithm:**

```
INPUT:  ranked_chunks[], budget, options
OUTPUT: context_package

effective_budget = budget × 0.95                    # MF-6: safety margin
expansion_budget = effective_budget × 0.30          # SF-4: max 30% for expansions
primary_budget   = effective_budget - expansion_budget

1. PRIMARY SELECTION:
   FOR chunk in ranked_chunks (desc score):
     IF chunk overlaps >50% line-range with selected chunk → SKIP    # SF-6: line-range overlap
     IF chunk.token_count > remaining primary_budget → SKIP
     ADD chunk, subtract tokens from primary_budget

2. EXPANSION (capped):
   FOR each selected chunk:
     neighbors = graph.bfs(chunk.symbol, depth=2, max=5)             # SF-4: max 5 per chunk
     FOR neighbor in neighbors (by score):
       IF neighbor.token_count > remaining expansion_budget → SKIP
       IF neighbor already selected → SKIP
       ADD neighbor, subtract from expansion_budget

3. METADATA ATTACHMENT:
   FOR each chunk in package:
     attach { path, symbol, lines, score, last_modified, change_summary, parse_quality }
     # ~12 tokens overhead per chunk (4% of ~300 token chunk = within NFR-5 <5%)

4. RETURN {
     chunks: [...],
     budget_used: <actual>,
     budget_limit: <stated>,
     effective_limit: <with safety margin>,
     strategy: "hybrid_l0" | "mixed" | "structural" | ...,
     enrichment_level: "full" | "partial" | "degraded",
     tokenizer: "cl100k_base"
   }
```

---

### 3.3 Index Stores

#### Metadata Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Central catalog of files, chunks, symbols. FTS5 keyword search. |
| **Inputs**     | Parsed data from Enrichment Pipeline (via Writer Thread). |
| **Outputs**    | File/chunk/symbol records, FTS5 search results. |
| **Dependencies** | SQLite (WAL mode, read via connection pool, write via Writer Thread). |

**Schema:**

```sql
-- Version tracking (SF-10)
schema_version (version INTEGER, applied_at TEXT)

-- File catalog
files (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL,
  content_hash TEXT NOT NULL,
  mtime REAL NOT NULL,
  size INTEGER,
  language TEXT,
  indexed_at TEXT,
  embedding_status TEXT DEFAULT 'pending'  -- 'pending' | 'partial' | 'complete'
)

-- Semantic chunks
chunks (
  id INTEGER PRIMARY KEY,
  file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
  parent_chunk_id INTEGER REFERENCES chunks(id),    -- for nested (method → class)
  symbol_name TEXT,
  kind TEXT,                -- 'function' | 'class' | 'method' | 'type' | 'interface' | 'header'
  start_line INTEGER,
  end_line INTEGER,
  content TEXT NOT NULL,
  token_count INTEGER NOT NULL,     -- pre-computed via cl100k_base
  signature TEXT,
  parse_quality TEXT DEFAULT 'full' -- 'full' | 'partial' (SF-3)
)

-- Symbol definitions
symbols (
  id INTEGER PRIMARY KEY,
  chunk_id INTEGER REFERENCES chunks(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  file_id INTEGER REFERENCES files(id),
  line INTEGER,
  signature TEXT,
  visibility TEXT           -- 'public' | 'private' | 'internal'
)

-- FTS5 for keyword search
CREATE VIRTUAL TABLE chunks_fts USING fts5(
  content, symbol_name, content=chunks, content_rowid=id
);

-- Indexes
CREATE INDEX idx_chunks_file ON chunks(file_id);
CREATE INDEX idx_symbols_name ON symbols(name);
CREATE INDEX idx_symbols_file ON symbols(file_id);
```

#### Graph Store (1M+ line support)

| Attribute      | Detail |
|---|---|
| **Purpose**    | Directed dependency graph. Fast BFS for structural scoring. |
| **Inputs**     | Edge records from Dep Extractor (via Writer Thread). |
| **Outputs**    | BFS distances, neighbor lists, subgraph extraction. |
| **Dependencies** | SQLite (persist) + in-memory CSR (query-time). |

**SQLite schema:**

```sql
edges (
  id INTEGER PRIMARY KEY,
  src_symbol_id INTEGER REFERENCES symbols(id) ON DELETE CASCADE,
  dst_symbol_id INTEGER REFERENCES symbols(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,    -- 'imports' | 'calls' | 'type_ref' | 'extends' | 'implements' | 'exports'
  file_id INTEGER REFERENCES files(id)
)
CREATE INDEX idx_edges_src ON edges(src_symbol_id);
CREATE INDEX idx_edges_dst ON edges(dst_symbol_id);
```

**In-memory representation: CSR (Compressed Sparse Row):**

```
Why CSR over hash-map adjacency list:
  • Hash map: ~40 bytes/edge (key + value + overhead) → 5M edges = 200MB ✗
  • CSR:      ~8 bytes/edge (offset array + edge array) → 5M edges = 42MB ✓

Structure:
  offsets[]:  one entry per node, pointing into edges[]     (N+1 × 4 bytes)
  edges[]:   packed destination node IDs + edge kind        (E × 8 bytes)

Memory at scale:
  100K lines:  ~50K edges  →  offsets ~200KB + edges ~400KB  = ~600KB
  500K lines:  ~500K edges →  offsets ~500KB + edges ~4MB    = ~4.5MB
  1M lines:    ~2M edges   →  offsets ~1MB   + edges ~16MB   = ~17MB
  2M lines:    ~5M edges   →  offsets ~2MB   + edges ~40MB   = ~42MB

All within the 100MB memory budget (NFR-7).

Build time from SQLite:
  100K lines: ~5ms
  1M lines:   ~50ms
  2M lines:   ~120ms

BFS traversal (depth ≤ 3):
  CSR BFS is cache-friendly sequential memory access.
  Even at 5M edges: <15ms for depth-3 BFS from any node.
```

**Crash recovery:**

```
On crash: in-memory CSR is lost.
On restart: rebuild from SQLite edges table (50-120ms for 1M+ lines).
During rebuild: queries use SQLite-only graph traversal (slower but correct).
Write-through: all edge mutations go through Writer Thread → SQLite first → CSR update.
```

#### Vector Store (pluggable interface)

| Attribute      | Detail |
|---|---|
| **Purpose**    | ANN search over chunk embeddings. |
| **Inputs**     | Vectors from Embedding Worker (via Writer Thread). |
| **Outputs**    | Top-K chunk IDs by cosine similarity. |
| **Dependencies** | sqlite-vec extension (primary). Pluggable via `VectorStore` interface. |

**Interface (NFR-12, NFR-13):**

```
trait VectorStore {
  fn insert(chunk_id: u64, vector: &[f32]) -> Result<()>
  fn delete(chunk_id: u64) -> Result<()>
  fn search(query_vec: &[f32], top_k: usize) -> Result<Vec<(u64, f32)>>
  fn count() -> usize
  fn model_id() -> String        // MF-1: track which model produced these vectors
  fn invalidate_all() -> Result<()>   // MF-1: clear on model change
}
```

**Primary implementation: SqliteVecStore**

```
Uses sqlite-vec extension within the same .shaktiman/index.db.
Transactional consistency with other stores.

Scaling characteristics:
  10K chunks:   search <20ms ✓
  50K chunks:   search <50ms ✓
  100K chunks:  search <80ms ✓   (approaching limit)
  200K+ chunks: search >100ms — consider alternative implementation

If sqlite-vec exceeds 80ms p95 at scale, swap implementation to:
  • usearch: embedded C++ HNSW, ~10ms at 500K vectors, MIT license
  • lancedb: embedded columnar vector DB, good for large-scale
  No re-architecture needed — just swap the VectorStore implementation.
```

**Embedding model tracking (MF-1):**

```sql
-- In config table
config (key TEXT PRIMARY KEY, value TEXT)
-- key='embedding_model_id', value='nomic-embed-text-v1.5'
-- key='embedding_dimensions', value='768'

-- In embeddings metadata
embedding_status on files table tracks per-file embedding state.
```

```
On model change:
  1. Detect: config.embedding_model_id != current model
  2. Invalidate: vectorStore.invalidate_all()
  3. Update: config.embedding_model_id = new model
  4. Re-queue: all chunks → embedding queue at Priority 4
  5. During re-embed: Query Router drops to Level 1 (structural + keyword)
```

#### Diff Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Track what changed, when, which symbols affected. Feeds change scoring + `diff()` tool. |
| **Inputs**     | Diff records from Diff Engine (post-parse, via Writer Thread). |
| **Outputs**    | Change records by file/symbol/time. Change scores for ranking. |
| **Dependencies** | SQLite. Metadata Store (cross-references). |

**Schema:**

```sql
diff_log (
  id INTEGER PRIMARY KEY,
  file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
  timestamp TEXT NOT NULL,
  change_type TEXT NOT NULL,    -- 'add' | 'modify' | 'delete' | 'rename'
  lines_added INTEGER DEFAULT 0,
  lines_removed INTEGER DEFAULT 0,
  hash_before TEXT,
  hash_after TEXT
)
CREATE INDEX idx_difflog_file_ts ON diff_log(file_id, timestamp);

diff_symbols (
  id INTEGER PRIMARY KEY,
  diff_id INTEGER REFERENCES diff_log(id) ON DELETE CASCADE,
  symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
  symbol_name TEXT NOT NULL,       -- preserved even if symbol deleted
  change_type TEXT NOT NULL        -- 'added' | 'modified' | 'removed' | 'signature_changed'
)
CREATE INDEX idx_diffsym_symbol ON diff_symbols(symbol_id);
```

**Diff Engine ordering (SF-1):** Runs **after** tree-sitter + Symbol Extractor, so it can map hunks to both old and new symbol IDs. Old symbols resolved from previous index; new symbols from current parse.

#### Session Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Track agent access patterns, infer working set. |
| **Inputs**     | Access events from MCP/CLI tool calls. |
| **Outputs**    | Session scores, working set membership. |
| **Dependencies** | In-memory LRU (current session) + SQLite (cross-session, with eviction). |

**Schema:**

```sql
access_log (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  chunk_id INTEGER REFERENCES chunks(id) ON DELETE CASCADE,
  operation TEXT NOT NULL     -- 'search_hit' | 'context_include' | 'direct_read'
)
CREATE INDEX idx_access_session ON access_log(session_id, timestamp);

working_set (
  session_id TEXT NOT NULL,
  chunk_id INTEGER NOT NULL,
  access_count INTEGER DEFAULT 1,
  last_accessed TEXT NOT NULL,
  queries_since_last_hit INTEGER DEFAULT 0,   -- SF-9: exploration decay
  PRIMARY KEY (session_id, chunk_id)
)
```

**Eviction (SF-5):**

```
• TTL: 30 days — rows older than 30 days deleted on startup and every 6 hours
• Max rows: 100K in access_log — oldest rows purged when exceeded
• Pruning runs during idle periods (no active queries for 30s)
```

**Exploration decay (SF-9):**

```
After each query:
  FOR each chunk in working_set WHERE chunk NOT in current result set:
    queries_since_last_hit += 1
    effective_session_score *= 0.9 ^ queries_since_last_hit

Effect: a chunk accessed 10 queries ago with no re-hits has:
  session_score *= 0.9^10 = 0.35 — significant discount.
  This prevents filter bubbles while preserving genuine working set affinity.
```

---

### 3.4 Enrichment Pipeline

**Process model (MF-3, MF-5):**

```
┌─────────────────────────────────────────────────────────────┐
│                    THREAD MODEL                              │
│                                                              │
│  Main Thread       → MCP server + CLI                       │
│  Writer Thread     → single serialized writer to SQLite      │
│  Watcher Thread    → fsevents / inotify                      │
│  Enrichment Pool   → 2-4 worker threads for parse/extract   │
│  Embedding Thread  → 1 thread, CPU-throttled, circuit-broken │
│                                                              │
│  SQLite connections:                                         │
│    • Writer Thread: 1 write connection (WAL mode)            │
│    • Query path: connection pool of N read connections       │
│    • All writes serialized through Writer Thread's queue     │
│                                                              │
│  Mutex:                                                      │
│    • Per-file enrichment mutex (in-memory ConcurrentHashMap) │
│    • Prevents watcher + query-time trigger from enriching    │
│      same file simultaneously                                │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### Enrichment Flow (complete)

```
FILE CHANGE DETECTED
         │
         ▼
  ┌──────────────┐
  │   Change     │   Filter: .gitignore, .shaktimanignore, mtime + hash check
  │   Detector   │
  └──────┬───────┘
         │
         ▼
  ┌──────────────┐
  │  ACQUIRE     │   Per-file mutex. If already held → skip (watcher)
  │  FILE MUTEX  │                                    or wait with timeout (query-time)
  └──────┬───────┘
         │
         ▼
  ┌──────────────┐
  │  Tree-sitter │   Parse source → AST
  │    Parser    │   Detect error nodes → set parse_quality flag (SF-3)
  └──────┬───────┘
         │
    ┌────┴───────────┬────────────────┐     (PARALLEL FAN-OUT)
    ▼                ▼                ▼
┌──────────┐  ┌──────────┐  ┌──────────────┐
│  Symbol  │  │  Chunk   │  │     Dep      │
│ Extractor│  │ Splitter │  │  Extractor   │
└────┬─────┘  └────┬─────┘  └──────┬───────┘
     │              │               │
     ▼              ▼               ▼
┌────────────────────────────────────────────┐
│  Results collected (symbols, chunks, edges) │
└────────────────────┬───────────────────────┘
                     │
                     ▼
              ┌──────────────┐
              │  Diff Engine │   Runs AFTER extraction (SF-1)
              │  (post-parse)│   Maps hunks to old + new symbol IDs
              └──────┬───────┘
                     │
                     ▼
              ┌──────────────┐
              │ Writer Thread│   Single SQLite transaction:
              │   (queue)    │   • UPSERT files, chunks, symbols
              │              │   • UPSERT/DELETE edges
              │              │   • INSERT diff_log, diff_symbols
              │              │   • UPDATE FTS5
              │              │   • Invalidate stale embeddings
              │              │   • Update CSR (in-memory graph)
              └──────┬───────┘
                     │
                     ▼
              ┌──────────────┐
              │   RELEASE    │
              │  FILE MUTEX  │
              └──────┬───────┘
                     │
                     ▼  (async, non-blocking)
              ┌──────────────┐
              │  Embedding   │   Priority queue → batch embed → write vectors
              │   Worker     │   Circuit breaker: 30s timeout, 3 fails → 5min cooldown (SF-2)
              └──────────────┘


TIMELINE (single file change, 1M-line codebase):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━▶
t=0       t=2ms        t=40ms           t=70ms         t=100ms     t=1-5s
File      Change       Parse+Extract    Diff Engine    Writer      Embedding
saved     detected     (parallel)       (post-parse)   commit      complete
          mutex acq                                    mutex rel
          ◄──────── sync, fast (~100ms total) ────────►      ◄─── async ──►
```

#### Embedding Worker (SF-2)

```
PRIORITY QUEUE:
  P1: Chunks bumped by query-time trigger (serve next query better)
  P2: Chunks from files modified in current session
  P3: Chunks from files in working set
  P4: All remaining un-embedded chunks (FIFO)

CIRCUIT BREAKER:
  State: CLOSED → OPEN → HALF_OPEN → CLOSED
  CLOSED:    Normal operation. Process batches (32 chunks/batch).
  OPEN:      After 3 consecutive failures (timeout/error).
             Skip all embedding for 5 minutes. Log warning.
             System operates at Level 1 (structural + keyword).
  HALF_OPEN: After cooldown, try 1 batch.
             Success → CLOSED. Failure → OPEN (reset cooldown).

TIMEOUT: 30s per batch. If Ollama doesn't respond in 30s, count as failure.

CPU THROTTLE (NFR-9):
  • Worker thread runs at reduced priority (nice +10)
  • Between batches: sleep 100ms to yield CPU
  • If system load >80%: pause embedding until load drops
```

#### Progressive Cold Index (1M+ support)

```
Cold indexing a 1M-line codebase cannot complete in 60s.
Adjusted targets:
  100K lines: <60s  (original target)
  500K lines: <5 min
  1M lines:   <10 min

CRITICAL: system must be usable DURING cold index.

PROGRESSIVE AVAILABILITY:
  1. Discover all files (1-5s)
  2. Process files in batches of 200
  3. After each batch:
     a. Commit to SQLite (via Writer Thread)
     b. Update CSR graph (incremental)
     c. Those files are IMMEDIATELY queryable (Level 1: structural + keyword)
     d. Report progress: "Indexed 2400/8500 files (28%)"
  4. Embedding runs independently after structural index complete

USER EXPERIENCE:
  shaktiman init
    ✓ Detected: TypeScript (4200 files), Python (180 files), Go (520 files)
    ✓ Indexing...  ░░░░░░░░░░░░░░░░░░░░ 0%
    ✓ Indexing...  ████░░░░░░░░░░░░░░░░ 20%  (queryable: 980 files)
    ✓ Indexing...  ████████████████████ 100% (4900 files, 6m 23s)
    ✓ Embedding in background... 0% (est. 25 min)
    ✓ System ready (structural mode — full hybrid when embedding completes)
```

#### Tree-sitter Partial Parse Handling (SF-3)

```
When tree-sitter encounters syntax errors:
  1. Parse produces partial AST with ERROR nodes
  2. Detect: count ERROR nodes in AST
     • 0 errors → parse_quality = 'full'
     • 1-3 errors → parse_quality = 'partial', extract what we can
     • >3 errors or >50% of AST is ERROR → parse_quality = 'error', keep previous index
  3. Chunks from partial parses:
     • Marked with parse_quality = 'partial'
     • Ranked at 50% weight (SF-3 penalty in Hybrid Ranker)
     • Re-indexed automatically when file next saved with valid syntax
  4. Previous clean index preserved alongside partial results
     (don't delete old chunks until new parse is clean)
```

---

### 3.5 Failure Handling

```
┌────────────────────────┬───────────────────────────────────────────────────────┐
│ Failure                │ Behavior                                              │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Tree-sitter parse fail │ Keep previous index for file. Mark as 'error'.       │
│                        │ Serve stale chunks (clearly flagged).                │
│                        │ Retry on next file save.                             │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Embedding model down   │ Circuit breaker opens. System drops to Level 1.      │
│ (Ollama not running)   │ Log warning. Retry every 5 min.                      │
│                        │ On startup: detect Ollama availability, warn if down.│
├────────────────────────┼───────────────────────────────────────────────────────┤
│ SQLite corruption      │ `shaktiman reindex` drops and recreates DB.          │
│                        │ Session history lost (documented trade-off).          │
│                        │ Embeddings must re-generate.                         │
│                        │ Config preserved (separate .shaktiman/config.json).  │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Disk full              │ Pre-flight check: require 100MB free before large    │
│                        │ writes (cold index). Warn and pause if low.          │
│                        │ WAL checkpoint requires disk space — monitor.        │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ fsevents misses events │ Periodic full scan every 5 min (hash comparison).    │
│ (batch save, IDE bulk) │ Catches any missed changes within 5 min window.      │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Embedding model change │ MF-1: invalidate all vectors, re-queue.             │
│                        │ System drops to Level 1 until re-embedding complete. │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Process crash          │ SQLite WAL ensures DB consistency.                   │
│                        │ In-memory CSR graph rebuilt from SQLite on restart   │
│                        │ (~50-120ms for 1M lines). Queries degraded briefly.  │
│                        │ Embedding queue lost — rebuilt from embedding_status.│
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Branch switch (git)    │ Hundreds of files change simultaneously.             │
│                        │ Watcher batches events (100ms debounce).             │
│                        │ Enrichment pool processes in parallel.              │
│                        │ Query-time triggers capped at 80ms — excess queued. │
├────────────────────────┼───────────────────────────────────────────────────────┤
│ Very large file        │ Files >10K lines: chunk at 1024 token max per chunk.│
│ (>10K lines)           │ Parsing still fast (tree-sitter handles large files).│
│                        │ Token counting: pre-compute, never count at query.   │
└────────────────────────┴───────────────────────────────────────────────────────┘
```

#### Reindex Recovery (NFR-10)

```
shaktiman reindex [--full | --embeddings-only]

--full:
  1. Delete .shaktiman/index.db
  2. Create fresh DB with current schema_version
  3. Run cold index from scratch
  4. Session history and diff history are lost (config preserved)

--embeddings-only:
  1. Delete all rows from embeddings / vector index
  2. Set all files to embedding_status='pending'
  3. Re-queue all chunks for embedding
  4. Metadata, graph, diffs, session data preserved
```

---

## 4. Data Flows

### 4.1 Query Flow (hot path)

```
Agent                                     Shaktiman
  │                                           │
  │  search("rate limit auth", budget=8192)   │
  │──────────────────────────────────────────▶│
  │                                           │
  │                                 ┌─────────┴──────────┐
  │                                 │  Query Router       │
  │                                 │  • check index ready│
  │                                 │  • strategy: L0     │
  │                                 │  • no enrichment    │
  │                                 │    triggers needed  │
  │                                 └─────────┬──────────┘
  │                                           │
  │                                 ┌─────────┴──────────────────┐
  │                                 │  Retrieval Engine           │
  │                                 │  PARALLEL fan-out:          │
  │                                 │  ├▶ sqlite-vec ANN  (35ms) │
  │                                 │  ├▶ CSR BFS         (8ms)  │
  │                                 │  ├▶ FTS5 search     (5ms)  │
  │                                 │  ├▶ Diff Store scan (4ms)  │
  │                                 │  └▶ Session LRU     (1ms)  │
  │                                 │  max(35ms) = 35ms parallel │
  │                                 └─────────┬──────────────────┘
  │                                           │
  │                                 ┌─────────┴──────────┐
  │                                 │  Hybrid Ranker      │
  │                                 │  normalize (5ms)    │
  │                                 │  weight + sort (2ms)│
  │                                 └─────────┬──────────┘
  │                                           │
  │                                 ┌─────────┴──────────┐
  │                                 │  Context Assembler  │
  │                                 │  dedup (3ms)        │
  │                                 │  budget fit (5ms)   │
  │                                 │  expand (8ms)       │
  │                                 │  meta attach (2ms)  │
  │                                 └─────────┬──────────┘
  │                                           │
  │  { chunks: [{                             │
  │      path: "src/auth/rateLimit.ts",       │
  │      symbol: "checkRateLimit",            │
  │      lines: [15, 52],                     │
  │      score: 0.92,                         │
  │      changed: "3h ago: added Redis TTL",  │
  │      content: "..."                       │
  │    }, ...],                               │
  │    budget_used: 5800,                     │
  │    budget_limit: 8192,                    │
  │    strategy: "hybrid_l0",                 │
  │    tokenizer: "cl100k_base"               │
  │  }                                        │
  │◀──────────────────────────────────────────│
  │                                           │
  │                            TOTAL: ~70ms   │
```

### 4.2 Push Mode Flow (FR-14)

```
┌───────────────────────────────────────────────────────────────────────────┐
│                        PUSH MODE LIFECYCLE                                │
│                                                                           │
│  1. TASK START                                                           │
│                                                                           │
│     Agent ──▶ Reads shaktiman://context/active                           │
│              └── Pre-assembled working set: top chunks from recent       │
│                  session + recent diffs. 4K tokens. Available in <5ms.   │
│                                                                           │
│     Agent ──▶ Calls prompt: task-start("Fix the rate limiting bug")      │
│              └── Shaktiman runs search("rate limiting bug") internally   │
│                  + appends workspace summary + recent diffs in auth.     │
│                  Returns 8K token context package. ~120ms.               │
│                                                                           │
│  2. DURING TASK                                                          │
│                                                                           │
│     Developer saves src/auth/rateLimit.ts                                │
│        │                                                                  │
│        ├──▶ Enrichment pipeline re-indexes (~100ms)                      │
│        ├──▶ Session Store updates working set                            │
│        ├──▶ Active resource re-assembled (background)                    │
│        └──▶ If working set shifted >30%:                                 │
│             └──▶ Send notification: context/changed                      │
│                  { changed: ["src/auth/rateLimit.ts"],                    │
│                    hint: "rateLimit modified, checkRateLimit updated" }   │
│                                                                           │
│     Agent receives notification                                          │
│        └──▶ Reads updated shaktiman://context/active                     │
│             └── Fresh context reflecting the file change                  │
│                                                                           │
│  3. SESSION CONTINUITY                                                   │
│                                                                           │
│     New conversation with Claude Code (same project)                     │
│        └──▶ Agent reads shaktiman://context/active                       │
│             └── Contains: chunks from last session's working set         │
│                  + diffs since last session ended                         │
│                  Agent "remembers" where it was working.                  │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Performance Budgets

### 5.1 Hot Path: Query → Response

| Step | 100K lines | 1M lines | Optimization |
|---|---|---|---|
| MCP overhead | 3ms | 3ms | Persistent connection |
| Embed query | 5ms | 5ms | LRU cache (last 100 queries) |
| Vector ANN | 20ms | 50ms | sqlite-vec; swap to usearch at scale |
| CSR BFS (depth ≤ 3) | 3ms | 12ms | Cache-friendly sequential access |
| FTS5 search | 4ms | 8ms | SQLite FTS5 with ranking |
| Diff Store lookup | 3ms | 5ms | Index on (file_id, timestamp) |
| Session lookup | 1ms | 1ms | In-memory LRU |
| Normalize + rank | 3ms | 5ms | Pure math, no I/O |
| Assemble + dedup | 8ms | 15ms | Pre-computed token counts |
| **Total** | **50ms** | **104ms** | **Within 200ms p95** |

### 5.2 Warm Path: Incremental Re-index

| Step | Time | Notes |
|---|---|---|
| Watcher event | 1ms | fsevents native |
| Acquire file mutex | <1ms | In-memory, rarely contended |
| Change detect (hash) | 2ms | Skip if mtime unchanged |
| Tree-sitter parse | 15-40ms | Full parse |
| Parallel extract | 10-20ms | Symbol + chunk + dep in parallel |
| Diff Engine | 5-15ms | Post-parse, line-level diff |
| Writer Thread commit | 5-15ms | Single SQLite transaction |
| Release mutex | <1ms | |
| **Total (sync)** | **40-95ms** | **Within 500ms** |
| Embedding (async) | 500ms-5s | Non-blocking queue |

### 5.3 Cold Path: Full Index

| Scale | Parse+Extract | Write | CSR Build | Total (usable) | Embedding (async) |
|---|---|---|---|---|---|
| 100K lines | 20-35s | 3-5s | 5ms | **<45s** | 5-15 min |
| 500K lines | 2-3 min | 15-30s | 30ms | **<4 min** | 30-60 min |
| 1M lines | 4-7 min | 30-60s | 80ms | **<8 min** | 1-2 hours |

System is queryable progressively (every 200 files committed).

---

## 6. Resource Budgets

### Memory (NFR-7)

| Component | 100K lines | 1M lines |
|---|---|---|
| SQLite page cache | 10 MB | 30 MB |
| CSR graph (in-memory) | 0.6 MB | 17 MB |
| Session LRU (current) | 1 MB | 2 MB |
| FTS5 auxiliary data | 2 MB | 8 MB |
| Watcher state | 1 MB | 3 MB |
| Embedding worker buffer | 5 MB | 5 MB |
| Query connection pool | 3 MB | 5 MB |
| **Total** | **~23 MB** | **~70 MB** |
| **NFR-7 target** | **<100 MB ✓** | **<100 MB ✓** |

### Disk (NFR-8)

| Component | 100K lines | 1M lines |
|---|---|---|
| SQLite metadata | 20 MB | 150 MB |
| Embeddings (768d × f32) | 30 MB (10K chunks) | 230 MB (75K chunks) |
| FTS5 index | 5 MB | 40 MB |
| Diff history (30d) | 2 MB | 15 MB |
| WAL file (transient) | 5 MB | 20 MB |
| **Total** | **~62 MB** | **~455 MB** |
| **NFR-8 target** | **<500 MB ✓** | **<500 MB ✓** |

---

## 7. Token Efficiency (SF-7: realistic ranges)

```
┌──────────────────────────────────────────────────────────────────────────┐
│                    TOKEN EFFICIENCY — REALISTIC ESTIMATES                 │
│                                                                          │
│  Metric                     │  Without Shaktiman  │  With Shaktiman     │
│  ──────────────────────────────────────────────────────────────────────  │
│  Tool calls per task        │  8-15 calls         │  1-3 calls          │
│  REDUCTION:                 │                     │  70-90% fewer       │
│                             │                     │                      │
│  Input tokens per task      │  15K-30K tokens     │  5K-10K tokens      │
│  REDUCTION:                 │                     │  45-65% fewer       │
│                             │                     │                      │
│  Irrelevant content         │  25-40% of reads    │  <5% of results    │
│  NOISE REDUCTION:           │                     │  85-95%             │
│                             │                     │                      │
│  Session re-discovery       │  3K-8K tokens       │  0-2K tokens        │
│  REDUCTION:                 │                     │  50-100%            │
│                             │                     │                      │
│  Metadata overhead / chunk  │  n/a                │  ~12 tokens (~4%)   │
│  NFR-5 target: <5%         │                     │  ✓                  │
│                             │                     │                      │
│  OVERALL ESTIMATE:          │  ~22K tokens avg    │  ~8K tokens avg     │
│  TOTAL REDUCTION:           │                     │  ~55-65%            │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 8. Architecture Decision Records

| # | Decision | Choice | Rationale |
|---|---|---|---|
| 1 | Storage engine | SQLite (WAL mode) | Zero-config, ACID, embedded. Single DB for all stores. |
| 2 | Write concurrency | Dedicated Writer Thread + queue | Eliminates SQLite writer contention. All mutations serialized. Reads unblocked (WAL). |
| 3 | Graph runtime | CSR in-memory + SQLite persist | CSR: 8 bytes/edge vs 40+ for hash maps. 42MB at 5M edges. Cache-friendly BFS. |
| 4 | Vector store | sqlite-vec + `VectorStore` interface | Transactional with main DB. Interface allows swap to usearch/lancedb at scale. |
| 5 | Parser | Tree-sitter | Multi-language, fast, partial parse detection. |
| 6 | Embedding model | Pluggable: Ollama default | Local-first. Model change invalidates all vectors (MF-1). |
| 7 | Tokenizer | cl100k_base + 95% safety margin | Best Claude approximation. Configurable. |
| 8 | Ranking | 5-signal normalized weighted sum | Default weights, `RankingStrategy` interface for future swap. |
| 9 | Push mode | MCP resources + notifications | Native MCP pattern. No polling. Agent reads resource at task start. |
| 10 | Enrichment order | Parse → Extract (parallel) → Diff → Write | Diff Engine post-parse avoids ghost symbol references (SF-1). |
| 11 | Query-time enrichment | Single-file, 80ms budget, file mutex | Prevents cascades (MF-4) and races (MF-5). |
| 12 | Embedding resilience | Priority queue + circuit breaker | 30s timeout, 3-fail trip, 5-min cooldown. System drops to Level 1. |
| 13 | Cold index strategy | Progressive (200-file batches) | System usable during indexing. Critical for 1M+ lines. |
| 14 | Session management | LRU + exploration decay + eviction | Prevents filter bubbles (SF-9). 30d TTL, 100K max rows (SF-5). |
| 15 | Schema evolution | schema_version table + migrations | Avoid data loss on upgrades (SF-10). Full rebuild only for major versions. |
| 16 | Parse errors | Detect, mark, penalize, preserve | Partial ASTs marked, ranked at 50%, clean version preserved (SF-3). |

---

## 9. Full Requirement Traceability

| Req | Satisfied By |
|---|---|
| **FR-1** Index symbols | Metadata Store + Symbol Extractor |
| **FR-2** Dependency graph | Graph Store (CSR) + Dep Extractor |
| **FR-3** Incremental index | Change Detector + file mutex + Writer Thread |
| **FR-4** Multi-language | Tree-sitter + per-language .scm queries (NFR-14) |
| **FR-5** Semantic search | Vector Store + Retrieval Engine |
| **FR-6** Hybrid retrieval | Hybrid Ranker (5 signals, normalized) |
| **FR-7** Chunk-level | Chunk Splitter (symbol boundaries) |
| **FR-8** Context assembly | Context Assembler (budget-fitted, safety margin) |
| **FR-9** Structural context | Context Assembler expansion step (capped at 30%, max 5/chunk) |
| **FR-10** Dedup | Context Assembler line-range overlap detection (>50%) |
| **FR-11** Metadata | Per-chunk metadata: path, symbol, lines, score, changed, parse_quality |
| **FR-12** MCP | MCP Server (tools + resources + prompts + notifications) |
| **FR-13** CLI | CLI Interface (init, query, status, diff, reindex, config, inspect) |
| **FR-14** Push mode | MCP resources (context/active, workspace/summary) + notifications + task-start prompt |
| **FR-15** Persist | SQLite + WAL mode + crash recovery from edges/metadata |
| **FR-16** Session tracking | Session Store (access_log + working_set + exploration decay) |
| **FR-17** Edit history | Diff Store (diff_log + diff_symbols) |
| **FR-18** Ignore patterns | Change Detector (.gitignore + .shaktimanignore) |
| **FR-19** Config | .shaktiman/config.json (per-project) |
| **NFR-1** Cold <60s (100K) | Progressive cold index: <45s for 100K lines |
| **NFR-2** Incr <500ms | Enrichment pipeline: 40-95ms measured |
| **NFR-3** Query <200ms | Hot path: 50-104ms measured |
| **NFR-4** Budget cap | Context Assembler hard limit + 95% safety margin |
| **NFR-5** Meta <5% | 12 tokens / 300 avg chunk = 4% |
| **NFR-6** 60% fewer calls | 1-3 calls vs 8-15 = 70-90% reduction |
| **NFR-7** Memory <100MB | Budget: 23MB (100K) / 70MB (1M). See Section 6. |
| **NFR-8** Disk <500MB | Budget: 62MB (100K) / 455MB (1M). See Section 6. |
| **NFR-9** CPU throttled | Embedding Worker: nice +10, 100ms sleep between batches, load check |
| **NFR-10** Reindex recovery | `shaktiman reindex` (--full or --embeddings-only). See Section 3.5. |
| **NFR-11** Graceful degrade | 5-level fallback chain (L0 → L0.5 → L1 → L2 → L3) |
| **NFR-12** Modular | `VectorStore` interface, `RankingStrategy` interface, layered architecture |
| **NFR-13** Swappable model | `VectorStore.model_id()` + invalidation on change (MF-1) |
| **NFR-14** New language | Tree-sitter grammar + .scm query file only |
| **NFR-15** Pluggable ranking | `RankingStrategy` interface. Default: `HybridStrategy` |

---

## Final Implementation Decisions (Post-Solution-Fit Analysis)

> These decisions refine the architecture for implementation. The design remains valid; these are technology and phasing choices.

| # | Original Design | Final Decision | Rationale |
|---|---|---|---|
| FD-1 | ZeroMQ DEALER/ROUTER + PUB/SUB for agent-daemon IPC | **MCP stdio server** (via `mcp-go` SDK) | Primary consumer is Claude Code via MCP (DC-9). ZMQ added ~15h complexity (registry, liveness, heartbeat, socket security) with no consumer beyond MCP. MCP provides transport, tools, resources, and notifications natively. |
| FD-2 | MessagePack serialization | **JSON** (stdlib `encoding/json`) | MCP uses JSON-RPC. Single serialization format. Human-readable. Eliminates format mismatch. |
| FD-3 | sqlite-vec for vector storage | **Brute-force in-process** (default), Qdrant optional | Satisfies DC-11 (no external DB) and DC-13 (embedded vector store). Cosine scan over ~75K chunks takes ~30ms. VectorStore interface enables Qdrant swap if profiling shows need. |
| FD-4 | CSR graph (in-memory, custom) from Phase 2 | **SQLite recursive CTEs** (Phase 2), CSR optional (Phase 3+) | SQLite CTEs perform 3-8ms at 100K lines, 30-80ms at 1M lines. Architecture already tolerates this via A3 fallback. Build CSR only if profiling shows need. |
| FD-5 | 4 languages in Phase 1 (TS, Python, Go, Rust) | **TypeScript-only in Phase 1**; Python+Go in Phase 2; Rust in Phase 3 | Tree-sitter query authoring is the highest-effort task. Ship MVP with one language, add incrementally. |
| FD-6 | No retrieval quality evaluation | **Eval harness in Phase 1** with 10-20 curated TypeScript test cases | 80% relevance success criterion requires measurement infrastructure. Validate before building more signals. |

**Impact on architecture layers:**

- **Layer 1 (Agent Interface):** MCP server replaces ZMQ ROUTER. Tools replace API methods. Resources replace PUB/SUB push. Notifications replace PUB/SUB events.
- **Layer 2 (Query Engine):** Unchanged. All 5 signals, fallback chain, and assembler design remain valid.
- **Layer 3 (Storage):** SQLite CTEs replace CSR for graph traversal in early phases. Brute-force replaces sqlite-vec for vectors.
- **Layer 4 (Enrichment):** Unchanged. Pipeline, watcher, and embedding worker design remain valid.
- **Layer 5 (Infrastructure):** Simplified. No daemon registry, liveness protocol, or socket file management needed — MCP client (Claude Code) manages the server process lifecycle.

---

## Status

**Architecture v3** — Complete. All 8 must-fix and 10 should-fix items from critique incorporated. Designed for 1M+ lines. Awaiting critique validation and confirmation before Step 3 (implementation planning).
