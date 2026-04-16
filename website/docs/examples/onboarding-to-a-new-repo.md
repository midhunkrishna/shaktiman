---
title: Onboarding to a new repo
sidebar_position: 2
---

# Onboarding to a new repo

**Task.** You've been dropped into a Go/TypeScript backend. You don't know the
layout, the main entry points, or where authentication is handled. Get oriented
without reading twenty files.

## Tool sequence

### 1. `summary` — what's in here

```jsonc
{ "name": "summary", "arguments": {} }
```

You get something like:

```
Files: 4213 | Chunks: 58104 | Symbols: 41220
Languages: typescript (3210), go (901), python (102)
Embeddings: 100%
```

Now you know the stack mix and that the index is fully populated (so conceptual
search will work, not just keyword).

### 2. `search` for the key concepts

Pick the questions you'd normally fan out a dozen greps for. For example:

```jsonc
{ "name": "search", "arguments": { "query": "http server entry point" } }
{ "name": "search", "arguments": { "query": "authentication middleware" } }
{ "name": "search", "arguments": { "query": "database connection setup" } }
```

Each returns the top 10 files ranked by a blend of semantic, structural, and
keyword signals. You get compact pointers (~12 tokens/result in the default
`locate` mode):

```
internal/server/server.go:42-91   NewServer                      0.94
cmd/app/main.go:15-38             main                           0.88
internal/server/router.go:12-28   setupRoutes                    0.81
...
```

### 3. `Read` just the hits that matter

At this point you've spent ~200 tokens total and know exactly three files to
read instead of crawling the tree. Read them.

### 4. `symbols` when a name looks central

```jsonc
{ "name": "symbols", "arguments": { "name": "NewServer" } }
```

Returns the definition with signature:

```jsonc
[{
  "name": "NewServer",
  "kind": "function",
  "path": "internal/server/server.go",
  "line": 42,
  "signature": "func NewServer(cfg types.Config, ...) (*Server, error)",
  "visibility": "public"
}]
```

### 5. `dependencies` to see who wires into it

```jsonc
{
  "name": "dependencies",
  "arguments": { "symbol": "NewServer", "direction": "callers", "depth": 2 }
}
```

Shows you who calls `NewServer` (and who calls those callers) — that's the
plumbing you'd otherwise reconstruct by grep.

## Token math

- **Without Shaktiman.** 8–15 `Read` calls × ~2–5k tokens = 15–60k tokens
  before you've even understood the layout.
- **With Shaktiman.** One `summary`, three `search` calls in `locate` mode
  (~400 tokens of results total), a couple of focused `Read` calls on the top
  hits (~6–10k tokens). You're at maybe 12k tokens and you actually know the
  repo.

## Variations

- **Test coverage orientation.** Replace any `search` call's scope with
  `"test"` to see how the concept is exercised.
- **Polyglot repo.** Language-agnostic tools; no need to know grep patterns for
  each language's declaration syntax.
- **Lightly-documented repo.** `symbols` still works on undocumented code —
  you get the signature even without a comment.

## See also

- [Searching & navigating the index](/guides/searching) — the full "pick the
  right tool" reference.
- [`search`](/reference/mcp-tools/search), [`symbols`](/reference/mcp-tools/symbols),
  [`dependencies`](/reference/mcp-tools/dependencies) — schemas.
