---
title: Performance Overview
sidebar_position: 1
---

# Performance

The knobs that actually move Shaktiman's latency, throughput, memory, and disk
usage — what each costs, how to measure its effect, and how to pick a setting.

:::info Representative numbers

The ranges on these pages come from measured behaviour during development, not
a reproducible benchmark harness. Treat them as orders-of-magnitude guidance.
A dedicated benchmark corpus is a planned follow-up; if you have a
well-defined repo + workload you'd like to contribute measurements from,
open an issue.

:::

## The four axes

Every knob on these pages trades between one or more of:

| Axis | What it means |
|---|---|
| **Latency** | How long a single query takes (p50, p95). User-facing. |
| **Throughput** | How many queries / index operations you can do per second. Matters during cold index and under heavy agent use. |
| **Memory** | Resident memory of `shaktimand` plus connection overhead. |
| **Disk** | `.shaktiman/` footprint on local storage. |
| **Index build time** | How long cold / incremental index takes. |

## When to tune

For **small to mid-sized repos** (under ~1 M lines), defaults are fine. Tune
only if you hit a specific problem:

| Problem | Start here |
|---|---|
| Queries feel slow | [Query performance](./query-performance) |
| Cold index takes forever | [Indexing performance](./indexing-performance) |
| `shaktimand` is using more memory than you can spare | [Backend selection](./backend-selection) |
| Disk footprint is surprising | [Backend selection](./backend-selection) |
| Many developers sharing infrastructure | [Scaling](./scaling) |

## How to measure

Three commands cover 90% of observational needs:

```bash
# Is the index healthy? How big?
shaktiman status --root .

# Is embedding caught up?
shaktiman enrichment-status --root .

# Time a representative query
time shaktiman search "your typical query" --root .
```

For per-query score breakdowns (useful when tuning `min_score`), add
`--explain --format text` to `search`.

For deeper profiling, Shaktiman's structured log
(`.shaktiman/shaktimand.log`, JSON per line) records the duration of each
major phase — pipe through `jq` to inspect.

## Not covered here

- **LLM-side latency.** Shaktiman affects prompt size (by virtue of
  budget-fitted context), but model inference time is out of scope.
- **Claude Code / Cursor / Zed overhead.** Tiny and client-specific.
- **Network-bound backends.** If you're using Qdrant Cloud or remote
  Postgres, your latency floor is network RTT, not Shaktiman.

## See also

- [Configuration → Config File](/configuration/config-file) — the full TOML
  schema with defaults.
- [Configuration → Backends](/configuration/backends) — the fundamental
  choice that shapes every other axis.
