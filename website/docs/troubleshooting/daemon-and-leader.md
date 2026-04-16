---
title: Daemon & leader election
sidebar_position: 2
---

# Daemon & leader election

Covers startup failures, lock contention, and leader/proxy confusion.

## Symptom: `shaktimand` exits immediately with "acquire daemon lock"

### Likely causes (ranked)

1. Another `shaktimand` is already running for this project and you hit some edge
   case (normally, proxies succeed — this error means the lock subsystem itself
   returned an unexpected failure).
2. `.shaktiman/daemon.pid` has stale POSIX permissions (e.g. owned by root from an
   earlier `sudo` run).
3. The filesystem doesn't support `flock` (very rare — some network filesystems).

### Diagnostic

```bash
ls -la /path/to/project/.shaktiman/daemon.pid
lsof /path/to/project/.shaktiman/daemon.pid    # who holds the lock?
tail -n 50 /path/to/project/.shaktiman/shaktimand.log
```

### Fix

- If another `shaktimand` owns the lock (expected): the second invocation should
  have entered proxy mode, not errored. Check the log for `"entering proxy mode"`
  lines. If it errored instead, file an issue with the log attached.
- Permissions issue: `chown` the `.shaktiman/` directory back to your user, then
  retry.
- Network FS: move the project to a local disk.

## Symptom: proxies appear frozen — `/mcp` calls hang

### Likely causes

1. The leader is alive but stuck (e.g. a long-running bulk write, stuck in a
   parse loop, or waiting on an unreachable Ollama).
2. The Unix socket was removed out from under the leader (someone `rm`'d
   `/tmp/shaktiman-*.sock`).

### Diagnostic

```bash
# Find the leader PID
cat /path/to/project/.shaktiman/daemon.pid

# Is it alive?
ps -p <pid>

# What's it doing?
# macOS:
sample <pid> 3

# Linux:
py-spy dump --pid <pid>    # or gdb, or just strace

# Socket still there?
ls -la /tmp/shaktiman-*.sock
```

### Fix

- Kill the leader: `kill <pid>`. A proxy will detect `ErrLeaderGone`, re-exec
  itself, and become the new leader. Your Claude Code sessions should resume
  within a second or two.
- If the socket file is missing but the leader is running, the leader is
  unrecoverable — kill it.

## Symptom: `shaktiman index` refuses to run

```
a shaktimand daemon is running for this project; it handles indexing automatically
```

### Likely cause

A `shaktimand` holds the flock on `.shaktiman/daemon.pid`. Both the CLI's writer
and the daemon's writer touching SQLite at once would race, so the CLI refuses.

### Fix

Either:
- **Let the daemon index** — it does this automatically. `enrichment_status` shows
  progress.
- Or **stop the daemon** (close the Claude Code session or `kill` the leader),
  then run `shaktiman index` from the CLI.

## Symptom: multiple sockets in `/tmp/shaktiman-*.sock` after a crash

Leftover sockets from crashed `shaktimand` processes don't hurt anything —
subsequent leaders create their own at a deterministic path based on
`SHA256(canonical_project_root)[:16]`. You can `rm /tmp/shaktiman-*.sock` to tidy
up; the next run creates fresh sockets.

## See also

- [Guides → Multi-instance concurrency](/guides/multi-instance) — the full
  leader/proxy design.
- [ADR-002](/design/adr-002-multi-instance) — why we chose single-daemon + socket
  proxy.
