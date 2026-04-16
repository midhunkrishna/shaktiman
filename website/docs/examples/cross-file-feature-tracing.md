---
title: Cross-file feature tracing
sidebar_position: 5
---

# Cross-file feature tracing

**Task.** You need to understand how "rate limiting" works across the codebase,
well enough to explain it in a design doc or to answer "should we add a new
rate limit for endpoint X?". The implementation is spread across middleware,
config, storage, and a couple of utility packages.

You want exactly 4 KB worth of the most relevant code — no more, no less — so
you can drop it into an LLM prompt or a review.

## Tool sequence

### 1. `context` with a budget

```jsonc
{
  "name": "context",
  "arguments": {
    "query": "rate limiting implementation across middleware config and storage",
    "budget_tokens": 4096
  }
}
```

Shaktiman's assembler does three things in one call:

1. **Ranks** chunks across the codebase using the five-signal score.
2. **Dedups** overlapping chunks (line-range overlap > 50 % → skip).
3. **Fits the budget** — packs top-ranked chunks up to 70 % of the budget as
   the primary set, then spends the remaining 30 % on structural-expansion
   neighbors (callees / callers of the primary chunks, capped at 5 per chunk).

You get back a ranked list of chunks — typically 8–15 of them — totalling
under 4 KB of source. Each comes with its path, symbol name, line range, and
score.

### 2. Use the output directly

If you're scripting:

```bash
shaktiman context "rate limiting implementation" \
  --root . --budget 4096 --format text
```

If you're driving from Claude Code:

> "Give me 4K tokens of ranked context across the rate-limiting implementation
> and summarize how it works."

Claude will call `context` under the hood.

## Picking a budget

| Budget | Use case |
|---|---|
| 1024 | A single focused concept, want just the core. |
| 2048 | A small feature spanning 2–3 files. |
| 4096 | Default. A feature spanning the usual 5–10 files. |
| 8192 | A subsystem. Broader reasoning, more tokens fed to the LLM. |
| 16384+ | You're writing a design doc. Still far less than "read every file". |

Larger budgets don't magically make the top hits better — they just pull in
more of the tail. If your query is well-scoped, a smaller budget gives you
denser context.

## When `context` isn't the right tool

- You know exactly which files matter → `search` in `locate` mode + `Read`.
- You need *every* occurrence, ranked or not → `Grep`.
- You need the full source of the top N hits (no dedup, no structural fill-in)
  → `search` with `mode:"full"`.

## Test-specific tracing

Scope to tests to see how the feature is exercised:

```jsonc
{
  "name": "context",
  "arguments": {
    "query": "rate limiting tests",
    "budget_tokens": 2048,
    "scope": "test"
  }
}
```

Useful when you're about to change behaviour and want to know which tests will
need updating.

## Token math

- **Without Shaktiman.** Grep for "rate limit", open 10 files, scroll past
  irrelevant helpers, hand-select the relevant chunks. Maybe 15–30k tokens of
  reading, half of which you throw away.
- **With Shaktiman.** One `context` call returns exactly 4K tokens of
  pre-deduplicated, ranked content. Less than a quarter of the cost, and it's
  already aligned with the query.

## See also

- [`context`](/reference/mcp-tools/context) — budget semantics, how the
  assembler spends the budget.
- [Searching & navigating the index](/guides/searching) — `context` vs `search`
  vs `Read`.
- [Architecture — §3.2 Context Assembler](/design/architecture) — the exact
  algorithm and trade-off numbers.
