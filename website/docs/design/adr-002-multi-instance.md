---
title: ADR-002 — Multi-Instance Concurrency
sidebar_position: 3
---

# ADR-002: Multi-Instance Concurrency

**Status:** AMENDED (4 amendments). **Status today: SHIPPED** as D1″ — single-daemon + socket proxy.

:::info[This is a summary]

The full ADR — context, original D1–D15 decision set, alternatives, consequences,
pre-mortem, FMEA, phased rollout, component design, open questions, and four
amendments tracing the evolution from refuse-to-start → satellite mode →
single-daemon + socket proxy — lives in the repo:
[`docs/design/adr-002-multi-instance-concurrency.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-002-multi-instance-concurrency.md).
Read that before changing the lockfile, proxy, or promotion paths.

:::

## Status today

**Shipped** as the single-daemon + socket-proxy variant (Amendment 4, 2026-04-09).
Key implementation anchors:

- `internal/lockfile/` — `flock` acquisition on `.shaktiman/daemon.pid`.
- `internal/proxy/` — the stdio-to-HTTP-over-Unix-socket bridge.
- `cmd/shaktimand/main.go` — leader/proxy branching and re-exec on promotion.

The original plan's D2–D5, D11–D14, D15 are **superseded** — they describe paths
(satellite mode, refuse-to-start, shared worktrees) that were replaced by D1″.

## Context

Two Claude Code windows on the same project would each spawn a `shaktimand`
and race for `index.db`, `embeddings.bin`, and the file watcher — `SQLITE_BUSY`,
WAL corruption, and doubled cold-index costs. Per-worktree separation wasted disk
and CPU proportional to window count.

## Decision (what ships)

Exactly one `shaktimand` per project owns the index (database, vector store,
watcher). It is the **leader** — determined by who holds the exclusive `flock`
on `.shaktiman/daemon.pid`.

Subsequent `shaktimand` invocations for the same project become **stateless
proxies**: they open zero file descriptors on the shared state, bridge their
client's stdio to the leader's Unix domain socket (`$TMPDIR/shaktiman-<hash>.sock`,
named by `SHA256(canonical_root)[:8]`), and forward every MCP request.

If the leader exits (graceful or crash), the `flock` releases and the first
proxy to notice re-execs itself to cold-start as the new leader. MCP clients
see a brief pause, not a failure.

## Key constraints

- **Single writer.** Only the leader touches `index.db`, `embeddings.bin`, and
  `shaktimand.log`. The validator in `ValidateBackendConfig` enforces the
  companion rule from ADR-003 A12 (postgres + brute_force/hnsw is rejected
  because `embeddings.bin` would race across daemons sharing a Postgres).
- **Canonicalized project root.** Symlinks and relative paths are resolved before
  hashing so two callers addressing the same directory converge on the same lock.
- **CLI guard.** `shaktiman index` / `reindex` refuse if a daemon is running —
  the daemon handles indexing itself.
- **POSIX only.** `flock` + Unix domain sockets assume macOS/Linux; Windows
  users are on WSL.

## When to revisit

- If `mcp-go`'s `StreamableHTTPServer` semantics change materially.
- If the single-writer invariant becomes the bottleneck for very large repos
  (today parsing and embedding dominate).
- If there's demand for promoting a proxy without a re-exec (faster failover).
