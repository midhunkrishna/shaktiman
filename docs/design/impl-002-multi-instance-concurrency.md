# Implementation Plan: Multi-Instance Concurrency (D1" Single-daemon + Socket Proxy)

**ADR:** [ADR-002](adr-002-multi-instance-concurrency.md) Amendment 4
**Version:** 0.9.3
**Date:** 2026-04-09

---

## Overview

When multiple Claude Code sessions open on the same project, each spawns its own `shaktimand`. Previously, these raced on SQLite/vector files with no coordination. D1" solves this: the first daemon becomes the leader (owns DB, vectors, watcher), subsequent daemons become stateless proxies bridging their Claude Code client's stdio to the leader via a Unix domain socket.

**Files changed:** 5 (`cmd/shaktimand/main.go`, `internal/daemon/daemon.go`, `cmd/shaktiman/main.go`, `internal/types/config.go`, `internal/types/config_test.go`)
**Files created:** 4 (`internal/lockfile/lockfile.go`, `internal/lockfile/lockfile_test.go`, `internal/proxy/bridge.go`, `internal/proxy/bridge_test.go`)
**New dependency:** `github.com/gofrs/flock v0.13.0` (BSD-3)

---

## Phases

### Phase 1: flock + singleton enforcement

New `internal/lockfile/` package provides `Acquire(projectRoot) (*Lock, error)` using `gofrs/flock`. Canonicalizes path via `filepath.EvalSymlinks` before computing lock path at `.shaktiman/daemon.pid`. Returns `ErrAlreadyLocked` when another process holds the lock.

**`cmd/shaktimand/main.go`:** Canonicalizes `projectRoot` at startup. Calls `lockfile.Acquire` before creating the daemon. On `ErrAlreadyLocked`, enters proxy mode (Phase 3).

### Phase 2: Leader socket listener

Leader daemon serves MCP on two transports sharing the same `MCPServer`:
1. `StdioServer` (for its own Claude Code client)
2. `StreamableHTTPServer` on a Unix domain socket (for proxy clients)

Socket path: `/tmp/shaktiman-<sha256(canonicalRoot)[:16]>.sock` (avoids macOS 104-byte path limit).

**`internal/daemon/daemon.go`:** New `SocketListener net.Listener` field. In `Start()`, if set, creates `StreamableHTTPServer` and serves on it. In `Stop()`, gracefully shuts down the socket server.

### Phase 3: Proxy bridge + promotion

New `internal/proxy/` package. `Bridge` reads JSON-RPC lines from stdin, POSTs to leader's `/mcp` endpoint via Unix socket, writes responses to stdout. Captures `Mcp-Session-Id` from first response and echoes it.

On leader exit (connection refused), proxy re-execs itself via `syscall.Exec`. The re-exec'd process acquires the flock (released by old leader) and enters leader path. If another proxy wins the race, loses re-enter proxy mode.

### Phase 4: CLI flock check

`shaktiman index` subcommand checks flock before opening the WriterManager. If a daemon holds the lock, refuses with "daemon is running; it handles indexing automatically."

### Phase 4b: ADR-003 A12

`ValidateBackendConfig` rejects `postgres + brute_force` and `postgres + hnsw` since file-backed vector stores race on `embeddings.bin` across daemons sharing the same Postgres.

---

## Key Invariants

1. **Exactly one writer** per project — only the leader touches `index.db`, `embeddings.bin`, `shaktimand.log`
2. **Proxies are stateless** — zero file descriptors on DB/vector/log, ~5-10 MB memory
3. **flock = source of truth** — released on any process exit including SIGKILL
4. **Socket = liveness signal** — connection-refused triggers promotion
5. **Path canonicalization** — `filepath.EvalSymlinks` prevents duplicate locks on same directory
6. **Cold-start promotion** — re-exec is a full restart, no cached state carried over

---

## Test Summary

| Package | Tests | Coverage |
|---|---|---|
| `internal/lockfile` | 10 tests | ~88% |
| `internal/proxy` | 9 tests | ~82% |
| `internal/daemon` (new code) | 3 tests | socket serving path |
| `internal/types` | 4 new test cases | A12 validation |
