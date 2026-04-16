---
title: Refactor impact analysis
sidebar_position: 3
---

# Refactor impact analysis

**Task.** You're about to rename `UserRepository.FindByID` to
`UserRepository.GetByID`, or change its signature. Before touching anything, you
want to know: how many call sites will break, in which files, and through which
call chains.

## Tool sequence

### 1. Confirm the symbol exists where you think

```jsonc
{ "name": "symbols", "arguments": { "name": "FindByID", "kind": "method" } }
```

You probably see several hits (`FindByID` is a popular name). Pick the one on
`UserRepository`:

```jsonc
[
  {
    "name": "FindByID",
    "kind": "method",
    "path": "internal/repos/user.go",
    "line": 87,
    "signature": "func (r *UserRepository) FindByID(ctx context.Context, id string) (*User, error)",
    "visibility": "public"
  },
  {
    "name": "FindByID",
    "kind": "method",
    "path": "internal/repos/order.go",
    "line": 45,
    ...
  }
]
```

Use the `kind:"method"` filter to drop `FindByID` matches on other types or test
helpers.

### 2. Callers — the direct breakage

```jsonc
{
  "name": "dependencies",
  "arguments": { "symbol": "FindByID", "direction": "callers", "depth": 1 }
}
```

Depth 1 first. Every direct caller is a site you'll update. Count them — if
it's single digits, a manual sweep is fine. If it's dozens or more, think
harder about whether the rename is worth it.

### 3. Transitive callers — the blast radius

```jsonc
{
  "name": "dependencies",
  "arguments": { "symbol": "FindByID", "direction": "callers", "depth": 3 }
}
```

Depth 3 shows you who calls the callers of the callers. Not sites you need to
edit, but places whose *behaviour* depends on this code path — worth knowing
if you're changing semantics, not just renaming.

### 4. Test callers separately

```jsonc
{
  "name": "dependencies",
  "arguments": {
    "symbol": "FindByID",
    "direction": "callers",
    "depth": 2,
    "scope": "test"
  }
}
```

Tests are excluded by default (scope `"impl"`). Run again with `scope:"test"`
to get the fixture call sites you'll need to update. A complete impact analysis
is always two calls: `"impl"` and `"test"`.

### 5. Structural conflicts — the new name

Before renaming to `GetByID`, check nothing already clashes:

```jsonc
{ "name": "symbols", "arguments": { "name": "GetByID" } }
```

If there's already a `GetByID` on a different type, proceed — method-level
overlap is fine. If there's one on `UserRepository`, you've got a collision to
resolve first.

## Token math

- **Without Shaktiman.** `grep -r FindByID`, read through each match to judge
  relevance, repeat for indirect callers manually. Maybe 20k tokens of reads
  before you have a confident count.
- **With Shaktiman.** Three `dependencies` calls and two `symbols` calls return
  structured data, maybe 1–2k tokens total. You have an accurate impact map.

## Variations

- **Interface methods.** If `FindByID` is on an interface, `dependencies`
  resolves concrete implementations — its callers include every implementation
  site, not just one.
- **Indirect calls via function variables.** Go variable-holding-a-function
  patterns are harder to trace through `dependencies`; the call edge may not be
  captured. `search "FindByID"` fills the gap by finding text references.

## See also

- [`dependencies`](/reference/mcp-tools/dependencies) — direction semantics
  and depth guidance.
- [`symbols`](/reference/mcp-tools/symbols) — the kind filter.
- [API change blast radius](./api-change-blast-radius) — similar workflow for
  public API changes.
