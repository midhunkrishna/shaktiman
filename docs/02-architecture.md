# Shaktiman: High-Level Architecture

> Architecture design for a local-first, high-performance code context system.
> Derived from [01-requirements.md](./01-requirements.md).

---

## 1. System Overview Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          CONSUMERS                                          │
│                                                                             │
│   ┌──────────────┐          ┌──────────────┐          ┌──────────────┐      │
│   │  Claude Code  │          │  Other MCP   │          │  Developer   │      │
│   │   (Agent)     │          │   Clients    │          │    (CLI)     │      │
│   └──────┬───────┘          └──────┬───────┘          └──────┬───────┘      │
│          │ MCP protocol            │ MCP protocol            │ direct       │
└──────────┼─────────────────────────┼─────────────────────────┼──────────────┘
           │                         │                         │
           ▼                         ▼                         ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     INTEGRATION LAYER                                       │
│                                                                             │
│   ┌─────────────────────────────────────┐     ┌──────────────────────┐      │
│   │           MCP Server                │     │     CLI Interface    │      │
│   │                                     │     │                      │      │
│   │  Tools:                             │     │  shaktiman query     │      │
│   │   • search(query, budget?)          │     │  shaktiman index     │      │
│   │   • context(files, task, budget?)   │     │  shaktiman status    │      │
│   │   • symbols(file)                   │     │  shaktiman reindex   │      │
│   │   • dependencies(symbol)            │     │  shaktiman config    │      │
│   │   • summary(scope)                  │     │                      │      │
│   │                                     │     │                      │      │
│   └──────────────┬──────────────────────┘     └──────────┬───────────┘      │
│                  │                                       │                  │
└──────────────────┼───────────────────────────────────────┼──────────────────┘
                   │                                       │
                   ▼                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     QUERY & ASSEMBLY LAYER                                  │
│                                                                             │
│   ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐      │
│   │   Query Router   │───▶│ Retrieval Engine  │───▶│Context Assembler │      │
│   │                  │    │                  │    │                  │      │
│   │ • strategy       │    │ • vector search  │    │ • rank & merge   │      │
│   │   selection      │    │ • symbol lookup  │    │ • dedup overlaps │      │
│   │ • fallback       │    │ • graph traverse │    │ • budget fitting │      │
│   │   chain          │    │ • keyword search │    │ • metadata attach│      │
│   │ • cache check    │    │ • hybrid scoring │    │ • token counting │      │
│   └──────────────────┘    └────────┬─────────┘    └──────────────────┘      │
│                                    │                                        │
│                     ┌──────────────┼──────────────┐                         │
│                     ▼              ▼              ▼                         │
│              ┌───────────┐  ┌───────────┐  ┌───────────┐                   │
│              │  Vector   │  │  Symbol   │  │  Session  │                   │
│              │  Search   │  │  + Graph  │  │  Recency  │                   │
│              │  Score    │  │  Score    │  │  Score    │                   │
│              └───────────┘  └───────────┘  └───────────┘                   │
│                     │              │              │                         │
│                     └──────────────┼──────────────┘                         │
│                                    ▼                                        │
│                           ┌──────────────┐                                  │
│                           │Hybrid Ranker │                                  │
│                           │              │                                  │
│                           │ weighted sum │                                  │
│                           │ of 3 signals │                                  │
│                           └──────────────┘                                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        INDEX LAYER                                          │
│                                                                             │
│   ┌───────────────┐  ┌───────────────┐  ┌────────────┐  ┌──────────────┐   │
│   │ Symbol Index  │  │  Dependency   │  │  Vector    │  │   Session    │   │
│   │               │  │    Graph      │  │   Store    │  │    Store     │   │
│   │               │  │               │  │            │  │              │   │
│   │ • functions   │  │ • imports     │  │ • chunk    │  │ • access log │   │
│   │ • classes     │  │ • exports     │  │   embeds   │  │ • working    │   │
│   │ • methods     │  │ • call edges  │  │ • ANN      │  │   set        │   │
│   │ • types       │  │ • type refs   │  │   search   │  │ • edit       │   │
│   │ • variables   │  │ • module map  │  │            │  │   history    │   │
│   │ • file meta   │  │               │  │            │  │              │   │
│   │ • chunk map   │  │               │  │            │  │              │   │
│   └───────┬───────┘  └───────┬───────┘  └──────┬─────┘  └──────┬───────┘   │
│           │                  │                 │               │            │
└───────────┼──────────────────┼─────────────────┼───────────────┼────────────┘
            │                  │                 │               │
            ▼                  ▼                 ▼               ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                       STORAGE LAYER                                         │
│                                                                             │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                        SQLite Database                              │   │
│   │                                                                     │   │
│   │  Tables:                         │  Extensions:                     │   │
│   │   • files (path, hash, mtime)    │   • sqlite-vec (vector index)   │   │
│   │   • chunks (id, file, range,     │                                  │   │
│   │           content, tokens)       │  Or separate:                    │   │
│   │   • symbols (name, kind, chunk,  │   • hnswlib index file           │   │
│   │            signature)            │                                  │   │
│   │   • edges (src, dst, kind)       │                                  │   │
│   │   • embeddings (chunk_id, vec)   │                                  │   │
│   │   • sessions (ts, chunk_id, op)  │                                  │   │
│   │   • config (key, value)          │                                  │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│   Location: .shaktiman/index.db  (per project)                              │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────────────────────────┐
│                     INGESTION LAYER (Background)                            │
│                                                                             │
│   ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                  │
│   │ File Watcher │───▶│   Change     │───▶│  Tree-sitter │                  │
│   │              │    │  Detector    │    │   Parser     │                  │
│   │ • fsevents   │    │              │    │              │                  │
│   │ • polling    │    │ • hash diff  │    │ • AST        │                  │
│   │   fallback   │    │ • mtime      │    │ • queries    │                  │
│   │              │    │ • .gitignore │    │ • multi-lang │                  │
│   └──────────────┘    └──────────────┘    └──────┬───────┘                  │
│                                                  │                          │
│                                    ┌─────────────┼─────────────┐            │
│                                    ▼             ▼             ▼            │
│                             ┌───────────┐ ┌───────────┐ ┌───────────┐      │
│                             │  Chunker  │ │  Symbol   │ │   Dep     │      │
│                             │           │ │ Extractor │ │ Extractor │      │
│                             │ • split   │ │           │ │           │      │
│                             │   at sym  │ │ • names   │ │ • imports │      │
│                             │   bounds  │ │ • sigs    │ │ • calls   │      │
│                             │ • token   │ │ • kinds   │ │ • refs    │      │
│                             │   count   │ │ • ranges  │ │ • exports │      │
│                             └─────┬─────┘ └─────┬─────┘ └─────┬─────┘      │
│                                   │             │             │             │
│                                   ▼             ▼             ▼             │
│                            ┌──────────────────────────────────────┐         │
│                            │         Write to Index Layer         │         │
│                            │  (chunks, symbols, edges → SQLite)  │         │
│                            └──────────────────┬──────────────────┘         │
│                                               │                            │
│                                               ▼                            │
│                            ┌──────────────────────────────────────┐         │
│                            │    Async Embedding Pipeline          │         │
│                            │                                      │         │
│                            │  • queue of un-embedded chunks       │         │
│                            │  • batch embed (local or API)        │         │
│                            │  • write vectors to store            │         │
│                            │  • CPU-throttled, low priority       │         │
│                            └──────────────────────────────────────┘         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Component Specifications

### 2.1 Integration Layer

#### MCP Server
| Attribute    | Detail |
|---|---|
| **Purpose**  | Primary interface for coding agents. Exposes Shaktiman as MCP tools. |
| **Inputs**   | MCP tool calls from Claude Code or other MCP clients. |
| **Outputs**  | Token-budgeted context packages (ranked code chunks + metadata). |
| **Dependencies** | Query & Assembly Layer. |

**MCP Tools Exposed:**

| Tool | Purpose | Token Efficiency Gain |
|---|---|---|
| `search(query, budget?)` | Semantic + hybrid search. Returns ranked chunks. | Replaces 5-15 grep/glob/read cycles with 1 call. |
| `context(files, task, budget?)` | Assemble full context for a task given active files. | Agent gets all relevant code in one shot. |
| `symbols(file)` | List symbols in a file with signatures. | Agent sees structure without reading whole file. |
| `dependencies(symbol)` | Get callers, callees, importers for a symbol. | No manual grep for usages. |
| `summary(scope)` | High-level project/module/file summary. | Orientation without reading dozens of files. |

#### CLI Interface
| Attribute    | Detail |
|---|---|
| **Purpose**  | Developer-facing interface for debugging, manual queries, and index management. |
| **Inputs**   | Shell commands and arguments. |
| **Outputs**  | Formatted text to stdout. |
| **Dependencies** | Same internal layers as MCP server. |

---

### 2.2 Query & Assembly Layer

#### Query Router
| Attribute    | Detail |
|---|---|
| **Purpose**  | Select retrieval strategy based on index readiness and query type. |
| **Inputs**   | Parsed query + index status flags. |
| **Outputs**  | Retrieval strategy enum + parameters to Retrieval Engine. |
| **Dependencies** | Index Layer (status checks). |

**Fallback chain** (DC-5, NFR-11):
```
Level 0: Full hybrid      (semantic + structural + recency)  ← embeddings ready
Level 1: Structural+kw    (symbol index + keyword match)     ← embeddings pending
Level 2: Keyword only      (FTS on chunk content)             ← index exists, no graph
Level 3: Filesystem pass   (fall back to raw file reads)      ← index unavailable
```

#### Retrieval Engine
| Attribute    | Detail |
|---|---|
| **Purpose**  | Execute the retrieval strategy: query multiple indexes, compute scores. |
| **Inputs**   | Query + strategy + active file context + session data. |
| **Outputs**  | Unsorted list of candidate chunks, each with sub-scores. |
| **Dependencies** | Vector Store, Symbol Index, Dependency Graph, Session Store. |

**Hybrid scoring formula:**
```
score(chunk) = w_s * semantic_score      # embedding cosine similarity
             + w_g * structural_score    # graph distance from active context
             + w_r * recency_score       # inverse time since last edit/access
             + w_k * keyword_score       # BM25 or FTS match score

Default weights: w_s=0.45, w_g=0.25, w_r=0.20, w_k=0.10
Weights shift when embeddings unavailable (w_k fills w_s share).
```

#### Context Assembler
| Attribute    | Detail |
|---|---|
| **Purpose**  | Take ranked candidates, deduplicate, fit within token budget, attach metadata. |
| **Inputs**   | Ranked chunk list + token budget + assembly options. |
| **Outputs**  | Final context package: ordered chunks with metadata, guaranteed within budget. |
| **Dependencies** | Pre-computed token counts from Index Layer. |

**Assembly algorithm:**
```
1. Sort candidates by hybrid score (descending)
2. For top-ranked chunk:
   a. Add chunk to package
   b. Pull structural context (callers/callees/type defs) — add if budget allows
   c. Subtract tokens from remaining budget
3. Deduplicate: if new chunk overlaps >50% with existing, skip
4. Repeat until budget exhausted or candidates depleted
5. Attach per-chunk metadata: {path, symbol, lines, score, last_modified}
```

---

### 2.3 Index Layer

#### Symbol Index
| Attribute    | Detail |
|---|---|
| **Purpose**  | Fast lookup of code symbols: functions, classes, methods, types, variables. |
| **Inputs**   | Parsed AST from tree-sitter (via Ingestion Layer). |
| **Outputs**  | Symbol records with name, kind, file, byte range, line range, signature. |
| **Dependencies** | SQLite storage. |

#### Dependency Graph
| Attribute    | Detail |
|---|---|
| **Purpose**  | Directed graph of code relationships: imports, calls, type references. |
| **Inputs**   | Import/export/call analysis from tree-sitter queries. |
| **Outputs**  | Edge records (src_symbol → dst_symbol, edge_kind). Supports traversal queries. |
| **Dependencies** | Symbol Index (node identity), SQLite storage. |

**Edge kinds:** `imports`, `calls`, `type_ref`, `extends`, `implements`, `exports`

#### Vector Store
| Attribute    | Detail |
|---|---|
| **Purpose**  | Approximate nearest neighbor search over chunk embeddings. |
| **Inputs**   | Embedding vectors from Async Embedding Pipeline. |
| **Outputs**  | Top-K chunk IDs ranked by cosine similarity to query vector. |
| **Dependencies** | Embedding model (via Ingestion Layer). Storage via sqlite-vec or hnswlib. |

#### Session Store
| Attribute    | Detail |
|---|---|
| **Purpose**  | Track agent access patterns and file edit history for recency scoring. |
| **Inputs**   | Access events (chunk read, file edit) from MCP/CLI interactions + file watcher. |
| **Outputs**  | Recency scores per chunk/file. Working set membership. |
| **Dependencies** | SQLite storage. |

---

### 2.4 Ingestion Layer

#### File Watcher
| Attribute    | Detail |
|---|---|
| **Purpose**  | Detect file changes in the project directory in real time. |
| **Inputs**   | Filesystem events (create, modify, delete, rename). |
| **Outputs**  | Changed file paths to Change Detector. |
| **Dependencies** | OS filesystem events (fsevents on macOS, inotify on Linux). Polling fallback. |

#### Change Detector
| Attribute    | Detail |
|---|---|
| **Purpose**  | Determine which files actually need re-indexing (filter noise). |
| **Inputs**   | File paths from watcher + stored hashes/mtimes. |
| **Outputs**  | List of files requiring re-parse. |
| **Dependencies** | File metadata table in SQLite. .gitignore / .shaktimanignore patterns. |

#### Tree-sitter Parser
| Attribute    | Detail |
|---|---|
| **Purpose**  | Parse source files into ASTs. Run extraction queries for symbols, imports, calls. |
| **Inputs**   | Source file content + language grammar + query files. |
| **Outputs**  | AST nodes, symbol definitions, import/call sites. |
| **Dependencies** | Tree-sitter runtime + per-language grammars + `.scm` query files. |

#### Chunker
| Attribute    | Detail |
|---|---|
| **Purpose**  | Split parsed code into semantic chunks at symbol boundaries. |
| **Inputs**   | AST + symbol ranges from parser. |
| **Outputs**  | Chunk records: content, byte range, line range, pre-computed token count. |
| **Dependencies** | Parser output. Token counter (tiktoken or similar). |

**Chunking strategy:**
```
Primary:   One chunk per top-level symbol (function, class, type def)
Nested:    Class methods get their own chunks, linked to parent class chunk
Orphan:    Top-level code outside symbols → file-header chunk
Oversized: Chunks > max_tokens → split at logical sub-boundaries (blocks, loops)
```

#### Async Embedding Pipeline
| Attribute    | Detail |
|---|---|
| **Purpose**  | Generate embeddings for chunks in the background without blocking queries. |
| **Inputs**   | Queue of un-embedded chunk IDs + content. |
| **Outputs**  | Embedding vectors written to Vector Store. |
| **Dependencies** | Embedding model (Ollama local or API-based). CPU/GPU resources. |

**Design:**
```
• Runs in a separate thread/worker with low CPU priority
• Processes chunks in batches (e.g., 32 at a time)
• Newly changed chunks go to front of queue (prioritize active working set)
• Stale embeddings (chunk content changed) are invalidated and re-queued
• Progress tracked: system knows % embedded for fallback decisions
```

---

### 2.5 Storage Layer

Single SQLite database per project at `.shaktiman/index.db`.

**Schema overview:**
```sql
-- File tracking
files (id, path, content_hash, mtime, size, language, indexed_at)

-- Semantic chunks
chunks (id, file_id, symbol_name, kind, start_line, end_line,
        start_byte, end_byte, content, token_count, signature)

-- Symbol definitions
symbols (id, chunk_id, name, kind, file_id, line, signature)

-- Dependency edges
edges (id, src_symbol_id, dst_symbol_id, kind, file_id)

-- Embeddings (if using sqlite-vec)
embeddings (chunk_id, vector)  -- vector column via sqlite-vec extension

-- Session tracking
sessions (id, timestamp, chunk_id, operation, session_id)

-- Full-text search
chunks_fts VIRTUAL TABLE using fts5(content, symbol_name)
```

---

## 3. Data Flow Diagrams

### 3.1 Indexing Flow (Background, Continuous)

```
  ┌──────────┐     ┌───────────┐     ┌────────────┐     ┌──────────┐
  │  Source   │────▶│   File    │────▶│   Change   │────▶│  Tree-   │
  │  Files    │     │  Watcher  │     │  Detector  │     │  sitter  │
  └──────────┘     └───────────┘     └────────────┘     └────┬─────┘
                                                             │
                                          ┌──────────────────┼────────────┐
                                          ▼                  ▼            ▼
                                    ┌──────────┐      ┌──────────┐  ┌─────────┐
                                    │ Chunker  │      │  Symbol  │  │  Dep    │
                                    │          │      │ Extract  │  │ Extract │
                                    └────┬─────┘      └────┬─────┘  └────┬────┘
                                         │                 │             │
                                         ▼                 ▼             ▼
                                    ┌──────────────────────────────────────┐
                                    │           SQLite (sync write)        │
                                    │    chunks + symbols + edges + FTS   │
                                    └─────────────────┬────────────────────┘
                                                      │
                                                      ▼ (async, low-priority)
                                    ┌──────────────────────────────────────┐
                                    │      Embedding Pipeline              │
                                    │  chunk content → model → vector     │
                                    │  vector → sqlite-vec / hnswlib      │
                                    └──────────────────────────────────────┘

  Timeline:
  ──────────────────────────────────────────────────────────────▶
  File saved   Parser+Chunker     Symbols+Edges     Embeddings
    t=0          t=50ms             t=100ms          t=500ms-5s
              ◄──── sync, fast ────►              ◄── async ──►
              Index usable here                   Semantic usable here
              (structural fallback)               (full hybrid)
```

### 3.2 Query Flow (Foreground, Latency-Critical)

```
  ┌──────────┐     ┌──────────┐     ┌──────────────┐     ┌───────────────┐
  │  Agent   │────▶│   MCP    │────▶│    Query     │────▶│   Retrieval   │
  │  Query   │     │  Server  │     │   Router     │     │    Engine     │
  └──────────┘     └──────────┘     └──────────────┘     └───────┬───────┘
                                                                 │
                                          ┌──────────────────────┼──────┐
                                          ▼                      ▼      ▼
                                    ┌───────────┐         ┌────────┐ ┌──────┐
                                    │  Vector   │         │ Symbol │ │ Sess │
                                    │  Search   │         │ +Graph │ │ ion  │
                                    └─────┬─────┘         └───┬────┘ └──┬───┘
                                          │                   │         │
                                          ▼                   ▼         ▼
                                    ┌──────────────────────────────────────┐
                                    │          Hybrid Ranker               │
                                    │  score = 0.45*sem + 0.25*struct     │
                                    │        + 0.20*recency + 0.10*kw     │
                                    └─────────────────┬────────────────────┘
                                                      │
                                                      ▼
                                    ┌──────────────────────────────────────┐
                                    │       Context Assembler              │
                                    │                                      │
                                    │  1. Take top-N by score              │
                                    │  2. Expand structural neighbors      │
                                    │  3. Dedup overlapping chunks         │
                                    │  4. Pack into token budget           │
                                    │  5. Attach metadata                  │
                                    └─────────────────┬────────────────────┘
                                                      │
                                                      ▼
                                    ┌──────────────────────────────────────┐
                                    │       Context Package (Response)     │
                                    │                                      │
                                    │  { chunks: [                         │
                                    │      { path, symbol, lines,          │
                                    │        content, score },             │
                                    │    ...                               │
                                    │    ],                                │
                                    │    budget_used: 6200,                │
                                    │    budget_limit: 8000,               │
                                    │    strategy: "hybrid"                │
                                    │  }                                   │
                                    └──────────────────────────────────────┘

  Latency budget (p95 < 200ms):
  ──────────────────────────────────────────▶
  Parse query    Vector+Symbol search    Rank+Assemble
    5ms              80-120ms               20-40ms
```

### 3.3 Push Context Flow (Proactive)

```
  ┌───────────────┐     ┌──────────────────────┐     ┌──────────────────┐
  │ Agent starts  │────▶│ MCP Server detects   │────▶│ Pre-assemble     │
  │ new task      │     │ task/file context     │     │ context package  │
  └───────────────┘     └──────────────────────┘     └────────┬─────────┘
                                                              │
                        ┌──────────────────────┐              │
                        │ Return as MCP        │◀─────────────┘
                        │ resource/prompt       │
                        └──────────────────────┘

  Trigger conditions:
  • Agent calls any Shaktiman tool → session store updated → future queries biased
  • Agent opens a file → related symbols pre-fetched into warm cache
  • New task description received → proactive context assembled and offered
```

---

## 4. Key Interactions

### 4.1 Agent → Shaktiman

```
Agent                              Shaktiman
  │                                    │
  │  search("auth middleware logic")   │
  │───────────────────────────────────▶│
  │                                    │──▶ embed query
  │                                    │──▶ vector search (top 20)
  │                                    │──▶ symbol/graph score boost
  │                                    │──▶ recency score boost
  │                                    │──▶ rank + assemble (budget: 8K)
  │  context package (3 chunks, 6.2K)  │
  │◀───────────────────────────────────│
  │                                    │──▶ log access → session store
  │                                    │
```

Agent gets relevant code in **1 tool call** instead of:
```
Without Shaktiman:
  grep("auth") → 12 matches → read(file1) → read(file2) → grep("middleware")
  → read(file3) → symbols(file3) → read(file4) → ...
  = 8-12 tool calls, 15K+ tokens read, 30+ seconds
```

### 4.2 Async Enrichment → Index

```
Embedding Pipeline                     Index
  │                                      │
  │  dequeue batch (32 chunk IDs)        │
  │◀─────────────────────────────────────│
  │                                      │
  │  generate embeddings (local model)   │
  │──────── (500ms - 2s) ──────────────▶ │
  │                                      │
  │  write vectors                       │
  │─────────────────────────────────────▶│
  │                                      │──▶ update embedding_status
  │                                      │──▶ update index readiness %
  │                                      │
  │  (repeat until queue empty)          │
```

Key property: **queries never wait on this pipeline.** If embeddings aren't ready, Query Router uses Level 1 fallback (structural + keyword).

### 4.3 Fallback to Filesystem

```
Query Router decision tree:

  Is index available?
  ├─ NO → Level 3: read raw files, return as-is (chunked by line count)
  │
  ├─ YES → Are embeddings ready?
  │   ├─ YES (>80% embedded) → Level 0: full hybrid
  │   ├─ PARTIAL (20-80%) → Level 0 for embedded chunks + Level 1 for rest
  │   └─ NO (<20%) → Level 1: structural + keyword (FTS)
  │
  └─ Is graph available?
      ├─ YES → include structural scoring
      └─ NO → Level 2: keyword only (FTS on chunks)
```

---

## 5. Performance-Critical Paths

### 5.1 Hot Path: Query → Response (< 200ms target)

| Step | Budget | Optimization |
|---|---|---|
| Parse & embed query | 5-10ms | Cache recent query embeddings |
| Vector ANN search | 40-80ms | hnswlib/sqlite-vec with pre-built index. Limit to top-50 candidates. |
| Symbol + graph lookup | 10-30ms | SQLite indexes on symbol name, file_id. Graph traversal BFS depth=2. |
| Session recency lookup | 5ms | In-memory LRU of recent session events |
| Hybrid scoring | 5ms | Simple weighted sum, no complex computation |
| Context assembly | 20-40ms | Pre-computed token counts. Greedy packing. |
| **Total** | **85-170ms** | **Well within 200ms p95** |

### 5.2 Warm Path: Incremental Re-index (< 500ms target)

| Step | Budget | Optimization |
|---|---|---|
| Detect change | 1ms | fsevents delivers path directly |
| Read + hash file | 1-5ms | Small files; skip if mtime unchanged |
| Tree-sitter parse | 10-50ms | Incremental parse if supported; full parse otherwise |
| Extract symbols + deps | 5-20ms | Tree-sitter queries are fast |
| Chunk + token count | 5-10ms | Count during chunking, one pass |
| Write to SQLite | 5-10ms | Batch within transaction |
| **Total** | **27-96ms** | **Well within 500ms** |
| Queue for embedding | 0ms | Append to queue, no blocking |

### 5.3 Cold Path: Full Index (< 60s for 100K lines)

| Step | Budget | Optimization |
|---|---|---|
| File discovery | 1-2s | Respect .gitignore, parallel stat |
| Parse all files | 15-30s | Parallel across CPU cores (tree-sitter is per-file) |
| Extract + chunk | 5-10s | Combined with parse pass |
| Write to SQLite | 2-5s | Batch inserts in large transactions |
| **Total (sync)** | **23-47s** | **Within 60s** |
| Embedding (async) | 2-10 min | Runs in background after index is usable |

---

## 6. Token Efficiency Analysis

### Where tokens are saved:

```
┌─────────────────────────────────────────────────────────────────┐
│                    TOKEN EFFICIENCY MAP                          │
│                                                                 │
│  Without Shaktiman          With Shaktiman                      │
│  ─────────────────          ──────────────                      │
│                                                                 │
│  Agent reads whole ────────▶ Chunk-level retrieval               │
│  files (2K tokens           (200-500 tokens per                  │
│  per file × 5 files         relevant function × 3 chunks        │
│  = 10K tokens)              = 900 tokens)                       │
│                             SAVING: ~90% per retrieval          │
│                                                                 │
│  Agent greps + reads ──────▶ Single search call                 │
│  in 8-12 tool calls         returns ranked results              │
│  (each with prompt          in 1 call                           │
│  overhead = ~500            SAVING: ~85% tool overhead          │
│  tokens × 10 = 5K)                                              │
│                                                                 │
│  Agent gets irrelevant ────▶ Budget-fitted, ranked              │
│  matches mixed in           results only                         │
│  (30% noise = 3K            SAVING: ~30% noise removal          │
│  wasted tokens)                                                  │
│                                                                 │
│  Agent re-discovers ───────▶ Session store biases               │
│  same files every            toward known working set            │
│  session (5K tokens          SAVING: ~100% redundant discovery  │
│  of exploration)                                                 │
│                                                                 │
│  Estimated total savings per typical task:                       │
│  Before: ~25K input tokens    After: ~8K input tokens            │
│  REDUCTION: ~68%                                                │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Metadata overhead budget (< 5% per chunk):

```
Chunk content:  ~300 tokens (typical function)
Metadata:         ~12 tokens  (path + symbol + lines + score)
Overhead:        4.0%  ✓ within budget
```

---

## 7. Developer Experience Flow

```
  Developer installs Shaktiman
           │
           ▼
  shaktiman init          ← creates .shaktiman/ in project root
           │                 scans project, detects languages
           ▼
  First cold index        ← runs in background, ~30-45s
           │                 system usable immediately (keyword fallback)
           ▼
  Embeddings generate     ← async, 2-10 min depending on project size
           │                 system progressively improves (hybrid kicks in)
           ▼
  Developer starts        ← Claude Code connects via MCP
  Claude Code               sees shaktiman tools available
           │
           ▼
  Agent uses search()     ← gets relevant code in 1 call
  and context()             no manual file pointing needed
           │
           ▼
  Files change            ← watcher detects, re-indexes in <500ms
           │                 agent's next query sees updated code
           ▼
  Session continues       ← recency bias improves with usage
                            agent "learns" the working set
```

---

## 8. Architecture Decision Records

| Decision | Choice | Rationale |
|---|---|---|
| **Storage** | Single SQLite DB | Zero-config, embedded, transactional, handles all metadata. Per DC-11. |
| **Vector index** | sqlite-vec (primary), hnswlib (fallback) | sqlite-vec keeps everything in one DB. hnswlib if perf insufficient. |
| **Parser** | Tree-sitter | Multi-language via grammars, fast, battle-tested. Per DC-12. |
| **Embedding** | Pluggable: Ollama default, API optional | Local-first per DC-1/DC-2. Nomic-embed-text as default local model. |
| **Integration** | MCP server | Native Claude Code support per DC-9/DC-10. No agent modifications. |
| **Chunking** | Symbol-boundary | Functions/classes as natural units. Better than fixed-size for code. |
| **Ranking** | Weighted hybrid (4 signals) | No single signal is sufficient. Weights are tunable per project. |
| **Async embedding** | Background worker queue | Never blocks queries per DC-4. Progressive enhancement. |
| **Fallback** | 4-level degradation chain | System usable from first second per DC-5. |
| **Process model** | Single process, multi-threaded | Simpler ops than multi-process. Watcher + embedder as worker threads. |

---

## Status

**Step 2: Architecture** — Complete. Awaiting confirmation before proceeding to Step 3 (Detailed Component Design / Implementation Planning).
