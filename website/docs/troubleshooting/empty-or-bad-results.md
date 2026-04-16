---
title: Empty or bad results
sidebar_position: 4
---

# Empty or bad results

Covers queries that return nothing useful — either empty, or ranked in a way that
surprises you.

## Symptom: `search` returns zero results for an obviously-present term

### Likely causes (ranked)

1. `scope: "impl"` (the default) is excluding the file as a test. Default test
   patterns for your language may be wider than you expect —
   `*.spec.ts` and `*.test.ts` for TypeScript; `*_test.go` and `testdata/` for
   Go; full list at
   [Config File → `[test]`](/configuration/config-file#test).
2. `min_score` is higher than the actual best hit. Default is 0.15; queries with
   weak signals may not beat it.
3. The file extension isn't wired for indexing.
4. The index is stale — your edits are recent and the watcher hasn't caught up.
5. Embeddings aren't ready (cold index still running) and the keyword fallback
   doesn't pattern-match the query terms exactly.

### Diagnostic

```bash
# Remove the scope filter
shaktiman search "your query" --root . --scope all

# Lower the score floor
shaktiman search "your query" --root . --min-score 0.0

# Inspect score signals
shaktiman search "your query" --root . --explain --format text

# Is the file even indexed?
shaktiman symbols "SomeSymbolInThatFile" --root . --scope all
```

### Fix

- If `scope: "impl"` was the issue: either use `scope: "all"` / `scope: "test"`,
  or adjust `[test].patterns` in `shaktiman.toml` to exclude the path.
- If `min_score` was the issue: lower it per-query (`--min-score`) or in config
  (`[search].min_score`). 0.05 is a reasonable floor for noisy queries.
- If the extension isn't wired:
  [add the language](/reference/supported-languages#adding-a-language).
- If the index is stale: save the file again and wait ~500ms.

## Symptom: results are ranked in a weird order

### Likely cause

The five-signal ranker combines semantic, structural, change, session, and
keyword signals. If one is unavailable (embeddings not built, Ollama down, fresh
session with no access history), that signal's weight is redistributed onto the
others. You may notice:

- **No embeddings** → ranking is keyword + structural + change + session. Exact
  term matches dominate.
- **Fresh session** → no session signal. Recently-changed files float up via the
  change signal.
- **Graph not populated for new files** → structural score is zero for them
  until the next enrichment pass.

### Diagnostic

```bash
shaktiman search "query" --root . --explain --format text
# Shows the per-signal contribution for each hit
```

### Fix

- If embeddings aren't built: wait, or check
  [`enrichment_status`](/reference/mcp-tools/enrichment-status). If the circuit
  breaker is stuck open, see
  [Embedding failures](/troubleshooting/embedding-failures).
- If you want keyword dominance, bias the score floor up with
  `--min-score 0.25` — low-keyword-signal noise drops out.
- If you want conceptual matches, wait for embeddings to finish cold-indexing.

## Symptom: `symbols` finds the name in some files but not others

### Likely cause

The symbol extractor is sensitive to how each tree-sitter grammar labels nodes.
Some constructs (e.g. interface default methods in Java, decorated methods in
Python, traits in Rust, namespace members in TypeScript) were under-covered
pre-ADR-004 and may still have edge cases.

### Diagnostic

```bash
# Does search find it (which uses FTS5, not the symbol index)?
shaktiman search "SymbolName" --root . --scope all --format text

# If search finds it and symbols doesn't, it's a chunking/symbol gap.
```

### Fix

- File an issue with the language, node type, and a minimal reproducer.
- Workaround: use
  [`search`](/reference/mcp-tools/search) instead of `symbols`, or `Grep` if you
  need the literal.
- See
  [Known Limitations → parser gaps](/reference/limitations#some-constructs-arent-chunked-per-language).

## See also

- [Searching & navigating the index](/guides/searching) — picking the right tool
  for the question.
- [`search` reference](/reference/mcp-tools/search) — every parameter.
