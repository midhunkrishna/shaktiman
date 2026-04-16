---
title: Searching & navigating the index
sidebar_position: 2
---

# Searching & navigating the index

Shaktiman exposes four query tools. They overlap a little, but each is the right
call for a different kind of question. Picking well saves tokens and gives you
better answers.

## Which tool for which question

| You want to... | Call | Why |
|---|---|---|
| Orient in the repo | [`summary`](/reference/mcp-tools/summary) | Files / languages / symbol counts / index health in one call. |
| Find code by concept | [`search`](/reference/mcp-tools/search) | Ranked hits across semantic, keyword, structural, change, and session signals. |
| Load N tokens of ranked context into a prompt | [`context`](/reference/mcp-tools/context) | Assembly step does dedup and budget fitting. |
| Find where a specific symbol is defined | [`symbols`](/reference/mcp-tools/symbols) | Exact name lookup — definition + signature + location, without reading the file. |
| Know who calls something (or what it calls) | [`dependencies`](/reference/mcp-tools/dependencies) | One call, full call chain, depth 1–5. |
| See what changed recently | [`diff`](/reference/mcp-tools/diff) | Time-windowed, symbol-level change attribution. |
| Find an **exact** string or regex | `Grep` | Shaktiman ranks by relevance; `Grep` matches literals. They're complementary. |

## The discovery workflow

The default loop when exploring an unfamiliar area:

```
1. summary                  → what's in this repo?
2. search "concept"          → which files matter?
3. Read only the top hits   → see the code
4. Edit / modify             → make the change
```

For deeper investigations, step 2 becomes a small cascade:

```
1. summary
2. search "concept"            → picks the right area
3. symbols name:"Foo"          → pinpoints a definition inside that area
4. dependencies symbol:"Foo"   → maps the blast radius
5. Read the files that matter
```

## `search` modes

Every hit a rank engine produces has a file path, line range, symbol, and score.
`search` returns them in one of two modes:

- **`locate`** (default) — compact text pointers, ~12 tokens per result. Claude
  then `Read`s just the files that matter.
- **`full`** — inline source for each hit. Useful when you want to *avoid* a
  follow-up `Read` but costs more tokens per result.

Set the default with `[search].default_mode` in
[`.shaktiman/shaktiman.toml`](/configuration/config-file); callers may override
per-call.

## Scoping to impl / tests

Every query tool accepts a `scope` parameter:

| Value | Effect |
|---|---|
| `"impl"` | **Default.** Excludes files matching `[test].patterns`. |
| `"test"` | Test files only — handy when you want to understand how a module is exercised. |
| `"all"` | No filter. |

Test-file detection is pattern-based (globs like `*_test.go` and directory prefixes
like `testdata/`). The defaults per language are listed under
[Config File → `[test]`](/configuration/config-file#test).

## Ranking, briefly

`search` and `context` combine five signals into a single score in `[0, 1]`:
semantic (vector), structural (graph distance), change (recency + magnitude),
session (access history in the current run), and keyword (FTS5 BM25). If any
signal is unavailable (e.g. embeddings aren't built yet, the circuit breaker is
open), its weight is redistributed onto the others — queries still work,
accuracy just changes.

For the math and the five-level fallback chain, see
[Architecture — §3.2 Query Engine](/design/architecture).

## Keyword fallback

When Ollama is down or embeddings haven't caught up yet, semantic scoring is
unavailable. Queries automatically drop to keyword + structural + change + session
ranking, with weight redistributed. You'll still get ranked results; they'll just
depend more on exact term matches than on conceptual similarity.

Check [`enrichment_status`](/reference/mcp-tools/enrichment-status) to see which
mode the engine is currently running in (circuit breaker state + embedding
percentage).

## When `Grep` is the right call

Shaktiman is the wrong tool when you need:

- An exact string match (variable names, log lines, error strings).
- A regex over files.
- To find every literal occurrence of a name (including comments / docstrings).

Use `Grep` for those — and if a `Grep` returns 30 hits, run `search` with similar
terms to pick which 3 to read.
