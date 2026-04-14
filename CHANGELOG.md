# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.4] — 2026-04-13

### Added

- **`reindex` CLI command** — purge all indexed data and re-index from scratch.
  Useful after major refactors or parser upgrades. Supports `--embed`,
  `--vector`, and `--force` (skip confirmation) flags.
- **Task prefix support for embedding models** — new `query_prefix` and
  `document_prefix` TOML keys under `[embedding]` allow configuring task
  prefixes required by models like `nomic-embed-text`
  (`"search_query: "` / `"search_document: "`). Prefixes are applied before
  batching (documents) and before query embedding + caching (queries). The
  prefixed text is used as the cache key to prevent stale vectors after a
  model switch.
- **Qdrant payload-based project isolation** — `project_id` is stored as a
  payload field on each Qdrant point and used to filter search, count, and
  purge operations. Matches the pgvector isolation model. `PurgeAll` uses
  filter-based delete when scoped, falls back to collection drop+recreate
  when unscoped.
- **Shared `internal/backends` package** — extracted store opening, closing,
  and purge logic out of daemon into a shared package used by both daemon
  and CLI (`backends.Open`, `Close`, `PurgeBackends`, `PurgeFiles`,
  `EmbeddingsPath`).

### Fixed

- **Split chunk siblings merged at retrieval time** — when large methods
  exceed 1024 tokens, the chunker splits them into fragments for embedding
  quality. Previously these fragments were returned independently, causing
  consumers to see incomplete methods. Now sibling fragments sharing the
  same `file_id`, `symbol_name`, and `kind` are reconstituted into complete
  methods before ranking. New `GetSiblingChunks` / `BatchGetSiblingChunks`
  methods on MetadataStore (SQLite + Postgres).
- **Assembler budget changed to best-effort spill-over** — the context
  assembler now includes one extra chunk beyond the token budget rather than
  dropping it, preventing truncation of the last relevant result.
- **Send-on-closed-channel race in WriterManager shutdown** — replaced bare
  blocking send with a `sync.Cond` wait loop. The channel is never closed;
  `drain()` sets a `closed` flag and broadcasts to wake blocked submitters.
  Eliminates the TOCTOU gap between releasing `mu` and entering `select`.
- **FileID carried in ScoredResult** — eliminates per-result `GetChunkByID`
  DB round trips during sibling expansion. 37% faster, 47% fewer
  allocations on benchmarks.

### Changed

- Codecov config updated; internal refactoring to improve testability.

## [0.9.3] — 2026-04-09

### Added

- **Multi-instance concurrency via single-daemon + socket proxy** (ADR-002
  Amendment 4) — when multiple Claude Code sessions open on the same project,
  the first `shaktimand` becomes the leader (owns DB, vectors, watcher) and
  subsequent instances become stateless proxies bridging their Claude Code
  client's stdio to the leader via a Unix domain socket. Zero stale reads,
  zero double-enrichment, zero races.
  - New `internal/lockfile/` package: `flock`-based singleton enforcement on
    `.shaktiman/daemon.pid` with path canonicalization via
    `filepath.EvalSymlinks`.
  - New `internal/proxy/` package: ~80 LOC stdio-to-HTTP bridge that forwards
    JSON-RPC requests to the leader's `StreamableHTTPServer` endpoint. Captures
    `Mcp-Session-Id` from first response for session continuity.
  - Leader serves MCP on both `StdioServer` (own client) and
    `StreamableHTTPServer` on `/tmp/shaktiman-<hash>.sock` (proxy clients),
    sharing the same `MCPServer` instance.
  - Proxy promotion: when leader exits, proxies detect connection-refused,
    attempt `flock`, and the winner re-execs as the new leader. Losers
    reconnect as proxies.
  - CLI `shaktiman index` checks flock before indexing; refuses if daemon is
    running.
- **ADR-003 A12: Postgres requires pgvector or qdrant** — `ValidateBackendConfig`
  now rejects `postgres + brute_force` and `postgres + hnsw` at startup.
  File-backed vector stores (`embeddings.bin`) race across daemons sharing the
  same Postgres database.

### Changed

- `cmd/shaktimand/main.go` canonicalizes `projectRoot` via `filepath.Abs` +
  `filepath.EvalSymlinks` at startup, preventing two daemons from the same
  directory via different paths.

## [0.9.2] — 2026-04-08

### Added

- **Recursive AST-driven chunking** (ADR-004,
  `internal/parser/chunker.go`) — replaces the whitelist-driven two-level
  chunker (file → class → method) with a recursive, size-gated AST traversal
  guided by `ChunkableTypes`. New `chunkNode` + `findChunkableChildren`
  decompose oversized nodes by walking structural containers
  (`body_statement`, `block`, `declaration_list`, `class_body`) to reach
  chunkable descendants at arbitrary depth. Fixes silent drops of nested
  modules, decorated methods, inner classes, trait methods, and namespaces.
  `walkForSymbols` mirrors the same recursion with a container whitelist.
  `buildSignatureFromExtracted` emits compact code-only parent summaries;
  comments are excluded from chunking entirely.
- **11 new `ChunkableTypes` across 5 languages** — Ruby `singleton_class`;
  Python `type_alias_statement`; Rust `function_signature_item`,
  `foreign_mod_item`, `union_item`; Java `compact_constructor_declaration`,
  `static_initializer`; TypeScript `internal_module`, `ambient_declaration`,
  `function_signature`, `generator_function_declaration`.
- **`AmbientType` in `LanguageConfig`** — TypeScript `declare` unwrapping,
  mirroring `ExportType`.
- **Chunk algorithm version tracking** — `parser.ChunkAlgorithmVersion` as
  the single source of truth. `MetadataStore.GetConfig` / `SetConfig` backed
  by the existing `config` key-value table (sqlite + postgres implementations,
  no migration needed). `daemon.ensureParserVersion` runs before cold
  indexing: on mismatch it purges every file (cascading chunks/symbols/edges
  via FKs) so cold-index re-parses with the new algorithm. Wired into
  `Start()` and `IndexProject()`.
- **Parser tests for recursive chunking** — 20 parser tests covering every
  nesting fix and new `ChunkableType`; 13 new testdata fixtures across
  ruby/python/rust/java/typescript projects. `TestConfig_GetSet` (sqlite),
  `TestEnsureParserVersion_PurgesOnMismatch`, `TestEnsureParserVersion_NoOpOnMatch`.
- **`symbols.kind` CHECK constraint expanded for `namespace`** —
  migration `002_symbols_kind_namespace.sql` (sqlite) and
  `005_symbols_kind_namespace.sql` (postgres) add `namespace` to the allowed
  set so TypeScript `internal_module` symbols persist. Previously the writer
  silently dropped them at the DB layer.
- **Multi-project isolation for PostgreSQL backend** — when multiple projects
  share the same Postgres database, each project's data is now fully isolated.
  New `projects` table registers each project by its canonicalized root path.
  `project_id` column added to `files` and `embeddings` tables. All queries
  (~26 sites across `metadata.go`, `graph.go`, `diff.go`, `pgvector/store.go`)
  are scoped by project. Migration `006_add_project_id.sql` handles backward
  compatibility: existing single-project databases are automatically claimed
  by the first daemon that starts.
- **`PgStore.EnsureProject` method** (`internal/storage/postgres/db.go`) —
  registers a project at startup using race-safe upsert-or-get pattern
  (`INSERT ON CONFLICT DO NOTHING` + fallback `SELECT`). Path is
  canonicalized via `filepath.Abs` + `filepath.EvalSymlinks` to prevent
  duplicates from symlinks or relative paths.
- **`PgStore.ProjectID` getter** (`internal/storage/postgres/db.go`) — exposes
  the project ID for pgvector pool sharing.
- **pgvector project scoping** (`internal/vector/pgvector/store.go`) —
  `Search`, `Upsert`, `UpsertBatch`, and `Count` are scoped by `project_id`.
  Search over-fetches by 3x to compensate for HNSW post-filtering, then trims
  to the requested `topK`.
- **Project isolation integration tests** — 13 Postgres tests
  (`internal/storage/postgres/project_test.go`) and 2 pgvector tests
  (`internal/vector/pgvector/project_test.go`) covering file isolation,
  symbol isolation, FTS isolation, stats isolation, diff isolation, edge
  resolution isolation, embed page isolation, batch hash isolation,
  concurrent startup, path canonicalization, and backward compatibility.
- **Qdrant multi-project warning** (`internal/daemon/daemon.go`) — startup
  warning when Qdrant + Postgres backends are used together, since Qdrant
  does not yet support project isolation.

### Changed

- **`LanguageConfig` simplification** — removed `ClassTypes`, `ClassBodyTypes`,
  and `ClassBodyType` triplet; all traversal now flows through
  `ChunkableTypes` + structural container recursion instead of a
  hand-maintained whitelist per language.
- **`lookupSymbolIDPg` project-scoped** (`internal/storage/postgres/graph.go`)
  — language-fallback and bare-name-fallback branches now filter by
  `project_id`, preventing cross-project symbol resolution in `InsertEdges`
  and `ResolvePendingEdges`.
- **`GetRecentDiffs` project-scoped** (`internal/storage/postgres/diff.go`) —
  the no-`FileID` branch now joins through `files` to filter by `project_id`.
- **`VectorStoreConfig` carries `ProjectID`** (`internal/vector/registry.go`)
  — extracted from the metadata store via `ProjectID()` interface assertion
  and passed to pgvector at construction.
- **`MetadataStoreConfig` carries `ProjectRoot`**
  (`internal/storage/registry.go`) — threaded from application config through
  to the Postgres factory for project registration after migrations.

### Fixed

- **Parser bug #1 — `extractName` returns type name for fields** — walked
  named children in order and returned `type_identifier` before
  `variable_declarator`, losing the variable name on Java field declarations.
  Now prefers `variable_declarator`.
- **Parser bug #2 — multi-declarator Java field symbols lost** — only the
  first declarator in `int a, b, c;` was emitted; remaining declarators are
  now walked and each produces its own symbol.
- **Parser bug #3** — resolved transitively by bugs #1 + #2.
- **Parser bug #4 — `walkForSymbols` recursion kind-string-switched** —
  replaced with a `NodeMeta` struct refactor driven by `ChunkableTypes` so
  symbol extraction and chunking share one source of truth.
- **Parser bug #5 — `findDeclarationChild` hardcoded** — now reads the
  declaration child kind from `LanguageConfig`.
- **Parser bug #6 — signature truncates multi-line declarations** —
  `buildSignatureFromExtracted` reconstructs the signature header via AST
  geometry (first/last byte range of declaration preamble) instead of
  single-line slicing.
- **Parser bug #7 — depth guard fallback is silent** — `chunkNode` now emits
  `slog.Warn` when the depth guard triggers so oversized-nesting cases are
  observable instead of silently line-split.
- **Parser bug #8 — `resolveCallee` drops receiver on member calls** —
  member-expression callees now preserve the receiver so `foo.bar()` resolves
  to `bar` on `foo`'s type rather than a bare `bar`.
- **Parser bug #9 — recursive self-calls dropped from call graph** —
  call-edge extraction now records self-recursive invocations instead of
  filtering them out.
- **Parser bug #10 — generic type arguments skipped by edges walker** —
  heritage walker now descends into `type_arguments` so `class Foo extends
  Bar<Baz>` records an edge to `Baz`.
- **Parser bug #11 — small containers decomposed eagerly** — `chunkNode`
  now size-gates before `findChunkableChildren`, so a container whose full
  body fits under `maxChunkTokens` is emitted as a single chunk (matching
  ADR-004: "a 20-line module with a 15-line class doesn't decompose").
  Previously any container with chunkable descendants was decomposed
  regardless of total size.
- **Parser bug #12 — depth guard off-by-one in `chunkNode`** — ADR-004 §6
  specifies `depth > maxChunkDepth` but the code used `>=`, capping effective
  recursion at depth 9 instead of 10 and swallowing innermost methods at the
  boundary into their parent class chunk.
- **Parser bug #13 — `namespace` rejected by `symbols.kind` CHECK constraint**
  — TypeScript namespace symbols were silently dropped at the DB layer after
  ADR-004 added `internal_module → namespace` to `SymbolKindMap`. Fixed by
  the sqlite + postgres migrations above.

## [0.9.1] — 2026-04-06

### Added

- **Migration validation tests** (`internal/parser/parser_test.go`) —
  `TestParse_ContextCancellation` (no panic on immediate cancel),
  `TestParse_InvalidLanguageReturnsError` (unsupported language error),
  `TestParse_AllLanguagesSmoke` (7 languages parse without error),
  `TestParse_ByteRangesValid` (chunk line ranges valid),
  `TestParse_TypeScriptClassSignature` (class signature byte slicing correct).

### Changed

- **Backend isolation** — storage and vector implementations moved into
  sub-packages (`internal/storage/sqlite/`, `internal/vector/bruteforce/`,
  `internal/vector/hnsw/`). Backends register via `init()` and are selected
  at runtime via config. The parent packages (`internal/storage/`,
  `internal/vector/`) are now pure registry + interface hubs.
- **Build tags for default backends** — `sqlite`, `bruteforce`, and `hnsw`
  are now gated by build tags (matching the existing pattern for `postgres`,
  `pgvector`, `qdrant`). This enables CGo-free builds for postgres-only
  deployments. Default build command is now:
  `go build -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...`
- **Test matrix resilience** — tests that target specific backends now skip
  gracefully when the backend is not compiled in, instead of failing.
- **Migrated tree-sitter bindings** — replaced stalled community
  `smacker/go-tree-sitter` (no activity since Aug 2024) with official
  `tree-sitter/go-tree-sitter` v0.25.0 and per-language grammar packages
  from `tree-sitter/*`. Actively maintained, semantically versioned.
- **Replaced deprecated `ParseCtx`** — migrated to `ParseWithOptions` with
  `ProgressCallback` for proper context cancellation. Callback checks
  `ctx.Done()` channel; nil-tree return differentiates cancellation from
  parse failure.

### Removed

- **Groovy language support temporarily dropped** — no official
  `tree-sitter/tree-sitter-groovy` Go bindings exist. Community fork has
  module path mismatch preventing use. Removed `.groovy`/`.gradle` extension
  mappings, `groovyConfig()`, groovy import/edge extraction, and related
  test fixtures. TODO comments mark all removal points for re-addition when
  official bindings become available.

### Dependencies

- Removed `github.com/smacker/go-tree-sitter` and all `smacker/go-tree-sitter-*`
  grammar packages.
- Added `github.com/tree-sitter/go-tree-sitter` v0.25.0.
- Added official grammar packages: `tree-sitter-typescript` v0.23.2,
  `tree-sitter-javascript` v0.23.1, `tree-sitter-python` v0.23.6,
  `tree-sitter-go` v0.23.4, `tree-sitter-rust` v0.23.2,
  `tree-sitter-java` v0.23.5, `tree-sitter-bash` v0.23.3.
- Added `github.com/mattn/go-pointer` v0.0.1 (transitive dependency).

## [0.9.0] — 2026-03-31

### Added

- **`BatchMetadataStore` interface** (`internal/types/interfaces.go`) — five
  batch query methods (`BatchGetSymbolIDsForChunks`, `BatchNeighbors`,
  `BatchGetChunkIDsForSymbols`, `BatchHydrateChunks`, `BatchGetFileHashes`)
  that eliminate N+1 query patterns on the search and indexing hot paths.
  All callers detect batch support via type assertion, falling back to
  legacy per-item queries for non-batch stores.
- **`HydratedChunk` entity** (`internal/types/entities.go`) — chunk data
  joined with file path and `is_test` in a single query result, used by
  `BatchHydrateChunks`.
- **`EmbedCache` LRU rewrite** (`internal/vector/embed_cache.go`) — replaced
  O(n) slice-scan eviction with `container/list`-backed doubly-linked list
  for O(1) move-to-back and eviction.
- **`SessionStore` LRU rewrite** (`internal/core/session.go`) — replaced
  O(n) slice-scan with `container/list` + atomic generation counter for O(1)
  LRU operations and O(1) decay computation.
- **File content carry-through** (`internal/daemon/scanner.go`,
  `internal/daemon/enrichment.go`) — `ScannedFile.Content` carries file
  bytes from scan to `enrichFile`, eliminating a second `os.ReadFile` per
  changed file on the indexing hot path.
- **`splitLargeChunks` incremental token counting** (`internal/parser/chunker.go`)
  — per-line incremental counting replaces O(n²) re-tokenization of the
  growing buffer; chunk boundaries emitted before the line that would
  exceed the limit.

### Changed

- MCP server version bumped to `0.9.0`.
- **Batch structural scores** (`internal/core/ranker.go`) —
  `computeStructuralScores` uses 3 batch SQL queries instead of ~3N
  individual queries (one `BatchGetSymbolIDsForChunks` JOIN, one
  `BatchNeighbors`, one `BatchGetChunkIDsForSymbols` IN-clause).
- **Batch FTS hydration** (`internal/core/retrieval.go`) —
  `hydrateFTSResults` uses 1 `BatchHydrateChunks` query instead of 3N
  per-result queries (`GetChunkByID` + `GetFilePathByID` + `GetFileIsTestByID`).
- **Batch merge hydration** (`internal/core/engine.go`) — `mergeResults`
  collects all unhydrated semantic-only chunk IDs, then batch-hydrates in
  1 query instead of 3N per-result queries.
- **Batch `filterChanged`** (`internal/daemon/enrichment.go`) —
  `BatchGetFileHashes` replaces N `GetFileByPath` calls; reduces
  ~12K queries to ~26 for Kubernetes-scale repos.
- **Heap-based top-K vector search** (`internal/vector/store.go`) —
  `BruteForceStore.Search` uses a min-heap of size K, allocating O(K)
  instead of O(N). 2.1× faster at 100K vectors (81ms → 38ms); allocation
  reduced from 1.5MB to 1.3KB per search at N=100K.
- **Fast vector serialization** (`internal/vector/store.go`) —
  `SaveToDisk` uses `math.Float32bits` direct byte encoding instead of
  `binary.Write` reflection. 18% faster (207ms → 170ms at 50K vectors);
  50% allocation reduction (310MB → 156MB).
- **SQLite cache and mmap tuning** — `cache_size=32MB`, `mmap_size=256MB`;
  up from default 2MB cache to reduce page thrash at scale.
- **Prepared statements for chunk/symbol insertion** — eliminates per-row
  SQL compilation in `processEnrichmentJob`; ~49 VDBE compilations saved
  per file for 50-chunk files.
- **`hex.EncodeToString` for SHA-256 hashes** (`internal/daemon/enrichment.go`,
  `internal/daemon/scanner.go`) — replaces `fmt.Sprintf("%x", ...)` to
  reduce allocations per hash.
- **1MB `bufio.Writer` for `SaveToDisk`** — up from default 4KB buffer to
  match typical write syscall granularity for large embedding files.

## [0.8.0] — 2026-03-30

### Added

- **Scope-based test file filtering** — all MCP tools (`search`, `context`,
  `symbols`, `dependencies`, `diff`) now exclude test files by default. New
  `scope` parameter: `"impl"` (default), `"test"` (test files only), `"all"`.
  Test classification stored as `is_test` on the `files` table, computed at
  index time from configurable glob patterns.
- **Configurable test file patterns** (`internal/types/config.go`) — `[test]`
  section in `shaktiman.toml` with `patterns` array. Auto-populated with
  language-specific defaults after indexing. Supports basename globs
  (`*_test.go`) and directory prefixes (`testdata/`, `__tests__/`).
- **`IsTestFile` utility** (`internal/daemon/scanner.go`) — path-based test
  file detection supporting Go, Python, TypeScript, JavaScript, Java, Groovy,
  Rust, and Bash patterns.
- **`GetFileIsTestByID` store method** (`internal/storage/metadata.go`) —
  single-column lookup for test file classification, used by all tool handlers
  during scope filtering.
- **Schema V3→V4 migration** (`internal/storage/schema.go`) — adds `is_test`
  column to `files` table. Existing files default to `is_test=0` (impl) until
  next re-index.
- **MCP server instructions** (`internal/mcp/server.go`) — `WithInstructions`
  set during initialize handshake, telling the LLM when to use shaktiman vs
  built-in tools and that test files are excluded by default.

### Changed

- MCP server version bumped to `0.8.0`.
- **Tool descriptions rewritten** — honest positioning as complementary to
  Grep/Glob (not a replacement), framing value around reducing context waste
  during codebase exploration. Each description now mentions default test
  exclusion.
- **CLAUDE.md templates updated** (`docs/reference/sample_claude.md`,
  `README.md`) — replaced "STOP RULE" / "INSTEAD of Grep" framing with
  bidirectional decision tables: when to use shaktiman, when to use Grep/Glob,
  and signs you should switch. Added scope guidance and subagent delegation
  template.

## [0.7.0] — 2026-03-29

### Added

- **`shaktiman init` command** (`cmd/shaktiman/main.go`) — scaffolds
  `.shaktiman/shaktiman.toml` with commented-out defaults. Refuses to
  overwrite an existing config. Allows users to configure vector backend
  and other settings before first index.
- **`--vector` CLI flag for `index`** (`cmd/shaktiman/main.go`) — selects
  vector store backend (`brute_force` or `hnsw`) at index time. Config
  resolution order: default → TOML → `--vector` flag.
- **CLI signal handling** (`cmd/shaktiman/main.go`) — `signal.NotifyContext`
  for SIGINT/SIGTERM with `defer d.Stop()`. Ctrl+C during embedding now
  saves progress to disk instead of losing it.
- **Periodic embedding checkpoint in CLI** (`cmd/shaktiman/main.go`,
  `internal/daemon/daemon.go`) — `RunPeriodicEmbeddingSave` exported
  wrapper runs 30s checkpoint saves during CLI embedding phase. Stops
  automatically when embedding completes.
- **TOML config loading for all CLI commands** (`cmd/shaktiman/main.go`,
  `cmd/shaktiman/query.go`) — `index`, `symbols`, `deps`, `diff`, and
  `enrichment-status` now call `LoadConfigFromFile` to respect
  `shaktiman.toml` settings (previously only `search` and `context` did).
- **CLI tests** (`cmd/shaktiman/main_test.go`) — `TestInitCmd_CreatesConfig`,
  `TestInitCmd_ExistingConfigNoOverwrite`, `TestIndexCmd_LoadsTOML`,
  `TestIndexCmd_VectorFlagOverridesToml`, `TestIndexCmd_InvalidVectorFlag`.
- **Daemon tests** (`internal/daemon/daemon_test.go`) —
  `TestRunPeriodicEmbeddingSave`, `TestRunPeriodicEmbeddingSave_NilVectorStore`,
  `TestStopSavesEmbeddings`.

### Changed

- MCP server version bumped to `0.7.0`.

## [0.6.0] — 2026-03-28

### Added

- **HNSW vector store backend** (`internal/vector/hnsw.go`) — `HNSWStore`
  adapter implementing `VectorStore` + `VectorPersister` interfaces, backed by
  hnswlib C++ via `midhunkrishna/hnswgo` CGo bindings. O(log n) approximate
  nearest-neighbor search as a config-selectable alternative to `BruteForceStore`.
  Cosine space with score conversion (`1.0 - distance`), automatic capacity
  growth via `ResizeIndex`, soft delete via `MarkDeleted`, atomic persistence
  with temp-file rename. 25 tests covering search, upsert, batch, delete,
  persistence round-trip, recall, concurrency (`-race` clean), and capacity
  growth.
- **`vector.backend` config** (`internal/types/config.go`) — `VectorBackend`
  field accepting `"brute_force"` (default) or `"hnsw"`. TOML parsing with
  validation in `[vector]` section. Sample config updated.
- **Vector store factory** (`internal/daemon/daemon.go`) — `newVectorStore()`
  selects backend based on config. `embeddingsPath()` returns backend-specific
  persistence path (`.hnsw` extension for HNSW, `.bin` for brute force) to
  avoid format conflicts.
- **`--format` CLI flag** (`cmd/shaktiman/main.go`) — persistent flag on root
  command accepting `json` (default, backward-compatible) or `text` (MCP-style
  plain text). Applied to all query subcommands: `search`, `context`, `symbols`,
  `deps`, `diff`, `enrichment-status`. Validated via `PersistentPreRunE`.
- **`--explain` flag for search** (`cmd/shaktiman/query.go`) — when using
  `--format text`, includes per-signal score breakdown in result headers.
- **Shared format package** (`internal/format/`) — extracted text formatters
  from `internal/mcp/format.go` into a new shared package with exported
  functions: `SearchResults`, `LocateResults`, `ContextPackage`, `Symbols`,
  `Dependencies`, `Diffs`, `IndexStats`. New text formatters for symbols,
  dependencies, diffs, and index stats.
- **Shared display types** (`internal/format/types.go`) — `SymbolResult`,
  `DepResult`, `DiffResult` structs with identical JSON tags to the previous
  local definitions. Used by both CLI and MCP to eliminate struct duplication.
- **Format package tests** (`internal/format/format_test.go`) — tests for all
  formatter functions including empty inputs, single/multi results, explain
  mode, and adjacent-same-file path elision.
- **Pull-based embedding pipeline** (`internal/vector/embedding.go`) —
  `RunFromDB` method replaces fire-and-forget channel submission with a
  cursor-based DB pagination loop. Zero data loss at any chunk count.
  Circuit breaker retry with bounded `maxRetries=30`, `Has()` reconciliation
  for crash recovery (skips chunks already in vector store loaded from disk).
- **Per-chunk embedded tracking** (`internal/storage/schema.go`) — schema
  migration v1→v2 adds `embedded INTEGER NOT NULL DEFAULT 0` column to
  `chunks` table with `idx_chunks_embedded` index. Enables cumulative
  multi-batch file completion tracking.
- **Embedding storage methods** (`internal/storage/metadata.go`) —
  `GetEmbedPage` (cursor-based pagination), `CountChunksNeedingEmbedding`,
  rewritten `MarkChunksEmbedded` with per-chunk `embedded` flag and
  cumulative file status (`pending` → `partial` → `complete`). Batches IDs
  in groups of 500 to stay within SQLite variable limits.
- **Embedding types** (`internal/types/interfaces.go`) — `EmbedJob`,
  `EmbedSource` interface, `EmbedProgress` struct. Placed in `types` package
  to avoid import cycles between `storage` and `vector`.
- **Daemon embedding integration** (`internal/daemon/daemon.go`) —
  `embedFromDB` method with `sync.Mutex` serialization between cold-index
  and branch-switch goroutines. `embeddingActive` atomic flag drives smart
  periodic save (30s during active embedding, 5min idle, 10s poll).
  Immediate save checkpoint on embed start for crash safety.
- **Watcher safety invariant** (`internal/daemon/writer.go`) — doc comment
  documenting how `processEnrichmentJob` + `vectorDeleter` ensure
  `RunFromDB` and watcher concurrency safety.
- **Embedding integration tests** (`internal/daemon/daemon_test.go`) —
  `TestEmbedProject_LargeChunkCount` (5200 chunks, zero drops),
  `TestEmbedProject_OllamaDown` (graceful failure),
  `TestEmbedProject_CrashRecovery` (Has() reconciliation),
  `TestEmbedProject_IncrementalAfterCold` (only new chunks re-embedded).
- **Embedding benchmarks** — `BenchmarkGetEmbedPage`,
  `BenchmarkMarkChunksEmbedded`, `BenchmarkCountChunksNeedingEmbedding`
  (`internal/storage/metadata_bench_test.go`); `BenchmarkRunFromDB_Throughput`
  (15K batches/sec), `BenchmarkRunFromDB_Memory`
  (`internal/vector/embedding_bench_test.go`).
- **Docs reorganization** — `docs/` flat files organized into
  `architecture/`, `design/`, `planning/`, `reference/` subdirectories.
- **Contributing guide** (`docs/reference/contributing_guide.md`) — test
  commands for unit, integration, benchmark, and coverage runs.
- **Indexing progress callback** (`internal/daemon/enrichment.go`) —
  `IndexProgress` type with `Indexed`, `Errors`, `Total` fields.
  `IndexAllInput.OnProgress` callback fires after each file indexed.
  `IndexProject` now accepts `onProgress func(IndexProgress)` parameter.
- **Embedding progress warnings** (`internal/types/interfaces.go`) —
  `EmbedProgress.Warning` string field populated during circuit breaker
  retries with attempt count. CLI displays warnings inline with progress.
- **Ollama health check** (`internal/vector/embedding.go`) —
  `EmbedWorker.EmbedderHealthy()` delegates to `OllamaClient.Healthy()`.
  `EmbedProject` calls it before starting embedding and returns a clear
  error when Ollama is unreachable.
- **TTY-aware CLI progress** (`cmd/shaktiman/main.go`) — `isTTY()`
  detection via `os.Stdout.Stat()`. TTY mode: `\r`-overwriting single
  line with percentage. Pipe mode: periodic log lines every 10%.
  Indexing and embedding progress displayed during `shaktiman index`.
- **Partial embedding failure handling** (`internal/daemon/daemon.go`) —
  `EmbedProject` now accepts `onProgress func(EmbedProgress)`, returns
  vector count even on error, saves embeddings to disk on partial failure.
  CLI prints warning with retry instructions when embedded < total.
- **Embedding progress tests** (`internal/vector/embedding_test.go`) —
  `TestEmbedderHealthy_Reachable`, `TestEmbedderHealthy_Unreachable`,
  `TestEmbedProgress_Warning`.
- **Indexing progress tests** (`internal/daemon/daemon_test.go`) —
  `TestIndexProgress_Callback`, `TestIndexProgress_NilCallback`,
  `TestEmbedProject_OllamaHealthCheck`.
- **Writer log test** (`internal/daemon/writer_test.go`) —
  `TestWriterChannelFull_LogsAtDebug` verifies channel-full messages
  log at Debug level.
- **CLAUDE.md MCP instructions** — STOP RULE gate before Grep/Glob,
  common mistakes table with negative examples, workflow recipes for
  refactoring (find callers, check tests, trace dependencies), strengthened
  subagent delegation template, checklist-style fallback policy.

### Changed

- MCP server version bumped to `0.6.0`.
- `internal/daemon/daemon.go` — `initEmbedding()` uses factory method instead
  of hardcoded `NewBruteForceStore`. Five `EmbeddingsPath` call sites updated
  to use `embeddingsPath()` helper.
- `internal/daemon/daemon.go` — `IndexProject` signature changed from
  `(ctx)` to `(ctx, onProgress func(IndexProgress))`. `EmbedProject`
  changed from `(ctx)` to `(ctx, onProgress func(EmbedProgress))`.
  `embedFromDB` passes through progress callback instead of inline logging.
- `internal/daemon/writer.go` — "writer channel full, blocking" log level
  changed from `Warn` to `Debug`. Expected backpressure behavior under load,
  not a problem requiring user attention.
- `internal/daemon/daemon_test.go` — mock Ollama server now handles
  `GET /` health check endpoint (returns 200 OK).
- `docs/reference/sample_claude.md` — rewritten with STOP RULE, negative
  examples, workflow recipes, and checklist fallback policy.
- `README.md` — inline CLAUDE.md template updated to match sample_claude.md.
  Added link to full template file.
- `internal/mcp/format.go` — functions replaced with thin delegates to
  `internal/format`. No behavior change.
- `internal/mcp/tools.go` — local `symbolResult`, `depResult`, `diffResult`
  structs replaced with `format.SymbolResult`, `format.DepResult`,
  `format.DiffResult`. JSON output unchanged.
- CLI query commands now use `format.*Result` types instead of local struct
  definitions.

### Dependencies

- Added `github.com/midhunkrishna/hnswgo v1.0.0` — CGo bindings to hnswlib
  C++ (fork of oligo/hnswgo with proper error propagation, idempotent `Free()`
  with finalizer safety net, `ErrIndexClosed` sentinel, `sync.RWMutex` on all
  methods).

## [0.5.0] — 2026-03-23

Phase 5 — Language Expansion: add Java, Groovy, Shell, and JavaScript support
with full-pipeline integration tests for all supported languages.

### Added

- **Java language support** (`internal/parser/languages.go`) — tree-sitter-java
  grammar with node mappings for `class_declaration`, `interface_declaration`,
  `enum_declaration`, `record_declaration`, `method_declaration`,
  `constructor_declaration`, `import_declaration`, `package_declaration`.
  Extensions: `.java`.
- **Groovy language support** — tree-sitter-groovy grammar with node mappings
  for `function_definition`, `function_declaration`, `class_definition`,
  `groovy_import`, `groovy_package`. Extensions: `.groovy`, `.gradle`.
- **Shell/Bash language support** — tree-sitter-bash grammar with
  `function_definition` chunking. Extensions: `.sh`, `.bash`.
- **JavaScript language support** — tree-sitter-javascript grammar (separate
  from TypeScript) with `function_declaration`, `class_declaration`,
  `generator_function_declaration`, `export_statement`, `import_statement`.
  Extensions: `.js`, `.jsx`, `.mjs`, `.cjs`.
- **Import edge extraction** (`internal/parser/edges.go`) — Java
  `scoped_identifier` → last name component; Groovy `dotted_identifier` → last
  dot-component; JavaScript delegates to TypeScript import logic.
- **Call edge extraction** — added `method_invocation` (Java) and
  `function_call` (Groovy) to call-expression detection.
- **Inheritance edge extraction** — Java `superclass`/`super_interfaces`;
  JavaScript `class_heritage`; Groovy `class_definition` superclass field.
- **Package declaration header routing** (`internal/parser/chunker.go`) —
  generalized to handle `package_declaration` (Java) and `groovy_package`
  alongside Go's `package_clause`.
- **Testdata fixtures** — `testdata/java_project/` (3 files),
  `testdata/groovy_project/` (2 files), `testdata/bash_project/` (2 files),
  `testdata/javascript_project/` (3 files).
- **Language compatibility integration tests** (`internal/daemon/daemon_test.go`)
  — `TestIntegration_LanguageCompatibility`: table-driven test exercising the
  full pipeline (scan → parse → index → search → context assembly) for all 7
  languages. `TestIntegration_MultiLanguageProject`: indexes a project with all
  7 languages simultaneously, verifies language stats and cross-language FTS
  search. `TestIntegration_IncrementalIndex_NewLanguage`: verifies incremental
  indexing correctly picks up newly added language files.
- **Parser unit tests** (`internal/parser/parser_test.go`) — 4 new tests:
  `TestParse_JavaClassWithMethods`, `TestParse_GroovyFunction`,
  `TestParse_BashFunctions`, `TestParse_JavaScriptClassWithMethods`.

## [0.4.0] — 2026-03-23

Phase 4 — Session Awareness & Operational Polish: session-aware ranking,
branch switch detection, summary tool, and production hardening.

### Added

- **Session store** (`internal/core/session.go`) — in-memory LRU map keyed on
  `filePath:startLine` for stability across chunk re-indexes. Max 2000 entries
  (~200KB). Three-signal scoring: recency (`exp(-0.07 * minutesAgo)`, ~10min
  half-life), frequency (`log2(accessCount+1)/4`, capped at 1.0), exploration
  decay (`exp(-0.1 * queriesSinceLastHit)`). `RecordAccess()`, `RecordBatch()`,
  `Score()`, `DecayAllExcept()` methods. Thread-safe via `sync.RWMutex`.
- **Session scoring in ranker** (`internal/core/ranker.go`) —
  `SessionScorer` interface in `types/interfaces.go`. `HybridRankInput` now
  accepts optional `SessionScorer`. Replaces hardcoded `sessionScore := 0.0`
  with actual lookup. `redistributeWeights()` now conditionally redistributes
  session weight only when scorer is nil.
- **Branch switch detection** (`internal/daemon/watcher.go`) — tracks file
  change rate in `flushPending()`. When >20 source files change within a single
  flush cycle, emits non-blocking signal on `branchSwitchCh`. Daemon handles
  signal by re-running `ScanRepo()` + `IndexAll()` + embedding queue.
- **Summary MCP tool** (`internal/mcp/tools.go`) — `summary` tool returns
  workspace overview: total files, chunks, symbols, language breakdown,
  embedding percentage, parse errors, stale files. Read-only, idempotent.
- **Shutdown grace period** (`internal/daemon/daemon.go`) — `Stop()` now uses
  a 15-second timeout context for writer drain. Logs shutdown duration in
  milliseconds. Prevents indefinite hang on stuck writer.
- **Performance benchmarks** (`internal/core/`) — `BenchmarkKeywordSearch`,
  `BenchmarkHybridRank` (100 candidates), `BenchmarkContextAssembly` (50
  candidates, 4096 budget), `BenchmarkSessionStore_Score` (~113ns),
  `BenchmarkSessionStore_RecordAccess` (~210ns),
  `BenchmarkSessionStore_RecordBatch` (~632ns).
- **Session store tests** (`internal/core/session_test.go`) — 8 unit tests:
  record/score, batch recording, LRU eviction, eviction with access refresh,
  exploration decay, time-based score decay, concurrent access with `-race`.
- **Ranker session test** (`internal/core/ranker_test.go`) —
  `TestHybridRank_WithSessionScorer` validates session signal affects ranking
  order. `TestRedistributeWeights_AllSignalsAvailable` validates no
  redistribution when all 5 signals are present.

### Changed

- MCP server version bumped to `0.4.0`.
- `redistributeWeights()` signature: now accepts `sessionReady bool` parameter
  instead of unconditionally zeroing session weight.
- `HybridRankInput.SessionScorer` field added (nil = session unavailable).
- `QueryEngine` gains `sessionStore` field with `SetSessionStore()` setter.
  Session scores recorded after every search via `recordSession()`.
- Daemon `New()` creates a `SessionStore(2000)` and wires it to the engine.

## [0.3.1] — 2026-03-23

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
- **`docs/reference/sample_claude.md`** — ready-to-copy CLAUDE.md template for projects
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
- **Interface decoupling** (`internal/core/`, `internal/types/interfaces.go`) —
  core package now depends on `types.MetadataStore` and `types.VectorStore`
  interfaces instead of concrete `*storage.Store` / `*vector.BruteForceStore`.
  `MetadataStore` expanded with `GetSymbolByID`, `GetFilePathByID`,
  `KeywordSearch`, `ComputeChangeScores`, `Neighbors`. `FTSResult` moved to
  `types` package (storage uses type alias for compatibility).
- **`ComputeChangeScores` batched** (`internal/storage/diff.go`) — rewritten
  from N+1 per-chunk queries to 2 batched `IN (...)` queries (symbol-level +
  file-level fallback). O(1) per chunk instead of O(N) round trips.
- **`MarkChunksEmbedded` batched** (`internal/storage/metadata.go`) — replaced
  per-chunk `SELECT` loop with `map[int64]bool` membership check and single
  batched `SELECT DISTINCT file_id FROM chunks WHERE id IN (...)` query.
- **`SaveToDisk` snapshot-then-release** (`internal/vector/store.go`) — copies
  vector map under RLock, releases lock before disk I/O. Eliminates writer
  stalls during persistence.
- **Log rotation** (`cmd/shaktimand/main.go`) — on startup, moves existing
  `shaktimand.log` to `.shaktiman/session-logs/<timestamp>.log` instead of
  truncating. Preserves logs from previous sessions.

### Fixed

- **Tree-sitter C memory leak** (`internal/parser/parser.go`) — added
  `defer tree.Close()` after parse. Without this, every parsed file leaked
  a C-heap tree allocation (~KBs each), growing unbounded over long sessions.
- **WriterManager deadlock** (`internal/daemon/writer.go`) — `Submit()` now
  releases mutex before blocking channel send. Previous code held the lock
  during `wm.ch <- job`, blocking `Close()` which also acquires the mutex.
  Added `<-wm.done` select fallback to unblock on shutdown.
- **FTS external-content staleness** (`internal/storage/fts.go`,
  `internal/daemon/daemon.go`) — added `IsFTSStale()` that compares chunk
  count vs FTS row count. Daemon checks on startup and triggers `RebuildFTS()`
  if stale. Guards against silent index corruption from crashes during bulk
  inserts with triggers disabled.
- **Metrics send-on-closed-channel panic** (`internal/mcp/metrics.go`) —
  replaced `close(r.ch)` shutdown with deadline-based drain (1s). Channel is
  never closed; GC collects it after `metricsRecorder` exits. Eliminates race
  between late `Record()` calls and channel close.
- **Watcher goroutine ordering** (`internal/daemon/daemon.go`) — wrapped
  watcher event goroutine with `AddProducer()`/`RemoveProducer()` so
  `WriterManager` waits for watcher-submitted jobs to drain before shutdown.
  Previously the watcher goroutine could orphan in-flight jobs.
- **`EmbedProject` premature return** (`internal/vector/embedding.go`,
  `internal/daemon/daemon.go`) — added `inflight sync.WaitGroup` to
  `EmbedWorker` tracking in-flight `processBatch` calls. `WaitIdle()` waits
  for both queue drain and batch completion. Replaces polling+sleep(1s) which
  could return before the final batch finished writing vectors.

### Dependencies

- Added `github.com/BurntSushi/toml v1.6.0`.

## [0.3.0] — 2026-03-21

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

## [0.2.0] — 2026-03-20

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

## [0.1.0] — 2026-03-20

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

[0.9.2]: https://github.com/shaktimanai/shaktiman/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/shaktimanai/shaktiman/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/shaktimanai/shaktiman/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/shaktimanai/shaktiman/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/shaktimanai/shaktiman/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/shaktimanai/shaktiman/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/shaktimanai/shaktiman/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/shaktimanai/shaktiman/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/shaktimanai/shaktiman/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/shaktimanai/shaktiman/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/shaktimanai/shaktiman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/shaktimanai/shaktiman/releases/tag/v0.1.0
