---
title: Claude Code
sidebar_position: 1
---

# Claude Code

Claude Code is Shaktiman's primary integration. Basic setup takes four steps in
[Getting Started → Claude Code Setup](/getting-started/claude-code-setup). This page
covers the next layer: telling Claude *when* to reach for Shaktiman, subagent
delegation, and troubleshooting the connection.

## A `CLAUDE.md` template

Drop this into your project's root-level `CLAUDE.md` so every Claude Code session
knows about Shaktiman:

```markdown
## MCP Tools (via shaktimand)

Shaktiman is a pre-built code index that reduces context usage during exploration.
Use it to narrow down before reading files — not as a replacement for Grep or Glob.

| Task | Tool | Why |
|---|---|---|
| Orient in unfamiliar codebase | `mcp__shaktiman__summary` | Codebase snapshot without reading files |
| Find code related to a concept | `mcp__shaktiman__search` | Ranked discovery — read only the top hits |
| Understand a topic across files | `mcp__shaktiman__context` | Token-budgeted chunks instead of reading many files |
| Find where a symbol is defined | `mcp__shaktiman__symbols` | Definition + signature without reading the file |
| Trace callers/callees | `mcp__shaktiman__dependencies` | Full call chain in one call |
| See what changed recently | `mcp__shaktiman__diff` | Symbol-level change tracking |
| Find exact string or regex | Grep | Shaktiman ranks by relevance, not pattern match |
| Find files by name/extension | Glob | Shaktiman indexes content, not filenames |
| Read a specific known file | Read | Direct file access |

### Discovery workflow

1. `mcp__shaktiman__summary` → orient (size, languages, health)
2. `mcp__shaktiman__search` → narrow to relevant files
3. `Read` → read only the files that matter
4. `Edit` → make changes
```

A longer template with subagent guidance is in
[`docs/reference/sample_claude.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/reference/sample_claude.md).

## Subagent delegation

Subagents launched via the `Agent` tool **do not inherit** the parent session's
`CLAUDE.md`. If you want a subagent to use Shaktiman during exploration, tell it
in the prompt:

> Use `mcp__shaktiman__search`, `mcp__shaktiman__symbols`, and
> `mcp__shaktiman__dependencies` for code discovery before reading files. All
> tools exclude test files by default — use `scope: "test"` when looking for
> test code. Use Grep for exact string / regex matching. Use Glob for finding
> files by name.

Without this, subagents default to Grep / Glob / Read loops and you lose the
token-efficiency win.

## When Claude Code reaches for built-in tools instead

Claude is trained to prefer the most specific tool for the job. You'll see:

- `Grep` for exact-string and regex queries.
- `Glob` for filename patterns (`*.yaml`, `src/**/index.ts`).
- `Read` when a specific file path is known.
- `mcp__shaktiman__search` for conceptual queries.

This is the right behaviour — don't override it. The `CLAUDE.md` above lists the
decision rule.

## Multiple Claude Code windows

Open as many sessions as you like. Each spawns its own `shaktimand`; they
coordinate via the leader/proxy mechanism described in
[Multi-instance concurrency](/guides/multi-instance). No manual coordination
needed.

## Where the setup and config details live

This page deliberately doesn't duplicate the install recipe or the config
reference. Head to:

- [Claude Code Setup](/getting-started/claude-code-setup) for the `.mcp.json`
  shape, the smoke test, and the Claude Code restart sequence.
- [Config file reference](/configuration/config-file#environment-variables)
  for env vars like `SHAKTIMAN_POSTGRES_URL` and `SHAKTIMAN_QDRANT_API_KEY`.
- [Troubleshooting → Daemon & leader](/troubleshooting/daemon-and-leader) if
  tools don't appear under `/mcp` or the connection is flaky.
