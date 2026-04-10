# Executive Summary: Multi-Instance Concurrency

**ADR:** [ADR-002](adr-002-multi-instance-concurrency.md)
**Implementation Plan:** [impl-002](impl-002-multi-instance-concurrency.md)
**Date:** 2026-04-09

---

## Problem

When multiple Claude Code sessions open on the same project, each spawns its own `shaktimand` daemon. With no coordination, these daemons race on `index.db` (SQLite `SQLITE_BUSY` errors), `embeddings.bin` (last-writer-wins corruption), and `shaktimand.log` (clobbed on rotation). The user sees flaky MCP tool failures with no actionable error message.

---

## Decisions

### 1. Single-daemon + socket proxy (D1")

The first `shaktimand` to start acquires an exclusive `flock` on `.shaktiman/daemon.pid` and becomes the **leader**. It owns the database, vector store, watcher, and enrichment pipeline. It also listens on a Unix domain socket for proxy clients.

Subsequent `shaktimand` processes detect the held lock and become **proxies** — stateless stdio-to-HTTP bridges (~80 LOC, ~5 MB memory) that forward their Claude Code client's MCP requests to the leader via the Unix socket. All queries route to one daemon: zero stale reads, zero double-enrichment, zero races.

**Why:** The original ADR proposed leader/follower with read-only followers, SQLite UNION ALL overlays, and Postgres RLS — all of which were either superseded by shipped features (`project_id` scoping, `BranchSwitchCh`) or had unverified load-bearing assumptions (FTS5-on-views). D1" achieves the same goal with ~250 LOC, zero schema changes, and zero interface changes.

### 2. Proxy promotion via re-exec

When the leader exits, proxies detect connection-refused on the socket. Each proxy attempts `flock` — exactly one wins (flock is atomic). The winner calls `syscall.Exec` to restart itself as the new leader. Losers reconnect to the new leader as proxies.

**Why:** Re-exec is the simplest correct promotion strategy. The proxy holds zero state, so process replacement has no cost. `stdin`/`stdout` fds are preserved across exec (POSIX), so the Claude Code client reconnects transparently.

### 3. Hash-based socket path

The Unix socket lives at `/tmp/shaktiman-<sha256(canonicalRoot)[:16]>.sock` instead of inside `.shaktiman/`. macOS limits Unix socket paths to 104 bytes; deep project paths would exceed this limit.

**Why:** The hash is deterministic from the canonicalized project root, so proxies compute the same path without coordination. Stale sockets from SIGKILL are cleaned by the next leader (flock guarantees any existing socket is stale).

### 4. CLI index guard

`shaktiman index` acquires the same flock before indexing. If a daemon holds it, the command refuses with "daemon is running; it handles indexing automatically."

**Why:** The CLI `index` subcommand creates its own `WriterManager`, which races with the daemon's writer on SQLite.

### 5. Postgres + file-backed vector rejection (ADR-003 A12)

`ValidateBackendConfig` now rejects `postgres + brute_force` and `postgres + hnsw`. Postgres MVCC handles metadata safely, but `embeddings.bin` is a local file that races across daemons.

**Why:** Without this, users on `postgres + brute_force` silently corrupt the vector store when two daemons share the same Postgres database.

---

## Scope

| Changed | Unchanged |
|---|---|
| `cmd/shaktimand/main.go` — flock, socket, proxy path | `internal/storage/` — no schema changes |
| `internal/daemon/daemon.go` — socket listener in Start/Stop | `internal/vector/` — no vector changes |
| `cmd/shaktiman/main.go` — flock check in index cmd | `internal/core/` — no query changes |
| `internal/types/config.go` — A12 validation | `internal/mcp/server.go` — no MCP changes |
| New `internal/lockfile/` — flock + path canonicalization | MetadataStore, VectorStore interfaces — unchanged |
| New `internal/proxy/` — stdio-to-HTTP bridge | |

---

## Risks

| Risk | Mitigation |
|---|---|
| `flock` is Unix-only; no Windows support | Documented as macOS/Linux only. Windows deferred until demand exists. |
| MCP session re-initialization on promotion | MCP spec requires clients handle server restart. Claude Code re-sends `initialize` on error. |
| `gofrs/flock` is a new dependency | Well-tested (Docker, Terraform), BSD-3, minimal API surface. |
| `StreamableHTTPServer` on Unix socket is novel | mcp-go v0.45.0 documents this usage; integration test validates. |

---

## Rollout

1. Single-daemon enforcement prevents races immediately on upgrade.
2. Proxy mode makes second Claude Code sessions work instead of failing.
3. No migration needed — zero schema changes.
4. Existing single-session users see no behavioral change.
