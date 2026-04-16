---
title: Custom agents
sidebar_position: 6
---

# Custom agents

You're building a bespoke agent (Agent SDK, LangChain, a hand-rolled script)
and want Shaktiman's tools without going through Claude Code / Cursor / Zed.
Two paths, pick one.

## Path A: drive the CLI

Simplest. Every MCP tool has a CLI twin; every CLI output is JSON by default.

```python
import json, subprocess

def shaktiman_search(query: str, project: str) -> list[dict]:
    raw = subprocess.check_output([
        "shaktiman", "search", query,
        "--root", project,
        "--format", "json",
    ])
    return json.loads(raw)
```

When to pick this path:

- Your agent framework doesn't speak MCP.
- You want to avoid holding a long-lived subprocess.
- Your queries are infrequent (a few per minute); the fork cost doesn't matter.

Trade-off: each invocation opens `.shaktiman/index.db` from scratch (~10-30ms).
For high-frequency queries, use Path B.

## Path B: spawn `shaktimand` and speak MCP

Long-lived subprocess; queries are cheap once it's warm. This is what Claude
Code / Cursor / Zed do internally.

1. Spawn `shaktimand <project-root>` as a child process.
2. Send JSON-RPC messages on stdin, read responses on stdout. Transport is
   line-delimited JSON-RPC (one request or response per line).
3. Follow the MCP handshake: `initialize` → `tools/list` → repeated
   `tools/call`.
4. Close stdin to shut down cleanly.

See [Generic MCP client](/integrations/generic-mcp-client) for the concrete
message shapes.

When to pick this path:

- You need sub-millisecond query latency (no fork overhead per call).
- You want to receive progress or future notifications (once Shaktiman starts
  emitting them).
- You're using an SDK that already speaks MCP.

## Path C (not recommended): read `.shaktiman/index.db` directly

SQLite with FTS5 is publicly-accessible; you could query chunks / symbols /
edges with a standard SQLite client. Don't.

- The schema is internal and can change between Shaktiman versions without
  notice.
- You'd lose the ranker — raw SQL gives you rows, not ranked results.
- You'd race with the daemon's writer under concurrent load.

The CLI and MCP are the supported APIs.

## Working with the daemon from a custom agent

If your agent is one of *several* clients in a given project (e.g. you're
running Claude Code and your custom script at the same time), Shaktiman's
[leader/proxy](/guides/multi-instance) mechanism handles this transparently:
the first `shaktimand` wins the flock and owns the index; yours becomes a
proxy that bridges to the leader. No coordination code needed on your side.

## Socket access

If you want to skip MCP entirely and talk to the leader directly, the Unix
socket is at `/tmp/shaktiman-<hash>.sock`. We don't document the raw protocol
on it — it's an internal format the proxy uses — and we may change it between
versions. Use the MCP stdio surface instead.

## Example: a minimal bulk-query script

```bash
#!/usr/bin/env bash
# Dump a symbol inventory for a directory subset
set -euo pipefail

PROJECT="${1:-.}"
PREFIX="${2:-internal/}"

shaktiman search "" --root "$PROJECT" --path "$PREFIX" --max 200 --format json \
  | jq -r '.[] | "\(.path):\(.start_line)\t\(.symbol)\t\(.score)"'
```

Adjust `--max` (hard cap 200) and `--path` for your needs.

## See also

- [CLI Reference](/reference/cli) — every subcommand and flag.
- [Generic MCP client](/integrations/generic-mcp-client) — the wire format.
- [Multi-instance concurrency](/guides/multi-instance) — what your script
  needs to know about other clients.
