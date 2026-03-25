# Shaktiman: Implementation Plan (Final)

> Step-by-step build plan from zero to working system.
> Derived from requirements, architecture v3, component design, data model, and API specification (with all addenda).
> Incorporates all solution-fit analysis findings.

---

## 0. Final Implementation Decisions

| Decision | Rationale |
|---|---|
| **MCP stdio replaces ZeroMQ** | Primary consumer is Claude Code via MCP (DC-9, DC-10). ZMQ added ~15h of unnecessary complexity (registry, liveness, heartbeat, HWM, socket security). MCP provides transport, tools, resources, and notifications natively. |
| **Brute-force vector store is default** | Satisfies DC-11 (no external DB) and DC-13 (embedded vector store). O(n) cosine scan over ~75K chunks takes ~30ms — within 200ms budget. Qdrant available as optional backend via `VectorStore` interface. |
| **CSR graph deferred to Phase 3+** | SQLite recursive CTEs perform 3-8ms at 100K lines, 30-80ms at 1M lines. Architecture already tolerates this (A3 fallback). Build CSR only if profiling shows need. |
| **TypeScript-only in Phase 1** | Tree-sitter queries are the highest-effort task (IP-12). Ship MVP with one language, add others incrementally. |
| **Retrieval evaluation harness in Phase 1** | 80% relevance criterion has no testing infrastructure. Must validate before building more signals. |
| **JSON replaces MessagePack** | MCP uses JSON-RPC. Single serialization format. Human-readable. Standard Go `encoding/json`. |

---

## 1. Tech Stack

| Component | Choice | Rationale |
|---|---|---|
| **Language** | **Go** | Goroutines map to Writer Thread / enrichment pool / watcher. Single binary. Fast compile times. |
| **SQLite** | `github.com/mattn/go-sqlite3` (CGo) | WAL mode, FTS5, connection pool. Most mature Go driver. |
| **Parsing** | `github.com/smacker/go-tree-sitter` | Standard Go tree-sitter bindings (CGo). |
| **Vector Store** | **Brute-force in-process** (default) | `[]float32` slices + cosine similarity. No external deps. `VectorStore` interface for Qdrant swap. |
| **Agent Integration** | **MCP stdio server** | `github.com/mark3labs/mcp-go` SDK. Claude Code starts shaktimand as MCP server process. |
| **Serialization** | `encoding/json` (stdlib) | JSON-RPC for MCP. Single format. Human-readable. |
| **File Watching** | `github.com/fsnotify/fsnotify` | Cross-platform (inotify/kqueue/FSEvents). |
| **Tokenizer** | `github.com/pkoukk/tiktoken-go` | cl100k_base with 95% safety margin. |
| **CLI** | `github.com/spf13/cobra` | Standard Go CLI framework. |
| **Hashing** | `crypto/sha256` (stdlib) | Content hashes and repo_id. |
| **UUID** | `github.com/google/uuid` | UUIDv4 for request correlation. |
| **Logging** | `log/slog` (stdlib) | Structured logging, Go 1.21+. |
| **LRU Cache** | `github.com/hashicorp/golang-lru/v2` | Session store, query embedding cache. |
| **Gitignore** | `github.com/sabhiram/go-gitignore` | .gitignore + .shaktimanignore pattern matching. |
| **FTS5** | SQLite built-in | External content mode with sync triggers (DM-1). |
| **Graph Traversal** | **SQLite recursive CTEs** (Phase 2), optional CSR (Phase 3+) | Adequate at 100K lines (~3-8ms). CSR only if profiling shows need. |

### Concurrency Model (Design → Go)

| Design Concept | Go Implementation |
|---|---|
| Writer Thread (C8) | Single goroutine reading from `chan WriteJob` |
| Priority lanes (A1) | 4 priority channels (Phase 4), cascading select (IP-8) |
| File mutex (A5) | `sync.Mutex` per file path in `sync.Map[string, *sync.Mutex]` |
| Enrichment pool | N goroutines, each owns its own `*sitter.Parser` (IP-2) |
| Circuit breaker (A7) | State machine with `sync.Mutex` (IP-15) |
| Session LRU (CA-4) | `golang-lru.Cache` with `sync.RWMutex` |
| Version counter (A4) | `atomic.Uint64` incremented on each write commit |
| SQLite isolation | Dual `sql.DB` instances: writer (MaxOpenConns=1) + reader pool (MaxOpenConns=4) (IP-3) |

---

## 2. Project Structure

```
/shaktiman
├── go.mod
├── go.sum
├── cmd/
│   ├── shaktimand/                   # daemon / MCP server binary
│   │   └── main.go                   # entry point, boot sequence (CA-7)
│   └── shaktiman/                    # CLI binary (direct in-process queries)
│       └── main.go                   # cobra-based CLI
│
├── internal/
│   ├── types/                        # shared types across all packages
│   │   ├── entities.go               # FileRecord, ChunkRecord, SymbolRecord, EdgeRecord
│   │   ├── interfaces.go            # VectorStore, GraphStore, MetadataStore interfaces (IP-11)
│   │   ├── config.go                 # ShaktimanConfig, defaults
│   │   └── constants.go             # limits, timeouts, weights
│   │
│   ├── parser/                       # C7 (partial): tree-sitter parsing + chunking
│   │   ├── parser.go                 # tree-sitter integration, grammar loading
│   │   ├── chunker.go               # chunk splitting (function/class/method/block)
│   │   ├── symbols.go               # symbol extraction (name, kind, signature)
│   │   ├── edges.go                  # edge extraction (imports, calls, type_ref) — Phase 2
│   │   ├── languages.go             # language registry
│   │   └── queries/                  # tree-sitter query files per language
│   │       ├── typescript.scm        # Phase 1
│   │       ├── python.scm            # Phase 2
│   │       ├── go.scm                # Phase 2
│   │       └── rust.scm              # Phase 3
│   │
│   ├── storage/                      # C5 + C6: SQLite metadata + diff store + graph queries
│   │   ├── db.go                     # dual sql.DB: writer (1 conn) + reader pool (4 conn) (IP-3)
│   │   ├── schema.go                # DDL for ALL tables, migrations
│   │   ├── metadata.go              # files, chunks, symbols CRUD
│   │   ├── diff.go                   # diff_log, diff_symbols CRUD — Phase 2
│   │   ├── fts.go                    # FTS5 queries + trigger management
│   │   └── graph.go                  # SQLite recursive CTE-based BFS — Phase 2
│   │
│   ├── vector/                       # C4: vector store
│   │   ├── store.go                  # BruteForceStore implementation (default)
│   │   ├── qdrant.go                # QdrantStore implementation (optional) — Phase 3+
│   │   └── embedding.go             # embedding worker, circuit breaker, Ollama client — Phase 3
│   │
│   ├── core/                         # C2: query engine, ranker, assembler
│   │   ├── query.go                  # query pipeline orchestration
│   │   ├── retrieval.go             # candidate retrieval (FTS5, vector, graph)
│   │   ├── ranker.go                # hybrid ranker (keyword → 3-signal → 5-signal)
│   │   ├── assembler.go             # context assembler (budget-fitted)
│   │   ├── session.go               # session store (LRU, decay, working set) — Phase 4
│   │   └── fallback.go              # fallback chain (L0-L3)
│   │
│   ├── daemon/                       # daemon process lifecycle
│   │   ├── daemon.go                # lifecycle (start, shutdown)
│   │   ├── watcher.go               # C9: file watcher (fsnotify, debounce, gitignore)
│   │   ├── enrichment.go            # C7: enrichment pipeline orchestration
│   │   ├── writer.go                # C8: writer goroutine (single → priority lanes)
│   │   └── config.go                # per-repo configuration loading
│   │
│   ├── mcp/                          # MCP server (primary agent interface)
│   │   ├── server.go                # MCP stdio server setup + lifecycle
│   │   ├── tools.go                  # MCP tool handlers: search, context, symbols, deps, diff, summary
│   │   ├── resources.go             # MCP resources: context/active, workspace/summary
│   │   └── notifications.go        # MCP notifications: context/changed (push mode) — Phase 4
│   │
│   └── eval/                         # retrieval quality evaluation
│       ├── harness.go               # evaluation runner
│       └── testcases/               # curated query → expected results
│           └── typescript.json       # 10-20 TypeScript evaluation cases
│
├── testdata/                         # test fixtures
│   ├── small_ts_project/             # TypeScript test codebase
│   └── small_py_project/            # Python test codebase (Phase 2)
│
├── docs/                             # design documents (existing)
└── .gitignore
```

### Import Graph (no cycles)

```
types  ←── parser
  ↑         ↑
  │         │
storage ←──┘
  ↑
  │
vector (implements types.VectorStore)
  ↑
  │
core (depends on types.VectorStore, types.GraphStore interfaces)
  ↑
  │
mcp ←── daemon ←── cmd/shaktimand
           │
           └─────── cmd/shaktiman (CLI reads SQLite directly, no MCP)
```

### Daemon vs CLI Architecture

```
Claude Code ──stdio──▶ shaktimand (MCP server)
                         ├── indexes repo on startup
                         ├── watches files (fsnotify)
                         ├── handles MCP tool calls (search, context, ...)
                         └── pushes resource updates (notifications)

Developer  ──terminal──▶ shaktiman (CLI)
                         ├── opens SQLite read-only (direct)
                         ├── status, search, symbols (in-process queries)
                         └── start/stop daemon (process management)
```

- **shaktimand**: MCP stdio server. Started by Claude Code's MCP configuration. Long-running. Does indexing, watching, query handling.
- **shaktiman**: CLI tool. Opens the SQLite database directly (read-only) for status/search queries. Can start/stop the daemon for manual use.

---

## 3. Phase 1 — Minimal Working System (MVP)

### 3.1 Goal

A working MCP server that Claude Code can connect to, index TypeScript files with tree-sitter, and retrieve code context via keyword search. Falls back to raw file content when nothing is indexed. Includes retrieval quality evaluation harness.

### 3.2 Features

| Feature | Maps To |
|---|---|
| MCP stdio server with tool registration | C1, DC-9, DC-10 |
| File scanning (walk, .gitignore) | C9 (partial) |
| Tree-sitter parsing → chunks + symbols (TypeScript) | C7 (partial) |
| SQLite storage (ALL tables, WAL mode, dual DB) | C5, C8 (basic) |
| FTS5 keyword search | C2 (partial) |
| Keyword-only context assembly with token budget | C2 (Level 2 fallback) |
| Filesystem fallback (Level 3) | C2 fallback chain |
| MCP tools: search, context | FR-5, FR-8 |
| MCP resource: workspace/summary | AP-1 |
| CLI: start, stop, status, search | C1 (CLI) |
| Retrieval evaluation harness | Success criterion validation |

### 3.3 Packages Required

- `internal/types` — entity types, interfaces, config
- `internal/parser` — tree-sitter parsing, chunking, symbol extraction (TypeScript only)
- `internal/storage` — full SQLite schema, metadata CRUD, FTS5, basic writer goroutine
- `internal/core` — keyword retrieval, basic context assembler, fallback chain stub
- `internal/mcp` — MCP stdio server, tool handlers (search, context), resource (summary)
- `internal/daemon` — daemon lifecycle, file scanner, basic enrichment pipeline
- `internal/eval` — retrieval evaluation harness
- `cmd/shaktimand` — MCP server binary
- `cmd/shaktiman` — CLI binary

### 3.4 Dependencies (go.mod)

```
module github.com/user/shaktiman

go 1.23

require (
    github.com/mattn/go-sqlite3 v1.14.24
    github.com/smacker/go-tree-sitter v0.0.0-20240827094652-0d261e6a13d3
    github.com/mark3labs/mcp-go v0.25.0
    github.com/spf13/cobra v1.8.1
    github.com/google/uuid v1.6.0
    github.com/pkoukk/tiktoken-go v0.1.7
    github.com/sabhiram/go-gitignore v0.0.0-20210923224102-525f6e181f06
    github.com/hashicorp/golang-lru/v2 v2.0.7
)
```

### 3.5 Task Breakdown

| # | Task | Est. | Deliverable |
|---|---|---|---|
| 1.1 | Initialize Go module + directory structure + empty packages | 1h | `go build ./...` succeeds |
| 1.2 | Define shared types: `FileRecord`, `ChunkRecord`, `SymbolRecord`, config | 2h | `internal/types` compiles |
| 1.3 | Define interfaces: `VectorStore`, `GraphStore`, `MetadataStore` | 1h | Interfaces in `types/interfaces.go` |
| 1.4 | SQLite schema (ALL tables from data model) + migrations | 2h | `schema.go` creates all tables on fresh DB |
| 1.5 | SQLite dual DB: writer (1 conn) + reader pool (4 conn) (IP-3) | 2h | `db.go` — `Open()`, `WithWriteTx()`, `QueryContext()` |
| 1.6 | Metadata store: insert/query files, chunks, symbols | 3h | `metadata.go` — CRUD functions |
| 1.7 | FTS5 setup: virtual table, sync triggers (DM-1) | 1h | `fts.go` — `KeywordSearch()` function |
| 1.8 | Basic writer goroutine: single channel, ordered shutdown (IP-4) | 2h | `writer.go` — reads `chan WriteJob`, commits |
| 1.9 | File scanner: walk directory, resolve symlinks (DM-7), .gitignore + .shaktimanignore | 2h | `watcher.go` — `ScanRepo()` returns file list |
| 1.10 | Tree-sitter parser: load TypeScript grammar, parse file → tree | 2h | `parser.go` — `Parse()` for TypeScript |
| 1.11 | Chunk splitter: extract functions/classes/methods/blocks | 3h | `chunker.go` — file → `[]ChunkRecord` |
| 1.12 | Symbol extractor: names, kinds, signatures, visibility | 2h | `symbols.go` — tree → `[]SymbolRecord` |
| 1.13 | Tree-sitter TypeScript query file (.scm) | 3h | `queries/typescript.scm` |
| 1.14 | Token counting: tiktoken-go per chunk during enrichment | 1h | Chunks have `token_count` populated |
| 1.15 | Enrichment pipeline (basic): scan → parse → chunk → symbols → write | 3h | `enrichment.go` — batch index a repo |
| 1.16 | Keyword retrieval: FTS5 search → ranked chunk list | 2h | `retrieval.go` — `KeywordSearch()` |
| 1.17 | Basic context assembler: budget-fitted chunk selection | 2h | `assembler.go` — greedy packing with token budget |
| 1.18 | Filesystem fallback (Level 3): read raw files when index empty | 1h | `query.go` — fallback path |
| 1.19 | Fallback chain stub: L2 (keyword) → L3 (filesystem) decision | 1h | `fallback.go` — route based on index state |
| 1.20 | MCP stdio server: setup, tool registration, lifecycle | 3h | `server.go` — working MCP server |
| 1.21 | MCP tool handlers: `search`, `context` | 3h | `tools.go` — handle tool calls |
| 1.22 | MCP resource: `workspace/summary` | 1h | `resources.go` — index stats |
| 1.23 | Daemon lifecycle: startup indexing, graceful shutdown | 2h | `daemon.go` — start/stop |
| 1.24 | CLI: start, stop, status, search commands | 2h | `cmd/shaktiman` binary |
| 1.25 | Request validation: parameter bounds (AP-5) | 1h | Validation in tool handlers |
| 1.26 | Retrieval evaluation harness: framework + 10-20 TypeScript test cases | 3h | `eval/` — `go test` runnable |
| 1.27 | Integration test: MCP server → index → search → verify results | 2h | Passing end-to-end test |

**Total: ~52h (~1.7 weeks)**

### 3.6 Key Implementation Patterns

**MCP server setup (task 1.20):**

```go
// internal/mcp/server.go

func StartServer(ctx context.Context, engine *core.QueryEngine, daemon *daemon.Daemon) error {
    s := mcp.NewServer(
        mcp.WithName("shaktiman"),
        mcp.WithVersion("0.1.0"),
    )

    // Register tools
    s.AddTool(mcp.Tool{
        Name:        "search",
        Description: "Search indexed code by keyword or natural language query",
        InputSchema: searchInputSchema(),
    }, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        return handleSearch(ctx, engine, req)
    })

    s.AddTool(mcp.Tool{
        Name:        "context",
        Description: "Assemble relevant code context for a task, fitted to a token budget",
        InputSchema: contextInputSchema(),
    }, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        return handleContext(ctx, engine, req)
    })

    // Register resources
    s.AddResource(mcp.Resource{
        URI:         "shaktiman://workspace/summary",
        Name:        "Workspace Summary",
        Description: "Overview of indexed codebase: files, symbols, languages, health",
        MimeType:    "application/json",
    }, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
        return handleWorkspaceSummary(ctx, daemon)
    })

    // Start stdio transport
    return mcp.ServeStdio(s)
}
```

**Writer goroutine with ordered shutdown (tasks 1.8, IP-4):**

```go
// internal/daemon/writer.go

type WriterManager struct {
    ch        chan WriteJob
    producers sync.WaitGroup
    db        *storage.DB
}

func (wm *WriterManager) Run(ctx context.Context) {
    for {
        select {
        case job := <-wm.ch:
            err := wm.db.WithWriteTx(func(tx *sql.Tx) error {
                return processWriteJob(tx, job)
            })
            if err != nil {
                slog.Error("write failed", "err", err)
            }
            if job.Done != nil {
                job.Done <- err
            }
        case <-ctx.Done():
            wm.producers.Wait()   // wait for all senders to stop
            close(wm.ch)          // safe: no more senders
            for job := range wm.ch {  // drain remaining
                _ = wm.db.WithWriteTx(func(tx *sql.Tx) error {
                    return processWriteJob(tx, job)
                })
            }
            return
        }
    }
}
```

**Retrieval evaluation harness (task 1.26):**

```go
// internal/eval/harness.go

type TestCase struct {
    Query           string   `json:"query"`
    ExpectedFiles   []string `json:"expected_files"`    // files that should appear in results
    ExpectedSymbols []string `json:"expected_symbols"`  // symbols that should appear
}

type EvalResult struct {
    Recall    float64  // fraction of expected items found in top-10
    Precision float64  // fraction of top-10 items that are expected
    MRR       float64  // mean reciprocal rank of first relevant result
}

func Evaluate(engine *core.QueryEngine, cases []TestCase) []EvalResult {
    results := make([]EvalResult, len(cases))
    for i, tc := range cases {
        searchResults, _ := engine.Search(context.Background(), tc.Query, 10)
        results[i] = computeMetrics(searchResults, tc)
    }
    return results
}
```

### 3.7 What You Can DO After Phase 1

```
# Configure Claude Code to use Shaktiman (in MCP settings)
{
  "mcpServers": {
    "shaktiman": {
      "command": "/path/to/shaktimand",
      "args": ["/path/to/my-project"]
    }
  }
}

# Claude Code now has access to Shaktiman tools:
> Tool: search("validate user token")
> Result: [
>   { path: "src/auth/login.ts", symbol: "validateToken", lines: "89-112", score: 0.85 },
>   { path: "src/auth/middleware.ts", symbol: "authGuard", lines: "15-45", score: 0.72 }
> ]

> Tool: context("how does user login work", budget_tokens=4096)
> Result: {
>   chunks: [ ... 8 budget-fitted ranked chunks ... ],
>   total_tokens: 3842,
>   strategy: "keyword_l2"
> }

# CLI works independently (reads SQLite directly)
$ ./shaktiman status /path/to/my-project
> Files: 342 | Chunks: 2840 | Symbols: 1920 | Strategy: keyword_only

$ ./shaktiman search "authentication" /path/to/my-project
> src/auth/login.ts:validateToken (89-112) — 0.85

# Run evaluation harness
$ go test ./internal/eval/ -v
> TypeScript recall@10: 0.75, precision@10: 0.60, MRR: 0.82
```

---

## 4. Phase 2 — Structured Intelligence

### 4.1 Goal

Add dependency graph via SQLite CTEs, diff tracking, file watching, incremental updates, and expand language support to Python and Go. The system now understands code relationships and change history.

### 4.2 Features

| Feature | Maps To |
|---|---|
| Edge extraction (imports, calls, type_ref, extends, implements) | C7 (edges) |
| Pending edge resolution (cross-file) | CA-1 |
| SQLite recursive CTE-based graph traversal (BFS) | C3 (via SQLite) |
| Diff engine (file change tracking) | C6 |
| Structural scoring (graph proximity) | C2 ranker |
| Change scoring (recency, magnitude) | C2 ranker |
| Hybrid ranker (keyword + structural + change — 3-signal) | C2 |
| Context assembler with structural expansion | C2 assembler (CA-5) |
| File watcher (incremental updates) | C9 |
| Incremental enrichment | C7 |
| Python + Go tree-sitter queries | FR-4 (language expansion) |
| MCP tools: symbols, dependencies, diff, enrich | API spec |

### 4.3 Packages Modified/Added

- `internal/parser` — ADD edge extraction, Python + Go grammars and queries
- `internal/storage` — ADD `graph.go` (SQLite CTE BFS), `diff.go`, pending_edges
- `internal/core` — ADD structural scoring, change scoring, 3-signal hybrid ranker, structural expansion
- `internal/daemon` — ADD file watcher, incremental enrichment, writer hash guard (CA-3)
- `internal/mcp` — ADD tools: symbols, dependencies, diff, enrich

### 4.4 Task Breakdown

| # | Task | Est. | Deliverable |
|---|---|---|---|
| 2.1 | Python tree-sitter query file (chunks + symbols) | 3h | `queries/python.scm` |
| 2.2 | Go tree-sitter query file (chunks + symbols) | 3h | `queries/go.scm` |
| 2.3 | Language registry: detect language from file extension, load grammar | 1h | `languages.go` — multi-language support |
| 2.4 | Edge extraction: imports, calls, type_ref from tree-sitter (TypeScript) | 4h | `edges.go` — `ExtractEdges()` |
| 2.5 | Edge extraction queries for Python | 3h | Updated `python.scm` |
| 2.6 | Edge extraction queries for Go | 3h | Updated `go.scm` |
| 2.7 | Pending edges table + two-phase resolution logic (CA-1) | 2h | `metadata.go` — cross-file edge resolution |
| 2.8 | SQLite recursive CTE BFS: `Neighbors(symbolID, depth, direction)` | 3h | `graph.go` — forward + reverse traversal |
| 2.9 | Diff engine: detect file changes, compute diff_log entries | 3h | `diff.go` — diff on enrichment |
| 2.10 | Diff symbols tracking | 1h | `diff.go` — affected symbols per diff |
| 2.11 | Structural scoring: graph proximity signal via SQLite BFS | 2h | `ranker.go` — `structuralScore()` |
| 2.12 | Change scoring: recency + magnitude signal | 2h | `ranker.go` — `changeScore()` |
| 2.13 | Hybrid ranker: keyword + structural + change (3-signal) | 2h | `ranker.go` — `HybridRank()` |
| 2.14 | Context assembler: structural expansion (30% budget, CA-5) | 2h | `assembler.go` — BFS neighbor expansion |
| 2.15 | File watcher: fsnotify, debounce (200ms), .gitignore | 3h | `watcher.go` — `WatchRepo()` goroutine |
| 2.16 | Incremental enrichment: single-file re-parse on change | 2h | `enrichment.go` — `EnrichFile()` |
| 2.17 | Writer goroutine: hash guard (CA-3), edge cleanup (DM-3) | 2h | `writer.go` — hash check, RESTRICT handling |
| 2.18 | MCP tools: symbols, dependencies, diff, enrich | 3h | `tools.go` — new tool handlers |
| 2.19 | Evaluation harness: add Python test cases | 1h | `eval/testcases/python.json` |
| 2.20 | Integration test: edit file → watcher → re-index → query reflects change | 2h | Passing incremental test |

**Total: ~47h (~1.5 weeks)**

### 4.5 Key Implementation Patterns

**SQLite recursive CTE BFS (task 2.8):**

```go
// internal/storage/graph.go

// Neighbors returns symbol IDs reachable from the given symbol via BFS.
// direction: "outgoing" (callees) or "incoming" (callers)
func (db *DB) Neighbors(symbolID int64, maxDepth int, direction string) ([]int64, error) {
    var query string
    if direction == "outgoing" {
        query = `
            WITH RECURSIVE reachable AS (
                SELECT dst_symbol_id AS id, 1 AS depth
                FROM edges WHERE src_symbol_id = ?
                UNION ALL
                SELECT e.dst_symbol_id, r.depth + 1
                FROM edges e
                JOIN reachable r ON e.src_symbol_id = r.id
                WHERE r.depth < ?
            )
            SELECT DISTINCT id FROM reachable`
    } else {
        query = `
            WITH RECURSIVE reachable AS (
                SELECT src_symbol_id AS id, 1 AS depth
                FROM edges WHERE dst_symbol_id = ?
                UNION ALL
                SELECT e.src_symbol_id, r.depth + 1
                FROM edges e
                JOIN reachable r ON e.dst_symbol_id = r.id
                WHERE r.depth < ?
            )
            SELECT DISTINCT id FROM reachable`
    }

    rows, err := db.QueryContext(context.Background(), query, symbolID, maxDepth)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var result []int64
    for rows.Next() {
        var id int64
        rows.Scan(&id)
        result = append(result, id)
    }
    return result, nil
}
```

**File watcher with debounce (task 2.15):**

```go
// internal/daemon/watcher.go

func (w *Watcher) WatchRepo(ctx context.Context, repoPath string) (<-chan FileChangeEvent, error) {
    fsWatcher, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    eventCh := make(chan FileChangeEvent, 10000)
    pending := make(map[string]time.Time)
    ticker := time.NewTicker(100 * time.Millisecond)

    go func() {
        defer fsWatcher.Close()
        defer ticker.Stop()
        defer close(eventCh)

        for {
            select {
            case event := <-fsWatcher.Events:
                realPath, err := filepath.EvalSymlinks(event.Name)
                if err != nil || w.ignore.MatchesPath(realPath) {
                    continue
                }
                pending[realPath] = time.Now()

            case <-ticker.C:
                now := time.Now()
                for path, lastEvent := range pending {
                    if now.Sub(lastEvent) >= 200*time.Millisecond {
                        delete(pending, path)
                        eventCh <- FileChangeEvent{Path: path, Time: now}
                    }
                }

            case <-ctx.Done():
                return
            }
        }
    }()

    // Add directory watches recursively
    filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
        if d != nil && d.IsDir() && !w.ignore.MatchesPath(path) {
            fsWatcher.Add(path)
        }
        return nil
    })

    return eventCh, nil
}
```

### 4.6 What You Can DO After Phase 2

```
# Search returns structurally-related code (TypeScript, Python, Go)
> Tool: search("validateToken")
> Result: [
>   { path: "src/auth/login.ts", symbol: "validateToken", score: 0.91 },
>   { path: "src/auth/middleware.ts", symbol: "authGuard", score: 0.78, relation: "calls validateToken" },
>   { path: "src/auth/types.ts", symbol: "AuthToken", score: 0.65, relation: "type used by validateToken" }
> ]

# See dependencies
> Tool: dependencies("validateToken", file="src/auth/login.ts")
> Result: { callers: [authGuard, loginHandler], callees: [decodeJWT, checkExpiry] }

# See recent changes
> Tool: diff(since="24h")
> Result: { changed_files: 3, affected_symbols: ["validateToken", "loginHandler"] }

# File edits picked up automatically — no manual reindex needed
```

---

## 5. Phase 3 — Semantic Intelligence

### 5.1 Goal

Add embeddings via Ollama, brute-force vector search, and the full 5-signal hybrid ranker. Optional CSR graph if profiling shows SQLite BFS is a bottleneck. Rust tree-sitter queries. The system now understands code by meaning.

### 5.2 Features

| Feature | Maps To |
|---|---|
| Embedding worker with circuit breaker | C4, A7 |
| Ollama HTTP client (batch embedding) | C4 |
| Brute-force vector store (in-process) | C4, DC-11, DC-13 |
| Semantic scoring (embedding similarity) | C2 ranker |
| Full 5-signal hybrid ranker | C2 (all weights) |
| Query embedding cache (LRU) | C2 cache |
| Fallback chain (L0 → L0.5 → L1 → L2 → L3) | C2, architecture §5 |
| Cold index: priority ordering, FTS5 disable/rebuild | A9, A11 |
| Rust tree-sitter queries | FR-4 (language expansion) |
| Optional: CSR graph (if profiling shows need) | C3 |

### 5.3 Task Breakdown

| # | Task | Est. | Deliverable |
|---|---|---|---|
| 3.1 | Brute-force vector store: `[]float32` slices + cosine similarity | 3h | `store.go` — `Search()`, `Upsert()`, `Delete()` |
| 3.2 | Embedding worker goroutine: queue, batch processing (IP-2: per-worker parser) | 3h | `embedding.go` — worker loop |
| 3.3 | Circuit breaker: DISABLED → CLOSED → OPEN → HALF_OPEN (IP-15: mutex) | 2h | `embedding.go` — state machine |
| 3.4 | Ollama HTTP client: health check, `/api/embeddings`, batch | 3h | `embedding.go` — `OllamaEmbed()` |
| 3.5 | Content hash guard for embedding resolution (DM-2) | 1h | `embedding.go` — verify before insert |
| 3.6 | Semantic scoring: normalize cosine similarity → 0.0-1.0 | 1h | `ranker.go` — `semanticScore()` |
| 3.7 | Full 5-signal hybrid ranker (sem 0.40, struct 0.20, change 0.15, session 0.15, kw 0.10) | 2h | `ranker.go` — weighted scoring |
| 3.8 | Weight redistribution when signals unavailable | 1h | `ranker.go` — dynamic weights |
| 3.9 | Query embedding cache (LRU, 3MB) | 1h | `query.go` — cached embed lookup |
| 3.10 | Fallback chain orchestration (L0 → L3) | 2h | `fallback.go` — full chain |
| 3.11 | Cold index: priority file ordering (A9) | 1h | `enrichment.go` — priority sort |
| 3.12 | Cold index: disable FTS5 triggers, bulk rebuild (A11) | 1h | `fts.go` — disable/rebuild |
| 3.13 | Embedding queue management (priorities, dedup) | 2h | `embedding.go` — priority queue |
| 3.14 | Rust tree-sitter query file (chunks + symbols + edges) | 4h | `queries/rust.scm` |
| 3.15 | MCP tool: enrichment_status | 1h | `tools.go` — embed progress |
| 3.16 | Optional: CSR graph build + BFS (only if profiling shows need) | 0-8h | `graph/csr.go` — deferred |
| 3.17 | Evaluation harness: measure semantic search improvement | 2h | Updated eval with semantic queries |
| 3.18 | Integration test: semantic search finds code by meaning | 2h | Passing semantic test |
| 3.19 | BruteForceStore persistence: save/load embeddings to binary file | 2h | `store.go` — `SaveToDisk()`, `LoadFromDisk()` |

**Total: ~34h (~1 week)** (without optional CSR: ~26h)

### 5.4 Key Implementation Patterns

**Brute-force vector store (task 3.1):**

```go
// internal/vector/store.go

type BruteForceStore struct {
    mu      sync.RWMutex
    vectors map[int64][]float32  // chunkID → embedding vector
    dim     int
}

func (s *BruteForceStore) Search(ctx context.Context, query []float32, topK int) ([]types.ScoredResult, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    type scored struct {
        id    int64
        score float32
    }
    results := make([]scored, 0, len(s.vectors))

    for id, vec := range s.vectors {
        sim := cosineSimilarity(query, vec)
        results = append(results, scored{id, sim})
    }

    // Partial sort: top-K via heap or sort
    slices.SortFunc(results, func(a, b scored) int {
        return cmp.Compare(b.score, a.score)  // descending
    })

    if len(results) > topK {
        results = results[:topK]
    }

    out := make([]types.ScoredResult, len(results))
    for i, r := range results {
        out[i] = types.ScoredResult{ChunkID: r.id, Score: r.score}
    }
    return out, nil
}

func cosineSimilarity(a, b []float32) float32 {
    var dot, normA, normB float32
    for i := range a {
        dot += a[i] * b[i]
        normA += a[i] * a[i]
        normB += b[i] * b[i]
    }
    if normA == 0 || normB == 0 {
        return 0
    }
    return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

func (s *BruteForceStore) Upsert(ctx context.Context, entries []types.VectorEntry) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    for _, e := range entries {
        s.vectors[e.ChunkID] = e.Vector
    }
    return nil
}

func (s *BruteForceStore) Delete(ctx context.Context, chunkIDs []int64) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    for _, id := range chunkIDs {
        delete(s.vectors, id)
    }
    return nil
}

// Persistence (task 3.19): binary format — header + N×(chunkID + vector)
// Ensures embeddings survive process restarts without full re-embedding (FR-15).

func (s *BruteForceStore) SaveToDisk(path string) error {
    s.mu.RLock()
    defer s.mu.RUnlock()

    f, err := os.CreateTemp(filepath.Dir(path), ".embeddings-*.tmp")
    if err != nil {
        return err
    }
    defer os.Remove(f.Name()) // clean up on failure

    w := bufio.NewWriter(f)
    // Header: magic(4) + version(4) + dim(4) + count(4)
    header := []byte("EMBV")
    binary.Write(w, binary.LittleEndian, header)
    binary.Write(w, binary.LittleEndian, uint32(1))          // version
    binary.Write(w, binary.LittleEndian, uint32(s.dim))
    binary.Write(w, binary.LittleEndian, uint32(len(s.vectors)))

    for id, vec := range s.vectors {
        binary.Write(w, binary.LittleEndian, id)
        binary.Write(w, binary.LittleEndian, vec)
    }
    if err := w.Flush(); err != nil {
        f.Close()
        return err
    }
    if err := f.Close(); err != nil {
        return err
    }
    return os.Rename(f.Name(), path) // atomic replace
}

func (s *BruteForceStore) LoadFromDisk(path string) error {
    f, err := os.Open(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil // no persisted data, start fresh
        }
        return err
    }
    defer f.Close()

    r := bufio.NewReader(f)
    var magic [4]byte
    var version, dim, count uint32
    binary.Read(r, binary.LittleEndian, &magic)
    if string(magic[:]) != "EMBV" {
        return fmt.Errorf("invalid embedding file magic: %q", magic)
    }
    binary.Read(r, binary.LittleEndian, &version)
    binary.Read(r, binary.LittleEndian, &dim)
    binary.Read(r, binary.LittleEndian, &count)

    s.mu.Lock()
    defer s.mu.Unlock()
    s.dim = int(dim)
    s.vectors = make(map[int64][]float32, count)

    for i := uint32(0); i < count; i++ {
        var id int64
        vec := make([]float32, dim)
        binary.Read(r, binary.LittleEndian, &id)
        binary.Read(r, binary.LittleEndian, &vec)
        s.vectors[id] = vec
    }
    return nil
}
```

**Embedding worker with circuit breaker (tasks 3.2-3.3):**

```go
// internal/vector/embedding.go

type EmbedWorker struct {
    queue   chan EmbedJob
    cb      *CircuitBreaker  // IP-15: mutex-based
    ollama  *OllamaClient
    store   types.VectorStore
    batchSz int
}

func (w *EmbedWorker) Run(ctx context.Context) {
    batch := make([]EmbedJob, 0, w.batchSz)
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case job := <-w.queue:
            batch = append(batch, job)
            if len(batch) >= w.batchSz {
                w.processBatch(ctx, batch)
                batch = batch[:0]
            }
        case <-ticker.C:
            if len(batch) > 0 {
                w.processBatch(ctx, batch)
                batch = batch[:0]
            }
        case <-ctx.Done():
            if len(batch) > 0 {
                timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                w.processBatch(timeoutCtx, batch)
                cancel()
            }
            return
        }
    }
}
```

### 5.5 What You Can DO After Phase 3

```
# Semantic search — finds code by meaning
> Tool: search("retry failed HTTP requests with exponential backoff")
> Result: chunks containing retry logic even if they never mention "retry" in code

# Context assembly uses all 5 signals (session defaults to 0 until Phase 4)
> Tool: context("how does auth work", budget_tokens=8192)
> Result: {
>   chunks: [...],
>   meta: { strategy: "hybrid_l0", enrichment_level: "full", latency_ms: 47 }
> }

# Graceful degradation when Ollama is down — falls back to Level 1 automatically

# All 4 languages supported: TypeScript, Python, Go, Rust
```

---

## 6. Phase 4 — Advanced Features & Polish

### 6.1 Goal

Add session tracking, push mode via MCP notifications, performance optimizations, and operational polish. The system is now production-ready.

### 6.2 Features

| Feature | Maps To |
|---|---|
| Session store (LRU, decay, working set) | CA-4 |
| Session scoring + exploration decay | C2, A10 |
| Push mode: MCP resource updates + notifications | FR-14, A8, CA-11 |
| Branch switch detection | A8 |
| Writer goroutine: priority lanes + back-pressure | A1 |
| Query-time enrichment | C7 (sync path), A5, CA-2 |
| MCP resource: context/active | Push mode |
| MCP tool: summary | AP-1 |
| Per-project config command | FR-19 |
| Performance tuning + benchmarks | NFR-1 through NFR-9 |

### 6.3 Task Breakdown

| # | Task | Est. | Deliverable |
|---|---|---|---|
| 4.1 | Session store: in-memory LRU + decay map + working set | 3h | `session.go` — full module |
| 4.2 | Session scoring integration in ranker | 1h | `ranker.go` — `sessionScore()` |
| 4.3 | Session store: periodic SQLite flush (30s / 10 queries) | 1h | `session.go` — ticker goroutine |
| 4.4 | Exploration decay: batched in-memory updates (A10) | 1h | `session.go` — `UpdateDecay()` |
| 4.5 | MCP resource: `context/active` (auto-assembled, push mode) | 2h | `resources.go` — dynamic resource |
| 4.6 | MCP notifications: `notifications/resources/updated` | 2h | `notifications.go` — push on change |
| 4.7 | Push mode: resource manager + debounce (500ms / 3s) | 2h | `daemon.go` — resource update goroutine |
| 4.8 | Branch switch detection (>20 files in <2s) | 1h | `watcher.go` — `detectBranchSwitch()` |
| 4.9 | Writer goroutine: priority lanes (P0-P3), cascading select (IP-8) | 3h | `writer.go` — multi-channel refactor |
| 4.10 | Writer goroutine: burst coalescing (200ms per file) | 1h | `writer.go` — coalesce logic |
| 4.11 | Query-time enrichment: sync parse → ephemeral chunks (CA-2) | 3h | `enrichment.go` — `EnrichForQuery()` |
| 4.12 | File mutex: decouple from writer goroutine (A5) | 1h | `enrichment.go` — revised flow |
| 4.13 | MCP tool: summary (AP-1) | 1h | `tools.go` — index aggregation |
| 4.14 | Per-project config: `shaktiman config` command + config file format | 2h | Config CRUD + `.shaktiman/config.json` |
| 4.15 | Shutdown grace period: drain in-flight, flush state | 1h | `daemon.go` — ordered shutdown |
| 4.16 | Performance benchmarks (cold index, query latency, memory) | 3h | `go test -bench` suite |
| 4.17 | Evaluation harness: final measurement across all languages | 2h | Full eval report |
| 4.18 | Integration test: full agent interaction with push mode | 2h | End-to-end test |

**Total: ~32h (~1 week)**

### 6.4 What You Can DO After Phase 4

```
# Push mode — Claude Code receives proactive context updates
> Agent opens file → daemon re-assembles context → MCP notification sent
> Agent edits file → daemon re-indexes in <500ms → pushes new context
> Agent switches branch → daemon detects → re-indexes → pushes

# Session awareness
> System learns which code the agent is working with
> Biases future results toward the working set
> Exploration decay prevents filter bubbles

# Production-grade
> Graceful shutdown with state flush
> Priority lanes prevent cold-index writes from blocking queries
> Circuit breaker for embedding model
> <200ms p95 query latency

# Per-project config
$ ./shaktiman config set token_budget 4096 /path/to/project
$ ./shaktiman config set languages typescript,python /path/to/project
```

---

## 7. First Working Demo (Phase 1 Completion)

### 7.1 Scenario

A Claude Code agent uses Shaktiman to understand a TypeScript project.

### 7.2 Setup

```bash
# Build
go build -o bin/ ./cmd/...

# Add to Claude Code MCP config (~/.claude/mcp_servers.json)
{
  "mcpServers": {
    "shaktiman": {
      "command": "/path/to/bin/shaktimand",
      "args": ["/path/to/my-project"]
    }
  }
}

# Claude Code starts shaktimand automatically
# Daemon indexes 342 TypeScript files on startup
# Log: "Indexed 342 files, 2840 chunks, 1920 symbols in 12.3s"
```

### 7.3 Agent Interaction (via MCP tools)

```
# 1. Agent reads workspace summary (resource)
> Resource: shaktiman://workspace/summary
> { files: 342, chunks: 2840, symbols: 1920, languages: { typescript: 342 },
>   enrichment_level: "keyword_only", embedding_pct: 0.0 }

# 2. Agent searches for auth-related code
> Tool: search({ query: "validate user token", max_results: 5 })
> [
>   { content: "function validateToken(token: string): AuthResult { ... }",
>     path: "src/auth/login.ts", symbol_name: "validateToken", kind: "function",
>     start_line: 89, end_line: 112, score: 0.85, token_count: 284 },
>   ...
> ]

# 3. Agent requests assembled context
> Tool: context({ query: "how does user login work",
>                 files: ["src/auth/login.ts"], budget_tokens: 4096 })
> { chunks: [ ... 8 budget-fitted ranked chunks ... ],
>   total_tokens: 3842, budget_tokens: 4096,
>   chunks_included: 8, chunks_available: 24,
>   strategy: "keyword_l2" }

# 4. If file not indexed, falls back to raw file content
> Tool: context({ files: ["src/unknown.ts"], budget_tokens: 2048 })
> { chunks: [{ content: "... raw file content ...", strategy: "filesystem_l3" }] }
```

### 7.4 Key Behaviors Demonstrated

1. **Zero-config for agent** — Claude Code starts shaktimand via MCP; no manual daemon management
2. **Single-call context** — agent gets relevant code in one tool call instead of 5-15 grep/read cycles
3. **Chunk-level results** — returns function bodies, not whole files (284 tokens vs 2000+)
4. **Budget compliance** — never exceeds token budget (3842/4096)
5. **Strategy transparency** — `strategy: "keyword_l2"` tells agent semantic search isn't available yet
6. **Filesystem fallback** — unindexed files return raw content within budget
7. **Measurable quality** — eval harness produces recall/precision/MRR metrics

---

## 8. Risks & Simplifications

### 8.1 What to Stub/Mock per Phase

| Component | Phase 1 | Phase 2 | Phase 3 | Phase 4 |
|---|---|---|---|---|
| Writer goroutine | Single channel | + hash guard | unchanged | Priority lanes |
| File watcher | Manual reindex | fsnotify auto-watch | unchanged | + branch detect |
| Ranking | Keyword only | 3-signal hybrid | 5-signal hybrid | + session scoring |
| Embeddings | Skipped | Skipped | Ollama + brute-force | unchanged |
| Graph | None | SQLite CTE BFS | + optional CSR | unchanged |
| Session | None | None | None | Full LRU + decay |
| Push mode | None | None | None | MCP notifications |
| Languages | TypeScript | + Python, Go | + Rust | unchanged |

### 8.2 What to Avoid in V1

- **Cross-repo anything** — strict single-repo isolation
- **Custom embedding models** — Ollama with nomic-embed-text only
- **Config UI** — CLI config only
- **Plugin system** — hardcoded tree-sitter grammars
- **Distributed mode** — single machine, single daemon per repo
- **Git history analysis** — diff tracks working-tree changes only
- **Summarization** — no LLM-generated summaries of code

### 8.3 Key Risks

| Risk | Mitigation |
|---|---|
| tree-sitter query files are hard to write | Start TypeScript only. Use nvim-treesitter queries as reference. |
| Retrieval relevance below 80% target | Eval harness measures this from Phase 1. Iterate on ranking weights with data. |
| Brute-force vector search too slow at 1M lines | ~30-40ms for 75K chunks. If too slow, add Qdrant optional backend via `VectorStore` interface. |
| Cold index for 1M+ lines exceeds 60s | Progressive indexing — usable immediately. Full index time is secondary. |
| Ollama not installed | Circuit breaker → system works at Level 1/2 without embeddings. |
| `mcp-go` SDK missing features | Thin SDK — if needed, implement MCP JSON-RPC over stdio directly (~1 day). |
| SQLite CTE BFS too slow at 1M lines | Profiled at 30-80ms (per A3). If needed, add CSR graph in Phase 3. |

### 8.4 External Dependencies

| Dependency | Required Phase | How to Get | Fallback |
|---|---|---|---|
| **Go 1.23+** | Phase 1 | `brew install go` | Required, no fallback |
| **C compiler** (for CGo) | Phase 1 | Xcode CLI tools / gcc | Required for go-sqlite3 + go-tree-sitter |
| **Ollama** | Phase 3 | `brew install ollama` | Circuit breaker → Level 1/2 (no embeddings) |

### 8.5 Performance Targets by Phase

| Metric | Phase 1 | Phase 2 | Phase 3 | Phase 4 |
|---|---|---|---|---|
| Cold index (100K TS) | <60s | <60s (3 langs) | <90s (+ embeds async) | <60s (optimized) |
| Incremental reindex | N/A (manual) | <500ms | <500ms | <500ms |
| Query latency (p95) | <100ms | <200ms | <200ms | <200ms |
| Memory (100K) | <40MB | <50MB | <70MB | <100MB |

---

## 9. Build Order Summary

```
PHASE 1 (~1.7 weeks)     PHASE 2 (~1.5 weeks)     PHASE 3 (~1 week)       PHASE 4 (~1 week)
═══════════════════       ══════════════════════    ═══════════════════     ═══════════════════
types + interfaces        Python/Go queries         brute-force vectors     session store
parser (TS only)          edge extraction (3 lang)  embedding worker        push mode (MCP notif)
storage (full schema)     pending edges             circuit breaker         writer priority lanes
writer (basic)            SQLite CTE BFS            Ollama client           query-time enrichment
file scanner              diff engine               semantic scoring        config command
keyword retrieval         structural scoring        5-signal ranker         perf benchmarks
context assembler         change scoring            fallback chain (full)
fallback chain (stub)     3-signal ranker           Rust queries
MCP server + tools        assembler expansion       cold index optimize
CLI                       file watcher              eval (semantic)
eval harness (TS)         incremental enrich
                          new MCP tools
```

**Total: ~165h (~5 weeks for a single focused engineer)**

---

## Status

**Step 7: Implementation Plan (Final)** — Complete. Incorporates all solution-fit analysis decisions. Awaiting confirmation before beginning implementation.
