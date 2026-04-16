---
title: ADR-001 — Code Review Capabilities
sidebar_position: 2
---

# ADR-001: Code Review Capabilities

**Status:** AMENDED (original 2026-02, amendment 2026-03-31) · **Status today: NOT SHIPPED.**

:::info[This is a summary]

The full ADR — context, six-gap analysis, alternatives, consequences, pre-mortem,
FMEA, phased rollout, component design, open questions, and the 2026-03-31
amendment on git-native symbol extraction — lives in the repo:
[`docs/design/adr-001-code-review-capabilities.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-001-code-review-capabilities.md).
Read that before making a call that touches review tooling.

:::

## Status today

**Not shipped.** No code-review-specific MCP tool exists in `internal/mcp/tools.go`
at the time of writing. The ADR is retained as a design record and still reflects
the intended direction if a review tool is picked up.

## Context

For the code-review use case, Shaktiman currently *increases* token usage compared
to a naive `git diff` + `Read` approach (~8,850 tokens vs. ~5,000 for a 3-file MR).
Six gaps drive the overhead: the diff tool can't target a specific MR, the specced
`include_impact` parameter is unimplemented, dependencies can't be queried in batch,
edge kinds aren't surfaced, search can't scope to a file set, and there's no unified
review tool — so a review needs 5–8 MCP round-trips.

## Decision

**Hybrid approach (Alternative D):** enhance existing tools *and* add a thin
`review` orchestration tool. Three layers:

1. **Fix existing tools.** Implement `include_impact` on diff; add batch-`symbols`
   to dependencies; surface edge kind in responses. All backed by already-present
   store methods.
2. **Git-aware diff source.** Add `commit_range` to diff; shell out to `git diff
   --name-status -z` to pick the file list, then look up symbols from the
   Shaktiman index. The 2026-03-31 amendment extends this to git-native symbol
   extraction so reviewers don't need to check out the target branch.
3. **`review` orchestration tool.** A single MCP call composing diff + dependencies
   + context within a token budget, producing a review-ready package in ~2,500–3,500
   tokens for a typical 3-file MR.

## Key constraints

- **Design philosophy preserved.** Git is a file-list source only, not a content
  store — "Shaktiman indexes the working tree, not git history" still holds.
- **No schema migration.** All changes use existing tables; the edge kind is
  already stored, just not surfaced through the `Neighbors()` CTE.
- **New external dependency on the `git` CLI**, isolated to a single `gitdiff`
  package with `-z` porcelain output.
- **Incremental delivery.** Layer 1 ships value even if Layer 3 never does.

## When to revisit

- When there's actual demand from reviewers using Claude Code for MRs.
- If someone picks up Layer 1 (the API gaps are individually useful) — that
  unlocks Layers 2 and 3 without committing to the full scope up front.
- If the MCP tool count becomes a concern (review would bring it to 8).
