---
title: Claude Code Setup
sidebar_position: 3
---

# Claude Code Setup

With the [quickstart](./quickstart) confirmed working, wiring Shaktiman into Claude
Code takes three steps. After this, every Shaktiman MCP tool becomes available to
your agent automatically.

:::info Paths

The steps below use absolute paths. Replace `/absolute/path/to/...` with your own.
You can get the absolute path of your built `shaktimand` binary with:

```bash
realpath ./shaktimand    # Linux
# or
echo "$(pwd)/shaktimand" # any POSIX shell
```

:::

## 1. Create `.mcp.json` in your project root

```json
{
  "mcpServers": {
    "shaktiman": {
      "command": "/absolute/path/to/shaktimand",
      "args": ["/absolute/path/to/your/project"],
      "env": {
        "SHAKTIMAN_LOG_LEVEL": "WARN"
      }
    }
  }
}
```

- `command` is the absolute path to the `shaktimand` binary.
- `args` is a single-element array containing the absolute path to the project root
  you want indexed.
- `env.SHAKTIMAN_LOG_LEVEL` controls structured-log verbosity. Use `"DEBUG"` while
  wiring things up, then drop back to `"WARN"` or remove the key.

## 2. Restart Claude Code

Open a new Claude Code session in the project. On first launch, `shaktimand` starts,
acquires the project lock, and begins cold-indexing in the background. The first
session may experience "index warming" briefly; queries work at Level 1 (keyword +
structural) while the index fills.

## 3. Confirm the tools are registered

In Claude Code, run:

```
/mcp
```

You should see `shaktiman` listed as a connected MCP server. The seven tools
(`summary`, `search`, `context`, `symbols`, `dependencies`, `diff`,
`enrichment_status`) are now available to the agent.

## 4. Run your first tool call

Ask the agent to orient itself:

> "Give me a one-line summary of this codebase."

The agent will call `mcp__shaktiman__summary`. You'll see something like:

```
Files: 4213 | Chunks: 58104 | Symbols: 41220
Languages: typescript (3210), go (901), python (102)
Embeddings: 100% (or lower while cold index is still running)
```

That's the confirmation. From here on, Claude Code will reach for Shaktiman whenever
it helps — ranked search, budget-fitted context, dependency traversal, and so on.

## What if it didn't work?

- `shaktimand` failed to start → check `.shaktiman/shaktimand.log` in your project.
- Tools don't appear under `/mcp` → verify the `command` path in `.mcp.json` points
  at a real executable (`ls -l /absolute/path/to/shaktimand`).
- Searches return nothing → the index may still be warming. Ask the agent to call
  `enrichment_status` or see
  [Troubleshooting → Indexing stuck](/troubleshooting/overview).

## Next

- [Reference → MCP Tools](/reference/mcp-tools/overview) — parameters and semantics
  for every tool the agent can call.
- [Integrations → Claude Code](/integrations/claude-code) — advanced setup (custom
  `CLAUDE.md`, subagent delegation hint).
- [Configuration](/configuration/config-file) — per-project tuning.
