---
title: Query performance
sidebar_position: 4
---

# Query performance

Knobs that control how fast `search`, `context`, `symbols`, and
`dependencies` respond.

## The knobs

| Knob | Default | Affects | Cost of raising |
|---|---|---|---|
| `search.max_results` | 10 | Top-N per search | Linear — scoring N chunks |
| `search.min_score` | 0.15 | Noise floor | Lower score floor = more marginal hits kept |
| `context.budget_tokens` | 4096 | Assembled context size | Each doubled budget ≈ 1.5–2× assembly work |
| `dependencies.depth` (per-call) | 2 | BFS depth | Result count grows roughly exponentially |
| `[search].default_mode` | `locate` | Output size per result | `full` includes source — much larger responses |

Every knob above is per-call except `search.max_results`, `search.min_score`,
and `context.budget_tokens`, which also have config defaults in
`[search]` / `[context]`. See
[Config File](/configuration/config-file).

## Baseline latencies

With defaults on a typical mid-size repo (~50k chunks, `brute_force` vector
store):

| Query | Cold (first call after daemon start) | Warm |
|---|---|---|
| `summary` | <5 ms | <5 ms |
| `symbols name:"X"` | 5–10 ms | 2–5 ms |
| `search` (keyword-only) | 10–20 ms | 5–10 ms |
| `search` (with embeddings) | 30–50 ms | 15–30 ms |
| `context budget:4096` | 50–80 ms | 30–50 ms |
| `dependencies depth:2` | 10–30 ms | 5–15 ms |
| `dependencies depth:5` | 50–200 ms | 20–100 ms |

First call after daemon start warms SQLite page cache; subsequent calls
benefit.

## Trade-off patterns

### "Search feels slow."

Most common causes, in order:

1. **Cold index, embeddings not ready.** `enrichment_status` shows the state.
   If embedding % is low, queries currently use keyword + structural fallback
   — still ranked, just slower to converge on conceptual matches.
2. **`max_results` is very high.** You asked for 100+ hits; scoring is linear
   in N.
3. **`brute_force` at scale.** At 100k+ chunks, brute-force vector scan adds
   real time. Consider `hnsw` (see
   [Backend selection](./backend-selection)).
4. **Very low `min_score`** (e.g. 0.0). You're pulling in marginal hits that
   get rejected later anyway.

**Fix.** Lower `max_results` to 10–20 for interactive use. Raise `min_score`
to 0.25 if marginal hits dominate. Move to `hnsw` if the repo is huge.

### "`context` is slow."

The assembler cost scales with:

- **Budget size** — more budget means packing more chunks, spending more
  structural-expansion budget on graph BFS.
- **Graph density** — the structural-expansion step BFSes the call graph; a
  densely-connected module does more work than a loosely-coupled one.

**Fix.** Ask for the smallest budget that answers your question. 1024–2048
is often enough for focused queries; 4096 is the Sweet-spot default.

### "`dependencies depth:5` takes seconds."

At depth 5 in a well-connected codebase, result sets grow combinatorially —
hundreds of transitive callers is normal. Options:

1. **Start shallow.** Run depth 1 or 2 first; only go deeper when the
   shallow result is inadequate.
2. **Narrow the symbol.** If you're querying a very-popular name like
   `Client` or `New`, disambiguate with `symbols` first and query the
   specific variant.

### "`dependencies` is missing callers."

Not a performance issue per se, but worth flagging: indirect calls (calls
through function variables, interface method-set resolution, reflection)
aren't always captured by the structural extractor. `search` over the symbol
name picks them up as a fallback. See
[Known Limitations](/reference/limitations).

## Measurement

```bash
time shaktiman search "query" --root . --max 10
time shaktiman search "query" --root . --max 10 --mode full     # bigger responses
time shaktiman context "query" --root . --budget 4096
time shaktiman deps "Symbol" --root . --depth 3
```

For score breakdowns:

```bash
shaktiman search "query" --root . --explain --format text
# Shows per-signal contribution per hit
```

## See also

- [Searching & navigating the index](/guides/searching) — picking the right
  tool.
- [Backend selection](./backend-selection) — when `hnsw` / `qdrant` is
  warranted.
- [Troubleshooting → Performance problems](/troubleshooting/performance-problems)
  — when queries aren't just slow but broken.
