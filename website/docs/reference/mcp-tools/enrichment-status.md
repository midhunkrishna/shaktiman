---
title: enrichment_status
sidebar_position: 8
---

# `enrichment_status`

Report embedding and indexing progress plus the embedding circuit-breaker state. Use it
to check whether semantic search is currently available and how complete the index is.

## Parameters

None.

## Output

Returns a JSON object with:

- **Chunk counts.** Total chunks indexed and how many have vector embeddings.
- **Embedding percentage.** The derived `embedded / total` ratio — drops to 0 when
  Ollama is unreachable and the embedding worker is disabled.
- **Pending jobs.** How many embedding jobs are queued behind the circuit breaker.
- **Circuit breaker state.** One of:

  | State | Meaning |
  |---|---|
  | `closed` | Normal operation — embedding is running. |
  | `open` | Embedding paused after repeated failures; will retry after the cooldown. |
  | `half_open` | A probe batch is in flight to see if the backend recovered. |
  | `disabled` | Embedding is off — either by config (`embedding.enabled = false`) or because the backend was unavailable for too long. |
  | `n/a` | Returned on the CLI path (`shaktiman enrichment-status`) where no embed worker is attached to the process — the tool can still report chunk counts and pending jobs, but circuit-breaker state is unknowable from outside the daemon. |

Result counts come from `core.GetEnrichmentStatus` and the embedding worker's
`Pending()` / `CircuitBreaker().State()` accessors in `internal/vector/`. From the MCP
handler, if no embedding worker is running (e.g. `EmbedEnabled = false`), pending is
reported as 0 and state as `disabled`. From the CLI handler (no worker process
attached), state is reported as `n/a`.

## When to use

- **First diagnostic** for `"search returns nothing useful"` — if embedding %% is 0 and
  the state is `open` or `disabled`, semantic search isn't running and you're seeing
  keyword-only results.
- **Progress tracking** during cold index of a large repo.
- **Before relying on semantic results.** If the state isn't `closed` and the
  embedding %% is low, lean on `symbols`, `dependencies`, and `search` in keyword
  fallback mode.

## When `enrichment_status` isn't the right tool

- General repo orientation (files, languages, symbol counts) → [`summary`](./summary).
- Root-causing embedding failures → this tool tells you *what* state you're in but not
  *why*. Consult `shaktimand`'s logs in `.shaktiman/shaktimand.log`.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `enrichmentStatusToolDef` /
  `enrichmentStatusHandler`.
- Aggregation: `core.GetEnrichmentStatus`, `internal/vector/` (EmbedWorker, circuit
  breaker).
