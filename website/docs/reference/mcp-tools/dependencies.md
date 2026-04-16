---
title: dependencies
sidebar_position: 6
---

# `dependencies`

Traverse the call graph from a symbol in one call — callers, callees, or both. Replaces
multiple rounds of grepping and reading to follow a chain of functions. There's no
built-in equivalent.

## Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `symbol` | string | ✓ | — | Symbol name to traverse from. |
| `direction` | string | | `"both"` | `"callers"` (→ `incoming`), `"callees"` (→ `outgoing`), or `"both"`. |
| `depth` | number | | `2` | BFS depth. Range 1–5. |
| `scope` | enum | | `"impl"` | `"impl"`, `"test"`, or `"all"`. |

The handler aliases `callers` → `incoming` and `callees` → `outgoing` internally so both
forms are accepted.

## Validation

- `symbol` is required.
- `direction` must be one of `callers`, `callees`, or `both` (or the internal
  equivalents). Any other value returns an MCP tool error.
- `depth` must be in `[1, 5]`.
- `scope` must be one of the enum values.

## Output

Returns a JSON array of dependency records. Each record includes the symbol name, the
file and line where it's defined, the edge kind (e.g. `calls`, `type_ref`), and the BFS
depth at which it was reached. Result count is carried in the MCP result-count
annotation.

## Picking a depth

- **`depth: 1`** — direct neighbors only. Fast, low noise. Good for "who calls this?"
- **`depth: 2`** (default) — two hops. Enough for most impact analyses.
- **`depth: 3+`** — use when mapping a subsystem. Result sets grow quickly; combine
  with a more specific `symbol` to keep output manageable.

The traversal is breadth-first, so larger depths surface deeper callers/callees but also
expand the result set combinatorially. Start shallow and increase only if needed.

## Example invocations

```jsonc
// Who calls processOrder? (direct callers only)
{
  "name": "dependencies",
  "arguments": { "symbol": "processOrder", "direction": "callers", "depth": 1 }
}

// Full reachable neighborhood (callers + callees, 3 hops)
{
  "name": "dependencies",
  "arguments": { "symbol": "validateToken", "direction": "both", "depth": 3 }
}

// Just the test callers of a private helper
{
  "name": "dependencies",
  "arguments": { "symbol": "buildURL", "direction": "callers", "scope": "test" }
}
```

## When `dependencies` isn't the right tool

- You want to find where a name is **defined** (not called) → use
  [`symbols`](./symbols).
- You want text references, including in comments or strings → use `Grep`.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `dependenciesToolDef` /
  `dependenciesHandler`.
- Traversal: `core.LookupDependencies`.
