---
title: context
sidebar_position: 4
---

# `context`

Budget-fitted, cross-file context assembly. Ask for *N* tokens, get exactly *N* tokens
of the most relevant, deduplicated code. Use it when you want to feed ranked context
into an LLM prompt without manually picking and trimming files.

## Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | string | ✓ | — | What you need context for. Capped at 10,000 characters. |
| `budget_tokens` | number | | `4096` | Token budget for the assembled package. Range 256–32768. |
| `scope` | enum | | `"impl"` | `"impl"`, `"test"`, or `"all"`. Same semantics as `search`. |

The default budget is read from `[context].budget_tokens` in
`.shaktiman/shaktiman.toml`.

## Validation

The handler returns an MCP tool error for any of:

- `query` missing, empty, or longer than 10,000 characters.
- `budget_tokens` outside 256–32768.
- `scope` not one of the enum values.

## Output

A context package — a ranked list of chunks, already deduplicated to fit the token
budget. Each chunk carries file path, symbol, line range, and the source. The tokenizer
used is `cl100k_base` (tiktoken) with a safety margin so the returned content fits
inside the stated budget.

## How the budget is spent

Under the hood `context` calls into `engine.Context` (`internal/core/assembler.go`):

1. **Primary selection** packs the top-ranked chunks up to the budget, skipping any
   chunk whose line range overlaps an already-selected chunk by more than 50 %.
2. **Structural expansion** (capped at ~30 % of the remaining budget) pulls in
   neighbors from the call graph, up to 5 per seed chunk.
3. **Metadata attach** adds ~12 tokens of per-chunk metadata (path, symbol, lines,
   score, parse quality).

Ask for smaller budgets (1024–2048) when your query is narrow; larger budgets waste
tokens on progressively less relevant chunks.

## Example invocations

```jsonc
// Default budget (4096 tokens)
{ "name": "context", "arguments": { "query": "payment processing flow" } }

// Tight focused budget
{
  "name": "context",
  "arguments": {
    "query": "how connection pooling is configured",
    "budget_tokens": 1024
  }
}

// Test-code context
{
  "name": "context",
  "arguments": { "query": "webhook verification", "scope": "test" }
}
```

## When `context` isn't the right tool

- You want to decide which files to `Read` yourself → use [`search`](./search) in
  `locate` mode, then `Read` the top hits.
- You need a specific symbol's definition → use [`symbols`](./symbols).
- You want the full source of every match without deduplication or a budget → use
  `search` with `mode: "full"`.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `contextToolDef` / `contextHandler`.
- Assembly: `internal/core/assembler.go` (`Assemble`, `structuralExpand`).
