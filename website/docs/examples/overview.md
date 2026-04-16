---
title: Examples Overview
sidebar_position: 1
---

# Examples

Concrete, end-to-end walkthroughs that use Shaktiman's MCP tools against a real
repo. Each example follows the same shape:

1. The task you're trying to do.
2. The tool sequence that gets you there (with actual invocations).
3. What you learn from the output.
4. How this saves tokens vs. the naive approach.

## When to read which

| You want to... | Example |
|---|---|
| Scope a refactor — what breaks if I change this? | [Refactor impact analysis](./refactor-impact-analysis) |
| Understand a repo you just joined | [Onboarding to a new repo](./onboarding-to-a-new-repo) |
| Find out what changed that could've caused a regression | [Bug triage with diff](./bug-triage-with-diff) |
| Pull budget-fitted context across files for an LLM prompt | [Cross-file feature tracing](./cross-file-feature-tracing) |
| Measure the blast radius of an API change | [API change blast radius](./api-change-blast-radius) |

## Conventions used in these examples

- Tool names are the **MCP** names (`mcp__shaktiman__search`), which is what Claude
  Code sees. The CLI equivalents (`shaktiman search`) also work — substitute as
  you prefer.
- Example outputs are illustrative, not literal — the exact numbers depend on
  your repo.
- `scope:"impl"` is the default and is usually what you want. Flip to
  `scope:"test"` or `scope:"all"` when the task requires it.

## Reading order

If you're new to Shaktiman, read
[Onboarding to a new repo](./onboarding-to-a-new-repo) first. The others are
self-contained.
