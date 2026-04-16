---
title: Cursor
sidebar_position: 2
---

# Cursor

[Cursor](https://cursor.com) supports MCP servers natively. The connection side
is identical to Claude Code's — Cursor launches `shaktimand` over stdio, and the
same seven MCP tools become available.

## Configuration

Cursor reads MCP configuration from two locations, project-scoped takes
precedence:

- **Project-scoped:** `<project-root>/.cursor/mcp.json`
- **Global:** `~/.cursor/mcp.json`

Use the same JSON shape as Claude Code's `.mcp.json`:

```json
{
  "mcpServers": {
    "shaktiman": {
      "command": "/absolute/path/to/shaktimand",
      "args": ["/absolute/path/to/project"],
      "env": {
        "SHAKTIMAN_LOG_LEVEL": "WARN"
      }
    }
  }
}
```

Restart Cursor after editing `mcp.json`. In the chat sidebar, the Shaktiman tools
should appear alongside the built-in ones.

:::info

Cursor's MCP surface evolves. If the file location or schema above is outdated,
check Cursor's official docs — Shaktiman's side (the stdio server) doesn't change.

:::

## Agent rules

Cursor's equivalent of `CLAUDE.md` is `.cursorrules` (legacy) or `.cursor/rules/`
(current). Drop the same tool-selection guidance there — see the
[Claude Code template](/integrations/claude-code#a-claudemd-template). The
"discovery workflow" and "when to use Grep vs shaktiman" rules translate
directly.

## One binary, multiple clients

You can use the same `shaktimand` binary from Claude Code, Cursor, Zed, or any
other MCP client simultaneously. Each client launches its own `shaktimand`
process; the [leader/proxy](/guides/multi-instance) mechanism ensures only one
owns the index at a time.

## See also

- [Getting Started → Claude Code Setup](/getting-started/claude-code-setup) — the
  install recipe transfers to Cursor directly.
- [Multi-instance concurrency](/guides/multi-instance) — what happens with
  multiple clients on the same project.
