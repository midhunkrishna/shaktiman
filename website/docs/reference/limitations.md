---
title: Known Limitations
sidebar_position: 3
---

# Known Limitations & Workarounds

Surprises and constraints that the shipped code has today, grouped by how much they're
likely to bite you. Each entry is anchored to authoritative source (code file or
design record) so you can verify whether it still holds.

## Backend combinations

### Postgres requires pgvector or Qdrant

When `[database].backend = "postgres"`, Shaktiman rejects `[vector].backend =
"brute_force"` or `"hnsw"`.

- **Why.** Both local vector backends persist to `embeddings.bin` on disk. When
  multiple `shaktimand` processes share the same Postgres database, they'd race on
  that file. The config validator refuses the combination up front.
- **Workaround.** Use `pgvector` (vectors in the same Postgres) or `qdrant`
  (externalised vector service) when running Postgres metadata.
- **Source.** `internal/types/config.go` — `ValidateBackendConfig`;
  [ADR-003](/design/adr-003-pluggable-backends) A12.

### pgvector implies postgres

`[vector].backend = "pgvector"` requires `[database].backend = "postgres"` — pgvector
is an extension of the same Postgres instance.

- **Source.** `ValidateBackendConfig`.

## Embedding

### Only Ollama is a first-class embedding backend

The embedding client in `internal/vector/ollama.go` speaks the Ollama HTTP API. There
is no built-in support for OpenAI, Voyage, or other hosted embedding providers today.

- **Workaround.** Ollama serves many open-source embedding models locally (e.g.
  `nomic-embed-text`, `mxbai-embed-large`). If you need a hosted provider, the
  embedding client is isolated — contributions welcome.
- **Source.** `internal/vector/ollama.go`; CLAUDE.md.

### Embedding dimensions must match the model

`[embedding].dims` must match the vector length the configured model actually
produces. Mismatches surface as errors during insertion or, worse, as silent
corruption of vector data.

- **Workaround.** Use the documented dimensionality for your model: 768 for
  `nomic-embed-text`, 1024 for `mxbai-embed-large`, etc.
- **Source.** `internal/types/config.go` — validation clamps to `1–4096` but does not
  cross-check against the model.

### Circuit breaker goes `disabled` after persistent failure

If Ollama is unreachable through several open/half-open cycles, the embedding circuit
breaker transitions to `disabled` (see [`enrichment_status`](/reference/mcp-tools/enrichment-status)).
Semantic search then runs in keyword-fallback mode until you restart the daemon with
Ollama reachable.

- **Workaround.** Confirm Ollama is running (`curl localhost:11434/api/tags`), then
  restart `shaktimand`. The circuit breaker resets on startup.
- **Source.** `internal/vector/` — circuit breaker state machine; addendum A7 on v3.

## Architecture features not yet shipped

### No MCP resources

The v3 design introduced `shaktiman://context/active` and
`shaktiman://workspace/summary` as MCP resources, plus a `context/changed`
notification and a `task-start` prompt. These are **not** registered in the shipped
server — agents must pull via the tools, not listen for push events.

- **Source.** `internal/mcp/tools.go` (only tools are registered) and
  `docs/architecture/03-architecture-v3-status.md`.

### No CSR graph

The v3 design describes an in-memory CSR graph for BFS-based structural scoring. The
shipped system uses SQLite recursive CTEs for call-graph traversal instead — fast
enough at current scale, and simpler to keep consistent.

- **Impact.** Deep BFS queries against very large repos may slow down faster than the
  v3 timing tables suggest. Flag an issue if you measure specific pain here.
- **Source.** `docs/architecture/03-architecture-v3-status.md` (FD-4 was never
  implemented).

## Parser behavior

### Some constructs aren't chunked per language

Tree-sitter grammar coverage is not uniform across languages. Some constructs that
look like chunks to a reader don't produce a chunk today.

- **Where to check.** `docs/review-findings/parser-bugs-from-recursive-chunking.md`
  tracks specific regressions from the recursive-chunking rollout
  ([ADR-004](/design/adr-004-recursive-chunking)).
- **Source.** `internal/parser/languages.go` — each language's `ChunkableTypes` map
  is the authoritative list.

### Symbol collisions across scopes

When two unrelated symbols share a name (e.g. a method `Send` on different types), the
symbols / dependencies tools return both — there's no scope-aware disambiguation at
query time beyond name + optional `kind` filter.

- **Workaround.** Use `--kind` / `kind:` to narrow to a symbol kind. When that's not
  enough, use [`search`](/reference/mcp-tools/search) with the qualifying context
  around the symbol to rank the version you want highest.
- **Source.** `docs/planning/09-symbol-collision.md`;
  `core.LookupSymbols`.

## Runtime

### Daemon is single-leader per project

Multiple `shaktimand` invocations on the same project root auto-elect a leader via
`flock`. Additional invocations become proxies that forward MCP requests to the leader
over `/tmp/shaktiman-<hash>.sock`. This is the design
([ADR-002](/design/adr-002-multi-instance)), not a bug — but it means only one process
is writing the index at any time.

- **Impact.** If the leader becomes unresponsive but still holds the lock, proxies
  will also appear frozen. Killing the leader releases the lock and one proxy will
  promote itself.
- **Source.** `internal/lockfile/`, `internal/proxy/`, `cmd/shaktimand/main.go`.

### `diff` duration strings must be Go durations

`diff --since` and the `diff` MCP tool use Go's `time.ParseDuration`. That means `"d"`
and `"w"` aren't accepted — use `"168h"` for a week.

- **Source.** `cmd/shaktiman/query.go` — `diffCmd`; `internal/mcp/tools.go` —
  `diffHandler`.

### CLI `reindex` is destructive; the TTY check enforces confirmation

`shaktiman reindex` deletes every indexed artifact before rebuilding. On non-TTY runs
(CI, scripts) it refuses unless you pass `--force`.

- **Source.** `cmd/shaktiman/main.go` — `reindexCmd`.

## See also

- [Troubleshooting](/troubleshooting/overview) — symptom-to-fix lookup for these and
  other issues.
- [Architecture Status](https://github.com/midhunkrishna/shaktiman/blob/master/docs/architecture/03-architecture-v3-status.md)
  — the canonical shipped-vs-designed delta.
