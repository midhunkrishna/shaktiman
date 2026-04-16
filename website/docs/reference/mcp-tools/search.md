---
title: search
sidebar_position: 3
---

# `search`

Ranked code discovery. Narrows a large codebase to the most relevant files for a query
so you only `Read` the ones that matter. Best for **conceptual** queries like `"error
handling"`, `"auth flow"`, `"database setup"`. For literal strings or regex, prefer
`Grep`.

## Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | string | ✓ | — | Search query text. Capped at 10,000 characters. |
| `mode` | enum | | `"locate"` | `"locate"` returns compact pointers (~12 tokens/result). `"full"` returns inline source. |
| `max_results` | number | | `10` | Maximum results returned. Range 1–200. |
| `min_score` | number | | `0.15` | Minimum relevance score threshold. Range 0.0–1.0. |
| `explain` | boolean | | `false` | Include per-signal score breakdown. |
| `path` | string | | — | Filter results to a file or directory prefix (e.g. `internal/mcp/`). |
| `scope` | enum | | `"impl"` | `"impl"` (default, excludes tests), `"test"` (tests only), or `"all"`. |

Defaults for `mode`, `max_results`, and `min_score` are read from the `[search]`
section of `.shaktiman/shaktiman.toml` — see
[Config File](/configuration/config-file).

## Validation

The handler returns an MCP tool error for any of:

- `query` missing, empty, or longer than 10,000 characters.
- `mode` not one of `"locate"` / `"full"`.
- `max_results` outside 1–200.
- `min_score` outside 0.0–1.0.
- `scope` not one of the enum values.

When `path` is set, the engine over-fetches up to 3× `max_results` (capped at 200) to
compensate for post-filtering, then truncates to `max_results` after applying the
prefix filter.

## Output

- **`mode: "locate"`** — compact text: file path, line range, symbol name, score, one
  per line. Roughly 12 tokens per result.
- **`mode: "full"`** — the same header plus the inline source for each chunk.
- **`explain: true`** — adds a per-signal score breakdown (semantic / structural /
  keyword / change / session contribution) to each result.

Every response carries a result count annotation so callers can tell at a glance how
many hits came back.

## Example invocations

```jsonc
// Conceptual query, compact output
{ "name": "search", "arguments": { "query": "rate limiting middleware" } }

// Scoped to a subdirectory, inline source, verbose scoring
{
  "name": "search",
  "arguments": {
    "query": "retry logic with backoff",
    "mode": "full",
    "path": "internal/vector/",
    "explain": true,
    "max_results": 5
  }
}

// Only test files — handy when exploring how a module is exercised
{ "name": "search", "arguments": { "query": "authentication", "scope": "test" } }
```

## When `search` isn't the right tool

- You know the **exact** string or regex you're looking for → use `Grep`.
- You want every definition with a specific name (not ranked hits) → use
  [`symbols`](./symbols).
- You want to fit N tokens of ranked context into an LLM prompt → use
  [`context`](./context), which dedupes and budget-fits for you.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `searchToolDef` (schema) and
  `searchHandler` (validation, over-fetch, path filtering).
- Ranking: `internal/core/` — the 5-signal hybrid ranker.
