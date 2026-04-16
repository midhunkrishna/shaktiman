# Architecture v3 — Implementation Status (Today)

> Cross-reference between the v3 design doc (`03-architecture-v3.md`) + its addendum
> (`03-architecture-v3-addendum.md`) and the code as it ships today. Prepared as part of
> the documentation-site rollout audit (`website/plans/docusaurus-site-setup.md`).
>
> **Rule of thumb:** if this status file and the v3 design disagree, **the code is
> canonical** — use this status file for the shipped behavior. The v3 doc is retained
> for historical design intent.

---

## Shipped, as described

- **5-signal hybrid ranker** (semantic + structural + change + session + keyword) with the
  fallback chain described in §3.2. Implementation: `internal/core/`.
- **Token-budgeted context assembly** with 95% safety margin, line-range overlap dedup,
  and capped structural expansion. Implementation: `internal/core/assembler.go`.
- **Tree-sitter parser** with partial-parse handling. Implementation: `internal/parser/`.
- **File watcher with debounce and branch-switch detection.** Implementation:
  `internal/daemon/watcher.go`.
- **Circuit breaker on the embedding client** (CLOSED / OPEN / HALF_OPEN / DISABLED),
  matching Addendum A7. Implementation: `internal/vector/`.
- **SQLite WAL-mode storage** with a single Writer path for bulk inserts, FTS5 rebuild
  optimization from Addendum A11. Implementation: `internal/storage/sqlite/`.
- **Incremental re-indexing** with per-file change detection. Implementation:
  `internal/daemon/enrichment.go`.

## Shipped, but different from v3

| Area | v3 design says | Ships as | Why |
|---|---|---|---|
| MCP tool surface | 6 tools (search, context, symbols, dependencies, diff, summary) with the parameter sets in §3.1 | **7 tools**: the above six **plus `enrichment_status`**, and every tool has a richer parameter surface than the doc shows (e.g. `search` accepts `mode`, `max_results`, `min_score`, `explain`, `path`, `scope`; `context` accepts `budget_tokens`, `scope`; etc.) | Tool surface evolved during implementation to support operational UX and locate/full modes. Authoritative schema: `internal/mcp/tools.go`. |
| Vector store | "Brute-force in-process (default), Qdrant optional" per FD-3 | **Four pluggable backends**: `brute_force` (default), `hnsw`, `qdrant`, `pgvector` — all registered via `internal/vector/registry.go`, selected by build tag and/or config. | ADR-003 introduced pluggable backends. HNSW and pgvector were added after v3. |
| Metadata store | SQLite only (Layer 5) | **Two pluggable metadata backends**: `sqlite` (default) and `postgres` (ADR-003), selected via build tag + `DatabaseBackend` config. Validation in `ValidateBackendConfig` rejects `postgres` with file-backed vector stores (A12). | ADR-003. |
| Graph store | CSR (in-memory) + SQLite persist, with delta buffer (A2) and versioned reads (A4) | **SQLite-only graph traversal** (recursive CTEs). CSR not implemented. Per FD-4, CSR was deferred to a later phase and never added. | Measured SQLite CTE latency was acceptable; CSR work was deferred. |
| Config file | `.shaktiman/config.json` per-project (FR-19) | **`.shaktiman/shaktiman.toml`** — TOML, not JSON. Schema mirrors the `Config` struct in `internal/types/config.go`. | TOML was adopted during implementation for better readability and comment support. |
| CLI commands | `init, query, status, diff, reindex, config, inspect, mcp-config` (line 242) | **Eleven commands**: `init, index, reindex, status, search, context, symbols, deps, diff, enrichment-status, summary`. No `config`, `inspect`, or `mcp-config` command. `query` is split into `search`/`context`/`symbols`/`deps`. | CLI was refactored to expose every MCP tool as a sibling command for scripting. |
| Watcher debounce | 100ms (§3.5 "Branch switch" row) | **200ms default** (`WatcherDebounceMs`, configurable). | Observed stability improvement during bursty edits. |

## In the design but NOT shipped

These are aspirational features that the code does **not** implement today. They should
**not** be presented on the user-facing docs site as current capabilities — only in the
historical "Design & ADRs" section with a note.

- **MCP Resources** (FR-14, §3.1). `shaktiman://context/active` and
  `shaktiman://workspace/summary` are not registered.
- **MCP Prompts** (§3.1). The `task-start` prompt is not registered.
- **MCP Notifications** (§3.1). No `context/changed` notification is emitted. Agents
  must pull, not listen.
- **Push mode end-to-end flow** (§4.2). Depends on the three items above.
- **CSR graph, delta buffer, versioned reads** (§3.3, Addendum A2–A4). Graph traversal
  uses SQLite recursive CTEs.

## Shipped but NOT in the v3 design

These are load-bearing features that arrived after v3 was written. The v3 doc doesn't
mention them at all — consult the linked ADRs.

- **Multi-instance leader/proxy daemon** (ADR-002). `flock` on
  `.shaktiman/daemon.pid`, Unix socket at `/tmp/shaktiman-<hash>.sock`,
  re-exec-on-promotion. Implementation: `internal/lockfile/`, `cmd/shaktimand/`,
  `internal/proxy/`.
- **Postgres metadata backend** (ADR-003). See table above. Implementation:
  `internal/storage/postgres/` (build tag `postgres`).
- **Qdrant and pgvector vector backends** (ADR-003). Implementation:
  `internal/vector/qdrant/`, `internal/vector/pgvector/` (build tags `qdrant`,
  `pgvector`).
- **Recursive AST-driven chunking** (ADR-004). Implementation: `internal/parser/`
  (container / leaf node handling).
- **Embedding task prefixes** (`EmbedQueryPrefix`, `EmbedDocumentPrefix`). Supports
  nomic-embed-text's `search_query:` / `search_document:` prefixes.

## Open questions / follow-up

- Revisit CSR vs. SQLite CTE latency once multi-host/large-repo profiles are available.
- Decide whether push-mode (MCP resources + notifications) is still a priority or should
  be formally removed from the roadmap.
- Align v3 §3.1 tool/parameter tables with `internal/mcp/tools.go` — or leave the v3
  doc frozen as historical and point readers to this status file + the user-facing
  reference pages.

---

*Last updated: 2026-04-16 during documentation-site rollout audit.*
