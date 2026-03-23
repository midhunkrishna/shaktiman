# Shaktiman MCP — CLAUDE.md Template

Copy this section into your project's CLAUDE.md to instruct Claude Code how to use the shaktiman MCP tools efficiently.

---

## MCP Tools (via shaktimand) — MANDATORY

**CRITICAL: Always use shaktiman MCP tools for code search and exploration. This applies to the main agent AND all subagents.** When delegating to any subagent (code-explainer, codebase-reader, Explore, reviewer, implementer, etc.), explicitly instruct it to use `mcp__shaktiman__*` tools instead of Read/Grep/Glob. Subagents do not automatically inherit these instructions.

### Locate-first pattern

**Always search in locate mode first** (the default). This returns compact file pointers — paths, line ranges, symbols, and relevance scores — without source code. Then use the `Read` tool to read specific files you need.

This pattern minimizes context usage:
1. `mcp__shaktiman__search` → discover relevant locations (~125 tokens for 10 results)
2. `Read` → read only the files you actually need
3. `Edit` → make changes

Only use `mode="full"` when you need inline source code without a separate Read call.

### Subagent delegation template

When spawning any subagent that needs to read or search code, include this in the prompt:

> **IMPORTANT: Use the MCP tools `mcp__shaktiman__search`, `mcp__shaktiman__symbols`, `mcp__shaktiman__dependencies`, and `mcp__shaktiman__diff` for all code search and exploration. Search defaults to locate mode (file pointers only). Use the Read tool to read specific files after locating them. Do NOT use Grep/Glob for code discovery — MCP search is faster and supports semantic matching.**

### Tool mapping

| Instead of | Use | For |
|---|---|---|
| Grep, Glob | `mcp__shaktiman__search` | Find code by keyword or concept (returns locations, not content) |
| Read (multi-file) | `mcp__shaktiman__search mode="full"` | Get source code inline when Read is inconvenient |
| Read (cross-file overview) | `mcp__shaktiman__context` | Multi-file understanding fitted to a token budget |
| Grep (definitions) | `mcp__shaktiman__symbols` | Look up function/class/type definitions by name |
| (no equivalent) | `mcp__shaktiman__dependencies` | Trace callers/callees of a symbol |
| (no equivalent) | `mcp__shaktiman__diff` | Recent file changes and affected symbols |

### Token efficiency tips

- **Keep `max_results` small** (5-10). You rarely need more than 10 results.
- **Use `min_score`** to filter noise. Default is 0.15; raise to 0.3+ for precise queries.
- **Use `context` sparingly** with small budgets (1024-2048 tokens). For single-file reading, use `Read` instead.
- **Prefer `symbols`** over `search` when you know the exact function/type name.

### Fallback policy

Use Read/Grep/Glob ONLY when:
1. Shaktiman MCP tools return no results or insufficient results
2. You need to read a specific file by exact path (e.g., go.mod, CLAUDE.md)
3. You need to write or edit files (MCP tools are read-only)
