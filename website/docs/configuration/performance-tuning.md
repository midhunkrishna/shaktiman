---
title: Performance tuning
sidebar_position: 5
---

# Performance tuning

A quick-reference page listing the tuning knobs and their defaults. For the
rationale, measurement recipes, and trade-offs, see the
[Performance section](/performance/overview).

## User-facing knobs (in `shaktiman.toml`)

| Knob | Default | Effect summary |
|---|---|---|
| `search.max_results` | 10 | Max results per search. Higher = more scoring work. |
| `search.default_mode` | `locate` | `locate` (compact) or `full` (inline source). |
| `search.min_score` | 0.15 | Relevance floor. Higher = less noise. |
| `context.budget_tokens` | 4096 | Default assembly budget. Higher = more work. |
| `embedding.batch_size` | 128 | Ollama batch size. Higher = better throughput if hardware allows. |
| `embedding.timeout` | `"120s"` | HTTP timeout per batch. Affects circuit-breaker sensitivity. |
| `embedding.query_prefix` / `document_prefix` | `""` | Model-specific task prefixes. Not performance per se, but affects recall. |
| `vector.backend` | `brute_force` | See [Backend selection](/performance/backend-selection). |
| `database.backend` | `sqlite` | See [Backend selection](/performance/backend-selection). |

## Knobs not exposed via TOML (but in `DefaultConfig`)

These ship with defaults that are almost always correct. If you need to
change them, edit `internal/types/config.go:DefaultConfig` and rebuild:

| Knob | Default | Effect summary |
|---|---|---|
| `EnrichmentWorkers` | 4 | Parallel parse / extract workers. |
| `WatcherDebounceMs` | 200 | File-event coalescing window. |
| `WriterChannelSize` | 500 | SQLite writer backpressure. |
| `MaxBudgetTokens` | 4096 | Cap for assembly (usually matches `context.budget_tokens`). |
| `Tokenizer` | `cl100k_base` | Tokenizer for budget accounting. |

## Per-call knobs (MCP / CLI)

Many MCP tools accept per-call overrides. Full list under each tool's
reference:

- [`search`](/reference/mcp-tools/search) — `mode`, `max_results`,
  `min_score`, `path`, `scope`.
- [`context`](/reference/mcp-tools/context) — `budget_tokens`, `scope`.
- [`dependencies`](/reference/mcp-tools/dependencies) — `direction`,
  `depth`, `scope`.
- [`diff`](/reference/mcp-tools/diff) — `since`, `limit`, `scope`.

Per-call wins over TOML defaults.

## Tuning playbook

Don't tune speculatively. Reach for these in order when something's actually
slow:

1. **Measure first.** `time shaktiman search "query" --root .` and
   `shaktiman enrichment-status`. Baseline is the first commit.
2. **Lower `max_results`** for interactive use. 10 is plenty for most
   agent workflows.
3. **Raise `min_score`** if marginal hits dominate result sets.
4. **Switch to `hnsw`** (single-dev) or `qdrant` (shared) once the repo
   grows past ~75k chunks.
5. **Raise `batch_size`** if your Ollama is a GPU and the queue is lagging.
6. **Raise `timeout`** if your Ollama is occasionally slow and the circuit
   breaker is tripping unnecessarily.

If you reach step 6 and queries are still slow, consult
[Troubleshooting → Performance problems](/troubleshooting/performance-problems).

## See also

- [Config File reference](/configuration/config-file) — full TOML schema with
  validation rules.
- [Performance → Overview](/performance/overview) — the four axes and how
  to measure each.
