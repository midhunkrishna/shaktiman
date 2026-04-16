---
title: From ctags / LSP
sidebar_position: 3
---

# Migrating from ctags / LSP

## Mental-model shift

`ctags` builds a flat file of symbol → location mappings, editor-integrated.
LSP (via `gopls`, `rust-analyzer`, `pyright`, etc.) adds type-aware
navigation — go-to-definition, find-references, hover — driven by the language
server. Both are **language-specific tooling** that your editor consumes.

Shaktiman is **language-agnostic, agent-oriented**. It answers the same
"where is X defined?" and "who calls X?" questions, but it's designed to be
called by an LLM agent over MCP, not clicked by a human in an IDE. It works
across all 9 supported languages with a single query shape.

You'd run Shaktiman **alongside** LSP, not instead of it. LSP serves your
editor; Shaktiman serves your agent.

## Feature parity table

| Capability | ctags / LSP | Shaktiman | Notes |
|---|---|---|---|
| Go to definition | `gopls` → click | `symbols name:"X"` | Same answer, different interface. |
| Find references | `gopls` → "Find all references" | `dependencies symbol:"X" direction:"callers"` | Shaktiman includes depth traversal. |
| Type information | LSP: full | Only signature text | No types in Shaktiman. |
| Rename refactor | LSP | — | Shaktiman doesn't edit code. |
| Hover / docs | LSP | — | Use `symbols` + `Read` the file. |
| Cross-language | Piecemeal (one server per language) | Same query shape for all | Shaktiman's main edge. |
| Works offline with no editor | Yes (ctags only) | Yes | Both do. |
| Indexes 1M-line repos | LSP often struggles | Designed for it | Shaktiman's graph + SQLite handles it. |
| Accessible to agents over MCP | — | Yes | The reason Shaktiman exists. |
| Transitive callers in one call | No (manual chain) | `depth:3` | Shaktiman's edge. |

## Side-by-side workflow

**"Who calls `validateToken`?"**

gopls / LSP in editor:

1. Open the file.
2. Right-click `validateToken` → Find all references.
3. Get flat list — one depth level only.
4. Open each reference to see context.

Shaktiman:

```bash
shaktiman deps validateToken --root . --direction callers --depth 3 --format json
```

One call, three hops of callers, structured output.

## Gaps

- **No type information.** Shaktiman has the symbol's signature as a string,
  not a parsed type system. LSP still rules for type queries.
- **No "find implementations of interface".** LSP knows the type system;
  Shaktiman has text and call edges.
- **ctags is faster to build** on small repos (just symbols, no parsing
  beyond that).
- **LSP is live.** It updates on every keystroke. Shaktiman updates on file
  save, not mid-edit.
- **LSP understands cross-module imports** with full qualification (e.g. Go's
  fully-qualified paths). Shaktiman's symbol index is by local name; you may
  see collisions between names that LSP would disambiguate.

## When to keep ctags / LSP

- **Always keep LSP** if your language has a good one — it's your editor's
  backbone.
- **Use ctags** if you work in very small / constrained environments and want
  a zero-config symbol index — but most users get better value from LSP.

## The real answer

These tools aren't alternatives. Your editor uses LSP. Your agent uses
Shaktiman. Both indexes exist, neither is the one-true-source.

## See also

- [`symbols`](/reference/mcp-tools/symbols), [`dependencies`](/reference/mcp-tools/dependencies) —
  the Shaktiman-side equivalents.
- [Supported Languages](/reference/supported-languages) — where the grammar
  coverage is uneven; LSP may have more.
