# 11 — Critique Remediation Plan

Remediates findings from the 2026-04-24 module-by-module critique-implementation pass across `internal/storage`, `internal/vector`, `internal/parser`, `internal/daemon + proxy + lockfile`, `internal/core + types + eval`, `internal/mcp`, and `cmd/`.

14 Must-Fix items grouped into 6 shippable batches. Correctness first, refactor last.

## Guiding principles

- One PR per batch, one commit per logical step on the feature branch.
- Tests ≥90% on new code. For bug fixes, add a failing test first.
- No observability-only PRs. Fold minimal counters into each correctness PR where they help verify the fix. Full metrics stack lands in Batch 6.
- Don't mix correctness and refactor in the same PR.
- Re-index implication called out per batch. Users get one "hard re-index required after upgrade" CHANGELOG note covering all affected PRs.

---

## Batch 1 — Ship in parallel (independent, small, HIGH severity)

### PR 1a: Postgres cross-project scoping

Reason: multi-project-per-Pg is a deployed topology; getters that don't filter by `project_id` return rows from a different project of the same user.

- `internal/storage/postgres/metadata.go` — add `WHERE files.project_id = $N` (via JOIN on files) to every getter that takes an id without project scoping:
  - `GetSymbolByID`
  - `GetFilePathByID`
  - `GetFileIsTestByID`
  - `GetChunkByID`
  - `GetChunksByFile`
  - `GetSymbolsByFile`
  - `BatchGetChunkIDsForSymbols`
- `GetSymbolByName` — verify scope is already applied.
- `UpsertFile` conflict `SET` clause — add `project_id = EXCLUDED.project_id`.
- `GetIndexStats` rows loop — stop swallowing scan errors.

Tests: new `postgres_isolation_test.go`. Two projects sharing a Pg; each getter called with project A's id returns empty for project B's rows. Failing test first.

Query-plan sanity: `EXPLAIN` confirms `idx_files_project` is used on the added JOIN.

### PR 1b: CLI stdout pollution

Reason: `shaktiman --format=json | jq` is broken because index progress text goes to stdout.

- `cmd/shaktiman/main.go:120-188` (runIndexPipeline) — route progress (`\r`, "Indexed: N files…") to `os.Stderr`. Stdout reserved for final structured payload.
- Audit all CLI command entrypoints for stray stdout writes during execution.

Tests: table-driven test that runs `shaktiman search --format=json` via `exec.Command`, pipes stdout into `json.Unmarshal`, asserts no decode error. Also asserts stderr contains expected progress substrings.

### PR 1c: Session race and ranker slice aliasing

Reason: `core.QueryEngine.Search` is called concurrently from MCP handlers. Current `recordSession` releases lock between `RecordBatch` and `DecayAllExcept`; interleaved callers corrupt session state.

- `internal/core/engine.go:312` (`recordSession`) — combine `RecordBatch` + `DecayAllExcept` under a single lock acquisition. Prefer a new `Session.RecordAndDecay(hits)` method over borrowing+releasing twice.
- `internal/core/engine.go:59` (`filterByScore`) — return a fresh slice. Document the contract.

Tests: concurrent test with `t.Parallel()` + `-race` hitting `Search` from 16 goroutines with a deterministic seed; assert identical serialized result streams across runs. Include a benchmark for the longer lock hold.

---

## Batch 2 — Correctness fixes per module

### PR 2a: Storage error-swallowing audit

- `internal/storage/sqlite/metadata.go:148` and `internal/storage/postgres/metadata.go:106` (`DeleteFileByPath`): use `errors.Is(err, sql.ErrNoRows)` / `errors.Is(err, pgx.ErrNoRows)`. Wrap in `WithWriteTx`.
- Grep the entire storage module for `if err != nil { return nil }` and similar silent-swallow patterns; fix each.
- Pg `UpdateChunkParents` — wrap in `WithWriteTx`. Replace N round-trips with a single `UPDATE … FROM (unnest($1, $2)) AS v(id, parent)` batch.

Tests: inject errors via a wrapped driver (sqlmock for sqlite, test Pg for postgres); assert non-nil, non-ErrNoRows errors propagate.

### PR 2b: ResolvePendingEdges file_id preservation

Reason: `DeleteEdgesByFile` relies on `file_id`. Today `ResolvePendingEdges` INSERTs without it, so resolved edges are never cleaned on file delete — stale graph.

- Schema change: add `file_id BIGINT NOT NULL` to `pending_edges` in new migration `007_pending_edges_file_id.sql` for both SQLite and Postgres.
- Migration truncates existing `pending_edges` (no backfill). Users get a "hard re-index required after upgrade" note in CHANGELOG.
- Update `internal/storage/sqlite/graph.go:116` and `internal/storage/postgres/graph.go:89` INSERT statements to preserve `file_id` from the pending row.

Tests: scenario — file A declares edge to undefined symbol; later file B defines it; on re-index of A, edge is correctly cleaned via `DeleteEdgesByFile`.

### PR 2c: Vector integrity

- `internal/vector/bruteforce/store.go:82` (`cosineSimilarity`) — per-entry `len(vec) == s.dim` guard; reject NaN/Inf.
- `internal/vector/bruteforce/store.go:316` (`LoadFromDisk`) — decode into a local map, atomic replace `s.vectors` only on successful CRC verification. Old state preserved on corruption.
- `internal/vector/embed_worker.go` — add a single rejection path at the worker boundary for zero/NaN/dim-mismatched embeddings. Applies uniformly to all backends.
- Optional: surface the hnsw "cannot return results" path as a `warn` + counter instead of silent empty.

Tests: unit tests per guard. Corruption test writes a truncated `embeddings.bin`, asserts load fails and store state unchanged.

### PR 2d: Parser robustness

- `internal/parser/chunker.go:236` (`findChunkableChildren`) — increment depth on structural recursion too, not only chunkable. Alternatively add an independent AST-depth cap.
- `internal/parser/parser.go:63` — `defer recover()` around tree-sitter CGO calls. Log panic, increment counter, return parse error instead of crashing the daemon worker.
- `internal/parser/edges.go:20` (`addEdge`) — dedup key includes `qualifiedDst` (`src|qualifiedDst|kind`). Preserves two imports of the same short name from different modules.
- `internal/parser/symbols.go:273` (`findContainingChunk`) — return `-1` sentinel when no chunk matches. Callers handle.

Tests: malformed-UTF-8 file that previously panicked; deeply-nested AST that bypassed the depth guard; two-import-same-short-name test that asserts both edges survive.

Re-index: required (edge dedup change alters graph shape). Covered by the same CHANGELOG note as 2b.

---

## Batch 3 — Daemon/proxy hardening

### PR 3: Daemon resilience

One cohesive PR because the changes are interdependent (framing, timeouts, readiness, shutdown all interact).

- `cmd/shaktimand/main.go:198` — promotion with jittered backoff (50–500ms). On lost re-exec race, retry flock directly rather than only proxying. Raise `WaitForSocket` timeout on promotion to ~30s.
- `internal/proxy/bridge.go:39` — per-request `context.WithTimeout`. `http.Transport.ResponseHeaderTimeout` configured. Stdout watchdog.
- `internal/proxy/bridge.go:53` — replace `bufio.Scanner` (1 MiB hard cap) with `json.Decoder` so large JSON-RPC messages don't silently kill the bridge. If framing constraints require length-prefixed reads, raise the cap and surface `ErrTooLong` as an RPC error to the client.
- `internal/proxy/bridge.go:79` — refresh `sessionID` from every response carrying a new one; thread-safe access.
- `internal/proxy/wait.go:12` — readiness marker (`.shaktiman/ready`) written after `socketServer.Serve` begins accepting. `WaitForSocket` polls the marker and connects.
- `cmd/shaktimand/main.go:203` — re-exec uses `os.Executable()` (backed by `/proc/self/exe` on Linux, canonicalized on darwin). Not `os.Args[0]`.
- `internal/daemon/daemon.go:431` (`Stop()`) — longer graceful deadline or broadcast-shutdown response to in-flight handlers so proxies don't see mid-stream EOF and deliver partial JSON to clients.

Tests: harness that starts two daemons for the same project; kills the leader; asserts exactly one proxy promotes within N ms. May be flaky in CI — mark `-short` skip if needed.

---

## Batch 4 — Operational

### PR 4a: Pg migration 006 safety

Reason: `006_add_project_id.sql:19` claims three-step safety but `ALTER TABLE … SET NOT NULL` still takes ACCESS EXCLUSIVE + full scan. Risky on large Pg.

- New migration `007_project_id_not_null_safe.sql` using `ADD CONSTRAINT … CHECK (project_id IS NOT NULL) NOT VALID; VALIDATE CONSTRAINT …`.
- Leave 006 in place for installs already past it. Gate 007 behind 006 completion.

Tests: migration test on a populated table; measure lock duration.

### PR 4b: shaktimand flag surface + startup banner

- `cmd/shaktimand/main.go:24` — switch from positional args to cobra or `flag`. Add `--help`, `--version`, `--log-level`, `--pprof-addr`.
- Startup banner: version, commit, build tags (sqlite/sqlite_fts5/postgres/pgvector/hnsw/bruteforce), vector + DB backend, socket path. One structured slog line so it's greppable.
- `signal.Ignore(syscall.SIGPIPE)` on daemon startup. SIGHUP for log reopen is optional.
- Validate `SHAKTIMAN_LOG_LEVEL`; log a warning on unknown values instead of silently defaulting.

---

## Batch 5 — Smaller correctness

### PR 5a: Symlink containment + rune-safety

- `internal/core/fallback.go:108` — replace `strings.HasPrefix(absPath, absRoot+sep)` with `filepath.Rel` + `..` prefix check. Case-fold on darwin/windows to defend against APFS case-insensitivity.
- `internal/core/fallback.go:142` and `internal/core/assembler.go:186` — rune-safe truncation (`utf8.RuneCount`, `utf8.DecodeLastRune`).
- `internal/core/retrieval.go:305` (`mergeSiblings`) — sort siblings by StartLine before joining content.
- `internal/core/assembler.go:58` — report overflow flag or cap `TotalTokens` at the budget when a chunk would push past.

### PR 5b: MCP safety

- `internal/mcp/tools.go` — response-size cap in `withMetrics` (truncate or error when marshaled bytes > configured max). Protects against MCP frame-size overruns that look like opaque client disconnects.
- `internal/mcp/metrics.go:17` — replace `resultCountMap` (pointer-keyed `sync.Map` with leak + potential pointer-reuse race) with closure-scoped count passed through an internal handler type, or an `_meta` field on the result.
- `internal/mcp/tools.go:367` — reject invalid `diff.limit` (no silent clamp) and negative/zero `since` durations. Same treatment in `cmd/shaktiman/query.go:380`.
- Per-call timeout wrapper around each handler.

---

## Batch 6 — Refactor, cleanup, observability

### PR 6a: Typed enums

Convert stringly-typed domain enums to typed string constants. Mechanical, touches many files, no behavior change.

- `internal/types`: `Kind`, `Visibility` → typed `string`-based constants (`ChunkKindHeader`, `SymbolVisibilityPublic`, etc.).
- `internal/mcp`: `Mode`, `Scope`, `Direction` — same.
- Add a test or `go vet` check that bars magic string literals in the enum set.

### PR 6b: Deduplication

Split into sub-PRs if one balloons:

- `cmd/shaktiman`: extract `withProjectStore(root, fn func(cfg, store) error) error` helper; collapse 7 duplicate openStore blocks in query commands.
- `internal/mcp`: extract `buildTool(name, desc, opts…)` helper that applies readonly/idempotent/non-destructive annotations and scope enum automatically. Collapse 7 near-identical ToolDef funcs.
- `internal/parser`: replace language `switch`es in `GetLanguageConfig`/`SupportedLanguage` with `var languageRegistry = map[string]func() *LanguageConfig{…}`. Declarative import-node table removes duplicated walkers in `edges.go`.
- `internal/storage`: extract shared embed-status helper used by `ResetEmbeddedFlags` and `MarkChunksEmbedded` across both backends. Reduce four near-identical loops to one.

### PR 6c: Observability

Design fits the local-first posture. Zero required deps; Prometheus optional behind a build tag.

**Phase 1 (this PR):**

- `internal/observability` — small `metrics.Registry` interface (counter/gauge/histogram). Default in-memory impl, zero deps. Wire from every module that produced "no metrics" findings.
  - Parser: parse duration, ERROR-node density, chunk count per file, tree-sitter recover count.
  - Vector / embed: embed latency histogram, queue drops, NaN/dim rejections, circuit-breaker state transitions, bruteforce dim-mismatch rejections, pgvector trim-below-topK, hnsw result-underflow.
  - Storage: slow FTS query count, Pg cross-project-guard hits (defense-in-depth counter).
  - Daemon: `lock.acquired`, `lock.contended`, promotion attempts and failures, proxy connect/disconnect, forwarded request latency, in-flight proxy count.
  - MCP: per-tool call count, latency histogram, error rate, response-size distribution.
  - Core: fallback-level distribution, embed-cache hit ratio, session decay rate.
- `cmd/shaktiman stats` subcommand — queries the leader over the UDS socket, prints human-formatted counters. Primary UX for diagnosis.
- Rolling `.shaktiman/metrics.jsonl` — append every 30s or on shutdown. Compress+rotate after N MB. Enables "what happened 3 minutes ago" post-mortem from a bug report.
- Structured `slog` events for discrete occurrences already called out above (promotion, tree-sitter recover, queue drop, NaN rejection).

**Phase 2 (later, optional):**

- `--metrics-addr=127.0.0.1:9100` flag serves Prometheus text format. Bound to loopback. Gated by build tag `prometheus` so the default build has zero extra deps. For power users running local Grafana.
- No OpenTelemetry. Overkill for a local-first daemon.

### PR 6d: Eval hardening

- `internal/eval/eval.go:87` — split `expected` into `expected_files` and `expected_symbols`. Require symbol matches to match by SymbolName.
- Add noise queries (query terms with no expected result) to catch false positives.
- Add a regression threshold (e.g. min MRR of 0.6) that fails CI when retrieval quality regresses.
- Optionally, add eval coverage for `Context` tool in addition to `Search`.

---

## Total sequencing

| Day     | Batch                                         |
| ------- | --------------------------------------------- |
| 1–2     | Batch 1 (1a, 1b, 1c — parallelizable)         |
| 3–6     | Batch 2 (2a, 2b, 2c, 2d)                      |
| 7–8     | Batch 3 (PR 3)                                |
| 9       | Batch 4 (4a, 4b)                              |
| 10      | Batch 5 (5a, 5b)                              |
| 11–14   | Batch 6 (6a, 6b, 6c, 6d)                      |

Realistic: ~2 weeks for a single person. Batches 1–3 carry the correctness weight (10 days); 4–6 are polish and tooling.

---

## Cross-cutting notes

- **Re-index note for CHANGELOG:** a single entry covers PR 2b and PR 2d — users performing the upgrade should run `shaktiman index` from scratch (delete `.shaktiman/` or equivalent reset) after migration 007 applies.
- **Build tag for observability:** Prometheus impl lives under `//go:build prometheus`. Default `go build` stays dep-free.
- **Tests:** every correctness PR adds a failing test before the fix. Race tests run under `-race`. Migration tests use a real Pg instance gated by `-tags postgres`.
