---
title: summary
sidebar_position: 2
---

# `summary`

Quick codebase snapshot: file count, languages, symbol count, and index health. Use it
first when orienting in an unfamiliar repository — the answer comes back in a single
call, without reading any source files.

## Parameters

None.

## Returns

A JSON object with aggregate counts. Fields include, at minimum:

- Total files indexed, grouped by language.
- Total chunk and symbol counts.
- Embedding status — how many chunks have vector embeddings (useful for deciding whether
  semantic search is available).

Exact shape is produced by `core.GetSummary` and may grow over time; treat it as a loose
schema.

## When to use

- **First call in a new repo.** Before you search, you want to know what's even there.
- **Health check** before running a long chain of queries — confirms the daemon is
  reachable and the index is populated.
- **Sizing calls.** If the repo has 8 languages, your query needs to be more specific
  than in a pure-Go repo.

## When `summary` isn't the right tool

- Looking up a specific symbol — use [`symbols`](./symbols).
- Checking whether the embedding backend is catching up — use
  [`enrichment_status`](./enrichment-status), which carries the circuit-breaker state
  and pending-job count.

## Source of truth

- Tool registration: `internal/mcp/tools.go` — `summaryToolDef` /
  `summaryHandler`.
- Aggregation: `core.GetSummary`.
