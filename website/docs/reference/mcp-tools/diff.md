---
title: diff
sidebar_position: 7
---

# `diff`

Show recent file changes and which symbols were added, modified, or removed.
Complements `git log` with structured, symbol-level attribution. Use it to see which
definitions were touched by recent work — useful for bug triage, code review, and
onboarding to a repo mid-flight.

## Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `since` | string | | `"24h"` | Time window as a Go duration (`24h`, `1h`, `30m`, `7d` is **not** supported — use `168h`). Max 720h. |
| `limit` | number | | `50` | Maximum diffs to return. Range 1–500; out-of-range values silently reset to 50. |
| `scope` | enum | | `"impl"` | `"impl"`, `"test"`, or `"all"`. |

## Validation

- `since` must be a valid Go `time.Duration` string. Invalid durations return an MCP
  tool error (e.g. `"1 day"` is rejected; use `"24h"`).
- Values above 720h (30 days) are clamped to 720h.
- Invalid `limit` is **not** an error — it silently defaults to 50.

Note: Go duration strings cover `"ns"`, `"us"`, `"µs"`, `"ms"`, `"s"`, `"m"`, `"h"`.
Day/week units aren't supported by the standard parser.

## Output

Returns a JSON array of diff records. Each record describes:

- The changed file.
- Timestamp of the change.
- Change type (`add`, `modify`, `delete`, `rename`).
- Line counts (added / removed).
- Affected symbols, each with its name and change type (`added`, `modified`, `removed`,
  `signature_changed`).

Newest-first. Capped by `limit`.

## Example invocations

```jsonc
// What changed in the last day?
{ "name": "diff", "arguments": {} }

// The last hour's worth
{ "name": "diff", "arguments": { "since": "1h" } }

// Last 3 days of changes, test files only
{
  "name": "diff",
  "arguments": { "since": "72h", "scope": "test", "limit": 100 }
}
```

## When `diff` isn't the right tool

- You want a full commit-level history with messages and authors → use `git log`
  directly.
- You need the textual diff content (patch) → `diff` returns symbol-level metadata,
  not unified diffs.
- You want to know what a specific symbol's callers used to look like → `diff` surfaces
  change metadata but not historical content.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `diffToolDef` / `diffHandler`.
- Query: `core.LookupDiffs`.
