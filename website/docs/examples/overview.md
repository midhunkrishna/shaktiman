---
title: Examples Overview
sidebar_position: 1
---

# Examples

Concrete, end-to-end walkthroughs that use Shaktiman's MCP tools against a real
repo. Each page shows the task, the tool sequence, what the output tells you,
and the token savings versus a naive Grep/Read approach.

## When to read which

| You want to... | Example |
|---|---|
| Scope a refactor — what breaks if I change this? | [Refactor impact analysis](./refactor-impact-analysis) |
| Understand a repo you just joined | [Onboarding to a new repo](./onboarding-to-a-new-repo) |
| Find out what changed that could've caused a regression | [Bug triage with diff](./bug-triage-with-diff) |
| Pull budget-fitted context across files for an LLM prompt | [Cross-file feature tracing](./cross-file-feature-tracing) |
| Measure the blast radius of an API change | [API change blast radius](./api-change-blast-radius) |

:::note

Tool names below use the **MCP** names (e.g. `mcp__shaktiman__search`) that
Claude Code sees; the CLI equivalents (`shaktiman search`) work the same way.
Example outputs are illustrative, not literal — numbers depend on your repo.
`scope:"impl"` is the default; flip to `"test"` or `"all"` when the task calls
for it.

:::
