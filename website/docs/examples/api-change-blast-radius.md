---
title: API change blast radius
sidebar_position: 6
---

# API change blast radius

**Task.** You're changing a public API — adding a required parameter to
`PaymentClient.Charge`, or making a previously-optional field mandatory. You
want the *full* reachable blast radius, not just direct callers, because
indirect behavioural changes matter for a public interface.

This is closely related to [Refactor impact analysis](./refactor-impact-analysis),
but for public APIs the depth and thoroughness matter more. You also want to
know which tests exercise each path.

## Tool sequence

### 1. Pin down the exact symbol

```jsonc
{ "name": "symbols", "arguments": { "name": "Charge", "kind": "method" } }
```

Pick the specific `Charge` on `PaymentClient`. Note its file, line, and
visibility — `visibility: "public"` confirms this is, in fact, an API call.

### 2. Full-depth callers, impl scope

```jsonc
{
  "name": "dependencies",
  "arguments": {
    "symbol": "Charge",
    "direction": "callers",
    "depth": 5
  }
}
```

Depth 5 is the maximum; for public APIs in a large codebase, you want it.
Results come back in depth-order — direct callers at depth 1, their callers at
depth 2, and so on. Each record includes the symbol, its file, and the depth
at which it was reached.

Sort or filter by depth in the output:

```bash
shaktiman deps Charge --root . --direction callers --depth 5 --format json \
  | jq 'sort_by(.depth) | group_by(.depth) | map({depth: .[0].depth, count: length})'
```

A typical response:

```jsonc
[
  { "depth": 1, "count": 3  },
  { "depth": 2, "count": 7  },
  { "depth": 3, "count": 14 },
  { "depth": 4, "count": 22 },
  { "depth": 5, "count": 35 }
]
```

Direct callers (depth 1) are the sites you must update. Depths 2+ are
behavioural blast radius — their correctness may depend on what `Charge` does,
even if they don't call it themselves.

### 3. Test-side blast radius

```jsonc
{
  "name": "dependencies",
  "arguments": {
    "symbol": "Charge",
    "direction": "callers",
    "depth": 5,
    "scope": "test"
  }
}
```

Gives you the tests that will need updating — directly or transitively.

### 4. Consumer-side context

If your API is consumed by code outside your repo, `dependencies` can't see it.
For first-party repos you also publish:

- Run `dependencies` on each repo separately (they have their own Shaktiman
  indexes).
- Communicate the change before merging — `dependencies` can't cross repo
  boundaries.

### 5. Spot-check with search

```jsonc
{
  "name": "search",
  "arguments": {
    "query": "PaymentClient Charge invocation",
    "max_results": 30,
    "scope": "all"
  }
}
```

Ranked by relevance, this surfaces call sites the dependency graph might have
missed — e.g. calls through function variables, interface assertions, or
reflection — which the structural extractor doesn't catch.

## Deciding whether to proceed

A rough triage rule:

- **Depth 1 ≤ 5 callers** → change is local; rename freely.
- **Depth 1 in 10s** → worth a deprecation window. Add the new signature,
  keep the old one wrapping the new, update callers over time.
- **Depth 5 reaches most of the codebase** → the API is load-bearing; break
  the change into multiple steps, each backwards-compatible.

## Token math

- **Without Shaktiman.** `grep -r Charge`, disambiguate from other `Charge`
  methods, walk callers manually, re-grep for their callers. Days of work on a
  large codebase.
- **With Shaktiman.** Two `dependencies` calls (impl + test) return the full
  reachable set in seconds. Triage rule runs itself.

## See also

- [`dependencies`](/reference/mcp-tools/dependencies) — direction and depth.
- [Refactor impact analysis](./refactor-impact-analysis) — the non-public
  counterpart.
- [Known Limitations](/reference/limitations) — what `dependencies` misses
  (indirect calls via function variables, cross-repo calls).
