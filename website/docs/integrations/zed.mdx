---
title: Zed
sidebar_position: 3
---

# Zed

[Zed](https://zed.dev) supports MCP servers under the "context servers" concept.
Setup is similar to Claude Code and Cursor — Zed launches `shaktimand` over
stdio, and the MCP tools become available to Zed's agent.

## Configuration

Zed's context-server configuration lives in `settings.json` (project-scoped at
`.zed/settings.json`, or global at `~/.config/zed/settings.json` on Linux /
`~/Library/Application Support/Zed/settings.json` on macOS).

```jsonc
{
  "context_servers": {
    "shaktiman": {
      "command": {
        "path": "/absolute/path/to/shaktimand",
        "args": ["/absolute/path/to/project"],
        "env": {
          "SHAKTIMAN_LOG_LEVEL": "WARN"
        }
      }
    }
  }
}
```

Restart Zed. The tools become callable from the agent panel.

:::info

Zed's MCP integration is evolving quickly — if the key name or nesting above
looks off, check Zed's docs. Shaktiman's side (the stdio server) is unchanged.

:::

## Agent rules

Zed's agent can be seeded with tool-selection guidance via system prompts or a
project-level agent file. The same decision logic applies — use Shaktiman for
ranked / budget-fitted queries, use built-in search for exact patterns. See the
[Claude Code template](/integrations/claude-code#a-claudemd-template) for
wording that translates directly.

## Multi-client usage

Zed, Claude Code, and Cursor can all talk to the same project simultaneously. The
[leader/proxy](/guides/multi-instance) mechanism means only one `shaktimand`
holds the index lock; the rest bridge through it.

## See also

- [Getting Started → Claude Code Setup](/getting-started/claude-code-setup) —
  the install recipe translates.
- [Generic MCP client](/integrations/generic-mcp-client) — what Shaktiman
  actually speaks on the wire.
