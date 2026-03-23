# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **TOML config file** (`internal/types/config.go`) — user-tunable knobs via
  `.shaktiman/shaktiman.toml`. Sample config auto-created on first run.
  Supports `[search]` (max_results, default_mode, min_score) and `[context]`
  (enabled, budget_tokens) sections. Pointer-field deserialization for correct
  partial-file handling.
- **Locate search mode** (`internal/mcp/format.go`) — compact one-line-per-result
  format returning path, line range, symbol, kind, and score without source
  code. ~97% token reduction vs full mode.
- **Score floor filtering** (`internal/core/engine.go`) — `filterByScore()`
  drops results below a configurable minimum relevance threshold (default 0.15)
  post-ranking. Applied in both semantic and keyword search paths.
- **CLI query commands** (`cmd/shaktiman/query.go`) — all 6 MCP tools now
  available as CLI subcommands: `search`, `context`, `symbols`, `deps`, `diff`,
  `enrichment-status`. JSON output, reads SQLite directly without MCP daemon.
  `search` and `context` respect `shaktiman.toml` config defaults.
- **CLI `--embed` flag** (`cmd/shaktiman/main.go`) —
  `shaktiman index --embed <root>` runs the embedding pipeline after cold
  indexing. Requires Ollama.
- **`EmbedProject` daemon method** (`internal/daemon/daemon.go`) — synchronous
  embedding for CLI use: queues chunks, runs worker until queue drains, saves
  embeddings to disk.
- **Result count metrics** (`internal/mcp/metrics.go`) — `withResultCount()`
  / `extractResultCount()` carry result count through the `withMetrics`
  wrapper via `sync.Map`. Logged and recorded in `ToolCallRecord`.
- **`docs/sample_claude.md`** — ready-to-copy CLAUDE.md template for projects
  using shaktiman. Documents locate-first pattern, tool mapping, subagent
  delegation, and token efficiency tips.
- **Context tool toggle** — `context.enabled = false` in `shaktiman.toml`
  disables the context MCP tool entirely.
- **Config tests** (`internal/types/config_test.go`) — tests for TOML loading,
  partial files, validation (7 invalid cases), sample creation, malformed input.

### Changed

- Default `SearchMaxResults`: 50 → 10.
- Default `ContextBudgetTokens` / `MaxBudgetTokens`: 8192 → 4096.
- Default search mode: `locate` (was always `full`).
- MCP search tool now accepts `mode` (locate/full) and `min_score` params.
- MCP tool descriptions rewritten to encourage locate-first pattern.
- MCP server accepts `Config` in `NewServerInput`; tool defs and handlers are
  config-driven.
- `.gitignore` fixed: `/shaktiman` and `/shaktimand` patterns now correctly
  match only root-level binaries, not `cmd/shaktiman/` source files.

### Dependencies

- Added `github.com/BurntSushi/toml v1.6.0`.

## [0.3.0] - 2026-03-21

Phase 3 — Semantic Intelligence + Hardening: vector embeddings via Ollama,
hybrid 5-signal ranking, Rust language support, and security/correctness
hardening from adversarial + security analysis.

### Added

- **Vector store** (`internal/vector/store.go`) — in-memory `BruteForceStore`
  with O(n) cosine similarity search, thread-safe via RWMutex. Persistence
  via binary file format (v2 with CRC32 integrity footer). `Has()` for
  membership check, `UpsertBatch()` with atomic pre-validation, `Delete()`
  for stale vector cleanup. Bounds validation on `LoadFromDisk()` (max dim
  4096, max count 2M, dim mismatch check).
- **Ollama embedding client** (`internal/vector/embedding.go`) —
  `OllamaClient` with `Embed()` and `EmbedBatch()` for single/batch
  embedding via Ollama HTTP API. `Healthy()` endpoint check. Response body
  limited to 50MB.
- **Circuit breaker** (`internal/vector/embedding.go`) — mutex-based state
  machine (Closed → Open → HalfOpen) with exponential backoff (5m → 10m →
  20m → 40m → 60m cap). Single-probe gate in HalfOpen via `halfOpenProbing`
  flag. Never permanently disables — always recoverable. `Reset()` for
  manual recovery.
- **Embed worker** (`internal/vector/embedding.go`) — `EmbedWorker` with
  batched processing (default 32), 500ms flush ticker, circuit breaker
  protection, `OnBatchDone` callback for DB status updates, and graceful
  drain on context cancellation.
- **Query embedding cache** (`internal/vector/embedding.go`) — LRU
  `EmbedCache` with defensive slice copying on `Get()`/`Put()` to prevent
  caller mutation.
- **Hybrid semantic search** (`internal/core/engine.go`) — `searchSemantic()`
  merges keyword + vector candidates, `HybridRank()` with 5-signal ranking
  (keyword, structural, change, semantic, session). `mergeResults()` hydrates
  vector-only entries from store.
- **Fallback level: Hybrid/Mixed** (`internal/core/fallback.go`) —
  `DetermineLevelFull()` considers embedding readiness and vector coverage
  (≥80% → Hybrid, ≥20% → Mixed, else Keyword). `embedReady` func plumbed
  from `EmbedWorker.EmbedReady()` through engine.
- **Cosine similarity normalization** (`internal/core/ranker.go`) —
  `NormalizeCosineSimilarity()` maps [-1,1] to [0,1] range for score
  blending.
- **Stale vector cleanup** (`internal/daemon/writer.go`) — `VectorDeleter`
  interface in `types/interfaces.go`. Writer collects old chunk IDs before
  `DELETE FROM chunks` and calls `vectorDeleter.Delete()` after successful
  transaction. Handles both enrichment re-index and file delete cases.
- **Dual embedding filter** (`internal/daemon/daemon.go`,
  `internal/storage/metadata.go`) — Option A: `queueEmbeddings()` filters
  by `vectorStore.Has()` for crash reconciliation. Option B: SQL filters by
  `files.embedding_status != 'complete'`. `MarkChunksEmbedded()` updates
  file status after successful batch upsert.
- **Periodic embedding checkpoint** (`internal/daemon/daemon.go`) — 5-minute
  `SaveToDisk` ticker prevents crash data loss.
- **Enrichment status tool** (`internal/mcp/tools.go`) — `enrichment_status`
  MCP tool showing total chunks, embedded count, embedding percentage,
  pending jobs, circuit breaker state, and index stats.
- **Rust language support** (`internal/parser/languages.go`) — tree-sitter
  Rust grammar with `function_item`, `struct_item`, `impl_item`,
  `enum_item`, `trait_item`, `type_item` node mappings. `.rs` extension
  registered in scanner and fallback.
- **Config extensions** — `OllamaURL`, `EmbeddingModel`, `EmbeddingDims`,
  `EmbeddingsPath`, `EmbedBatchSize`, `EmbedEnabled` fields.
- **README.md** — project documentation covering installation, Claude Code
  integration, MCP tools reference, CLI usage, architecture, configuration,
  and contributing guide.
- **File logging** (`cmd/shaktimand/main.go`) — daemon logs to
  `.shaktiman/shaktimand.log` instead of stderr (stdout reserved for MCP
  protocol). Log file truncated on startup. Configurable level via
  `SHAKTIMAN_LOG_LEVEL` env var. Startup log includes config summary and PID.
- **MCP tool logging middleware** (`internal/mcp/server.go`) — `withLogging()`
  wraps all tool handlers with `duration_ms` and `is_error` tracking.
- **Operation timing** — `duration_ms` logged for search
  (`internal/core/engine.go`), cold index (`internal/daemon/enrichment.go`),
  embed batch (`internal/vector/embedding.go`), and vector save/load
  (`internal/vector/store.go`).
- **Back-pressure warnings** — `WriterManager.Submit()` warns when channel is
  full before blocking (`internal/daemon/writer.go`). `EmbedWorker.Submit()`
  counts dropped jobs with rate-limited warnings (every 100 drops).
- **Circuit breaker transition logging** (`internal/vector/embedding.go`) —
  logs state transitions (open/recovered) with `stateString()` helper.
- **Scanner debug logging** (`internal/daemon/scanner.go`) — all skip reasons
  logged at debug level with per-reason context. Scan completion summary with
  `files_found` and `files_skipped` counts.
- **`observability.Op()` helper** (`internal/observability/`) — timed
  operation logger used by daemon cold index.
- **New tests** — vector store (Has, bounds validation, UpsertBatch
  atomicity, CRC32 corruption), circuit breaker (exponential backoff, cap,
  recovery, single probe), embed cache (slice isolation), engine (semantic
  search integration), scanner (relative root, dot root, symlink-outside-root).
  Total: 98 tests, all pass with `-race`.

### Changed

- `QueryEngine.SetVectorStore()` now accepts `readyFn func() bool` to check
  circuit breaker state instead of hardcoding `EmbeddingReady: true`.
- `QueryEngine.determineLevel()` checks `embedReady` func and vector count
  for accurate fallback level selection.
- `WriterManager.processWriteJob()` and `processEnrichmentJob()` return
  `([]int64, error)` to surface stale chunk IDs for vector cleanup.
- File upsert in writer resets `embedding_status` to `'pending'` on conflict
  to trigger re-embedding after file changes.
- `GetChunksNeedingEmbedding()` SQL now filters by
  `files.embedding_status != 'complete'`.
- `HybridRank()` accepts optional `SemanticScores` map and `SemanticReady`
  flag for 5-signal blending.
- MCP tool definitions now include `destructive: false` and
  `idempotent: true` hint annotations for all six tools.
- Periodic embedding checkpoint log promoted from Debug to Info level.

### Fixed

- **Stale vectors on chunk re-index** — old chunk IDs now deleted from
  vector store when files are re-indexed or deleted.
- **Embedding queue overflow** — `queueEmbeddings()` filters already-embedded
  chunks via both DB flag and vector store membership check.
- **Crash embedding loss** — periodic 5-minute SaveToDisk checkpoint instead
  of save-only-on-graceful-shutdown.
- **LoadFromDisk OOM** — bounds validation rejects oversized dim (>4096) and
  count (>2M) from crafted persistence files. Dim mismatch check prevents
  silent model change corruption.
- **UpsertBatch partial write** — pre-validates all vector dimensions before
  acquiring write lock; no partial updates on dim mismatch.
- **Circuit breaker permanent disable** — replaced `StateDisabled` with
  exponential backoff (5m → 60m cap). System always retries.
- **HalfOpen unlimited probes** — `halfOpenProbing` flag limits to one
  concurrent probe request. Single failure in HalfOpen immediately re-opens.
- **FilesystemFallback symlink escape** — resolves symlinks via
  `filepath.EvalSymlinks()` and rejects paths outside project root.
- **FilesystemFallback ctx.Done break** — `break` inside `select` now uses
  labeled loop to correctly exit file iteration on cancellation.
- **Diff tool unbounded params** — `since` capped at 720h, `limit` capped
  at 500.
- **Ollama response unbounded** — success path now uses `io.LimitReader`
  (50MB cap) before JSON decode.
- **Embedding dir permissions** — `os.MkdirAll` uses `0o700` instead of
  `0o755`.
- **Persistence file corruption** — v2 format adds CRC32 footer; load
  verifies integrity. Backward-compatible with v1 (no CRC).
- **EmbedCache mutation** — `Get()` and `Put()` now copy slices to prevent
  caller corruption of cached embeddings.
- **MCP error leakage** — all tool handlers truncate error messages to 200
  chars via `sanitizeError()`.
- **Scanner symlink boundary false match** — prefix check now includes path
  separator (`absRoot + "/"`) to prevent `/project-foo` matching `/project`.
  Root resolved to absolute path once upfront instead of per-file.
- **Writer `LastInsertId()` stale ID** — `processEnrichmentJob()` now always
  uses explicit `SELECT id FROM files` after upsert. `LastInsertId()` returned
  stale IDs on `ON CONFLICT DO UPDATE` path because `sqlite3_last_insert_rowid`
  is connection-scoped.

### Security

- Symlink boundary enforcement in `FilesystemFallback`.
- CRC32 integrity check on embedding persistence file.
- Response size limits on Ollama HTTP responses.
- Input bounds on diff tool duration and result limit.
- Error message sanitization in MCP handlers.
- Restrictive directory permissions for `.shaktiman/` data.

## [0.2.0] - 2026-03-20

Phase 2 — Structured Intelligence: multi-language support, dependency graph,
diff tracking, hybrid ranking, file watching, and incremental updates.

### Added

- **Multi-language parser** (`internal/parser/languages.go`) — `LanguageConfig`
  registry with per-language node type mappings, Python and Go grammars via
  `go-tree-sitter`, `SupportedLanguage()` check. Python node types:
  `function_definition`, `class_definition`, `decorated_definition`.
  Go node types: `function_declaration`, `method_declaration`,
  `type_declaration`.
- **Edge extraction** (`internal/parser/edges.go`) — AST-based dependency edge
  extraction for all three languages. Extracts `imports`, `calls`, `inherits`,
  and `implements` edges with deduplication. TypeScript: import clauses,
  member expressions, class heritage. Python: import statements, call
  expressions, class bases. Go: import specs, selector expressions.
- **Graph storage** (`internal/storage/graph.go`) — `InsertEdges()` with
  two-phase resolution (CA-1): resolved edges go to `edges` table, unresolved
  to `pending_edges`. `ResolvePendingEdges()` resolves pending edges when new
  symbols appear. `Neighbors()` performs BFS via SQLite recursive CTEs for
  outgoing/incoming/both directions. `DeleteEdgesByFile()` for re-indexing.
- **Diff engine** (`internal/storage/diff.go`) — `InsertDiffLog()` and
  `InsertDiffSymbols()` for file and symbol-level change tracking.
  `GetRecentDiffs()` with time/file/limit filters. `ComputeChangeScores()`
  returns `exp(-0.05 * hours) * min(magnitude / 50, 1.0)` scores.
- **Hybrid ranker** (`internal/core/ranker.go`) — 3-signal ranking: keyword
  (0.5) + structural (0.3) + change (0.2). `HybridRank()` re-ranks candidates
  using BFS neighbor overlap for structural boost and recency-weighted change
  scores. `DefaultRankWeights()` for default blending.
- **Structural expansion** (`internal/core/assembler.go`) — after greedy pack,
  allocates 30% of remaining budget for BFS neighbor chunks. Strategy name
  updated to `hybrid_l0` when store is available.
- **File watcher** (`internal/daemon/watcher.go`) — `fsnotify`-based directory
  watching (IP-16), 200ms debounce, recursive directory addition, automatic
  watch on new directories, skip of `.git`/`node_modules`/etc.
- **Incremental enrichment** (`internal/daemon/enrichment.go`) —
  `EnrichFile()` for single-file re-indexing from watcher events, content hash
  check to skip unchanged files, file deletion handling.
- **Writer hash guard** (CA-3) — before processing enrichment jobs, checks
  content hash and indexed_at timestamp to skip stale or already-indexed writes.
- **Diff computation in writer** — on re-index, computes symbol-level diffs
  (added/modified/removed/signature_changed) and records `diff_log` +
  `diff_symbols` entries.
- **New MCP tools** — `symbols` (lookup by name with kind filter),
  `dependencies` (BFS callers/callees with configurable depth),
  `diff` (recent changes with affected symbols).
- **Config extensions** — `WatcherEnabled` (default true),
  `WatcherDebounceMs` (default 200).
- **Scanner multi-language** — `.py` and `.go` extensions added.
- **Test fixtures** — `testdata/python_project/` (3 Python files),
  `testdata/go_project/` (3 Go files).
- **New tests** — parser Python/Go tests (6), edge extraction tests (6),
  graph storage tests (5), diff storage tests (4), hybrid ranker tests (4).
  Total: 57 tests, all pass with `-race`.

### Changed

- `ParseResult` now includes `Edges []types.EdgeRecord`.
- `EdgeRecord` extended with `SrcSymbolName`, `DstSymbolName`, `IsCrossFile`
  fields for parser output before DB resolution.
- `WriteJob` extended with `Edges []types.EdgeRecord`.
- `extractSymbols()` and `walkForSymbols()` now accept `*LanguageConfig`
  for language-aware symbol extraction, including Go uppercase export rule.
- `chunkFile()` uses `LanguageConfig` for language-specific node type mapping
  instead of hardcoded TypeScript maps.
- `Assemble()` accepts optional `Store` and `Ctx` for structural expansion.
- `QueryEngine.Search()` and `Context()` apply hybrid ranking before assembly.
- Daemon `Start()` launches file watcher after cold index completes.

### Dependencies

- Added `github.com/fsnotify/fsnotify` v1.9.0 for file watching.

## [0.1.0] - 2026-03-20

Phase 1 — Minimal working system: TypeScript-only parsing, SQLite storage,
FTS5 keyword search, budget-fitted context assembly, and MCP tools.

### Added

- **Types & interfaces** (`internal/types/`) — `FileRecord`, `ChunkRecord`,
  `SymbolRecord`, `EdgeRecord`, `WriteJob`, `ScoredResult`, `ContextPackage`,
  `IndexStats`, `Config` with `DefaultConfig()`, and `MetadataStore`,
  `VectorStore`, `GraphStore` interfaces.
- **SQLite dual-DB storage** (`internal/storage/`) — WAL-mode writer
  (MaxOpenConns=1) + reader pool (MaxOpenConns=4), schema with 15 tables,
  `Migrate()` for idempotent DDL, CRUD for files/chunks/symbols, cascade
  deletes, and `GetIndexStats()`.
- **FTS5 keyword search** (`internal/storage/fts.go`) — external content
  virtual table with sync triggers, `KeywordSearch()` with BM25 ranking,
  `DisableFTSTriggers()`/`EnableFTSTriggers()` for bulk insert performance,
  and `RebuildFTS()`.
- **Tree-sitter parser** (`internal/parser/`) — TypeScript parsing via
  `go-tree-sitter`, semantic chunking by AST node type (functions, classes,
  interfaces, enums, type aliases), class method splitting with
  `ParentIndex`, header chunk from imports, merge of tiny chunks (<20
  tokens), split of oversized chunks (>1024 tokens), symbol extraction with
  export tracking, and token counting via `tiktoken-go` (cl100k_base).
- **Writer goroutine** (`internal/daemon/writer.go`) — `WriterManager` with
  serialized SQLite writes, channel capacity 500, ordered shutdown via
  `AddProducer()`/`RemoveProducer()` + `sync.WaitGroup`, 10s drain timeout,
  and sync marker pattern for write completion.
- **File scanner** (`internal/daemon/scanner.go`) — `ScanRepo()` with
  `filepath.WalkDir`, `.gitignore`/`.shaktimanignore` support, symlink
  resolution (skip if target outside project), binary detection, SHA-256
  content hashing, and TypeScript-only extension filter.
- **Enrichment pipeline** (`internal/daemon/enrichment.go`) — N worker
  goroutines (default 4), each owning a `Parser` instance (not
  goroutine-safe), content-hash-based change detection, FTS trigger
  disable/rebuild optimization for cold index.
- **Query engine** (`internal/core/`) — `KeywordSearch()` with FTS5 +
  BM25 normalization + chunk hydration, `Assemble()` with greedy
  budget-fitted packing and >50% line-overlap dedup, fallback chain
  (L2 keyword → L3 filesystem), and `QueryEngine` orchestrating `Search()`
  and `Context()`.
- **MCP server** (`internal/mcp/`) — `search` and `context` tools with
  input validation (query max 10k chars, max_results 1–200, budget_tokens
  256–32768), `workspace/summary` resource returning `IndexStats`, served
  via `mark3labs/mcp-go` stdio transport.
- **Daemon lifecycle** (`internal/daemon/daemon.go`) — `New()` opens DB +
  runs migrations, `Start()` launches writer + background cold index + MCP
  server, `IndexProject()` for synchronous CLI indexing, `Stop()` for
  graceful shutdown.
- **CLI** (`cmd/shaktiman/`) — `shaktiman index <root>`, `shaktiman status
  <root>`, `shaktiman search <query> --root <path>` via Cobra. Reads SQLite
  directly without MCP server.
- **MCP daemon binary** (`cmd/shaktimand/`) — stdio MCP server entry point
  with signal handling, logs to stderr.
- **Test fixtures** (`testdata/typescript_project/`) — 6 TypeScript files
  covering auth, middleware, models, handlers, utils, and server entry point.
- **Eval harness** (`internal/eval/`) — `Evaluate()` computing recall@K,
  precision@K, and MRR against 10 curated test cases.
- **Unit tests** — storage (10 tests), parser (8 tests), core engine
  (8 tests), daemon integration (3 tests). All pass with `-race`.

### Fixed

- `extractName()` panic when called with `nil` source — now threads
  `source []byte` through all call sites.
- In-memory DB shared cache conflicts in parallel tests — each `Open()`
  call now generates a unique DB name via atomic counter.
- `walkForSymbols()` not recursing into root `program` node — added general
  fallthrough recursion for unhandled container nodes.
- `chunkClass()` failing on `export class` — now unwraps `export_statement`
  to find inner `class_declaration` for tree walking while preserving the
  outer node for content.

[unreleased]: https://github.com/shaktimanai/shaktiman/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/shaktimanai/shaktiman/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/shaktimanai/shaktiman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/shaktimanai/shaktiman/releases/tag/v0.1.0
