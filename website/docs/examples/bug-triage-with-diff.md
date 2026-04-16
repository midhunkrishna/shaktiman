---
title: Bug triage with diff
sidebar_position: 4
---

# Bug triage with diff

**Task.** Production started 500-ing at 10:30 this morning. You want a short
list of what changed in the hours before — at the symbol level, not just file
level — so you can narrow the suspect set before diving in.

## Tool sequence

### 1. `diff` — what changed, when

```jsonc
{ "name": "diff", "arguments": { "since": "6h", "limit": 200 } }
```

You get a list of recent file changes, each with:

- Path and timestamp of the change.
- Line counts added / removed.
- **Affected symbols** — which functions / methods / types were added,
  modified, removed, or had their signatures change.

```jsonc
[
  {
    "path": "internal/payments/charge.go",
    "timestamp": "2026-04-16T09:42:00Z",
    "change_type": "modify",
    "lines_added": 12, "lines_removed": 4,
    "changed_symbols": [
      { "name": "(*Processor).Charge", "change_type": "modified" }
    ]
  },
  {
    "path": "internal/server/handlers.go",
    "timestamp": "2026-04-16T09:28:00Z",
    "change_type": "modify",
    "lines_added": 1, "lines_removed": 1,
    "changed_symbols": [
      { "name": "handleCheckout", "change_type": "signature_changed" }
    ]
  }
  ...
]
```

That's 2 suspects out of what might be dozens of commits.

### 2. Filter by relevant area if you have a hunch

If the incident is on checkout, rerun the same call with `scope:"impl"` (the
default) and ignore hits in other modules. Or use `search` on the symptom:

```jsonc
{ "name": "search", "arguments": { "query": "checkout 500" } }
```

The `change` signal in the ranker boosts recently-modified chunks — so a
search for "checkout" in a period of active checkout edits will naturally
surface those first.

### 3. Read the suspect chunks

Now you have a shortlist. `Read` the specific files, or use `search` with
`mode:"full"` to get inline source:

```jsonc
{
  "name": "search",
  "arguments": {
    "query": "charge processor",
    "mode": "full",
    "max_results": 3
  }
}
```

### 4. Trace callers if the change looks like it could propagate

If the modified symbol is called from many places:

```jsonc
{
  "name": "dependencies",
  "arguments": { "symbol": "Charge", "direction": "callers", "depth": 2 }
}
```

Now you know whether the behavioural change reaches into the code path that
broke.

## Important caveat on the `since` window

Shaktiman's `diff` reads its own change log — populated by the enrichment
pipeline as files are re-indexed. It does **not** re-run `git log`. If:

- The daemon wasn't running when the changes landed (e.g. you were on another
  branch, or `shaktimand` was down), those changes won't show up in `diff`.
- The enrichment hasn't caught up yet (big merge just landed, cold re-index
  in progress), some symbols may not have made it into `diff_symbols` yet.

When in doubt, cross-check with `git log --since=6h --name-only` as a sanity
check.

## Token math

- **Without Shaktiman.** `git log --since 6h` lists commits; you
  open each diff, scan for symbol-level changes, and hold the picture in your
  head. 10–20k tokens and lost focus.
- **With Shaktiman.** One `diff` call returns the symbol-level summary. You pick
  suspects in seconds, then read only those files.

## Variations

- **Wider window.** `since: "168h"` for a week of changes. Cap 720h (30 days).
- **Everything including tests.** `scope: "all"` if a test change might be
  relevant (e.g. someone flipped an assertion).

## See also

- [`diff`](/reference/mcp-tools/diff) — parameters and the Go-duration gotcha
  (no `"d"` or `"w"` — use `168h` for a week).
- [Searching & navigating the index](/guides/searching) — the `change` signal
  in the ranker.
