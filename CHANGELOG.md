# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[unreleased]: https://github.com/shaktimanai/shaktiman/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/shaktimanai/shaktiman/releases/tag/v0.1.0
