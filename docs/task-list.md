# Shaktiman: Task Status Tracker

> Derived from 07-implementation-plan.md and 07-implementation-plan-addendum.md.
> Cross-referenced against the current codebase as of 2026-03-23.

## Status Legend

| Status | Meaning |
|--------|---------|
| **DONE** | Implemented and present in codebase |
| **PENDING** | Not yet started; expected in current or next phase |
| **PLANNED** | Scheduled for a future phase |
| **DEFERRED** | Explicitly deferred per plan or addendum |

---

## Phase 1 — Minimal Working System (MVP)

| # | Task | Est. | Status | Notes |
|---|------|------|--------|-------|
| 1.1 | Initialize Go module + directory structure + empty packages | 1h | **DONE** | go.mod, all packages in place |
| 1.2 | Define shared types: `FileRecord`, `ChunkRecord`, `SymbolRecord`, config | 2h | **DONE** | `types/entities.go`, `types/config.go` |
| 1.3 | Define interfaces: `VectorStore`, `GraphStore`, `MetadataStore` | 1h | **DONE** | `types/interfaces.go` |
| 1.4 | SQLite schema (ALL tables) + migrations | 2h | **DONE** | `storage/schema.go` — 13 tables incl. FTS5, diff, access_log, working_set, tool_calls |
| 1.5 | SQLite dual DB: writer (1 conn) + reader pool (4 conn) (IP-3) | 2h | **DONE** | `storage/db.go` — `Open()`, `WithWriteTx()`, `QueryContext()` |
| 1.6 | Metadata store: insert/query files, chunks, symbols | 3h | **DONE** | `storage/metadata.go` — full CRUD |
| 1.7 | FTS5 setup: virtual table, sync triggers (DM-1) | 1h | **DONE** | `storage/fts.go` — `KeywordSearch()`, external content triggers |
| 1.8 | Basic writer goroutine: single channel, ordered shutdown (IP-4) | 2h | **DONE** | `daemon/writer.go` — producer WaitGroup + drain |
| 1.9 | File scanner: walk directory, resolve symlinks, .gitignore + .shaktimanignore | 2h | **DONE** | `daemon/scanner.go` — `ScanRepo()`, symlink resolution, both ignore files |
| 1.10 | Tree-sitter parser: load TypeScript grammar, parse file | 2h | **DONE** | `parser/parser.go`, `parser/languages.go` |
| 1.11 | Chunk splitter: extract functions/classes/methods/blocks | 3h | **DONE** | `parser/chunker.go` — semantic chunking with token limits |
| 1.12 | Symbol extractor: names, kinds, signatures, visibility | 2h | **DONE** | `parser/symbols.go` — per-language handlers |
| 1.13 | Tree-sitter TypeScript queries | 3h | **DONE** | Programmatic node configs in `parser/languages.go` (not .scm files) |
| 1.14 | Token counting: tiktoken-go per chunk | 1h | **DONE** | `parser/token.go` — cl100k_base encoding |
| 1.15 | Enrichment pipeline: scan → parse → chunk → symbols → write | 3h | **DONE** | `daemon/enrichment.go` — worker pool with per-worker parsers (IP-2) |
| 1.16 | Keyword retrieval: FTS5 search → ranked chunk list | 2h | **DONE** | `core/retrieval.go` |
| 1.17 | Basic context assembler: budget-fitted chunk selection | 2h | **DONE** | `core/assembler.go` — greedy packing with overlap skip |
| 1.18 | Filesystem fallback (Level 3): read raw files when index empty | 1h | **DONE** | `core/fallback.go` — `FilesystemFallback()` |
| 1.19 | Fallback chain stub: L2 → L3 decision | 1h | **DONE** | `core/fallback.go` — `DetermineLevel()` |
| 1.20 | MCP stdio server: setup, tool registration, lifecycle | 3h | **DONE** | `mcp/server.go` — mcp-go SDK, v0.4.0 |
| 1.21 | MCP tool handlers: `search`, `context` | 3h | **DONE** | `mcp/tools.go` — 7 tools total (also added symbols, deps, diff, enrichment_status, summary) |
| 1.22 | MCP resource: `workspace/summary` | 1h | **DONE** | `mcp/resources.go` — JSON IndexStats |
| 1.23 | Daemon lifecycle: startup indexing, graceful shutdown | 2h | **DONE** | `daemon/daemon.go` — `Start()`, `Stop()`, ordered shutdown |
| 1.24 | CLI: start, stop, status, search commands | 2h | **DONE** | `cmd/shaktiman/` — 8 commands (index, status, search, context, symbols, deps, diff, enrichment-status) |
| 1.25 | Request validation: parameter bounds (AP-5) | 1h | **DONE** | Validation in MCP tool handlers |
| 1.26 | Retrieval evaluation harness: framework + TypeScript test cases | 3h | **DONE** | `internal/eval/` — eval.go + testcases.go (TypeScript cases) |
| 1.27 | Integration test: MCP server → index → search → verify | 2h | **DONE** | `daemon/daemon_test.go` — `TestIntegration_IndexAndSearch` |

**Phase 1 Status: COMPLETE (27/27 tasks done)**

---

## Phase 2 — Structured Intelligence

| # | Task | Est. | Status | Notes |
|---|------|------|--------|-------|
| 2.1 | Python tree-sitter queries (chunks + symbols) | 3h | **DONE** | `parser/languages.go` — `pythonConfig()` |
| 2.2 | Go tree-sitter queries (chunks + symbols) | 3h | **DONE** | `parser/languages.go` — `goConfig()` |
| 2.3 | Language registry: detect language, load grammar | 1h | **DONE** | `parser/languages.go` — `GetLanguageConfig()` with extension mapping |
| 2.4 | Edge extraction: imports, calls, type_ref (TypeScript) | 4h | **DONE** | `parser/edges.go` — `extractEdges()` |
| 2.5 | Edge extraction queries for Python | 3h | **DONE** | `parser/edges.go` — multi-language edge extraction |
| 2.6 | Edge extraction queries for Go | 3h | **DONE** | `parser/edges.go` — Go import/call extraction |
| 2.7 | Pending edges table + two-phase resolution (CA-1) | 2h | **DONE** | `storage/graph.go` — `InsertEdges()`, `ResolvePendingEdges()` |
| 2.8 | SQLite recursive CTE BFS: `Neighbors()` | 3h | **DONE** | `storage/graph.go` — forward + reverse BFS with depth control |
| 2.9 | Diff engine: detect file changes, compute diff_log | 3h | **DONE** | `storage/diff.go` — `InsertDiffLog()` |
| 2.10 | Diff symbols tracking | 1h | **DONE** | `storage/diff.go` — `InsertDiffSymbols()`, `GetDiffSymbols()` |
| 2.11 | Structural scoring: graph proximity signal | 2h | **DONE** | `core/ranker.go` — structural signal (weight 0.20) |
| 2.12 | Change scoring: recency + magnitude signal | 2h | **DONE** | `core/ranker.go` — change signal (weight 0.15) |
| 2.13 | Hybrid ranker: keyword + structural + change (3-signal) | 2h | **DONE** | `core/ranker.go` — `HybridRank()` (implemented as full 5-signal, exceeding plan) |
| 2.14 | Context assembler: structural expansion (30% budget, CA-5) | 2h | **DONE** | `core/assembler.go` — BFS neighbor expansion |
| 2.15 | File watcher: fsnotify, debounce, .gitignore | 3h | **DONE** | `daemon/watcher.go` — directory watching, configurable debounce |
| 2.16 | Incremental enrichment: single-file re-parse on change | 2h | **DONE** | `daemon/enrichment.go` — `IndexIncremental()` |
| 2.17 | Writer goroutine: hash guard (CA-3), edge cleanup (DM-3) | 2h | **DONE** | `daemon/writer.go` — content hash check before re-index |
| 2.18 | MCP tools: symbols, dependencies, diff, enrich | 3h | **DONE** | `mcp/tools.go` — all tool handlers registered |
| 2.19 | Evaluation harness: add Python test cases | 1h | **PENDING** | Only TypeScript test cases in `eval/testcases.go` |
| 2.20 | Integration test: edit file → watcher → re-index → query reflects change | 2h | **PENDING** | No watcher-based incremental integration test found |

**Phase 2 Status: 18/20 tasks done, 2 pending**

---

## Phase 3 — Semantic Intelligence

| # | Task | Est. | Status | Notes |
|---|------|------|--------|-------|
| 3.1 | Brute-force vector store: cosine similarity | 3h | **DONE** | `vector/store.go` — `BruteForceStore`, RWMutex-protected |
| 3.2 | Embedding worker goroutine: queue, batch processing | 3h | **DONE** | `vector/embedding.go` — `EmbedWorker` with batch ticker |
| 3.3 | Circuit breaker: state machine with mutex (IP-15) | 2h | **DONE** | `vector/embedding.go` — `ErrOllamaUnreachable`, `ErrCircuitOpen` |
| 3.4 | Ollama HTTP client: health check, `/api/embeddings`, batch | 3h | **DONE** | `vector/embedding.go` — `OllamaClient` with HTTP, timeout |
| 3.5 | Content hash guard for embedding resolution (DM-2) | 1h | **DONE** | Hash guard in `daemon/writer.go` at enrichment time |
| 3.6 | Semantic scoring: normalize cosine similarity | 1h | **DONE** | `core/ranker.go` — semantic signal (weight 0.40) |
| 3.7 | Full 5-signal hybrid ranker | 2h | **DONE** | `core/ranker.go` — sem 0.40, struct 0.20, change 0.15, session 0.15, kw 0.10 |
| 3.8 | Weight redistribution when signals unavailable | 1h | **DONE** | `core/ranker.go` — proportional redistribution |
| 3.9 | Query embedding cache (LRU) | 1h | **DONE** | `core/engine.go` or `core/retrieval.go` — `EmbedCache` LRU (100 entries) |
| 3.10 | Fallback chain orchestration (L0 → L3) | 2h | **DONE** | `core/fallback.go` — `DetermineLevel()` / `DetermineLevelFull()` (4 levels) |
| 3.11 | Cold index: priority file ordering (A9) | 1h | **PENDING** | `IndexAll()` does not sort files by priority |
| 3.12 | Cold index: disable FTS5 triggers, bulk rebuild (A11) | 1h | **DONE** | `storage/fts.go` — `DisableFTSTriggers()` used in bulk insert path |
| 3.13 | Embedding queue management (priorities, dedup) | 2h | **PENDING** | Simple FIFO queue; no priority or dedup logic |
| 3.14 | Rust tree-sitter queries (chunks + symbols + edges) | 4h | **DONE** | `parser/languages.go` — `rustConfig()` |
| 3.15 | MCP tool: enrichment_status | 1h | **DONE** | `mcp/tools.go` — enrichment_status tool |
| 3.16 | Optional: CSR graph build + BFS | 0-8h | **DEFERRED** | SQLite CTEs used; CSR deferred per plan (IP-6, IP-7) |
| 3.17 | Evaluation harness: measure semantic search improvement | 2h | **PENDING** | Eval harness exists but no semantic-specific evaluation |
| 3.18 | Integration test: semantic search finds code by meaning | 2h | **PENDING** | No semantic integration test found |
| 3.19 | BruteForceStore persistence: save/load binary file | 2h | **DONE** | `vector/store.go` — `SaveToDisk()`, `LoadFromDisk()` with CRC32 |

**Phase 3 Status: 14/19 tasks done, 4 pending, 1 deferred**

---

## Phase 4 — Advanced Features & Polish

| # | Task | Est. | Status | Notes |
|---|------|------|--------|-------|
| 4.1 | Session store: in-memory LRU + decay + working set | 3h | **DONE** | `core/session.go` — LRU (2000 entries), `DecayAllExcept()` |
| 4.2 | Session scoring integration in ranker | 1h | **DONE** | `core/ranker.go` — session signal (weight 0.15) |
| 4.3 | Session store: periodic SQLite flush (30s / 10 queries) | 1h | **PENDING** | SessionStore is purely in-memory; no periodic flush to SQLite |
| 4.4 | Exploration decay: batched in-memory updates (A10) | 1h | **DONE** | `core/session.go` — `DecayAllExcept()` |
| 4.5 | MCP resource: `context/active` (auto-assembled, push mode) | 2h | **PLANNED** | Not implemented; only `workspace/summary` resource exists |
| 4.6 | MCP notifications: `notifications/resources/updated` | 2h | **PLANNED** | No `notifications.go`; no push notification system |
| 4.7 | Push mode: resource manager + debounce (500ms / 3s) | 2h | **PLANNED** | Not implemented |
| 4.8 | Branch switch detection (>20 files in <2s) | 1h | **DONE** | `daemon/watcher.go` — `branchSwitchCh` triggers on >20 files in one flush |
| 4.9 | Writer goroutine: priority lanes (P0-P3), cascading select (IP-8) | 3h | **PLANNED** | Writer uses single channel; no priority lanes |
| 4.10 | Writer goroutine: burst coalescing (200ms per file) | 1h | **PLANNED** | No coalescing logic found |
| 4.11 | Query-time enrichment: sync parse → ephemeral chunks (CA-2) | 3h | **PLANNED** | No `EnrichForQuery()` or sync parse path |
| 4.12 | File mutex: per-path sync.Map (A5) | 1h | **PLANNED** | General mutex only; no per-file `sync.Map` locking |
| 4.13 | MCP tool: summary (AP-1) | 1h | **DONE** | `mcp/tools.go` — `summary` tool registered |
| 4.14 | Per-project config: `shaktiman config` command | 2h | **PENDING** | TOML config loading exists (`types/config.go`), but no CLI `config` subcommand |
| 4.15 | Shutdown grace period: drain in-flight, flush state | 1h | **DONE** | `daemon/daemon.go` — ordered shutdown with writer drain |
| 4.16 | Performance benchmarks (cold index, query latency, memory) | 3h | **DONE** | Benchmarks in `core/engine_test.go` and `core/session_test.go` |
| 4.17 | Evaluation harness: final measurement across all languages | 2h | **PENDING** | Eval has only TypeScript test cases |
| 4.18 | Integration test: full agent interaction with push mode | 2h | **PLANNED** | Push mode not implemented; no push integration test |

**Phase 4 Status: 6/18 tasks done, 3 pending, 9 planned**

---

## Addendum Items (IP-1 through IP-16)

| # | Finding | Status | Notes |
|---|---------|--------|-------|
| IP-1 | ZMQ socket thread safety | **N/A** | Superseded — ZMQ replaced by MCP stdio |
| IP-2 | Tree-sitter per-worker parser instances | **DONE** | Each enrichment worker owns its own Parser |
| IP-3 | SQLite dual DB instances | **DONE** | `storage/db.go` — writer (1 conn) + reader (4 conn) |
| IP-4 | Writer ordered shutdown with WaitGroup | **DONE** | `daemon/writer.go` — producer WaitGroup + channel close + drain |
| IP-5 | Writer channel capacity (500 not 5000) | **DONE** | Channel capacity is 500 |
| IP-6 | BFS delta buffer merge during traversal | **DEFERRED** | CSR deferred; SQLite CTEs used |
| IP-7 | Reverse CSR for bidirectional BFS | **DEFERRED** | CSR deferred; SQLite CTEs support both directions |
| IP-8 | Priority writer cascading select | **PLANNED** | Single-channel writer; priority lanes not yet implemented |
| IP-9 | Missing Phase 1 tasks (schema, repo_id, tokens, validation) | **DONE** | Superseded; all items incorporated into Phase 1 |
| IP-10 | Symlink handling + .shaktimanignore | **DONE** | Symlink resolution and .shaktimanignore in scanner |
| IP-11 | Import cycle prevention — types package | **DONE** | All interfaces/results in `internal/types/` |
| IP-12 | Tree-sitter query time re-estimation | **DONE** | All 4 languages implemented (TS, Python, Go, Rust) |
| IP-13 | MCP compatibility layer | **DONE** | Superseded; MCP is Phase 1 primary transport |
| IP-14 | Vector store decision (brute-force default) | **DONE** | BruteForceStore is default; Qdrant not implemented |
| IP-15 | Circuit breaker — mutex instead of atomics | **DONE** | Circuit breaker in `vector/embedding.go` |
| IP-16 | fsnotify macOS — directory-level watching | **DONE** | `daemon/watcher.go` watches directories, not individual files |

---

## Summary

| Phase | Done | Pending | Planned | Deferred | Total |
|-------|------|---------|---------|----------|-------|
| Phase 1 — MVP | 27 | 0 | 0 | 0 | 27 |
| Phase 2 — Structured Intelligence | 18 | 2 | 0 | 0 | 20 |
| Phase 3 — Semantic Intelligence | 14 | 4 | 0 | 1 | 19 |
| Phase 4 — Advanced Features | 6 | 3 | 9 | 0 | 18 |
| **Totals** | **65** | **9** | **9** | **1** | **84** |

**Overall progress: 65/84 tasks complete (77%)**

### Pending Items (not started, but in scope)

1. **2.19** — Python evaluation test cases
2. **2.20** — Watcher-based incremental integration test
3. **3.11** — Cold index priority file ordering
4. **3.13** — Embedding queue priorities/dedup
5. **3.17** — Semantic search evaluation measurement
6. **3.18** — Semantic search integration test
7. **4.3** — Session store periodic SQLite flush
8. **4.14** — CLI `config` subcommand
9. **4.17** — Multi-language evaluation harness

### Planned Items (future work, not yet started)

1. **4.5** — MCP resource: `context/active` (push mode)
2. **4.6** — MCP notifications: `resources/updated`
3. **4.7** — Push mode: resource manager + debounce
4. **4.9** — Writer priority lanes (P0-P3) with cascading select
5. **4.10** — Writer burst coalescing
6. **4.11** — Query-time enrichment (sync parse)
7. **4.12** — Per-file mutex (`sync.Map`)
8. **4.18** — Push mode integration test

### Deferred Items

1. **3.16** — CSR graph (SQLite CTEs adequate; build only if profiling shows need)
