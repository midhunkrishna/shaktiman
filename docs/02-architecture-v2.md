# Shaktiman: High-Level Architecture v2

> Architecture design for a local-first, high-performance code context system.
> Derived from [01-requirements.md](./01-requirements.md). Supersedes v1.

---

## 1. System Overview

```
╔══════════════════════════════════════════════════════════════════════════════╗
║                              CONSUMERS                                      ║
║                                                                              ║
║    ┌───────────────┐       ┌───────────────┐       ┌───────────────┐         ║
║    │  Claude Code  │       │   Other MCP   │       │   Developer   │         ║
║    │    (Agent)    │       │    Clients    │       │     (CLI)     │         ║
║    └───────┬───────┘       └───────┬───────┘       └───────┬───────┘         ║
║            │ MCP                   │ MCP                   │ direct          ║
╚════════════┼═══════════════════════┼═══════════════════════┼═════════════════╝
             │                       │                       │
             ▼                       ▼                       ▼
╔══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 1 — INTERFACE                                                        ║
║                                                                              ║
║    ┌──────────────────────────────────┐      ┌────────────────────────┐      ║
║    │          MCP Server              │      │     CLI Interface     │      ║
║    │                                  │      │                        │      ║
║    │  search(query, budget?)          │      │  shaktiman query       │      ║
║    │  context(files, task, budget?)   │      │  shaktiman status      │      ║
║    │  symbols(file)                   │      │  shaktiman diff        │      ║
║    │  dependencies(symbol)            │      │  shaktiman reindex     │      ║
║    │  diff(scope, since?)             │      │  shaktiman config      │      ║
║    │  summary(scope)                  │      │                        │      ║
║    └──────────────┬───────────────────┘      └───────────┬────────────┘      ║
║                   │                                      │                   ║
╚═══════════════════┼══════════════════════════════════════┼═══════════════════╝
                    │                                      │
                    ▼                                      ▼
╔══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 2 — QUERY ENGINE                                                     ║
║                                                                              ║
║    ┌────────────┐    ┌──────────────┐    ┌──────────────┐    ┌───────────┐   ║
║    │   Query    │───▶│  Retrieval   │───▶│   Hybrid     │───▶│  Context  │   ║
║    │   Router   │    │   Engine     │    │   Ranker     │    │ Assembler │   ║
║    │            │    │              │    │              │    │           │   ║
║    │ • strategy │    │ • fan-out to │    │ • 5-signal   │    │ • dedup   │   ║
║    │   select   │    │   5 stores   │    │   weighted   │    │ • budget  │   ║
║    │ • fallback │    │ • parallel   │    │   merge      │    │   fit     │   ║
║    │ • enrich   │    │   scoring    │    │              │    │ • meta    │   ║
║    │   trigger  │    │              │    │              │    │   attach  │   ║
║    └────────────┘    └──────────────┘    └──────────────┘    └───────────┘   ║
║                             │                                                ║
║               ┌─────────────┼─────────────┬──────────────┐                   ║
║               ▼             ▼             ▼              ▼                   ║
║         ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐              ║
║         │ Semantic │  │Structural│  │ Recency  │  │ Keyword  │              ║
║         │  Score   │  │  Score   │  │  Score   │  │  Score   │              ║
║         │ (vector) │  │ (graph)  │  │ (diff+   │  │ (FTS5)   │              ║
║         │          │  │          │  │  session)│  │          │              ║
║         └──────────┘  └──────────┘  └──────────┘  └──────────┘              ║
║                                          │                                   ║
║                                    ┌──────────┐                              ║
║                                    │  Diff    │ ◀── NEW: diffs feed          ║
║                                    │  Score   │     recency signal           ║
║                                    └──────────┘                              ║
║                                                                              ║
╚══════════════════════════════════════════════════════════════════════════════╝
                    │
                    ▼
╔══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 3 — INDEX STORES                                                     ║
║                                                                              ║
║    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌────────────┐           ║
║    │  Metadata   │ │   Graph     │ │   Vector    │ │   Diff     │           ║
║    │   Store     │ │   Store     │ │   Store     │ │   Store    │           ║
║    │             │ │             │ │             │ │            │           ║
║    │ files       │ │ edges (adj) │ │ embeddings  │ │ change log │           ║
║    │ chunks      │ │ in-memory   │ │ ANN index   │ │ affected   │           ║
║    │ symbols     │ │ adjacency   │ │ (sqlite-vec │ │ symbols    │           ║
║    │ token counts│ │ + SQLite    │ │  or hnswlib)│ │ hunks      │           ║
║    │ FTS5 index  │ │ persist     │ │             │ │ timestamps │           ║
║    │             │ │             │ │             │ │            │           ║
║    └──────┬──────┘ └──────┬──────┘ └──────┬──────┘ └─────┬──────┘           ║
║           │               │               │              │                   ║
║    ┌──────┴───────────────┴───────────────┴──────────────┴──────┐            ║
║    │                     Session Store                          │            ║
║    │           access log · working set · edit history          │            ║
║    └───────────────────────────────────────────────────────────┘            ║
║                                                                              ║
╚══════════════════════════════════════════════════════════════════════════════╝
                    │
                    ▼
╔══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 4 — ENRICHMENT PIPELINE (background, parallel)                       ║
║                                                                              ║
║    ┌──────────────┐     ┌──────────────┐                                     ║
║    │ File Watcher │────▶│   Change     │                                     ║
║    │ (fsevents)   │     │  Detector    │                                     ║
║    └──────────────┘     └──────┬───────┘                                     ║
║                                │                                             ║
║                    ┌───────────┴──────────┐                                  ║
║                    ▼                      ▼                                  ║
║           ┌──────────────┐      ┌──────────────┐                             ║
║           │  Diff Engine │      │  Tree-sitter │                             ║
║           │  (hunks +    │      │  Parser      │                             ║
║           │   affected   │      │              │                             ║
║           │   symbols)   │      └──────┬───────┘                             ║
║           └──────┬───────┘             │                                     ║
║                  │          ┌──────────┼──────────┐     (parallel fan-out)   ║
║                  │          ▼          ▼          ▼                          ║
║                  │   ┌──────────┐┌──────────┐┌──────────┐                   ║
║                  │   │  Symbol  ││  Chunk   ││   Dep    │                   ║
║                  │   │ Extractor││ Splitter ││ Extractor│                   ║
║                  │   └────┬─────┘└────┬─────┘└────┬─────┘                   ║
║                  │        │           │           │                          ║
║                  ▼        ▼           ▼           ▼                          ║
║           ┌─────────────────────────────────────────────┐                    ║
║           │        Sync Write (SQLite transaction)      │                    ║
║           │   metadata + symbols + chunks + edges + FTS │                    ║
║           │              + diff records                 │                    ║
║           └──────────────────┬──────────────────────────┘                    ║
║                              │                                               ║
║                              ▼  (async, low-priority, non-blocking)         ║
║           ┌─────────────────────────────────────────────┐                    ║
║           │         Embedding Worker                    │                    ║
║           │   • priority queue (recent edits first)     │                    ║
║           │   • batch processing (32 chunks/batch)      │                    ║
║           │   • CPU-throttled, low I/O priority         │                    ║
║           │   • pluggable model (Ollama / API)          │                    ║
║           └─────────────────────────────────────────────┘                    ║
║                                                                              ║
╚══════════════════════════════════════════════════════════════════════════════╝
                    │
                    ▼
╔══════════════════════════════════════════════════════════════════════════════╗
║  LAYER 5 — STORAGE                                                          ║
║                                                                              ║
║    ┌────────────────────────────────────────────────────────────────────┐    ║
║    │                    SQLite Database                                 │    ║
║    │                    .shaktiman/index.db                             │    ║
║    │                                                                    │    ║
║    │  Metadata:  files · chunks · symbols · config                     │    ║
║    │  Graph:     edges (src, dst, kind) + adjacency cache              │    ║
║    │  Vectors:   embeddings via sqlite-vec (or hnswlib sidecar file)   │    ║
║    │  Diffs:     diff_log · affected_symbols · hunks                   │    ║
║    │  Sessions:  access_log · working_set                              │    ║
║    │  Search:    chunks_fts (FTS5 virtual table)                       │    ║
║    │                                                                    │    ║
║    └────────────────────────────────────────────────────────────────────┘    ║
║                                                                              ║
╚══════════════════════════════════════════════════════════════════════════════╝
```

---

## 2. Component Specifications

### 2.1 Interface Layer

#### MCP Server

| Attribute      | Detail |
|---|---|
| **Purpose**    | Primary interface for coding agents. Exposes Shaktiman capabilities as MCP tools. |
| **Inputs**     | MCP tool calls from Claude Code or other MCP-compatible clients. |
| **Outputs**    | Token-budgeted context packages (ranked chunks + metadata). |
| **Dependencies** | Query Engine (Layer 2). |

**MCP Tools:**

| Tool | Purpose | Replaces |
|---|---|---|
| `search(query, budget?)` | Hybrid semantic + structural search | 5-15 grep/glob/read cycles |
| `context(files, task, budget?)` | Full task context from active files | Manual exploration |
| `symbols(file)` | Symbol list with signatures for a file | Reading entire file |
| `dependencies(symbol)` | Callers, callees, importers of a symbol | Manual grep for usages |
| `diff(scope, since?)` | What changed in a scope since a timestamp | git log + manual reads |
| `summary(scope)` | Project/module/file orientation | Reading dozens of files |

#### CLI Interface

| Attribute      | Detail |
|---|---|
| **Purpose**    | Developer-facing: debugging, manual queries, index management. |
| **Inputs**     | Shell commands (`shaktiman <subcommand>`). |
| **Outputs**    | Formatted text to stdout. |
| **Dependencies** | Same internals as MCP Server. |

**Commands:** `init`, `query`, `status`, `diff`, `reindex`, `config`, `inspect`.

---

### 2.2 Query Engine

#### Query Router

| Attribute      | Detail |
|---|---|
| **Purpose**    | Select retrieval strategy based on index readiness. Trigger on-demand enrichment when needed. |
| **Inputs**     | Parsed query + index status flags + enrichment readiness. |
| **Outputs**    | Strategy enum to Retrieval Engine. Side-effect: enrichment trigger if needed. |
| **Dependencies** | Index Stores (status), Enrichment Pipeline (trigger). |

**Strategy selection + fallback chain:**

```
┌─────────────────────────────────────────────────────────────────────┐
│                    QUERY ROUTER DECISION TREE                       │
│                                                                     │
│  Is target file/symbol indexed?                                     │
│  ├─ NO ─────────────────────────────────────────────┐               │
│  │   ├─▶ TRIGGER: immediate index for that file     │               │
│  │   └─▶ FALLBACK: Level 3 (raw file read)          │               │
│  │                                                   │               │
│  └─ YES                                              │               │
│     │                                                │               │
│     ├─ Embeddings ready (>80%)?                      │               │
│     │  ├─ YES → Level 0: FULL HYBRID                 │               │
│     │  │   (semantic + structural + recency + kw)    │               │
│     │  │                                              │               │
│     │  ├─ PARTIAL (20-80%) → Level 0.5: MIXED        │               │
│     │  │   (hybrid for embedded, structural for rest)│               │
│     │  │   + TRIGGER: priority embed for hit chunks  │               │
│     │  │                                              │               │
│     │  └─ NO (<20%) → Level 1: STRUCTURAL + KW       │               │
│     │      (symbol index + FTS5 + graph + diff)      │               │
│     │      + TRIGGER: priority embed for hit chunks  │               │
│     │                                                │               │
│     └─ Graph available?                              │               │
│        ├─ YES → include structural scoring           │               │
│        └─ NO → Level 2: KEYWORD ONLY (FTS5)          │               │
│                                                      │               │
│  Is file missing from index entirely?                │               │
│  └─ YES → Level 3: FILESYSTEM PASSTHROUGH            │               │
│            read file, chunk by lines, return raw     │               │
└─────────────────────────────────────────────────────────────────────┘
```

#### Retrieval Engine

| Attribute      | Detail |
|---|---|
| **Purpose**    | Execute retrieval: fan out to stores in parallel, collect candidate chunks with sub-scores. |
| **Inputs**     | Query + strategy + active file context + session state. |
| **Outputs**    | Candidate chunks, each with per-signal sub-scores. |
| **Dependencies** | All 5 Index Stores (parallel reads). |

**Parallel fan-out:**

```
Query arrives
    │
    ├──▶ Vector Store    → semantic_score    (cosine similarity)
    ├──▶ Graph Store     → structural_score  (BFS distance from active context)
    ├──▶ Metadata Store  → keyword_score     (FTS5 / BM25 match)
    ├──▶ Diff Store      → diff_score        (recency + relevance of recent changes)
    └──▶ Session Store   → session_score     (access recency + working set membership)
         │
         ▼
    All scores returned (parallel, <100ms total)
```

#### Hybrid Ranker

| Attribute      | Detail |
|---|---|
| **Purpose**    | Merge 5 sub-scores into a single relevance score per chunk. |
| **Inputs**     | Candidate chunks with sub-scores from Retrieval Engine. |
| **Outputs**    | Ranked candidate list. |
| **Dependencies** | None (pure computation). |

**Scoring formula:**

```
score(chunk) = w_sem  * semantic_score       # 0.40 — embedding cosine similarity
             + w_str  * structural_score     # 0.20 — graph distance to active context
             + w_diff * diff_score           # 0.15 — recency/significance of changes
             + w_sess * session_score        # 0.15 — working set membership + access freq
             + w_kw   * keyword_score        # 0.10 — FTS5 match score

Weight adaptation:
  • Embeddings unavailable → w_sem redistributed: w_kw += 0.25, w_str += 0.10, w_diff += 0.05
  • Graph unavailable      → w_str redistributed: w_sem += 0.10, w_kw += 0.10
  • No diff history        → w_diff redistributed: w_sess += 0.15
```

#### Context Assembler

| Attribute      | Detail |
|---|---|
| **Purpose**    | Pack top-ranked chunks into a token-budgeted context package with metadata. |
| **Inputs**     | Ranked chunk list + token budget (default 8192). |
| **Outputs**    | Context package: ordered chunks, metadata, budget accounting. |
| **Dependencies** | Pre-computed token counts from Metadata Store. |

**Assembly algorithm:**

```
INPUT:  ranked_chunks[], budget (tokens), options
OUTPUT: context_package

1. INIT remaining_budget = budget
2. FOR chunk in ranked_chunks (descending score):
   a. IF chunk overlaps >50% with already-selected chunk → SKIP
   b. IF chunk.token_count > remaining_budget → SKIP (or TRIM if partial allowed)
   c. ADD chunk to package
   d. remaining_budget -= chunk.token_count
   e. EXPAND: pull structural neighbors (callers, callees, type defs)
      - for each neighbor, add IF score > threshold AND budget allows
   f. remaining_budget -= metadata_overhead (per-chunk: ~12 tokens)
3. ATTACH per-chunk metadata:
   { path, symbol, lines, score, last_modified, change_summary }
4. RETURN package with { chunks[], budget_used, budget_limit, strategy, enrichment_level }
```

---

### 2.3 Index Stores (Layer 3)

#### Metadata Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Central catalog of files, chunks, and symbols. Provides FTS5 full-text search. |
| **Inputs**     | Parsed data from Enrichment Pipeline. |
| **Outputs**    | File records, chunk content + token counts, symbol definitions, FTS results. |
| **Dependencies** | SQLite. |

**Tables:**

```sql
files     (id, path, content_hash, mtime, size, language, indexed_at, embedding_status)
chunks    (id, file_id, symbol_name, kind, start_line, end_line, content, token_count, signature)
symbols   (id, chunk_id, name, kind, file_id, line, signature, visibility)

-- FTS5 virtual table for keyword search
chunks_fts USING fts5(content, symbol_name, content=chunks, content_rowid=id)
```

#### Graph Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Directed graph of code relationships. Fast traversal for structural scoring. |
| **Inputs**     | Dependency edges from Enrichment Pipeline. |
| **Outputs**    | BFS/DFS traversal results, shortest path distances, subgraph extraction. |
| **Dependencies** | SQLite (persistence) + in-memory adjacency list (query-time performance). |

**Design:**

```
Persistence: SQLite edge table
  edges (id, src_symbol_id, dst_symbol_id, kind, file_id)
  Edge kinds: imports, calls, type_ref, extends, implements, exports

Runtime: In-memory adjacency list
  • Loaded from SQLite at startup (~5ms for 100K-line codebase)
  • Write-through: new edges written to both SQLite and in-memory
  • BFS traversal depth capped at 3 for scoring (prevents runaway on deep graphs)
  • Memory: ~10 bytes/edge × 50K edges = ~500KB for large codebase
```

#### Vector Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | ANN (approximate nearest neighbor) search over chunk embeddings. |
| **Inputs**     | Embedding vectors from Embedding Worker. |
| **Outputs**    | Top-K chunk IDs ranked by cosine similarity to query vector. |
| **Dependencies** | sqlite-vec extension (primary) or hnswlib sidecar file (fallback). |

**Design:**

```
Primary: sqlite-vec
  • Keeps vectors in same DB as metadata (transactional consistency)
  • Adequate for codebases up to ~500K chunks

Fallback: hnswlib (.shaktiman/vectors.hnsw)
  • If sqlite-vec query latency exceeds 80ms p95
  • Separate file, loaded into memory at startup
  • Faster ANN but loses transactional consistency

Query: embed(query_text) → vector → ANN search → top-50 chunk IDs → join with metadata
```

#### Diff Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Track what changed, when, and which symbols were affected. Feeds diff-aware recency scoring and the `diff()` MCP tool. |
| **Inputs**     | Diff computations from the Diff Engine in the Enrichment Pipeline. |
| **Outputs**    | Change records queryable by file, symbol, time range. Diff scores for ranking. |
| **Dependencies** | SQLite. Metadata Store (symbol/chunk cross-references). |

**Tables:**

```sql
diff_log (
  id,
  file_id,
  timestamp,
  change_type,         -- 'add' | 'modify' | 'delete' | 'rename'
  lines_added,
  lines_removed,
  content_hash_before,
  content_hash_after
)

diff_symbols (
  id,
  diff_id,
  symbol_id,           -- which symbol was affected
  change_type          -- 'added' | 'modified' | 'removed' | 'signature_changed'
)
```

**What Diff Store enables:**

```
1. Agent asks: "What changed in the auth module recently?"
   → Diff Store query: SELECT from diff_log JOIN diff_symbols WHERE module='auth' ORDER BY timestamp DESC

2. Hybrid Ranker computes diff_score:
   → Chunks with recent, significant diffs get boosted
   → score = recency_decay(timestamp) × change_magnitude(lines_added + lines_removed)

3. Context Assembler attaches change_summary:
   → Each chunk in the package gets: "Modified 2h ago: added error handling to validateToken"

4. Session-aware prioritization:
   → If agent is working on area X, and area X had recent diffs, those chunks are
     boosted even if the agent hasn't explicitly searched for them
```

#### Session Store

| Attribute      | Detail |
|---|---|
| **Purpose**    | Track agent access patterns and infer the working set. |
| **Inputs**     | Access events from MCP/CLI interactions. |
| **Outputs**    | Session scores per chunk, working set membership. |
| **Dependencies** | In-memory (current session) + SQLite (cross-session persistence). |

**Tables:**

```sql
access_log  (id, session_id, timestamp, chunk_id, operation)  -- 'read', 'search_hit', 'context_include'
working_set (session_id, chunk_id, access_count, last_accessed)
```

---

### 2.4 Enrichment Pipeline (Layer 4)

The enrichment pipeline is **event-driven** and **multi-stage with parallel fan-out**.

#### File Watcher

| Attribute      | Detail |
|---|---|
| **Purpose**    | Detect filesystem changes in real-time. |
| **Inputs**     | OS filesystem events (create, modify, delete, rename). |
| **Outputs**    | Changed file paths to Change Detector. |
| **Dependencies** | fsevents (macOS), inotify (Linux). Polling fallback. |

#### Change Detector

| Attribute      | Detail |
|---|---|
| **Purpose**    | Filter noise, determine which files actually need re-processing. |
| **Inputs**     | File paths from Watcher + stored hashes/mtimes from Metadata Store. |
| **Outputs**    | List of files requiring enrichment, tagged with change type. |
| **Dependencies** | Metadata Store (file hashes). .gitignore / .shaktimanignore patterns. |

#### Diff Engine (NEW)

| Attribute      | Detail |
|---|---|
| **Purpose**    | Compute fine-grained diffs: what lines changed, which symbols were affected. |
| **Inputs**     | Changed file content + previous indexed content (from Metadata Store). |
| **Outputs**    | Diff records (hunks, affected symbols) → written to Diff Store. |
| **Dependencies** | Metadata Store (previous chunk content), Symbol Index (symbol identification). |

**How it works:**

```
1. Read previous chunk content from Metadata Store
2. Compute line-level diff against new file content
3. Map changed line ranges to affected symbols (using previous symbol index)
4. Write diff_log + diff_symbols records
5. Tag affected chunks for re-processing
```

#### Tree-sitter Parser

| Attribute      | Detail |
|---|---|
| **Purpose**    | Parse source files into ASTs. Extract symbols, imports, calls via `.scm` query files. |
| **Inputs**     | Source file content + language grammar + query files. |
| **Outputs**    | AST, symbol definitions, import/call sites. |
| **Dependencies** | Tree-sitter runtime + per-language grammars + `.scm` query files. |

#### Parallel Extractors (fan-out after parse)

After tree-sitter produces the AST, three extractors run **in parallel**:

| Extractor | Output | Target Store |
|---|---|---|
| **Symbol Extractor** | Symbol records (name, kind, signature, range) | Metadata Store |
| **Chunk Splitter** | Chunk records (content, token count, boundaries) | Metadata Store |
| **Dep Extractor** | Edge records (imports, calls, type refs) | Graph Store |

**Chunking strategy:**

```
Primary:   One chunk per top-level symbol (function, class, type alias, interface)
Nested:    Class methods → own chunks, linked to parent class via parent_chunk_id
Orphan:    Top-level code outside symbols → file-header chunk (imports, constants)
Oversized: Chunks > max_tokens (default 1024) → split at logical sub-boundaries
Minimum:   Chunks < 20 tokens → merge with adjacent chunk to reduce fragmentation
```

#### Embedding Worker

| Attribute      | Detail |
|---|---|
| **Purpose**    | Generate embeddings for chunks in the background. |
| **Inputs**     | Priority queue of un-embedded/stale chunk IDs + content. |
| **Outputs**    | Embedding vectors → written to Vector Store. |
| **Dependencies** | Embedding model (Ollama local / API). CPU/GPU. |

**Priority queue ordering:**

```
Priority 1: Chunks hit by a query but missing embeddings (query-time trigger)
Priority 2: Chunks from files modified in current session
Priority 3: Chunks from files in the working set
Priority 4: All remaining un-embedded chunks (FIFO)
```

---

### 2.5 Enrichment Pipeline — Complete Flow

```
FILE CHANGE DETECTED
         │
         ▼
  ┌──────────────┐
  │   Change     │   Filter: .gitignore, .shaktimanignore, mtime check
  │   Detector   │
  └──────┬───────┘
         │
         │   changed files + change types
         │
    ┌────┴──────────────────────────────┐
    │                                   │
    ▼                                   ▼
┌──────────┐                    ┌──────────────┐
│   Diff   │                    │  Tree-sitter │
│  Engine  │                    │    Parser    │
│          │                    │              │
│ compute  │                    │   produce    │
│ hunks +  │                    │   AST        │
│ affected │                    │              │
│ symbols  │                    └──────┬───────┘
└────┬─────┘                           │
     │                      ┌──────────┼──────────┐  (PARALLEL FAN-OUT)
     │                      ▼          ▼          ▼
     │               ┌──────────┐┌──────────┐┌──────────┐
     │               │  Symbol  ││  Chunk   ││   Dep    │
     │               │ Extractor││ Splitter ││ Extractor│
     │               └────┬─────┘└────┬─────┘└────┬─────┘
     │                    │           │           │
     ▼                    ▼           ▼           ▼
┌──────────────────────────────────────────────────────┐
│              SYNC WRITE (single SQLite transaction)  │
│                                                      │
│   Metadata Store:  files, chunks, symbols, FTS5      │
│   Graph Store:     edges (+ update in-memory adj)    │
│   Diff Store:      diff_log, diff_symbols            │
│                                                      │
│   Also: invalidate stale embeddings for changed      │
│         chunks, add to embedding queue               │
│                                                      │
└────────────────────────┬─────────────────────────────┘
                         │
                         ▼  (ASYNC, non-blocking)
              ┌─────────────────────┐
              │  Embedding Worker   │
              │                     │
              │  Process queue:     │
              │  batch embed →      │
              │  write vectors →    │
              │  update status      │
              └─────────────────────┘


TIMELINE (single file change):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━▶
t=0        t=5ms         t=50ms              t=100ms          t=500ms-5s
File       Change        Diff + Parse        Sync write       Embedding
saved      detected      + extract           complete         complete
                         (parallel)
           ◄───── sync, fast (~100ms) ──────►          ◄── async ──►
           Index usable here (structural)              Full hybrid here
```

---

## 3. Key Interactions

### 3.1 Agent → Shaktiman (Primary Query Path)

```
Claude Code                                    Shaktiman
    │                                              │
    │  search("auth middleware validation")        │
    │─────────────────────────────────────────────▶│
    │                                              │
    │                                    ┌─────────┴─────────┐
    │                                    │   Query Router     │
    │                                    │   strategy: L0     │
    │                                    │   (full hybrid)    │
    │                                    └─────────┬─────────┘
    │                                              │
    │                                    ┌─────────┴──────────────────┐
    │                                    │  Retrieval Engine          │
    │                                    │  parallel fan-out:         │
    │                                    │  ├▶ Vector Store (40ms)    │
    │                                    │  ├▶ Graph Store  (10ms)   │
    │                                    │  ├▶ FTS5         (5ms)    │
    │                                    │  ├▶ Diff Store   (5ms)    │
    │                                    │  └▶ Session      (2ms)    │
    │                                    └─────────┬──────────────────┘
    │                                              │
    │                                    ┌─────────┴─────────┐
    │                                    │  Hybrid Ranker     │
    │                                    │  5 signals → score │
    │                                    └─────────┬─────────┘
    │                                              │
    │                                    ┌─────────┴─────────┐
    │                                    │ Context Assembler  │
    │                                    │ budget: 8192 tok   │
    │                                    │ dedup + pack + meta│
    │                                    └─────────┬─────────┘
    │                                              │
    │  context_package {                           │
    │    chunks: [                                  │
    │      { path: "src/auth/middleware.ts",        │
    │        symbol: "validateToken",              │
    │        lines: [42, 78],                      │
    │        content: "...",                        │
    │        score: 0.94,                          │
    │        changed: "2h ago: added rate limit"   │ ◀── from Diff Store
    │      },                                      │
    │      ...                                     │
    │    ],                                        │
    │    budget_used: 6200,                        │
    │    budget_limit: 8192,                       │
    │    strategy: "hybrid_l0"                     │
    │  }                                           │
    │◀─────────────────────────────────────────────│
    │                                              │
    │                              (side-effect:   │
    │                               log to Session │
    │                               Store)         │
    │                                              │

TOTAL LATENCY: ~120ms (well within 200ms p95)
```

**What the agent avoided:**

```
WITHOUT Shaktiman (typical agent exploration):
  1. grep("auth")         → 14 results           ~500 tokens overhead
  2. read(middleware.ts)   → 2100 tokens           (only needed 400)
  3. grep("validate")     → 8 results             ~500 tokens overhead
  4. read(validator.ts)    → 1800 tokens           (only needed 300)
  5. grep("middleware")    → 11 results            ~500 tokens overhead
  6. read(types.ts)        → 900 tokens            (only needed 150)
  7. read(config.ts)       → 600 tokens            (irrelevant)
  8-12. more exploration...

  TOTAL: 10-12 tool calls, ~8K-15K tokens consumed, 30-45 seconds

WITH Shaktiman:
  1. search("auth middleware validation")

  TOTAL: 1 tool call, ~6.2K tokens (all relevant), ~120ms
```

### 3.2 Parallel Diff Enrichment → Shaktiman

```
Developer saves file
    │
    ▼
┌──────────┐                                ┌──────────────────┐
│  File    │  "src/auth/middleware.ts"       │  Shaktiman       │
│  Watcher │───────────────────────────────▶│  Enrichment      │
└──────────┘                                │  Pipeline        │
                                            │                  │
                                            │  1. Change       │
                                            │     Detector     │
                                            │     (hash diff)  │
                                            │        │         │
                                            │  ┌─────┴─────┐   │
                                            │  │           │   │
                                            │  ▼           ▼   │
                                            │ Diff       Parse │
                                            │ Engine     AST   │
                                            │  │           │   │
                                            │  │     ┌─────┼───┼──┐
                                            │  │     ▼     ▼   │  ▼
                                            │  │   Symbol Chunk │ Dep
                                            │  │   Extract Split│ Extract
                                            │  │     │     │   │  │
                                            │  ▼     ▼     ▼   │  ▼
                                            │ ┌────────────────┐│
                                            │ │ SQLite WRITE   ││
                                            │ │ (single txn)   ││
                                            │ │                ││
                                            │ │ • update chunks││
                                            │ │ • update syms  ││
                                            │ │ • update edges ││
                                            │ │ • write diff   ││
                                            │ │ • invalidate   ││
                                            │ │   embeddings   ││
                                            │ └───────┬────────┘│
                                            │         │         │
                                            │         ▼         │
                                            │  Embedding queue: │
                                            │  [chunk_42,       │
                                            │   chunk_43] added │
                                            │  at Priority 2    │
                                            └──────────────────┘

INDEX READY: ~100ms after file save (structural fallback)
EMBEDDINGS READY: ~1-5s after file save (full hybrid)
AGENT QUERY AT t=150ms: uses structural+keyword, triggers priority embed
AGENT QUERY AT t=6s:    uses full hybrid (embeddings now ready)
```

### 3.3 Query-Time Enrichment Triggers

```
┌─────────────────────────────────────────────────────────────────────────┐
│                  QUERY-TIME ENRICHMENT TRIGGERS                         │
│                                                                         │
│  Agent queries for code in a file/symbol that isn't fully enriched.    │
│  The system serves best-available results AND triggers background      │
│  enrichment so the next query is better.                               │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │ TRIGGER 1: Missing Embeddings                                    │   │
│  │                                                                  │   │
│  │ Query hits chunks that have metadata+symbols but no embeddings.  │   │
│  │ → Serve results via structural+keyword scoring (Level 1)         │   │
│  │ → Bump those chunks to Priority 1 in embedding queue            │   │
│  │ → Next query for same area: full hybrid (Level 0)               │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │ TRIGGER 2: Unindexed File                                        │   │
│  │                                                                  │   │
│  │ Query references a file that hasn't been indexed at all.         │   │
│  │ (e.g., newly created file, or file outside normal watch scope)   │   │
│  │ → Trigger IMMEDIATE synchronous index for that one file          │   │
│  │   (parse + chunk + symbols + deps: ~50ms)                       │   │
│  │ → Serve structural results for this query                       │   │
│  │ → Queue embedding at Priority 1                                 │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │ TRIGGER 3: Stale Index                                           │   │
│  │                                                                  │   │
│  │ Query hits chunks whose file has changed since last index.       │   │
│  │ (watcher event pending but enrichment hasn't run yet)            │   │
│  │ → Trigger IMMEDIATE re-index for affected file                  │   │
│  │ → Serve fresh structural results                                │   │
│  │ → Invalidate and re-queue embeddings                            │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │ TRIGGER 4: Missing Dependencies                                  │   │
│  │                                                                  │   │
│  │ Agent calls dependencies(symbol) but graph edges are incomplete. │   │
│  │ → Trigger dep extraction for that file and its direct imports   │   │
│  │ → Serve partial graph (what we have)                            │   │
│  │ → Full graph available on next call                             │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  Design principle: NEVER BLOCK. Serve best-available, improve in       │
│  background. Every query makes the next query better.                  │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.4 Fallback to Filesystem

```
FALLBACK PATH (when index is unavailable or insufficient):

Agent query ──▶ Query Router ──▶ Index check FAILS
                                        │
                                        ▼
                              ┌──────────────────┐
                              │ Filesystem Reader │
                              │                  │
                              │ 1. Resolve file  │
                              │    path(s) from  │
                              │    query hints   │
                              │                  │
                              │ 2. Read raw file │
                              │    content       │
                              │                  │
                              │ 3. Chunk by line │
                              │    boundaries    │
                              │    (500 line max │
                              │    per chunk)    │
                              │                  │
                              │ 4. Count tokens  │
                              │                  │
                              │ 5. Fit to budget │
                              │                  │
                              │ 6. TRIGGER:      │
                              │    index this    │
                              │    file now      │
                              └────────┬─────────┘
                                       │
                                       ▼
                              Return raw chunks
                              (no ranking, no graph,
                               but within budget)

This ensures the system NEVER fails to return something useful.
The fallback itself triggers enrichment, so it's self-healing.
```

---

## 4. Performance-Critical Paths

### 4.1 Hot Path: Query → Response (target < 200ms p95)

```
LATENCY BUDGET BREAKDOWN:

│ Step                        │ Time     │ Optimization                          │
│─────────────────────────────│──────────│───────────────────────────────────────│
│ MCP protocol overhead       │  2-5ms   │ Persistent connection, no reconnect  │
│ Embed query text            │  3-8ms   │ Cache recent query embeddings (LRU)  │
│ Vector ANN search           │ 30-60ms  │ sqlite-vec / hnswlib, top-50 limit  │
│ Graph BFS (depth ≤ 3)       │  5-15ms  │ In-memory adjacency list            │
│ FTS5 keyword search         │  3-8ms   │ SQLite FTS5 with rank function      │
│ Diff Store lookup           │  2-5ms   │ Index on (file_id, timestamp)       │
│ Session Store lookup        │  1-3ms   │ In-memory LRU for current session   │
│ Hybrid ranking              │  2-5ms   │ Simple weighted sum, no ML          │
│ Context assembly + dedup    │ 10-25ms  │ Pre-computed token counts, greedy   │
│ Serialize response          │  1-3ms   │ Minimal JSON                        │
│─────────────────────────────│──────────│───────────────────────────────────────│
│ TOTAL                       │ 59-137ms │ Well within 200ms p95               │
```

### 4.2 Warm Path: Incremental Re-index (target < 500ms)

```
│ Step                        │ Time     │ Notes                                │
│─────────────────────────────│──────────│──────────────────────────────────────│
│ Watcher delivers event      │    1ms   │ fsevents native                     │
│ Change detection (hash)     │  1-5ms   │ Skip if mtime unchanged            │
│ Diff computation            │  5-15ms  │ Line-level diff on cached content   │
│ Tree-sitter parse           │ 10-40ms  │ Full parse (incremental if avail)   │
│ Parallel extract (3 ways)   │ 10-20ms  │ Symbol + chunk + dep in parallel   │
│ SQLite transaction write    │  5-15ms  │ Single transaction for all stores   │
│─────────────────────────────│──────────│──────────────────────────────────────│
│ TOTAL (sync)                │ 32-96ms  │ Well within 500ms                   │
│ Embedding (async)           │ 500ms-5s │ Non-blocking background queue       │
```

### 4.3 Cold Path: Full Index (target < 60s for 100K lines)

```
│ Step                        │ Time     │ Notes                                │
│─────────────────────────────│──────────│──────────────────────────────────────│
│ File discovery              │  1-2s    │ Respect .gitignore, parallel stat   │
│ Parse all files (parallel)  │ 15-30s   │ N cores × tree-sitter per file     │
│ Extract + chunk (parallel)  │  5-10s   │ Combined with parse pass           │
│ Batch SQLite writes         │  2-5s    │ Large transactions (1000 rows/txn) │
│ Build in-memory graph       │  <1s     │ Load edges into adjacency list     │
│─────────────────────────────│──────────│──────────────────────────────────────│
│ TOTAL (sync)                │ 23-48s   │ Within 60s                          │
│ Embedding (async)           │ 2-10min  │ Background, system usable at t=48s │
```

---

## 5. Token Efficiency — Where Every Saving Comes From

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                         TOKEN EFFICIENCY LEDGER                              │
│                                                                              │
│  SAVING 1: Chunk-level retrieval (vs. whole-file reads)                     │
│  ─────────────────────────────────────────────────────                       │
│  Before: read(file) = 2000 tokens × 5 files = 10,000 tokens                │
│  After:  3 relevant chunks × 300 tokens = 900 tokens                        │
│  DELTA: −9,100 tokens (−91%)                                               │
│                                                                              │
│  SAVING 2: Single-call retrieval (vs. multi-step exploration)               │
│  ─────────────────────────────────────────────────────────                   │
│  Before: 10 tool calls × 500 tokens prompt overhead = 5,000 tokens          │
│  After:  1 tool call × 500 tokens prompt overhead = 500 tokens              │
│  DELTA: −4,500 tokens (−90%)                                               │
│                                                                              │
│  SAVING 3: Ranked relevance (vs. noise-mixed results)                       │
│  ───────────────────────────────────────────────────                         │
│  Before: 30% of read content is irrelevant = ~3,000 wasted tokens           │
│  After:  budget-fitted, ranked — <5% noise = ~300 wasted tokens             │
│  DELTA: −2,700 tokens (−90%)                                               │
│                                                                              │
│  SAVING 4: Session continuity (vs. re-discovery)                            │
│  ──────────────────────────────────────────────                              │
│  Before: Agent re-explores same areas = ~5,000 tokens per session restart   │
│  After:  Session store biases toward known working set = 0 re-discovery     │
│  DELTA: −5,000 tokens (−100%)                                              │
│                                                                              │
│  SAVING 5: Diff-aware context (vs. blind recency)                           │
│  ─────────────────────────────────────────────────                           │
│  Before: Agent reads unchanged files to check for updates = ~2,000 tokens   │
│  After:  diff() tool tells agent exactly what changed = ~200 tokens          │
│  DELTA: −1,800 tokens (−90%)                                               │
│                                                                              │
│  ═══════════════════════════════════════════════                             │
│  TOTAL BEFORE: ~25,000 tokens per typical task                               │
│  TOTAL AFTER:  ~7,000 tokens per typical task                                │
│  NET SAVINGS:  ~18,000 tokens (−72%)                                        │
│  ═══════════════════════════════════════════════                             │
│                                                                              │
│  Metadata overhead per chunk: ~12 tokens / ~300 token chunk = 4% (< 5%)     │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 6. Developer Experience Flow

```
INITIAL SETUP (one-time):

  $ cd my-project
  $ shaktiman init
    ✓ Detected: TypeScript (342 files), Python (28 files)
    ✓ Created .shaktiman/
    ✓ Indexing... ████████████████████ 100% (38s)
    ✓ Index ready (structural mode)
    ✓ Embedding in background... 12% (est. 4 min remaining)

  # Add to Claude Code MCP config:
  $ shaktiman mcp-config >> ~/.claude/settings.json

ONGOING USAGE:

  Developer starts Claude Code
       │
       ▼
  Claude Code discovers Shaktiman MCP tools
  (search, context, symbols, dependencies, diff, summary)
       │
       ▼
  Agent receives task: "Fix the rate limiting bug in auth"
       │
       ├─▶ Agent calls: search("rate limiting auth bug")
       │   └─▶ Returns: 3 chunks (validateToken, rateLimiter, authConfig)
       │       with change summaries: "rateLimiter modified 1h ago"
       │
       ├─▶ Agent calls: dependencies("rateLimiter")
       │   └─▶ Returns: callers [validateToken, apiGateway],
       │       callees [redisClient, configLoader]
       │
       ├─▶ Agent now has full picture — edits rateLimiter
       │
       ├─▶ Developer saves file
       │   └─▶ Shaktiman re-indexes in ~100ms
       │       └─▶ Agent's next query reflects the change
       │
       └─▶ Agent calls: diff("src/auth/", since="30min")
           └─▶ Returns: summary of all changes in auth module
               Agent verifies fix is consistent

  TOTAL AGENT EFFORT: 3 tool calls, ~7K tokens, ~15 seconds
  WITHOUT SHAKTIMAN: 12+ tool calls, ~25K tokens, ~45 seconds
```

---

## 7. Architecture Decision Records

| Decision | Choice | Rationale | Alternatives Considered |
|---|---|---|---|
| **Primary storage** | SQLite (single DB) | Zero-config, embedded, ACID, handles all stores | Postgres (too heavy), RocksDB (no SQL) |
| **Vector index** | sqlite-vec, hnswlib fallback | sqlite-vec: transactional, same DB. hnswlib: faster ANN if needed | FAISS (heavy), Chroma (server-based) |
| **Graph runtime** | In-memory adjacency + SQLite persist | Fast BFS (<15ms) with durability. ~500KB memory for large codebase | Neo4j (external dep), pure SQLite (too slow for BFS) |
| **Parser** | Tree-sitter | Multi-language via grammars, fast, mature. Per DC-12 | LSP (heavier), regex (fragile) |
| **Embedding model** | Pluggable: Ollama default | Local-first per DC-1/DC-2. Nomic-embed-text as default | OpenAI API only (cloud dep), fixed model (inflexible) |
| **Integration** | MCP server | Native Claude Code support per DC-9/DC-10 | Custom API (non-standard), stdio pipe (fragile) |
| **Chunking** | Symbol-boundary | Natural code units. Better retrieval than fixed-size | Fixed-size (misses symbol boundaries), file-level (too coarse) |
| **Ranking** | 5-signal weighted hybrid | No single signal sufficient. Diff signal adds change-awareness | Single signal (poor recall), ML ranker (complexity) |
| **Enrichment** | Event-driven + parallel fan-out | Parallel extractors: faster incremental. Async embedding: non-blocking | Sequential pipeline (slower), eager embedding (blocks) |
| **Diff tracking** | Dedicated Diff Store | Enables diff-aware ranking + `diff()` tool. Lightweight | Git log only (slow, incomplete), no diff tracking (loses signal) |
| **Process model** | Single process, multi-threaded | Simple ops. Watcher + embedder as workers | Multi-process (IPC overhead), separate services (overengineered) |
| **Query-time enrichment** | Pull-based triggers | Self-healing: every query improves the index. Never blocks | Lazy-only (slow convergence), eager-only (wastes resources) |

---

## 8. Requirement Traceability

| Requirement | Addressed By |
|---|---|
| FR-1 (Index symbols) | Metadata Store + Symbol Extractor |
| FR-2 (Dep graph) | Graph Store + Dep Extractor |
| FR-3 (Incremental index) | Change Detector + Diff Engine |
| FR-4 (Multi-language) | Tree-sitter + per-language .scm queries |
| FR-5 (Semantic search) | Vector Store + Retrieval Engine |
| FR-6 (Hybrid retrieval) | Hybrid Ranker (5 signals) |
| FR-7 (Chunk-level) | Chunk Splitter (symbol boundaries) |
| FR-8 (Context assembly) | Context Assembler (budget-fitted) |
| FR-9 (Structural context) | Context Assembler expansion step |
| FR-10 (Dedup) | Context Assembler overlap detection |
| FR-11 (Metadata) | Context Assembler metadata attachment |
| FR-12 (MCP) | MCP Server |
| FR-13 (CLI) | CLI Interface |
| FR-14 (Push mode) | Session Store → proactive pre-fetch |
| FR-15 (Persist) | SQLite + hnswlib sidecar |
| FR-16 (Session tracking) | Session Store |
| FR-17 (Edit history) | Diff Store + Session Store |
| FR-18 (Ignore patterns) | Change Detector |
| FR-19 (Config) | Per-project .shaktiman/config.json |
| NFR-1 (Cold <60s) | Parallel parse + batch write |
| NFR-2 (Incr <500ms) | Parallel fan-out enrichment |
| NFR-3 (Query <200ms) | Hot path budget: 59-137ms |
| NFR-4 (Budget cap) | Context Assembler hard limit |
| NFR-5 (Meta <5%) | 12 tokens / 300 token chunk = 4% |
| NFR-6 (60% fewer calls) | 1 call vs 10-12 = 91% reduction |
| NFR-11 (Graceful degrade) | 4-level fallback chain |
| DC-4 (Async embed) | Embedding Worker (non-blocking queue) |
| DC-5 (Work before embed) | Query Router fallback chain |

---

## Status

**Step 2: Architecture v2** — Complete. Awaiting critique review and confirmation before Step 3.
