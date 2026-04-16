---
title: symbols
sidebar_position: 5
---

# `symbols`

Find where a function, type, class, method, interface, or variable is defined — by
exact name. Returns file path, line number, signature, and visibility without reading
the whole file.

Unlike [`search`](./search), which ranks by relevance, `symbols` returns **every
definition** matching the name, optionally narrowed by kind and scope.

## Parameters

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | ✓ | — | Symbol name to look up (exact match). |
| `kind` | string | | — | Optional filter: `function`, `class`, `method`, `type`, `interface`, `variable`. |
| `scope` | enum | | `"impl"` | `"impl"`, `"test"`, or `"all"`. |

## Validation

- `name` is required. A missing or empty `name` returns an MCP tool error.
- `scope` must be one of the three enum values.

## Output

Returns JSON. When the name has no incoming references, the response is an array of
**Definition** objects:

```jsonc
[
  {
    "name": "NewServer",
    "kind": "function",
    "path": "internal/mcp/server.go",
    "line": 42,
    "signature": "func NewServer(cfg types.Config, ...) (*Server, error)",
    "visibility": "public"
  }
]
```

When the lookup produces both definitions and incoming references, the response is
enriched:

```jsonc
{
  "definitions": [ /* array as above */ ],
  "referenced_by": [ /* symbols that reference the looked-up name */ ],
  "note": "string — contextual message if applicable"
}
```

Use this enriched shape to discover callers without a second call to
[`dependencies`](./dependencies) in the simple case.

## Example invocations

```jsonc
// Find every definition named NewServer
{ "name": "symbols", "arguments": { "name": "NewServer" } }

// Narrow to a kind — useful when the name is reused (e.g. class and function)
{ "name": "symbols", "arguments": { "name": "Config", "kind": "type" } }

// Look for test helpers named `setup`
{ "name": "symbols", "arguments": { "name": "setup", "scope": "test" } }
```

## When `symbols` isn't the right tool

- The name is fuzzy or partial → use [`search`](./search).
- You want **callers**, not definitions → use
  [`dependencies`](./dependencies) with `direction: "callers"`.
- You want to know every literal textual occurrence (including strings and comments) →
  use `Grep`.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `symbolsToolDef` / `symbolsHandler`.
- Lookup logic: `core.LookupSymbols`.
