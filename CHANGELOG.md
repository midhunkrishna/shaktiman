# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[unreleased]: https://github.com/shaktimanai/shaktiman/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/shaktimanai/shaktiman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/shaktimanai/shaktiman/releases/tag/v0.1.0
