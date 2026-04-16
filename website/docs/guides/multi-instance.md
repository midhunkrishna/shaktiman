---
title: Multi-instance concurrency
sidebar_position: 3
---

# Multi-instance concurrency

You can run multiple Claude Code sessions on the same project directory at the same
time. Each session spawns its own `shaktimand` process, but only **one** of them
owns the index — the rest become **stateless proxies** that forward MCP traffic to
the owner. No configuration is required; it works out of the box. This page
explains what's happening so you can reason about log output and failure modes.

The design is captured in
[ADR-002 — Multi-Instance Concurrency](/design/adr-002-multi-instance).

## Leader vs. proxy

```
┌─── Claude Code window A ───┐       ┌─── Claude Code window B ───┐
│      shaktimand (leader)   │       │       shaktimand (proxy)   │
│      holds .shaktiman/     │◄──────┤  connects via unix socket  │
│      daemon.pid flock      │  MCP  │  $TMPDIR/shaktiman-*.sock  │
└────────────────────────────┘       └────────────────────────────┘
         │                                         ▲
         │ owns: SQLite, vector store,             │
         │        file watcher                     │
         ▼                                         │
    .shaktiman/index.db, embeddings.bin ───────────┘
```

- The **leader** is the first `shaktimand` to start. It acquires an exclusive
  `flock` on `.shaktiman/daemon.pid`, opens the database, starts the watcher,
  creates the Unix socket, and serves its client over stdio.
- **Proxies** are subsequent `shaktimand` invocations. They detect the lock is
  held, do *not* open the database, and bridge their client's stdio to the
  leader's socket. All MCP tool calls route through the leader.

Implementation: `internal/lockfile/`, `internal/proxy/`, `cmd/shaktimand/main.go`.

## Socket location

The leader's socket lives in the OS temp directory — `os.TempDir()` in Go, i.e.
`$TMPDIR` — at `shaktiman-<hash>.sock`, where `<hash>` is the first 16 hex
characters of `SHA256(canonical_project_root)`. The SHA-of-the-path trick keeps
sockets unique per project without tripping macOS's 104-character socket-path
limit.

Concretely, the temp directory is platform-dependent:

- **Linux:** usually `/tmp` (unless `$TMPDIR` is set).
- **macOS:** typically `/var/folders/<hash>/T/` per user; rarely `/tmp`.

You can inspect the socket:

```bash
ls -la "${TMPDIR:-/tmp}"/shaktiman-*.sock
# srwxrwxr-x  1 you  staff  0 Apr 16 12:00 …/shaktiman-ab12cd34ef567890.sock
```

Implementation: `internal/lockfile/lockfile.go` (`socketPathFromRoot`).

## Leader promotion

If the leader exits (graceful shutdown, crash, `kill`), the `flock` is released and
the OS notifies any process waiting on it. A proxy detects the leader-gone signal
(`proxy.ErrLeaderGone`) and re-execs itself — the new process starts cold, tries
the flock again, wins it, and becomes the new leader. The MCP client (Claude Code)
doesn't see the transition beyond a brief pause.

## Why this design

The [ADR](/design/adr-002-multi-instance) walks through the alternatives. The
short version:

- **Two daemons sharing a SQLite DB** → `SQLITE_BUSY` under concurrent writes,
  potential WAL corruption.
- **Two daemons, separate DBs per worktree** → cold-indexes the same repo 2×
  wastes disk and CPU proportional to the number of windows you open.
- **One daemon, socket proxies** (what ships) → single writer, no races, proxies
  are cheap.

## How to tell which you are

```bash
shaktimand /path/to/project &    # first one: becomes leader
shaktimand /path/to/project &    # second one: becomes proxy
```

Both will log to `.shaktiman/shaktimand.log` — the leader rotates the previous log
on startup, proxies append (never rotate, because the leader's file descriptor
would become detached). Grep for `"acquire daemon lock"` to find leader events.

If you run the CLI (`shaktiman search`, `shaktiman index`) while a daemon is
running, it reads the index directly without going through the daemon — except for
`index` / `reindex`, which **refuse** to run if the lock is held (they would race
with the daemon's writer). The daemon handles re-indexing automatically.

## When it matters

- **You open a second Claude Code window on the same project.** It Just Works.
- **You open Claude Code on two different `git worktree` checkouts.** Each has its
  own `.shaktiman/` directory, so each gets its own leader. No shared state.
- **The leader gets stuck.** Proxies will also appear stuck (they're bridging to
  it). Kill the leader; one of the proxies promotes itself.

## Backend caveat

When `[database].backend = "postgres"`, local vector backends (`brute_force`,
`hnsw`) are rejected by `ValidateBackendConfig` — they race on
`embeddings.bin` across daemons sharing a Postgres database. Use `pgvector` or
`qdrant` instead. See
[ADR-003](/design/adr-003-pluggable-backends) A12 and
[Known Limitations](/reference/limitations#postgres-requires-pgvector-or-qdrant).

## See also

- [ADR-002 — Multi-Instance Concurrency](/design/adr-002-multi-instance)
- [CLI Reference — `shaktimand`](/reference/cli#shaktimand-mcp-daemon)
- [Troubleshooting — Daemon and leader](/troubleshooting/daemon-and-leader)
