---
title: MCP Tools Overview
sidebar_position: 1
---

# MCP Tools Overview

`shaktimand` is a stdio MCP server. When your client (Claude Code, Cursor, Zed, or any
MCP-speaking agent) launches the daemon, the following tools become available. Each tool
is **read-only**, **idempotent**, and **non-destructive** (advertised via the MCP hints
on every tool registration).

## Tools at a glance

| Tool | When to reach for it | Key parameters |
|---|---|---|
| [`summary`](./summary) | Orient in an unfamiliar codebase — file count, languages, symbol count, index health | none |
| [`search`](./search) | Find code by concept (not exact string) — returns ranked file pointers | `query`, `mode`, `max_results`, `min_score`, `path`, `scope` |
| [`context`](./context) | Gather multi-file context within a token budget | `query`, `budget_tokens`, `scope` |
| [`symbols`](./symbols) | Find where a function / type / class is defined, by name | `name`, `kind`, `scope` |
| [`dependencies`](./dependencies) | Traverse the call graph (callers or callees) of a symbol | `symbol`, `direction`, `depth`, `scope` |
| [`diff`](./diff) | See recent file changes with symbol-level attribution | `since`, `limit`, `scope` |
| [`enrichment_status`](./enrichment-status) | Check indexing / embedding progress and circuit-breaker state | none |

## Conventions shared by every tool

### `scope` parameter

Most tools accept a `scope` parameter that controls whether test files are included:

| Value | Effect |
|---|---|
| `"impl"` | **Default.** Implementation files only; test files excluded. |
| `"test"` | Test files only. |
| `"all"` | Both implementation and test files. |

Test files are detected via the `TestPatterns` configured per language (e.g. `*_test.go`
for Go, `*.test.ts` / `*.spec.ts` for TypeScript).

### Query length

`search` and `context` cap the `query` string at 10,000 characters. Queries above that
limit return an error without being executed.

### Error handling

Tools return MCP tool errors for invalid inputs — missing required parameters,
out-of-range numeric arguments, invalid enum values, and malformed durations. Internal
errors are sanitized to 200 characters before being returned, so callers won't see
stack traces or internal paths.

### When built-in tools are the right choice

Shaktiman is a **ranked** discovery tool. When you need exact pattern matching, reach
for the built-in tools in your client:

| Task | Use |
|---|---|
| Exact string match | `Grep` (literal search) |
| Regex match | `Grep` |
| Find files by name | `Glob` |
| Read a specific known file | `Read` |

`search` ranks by conceptual relevance. `Grep` returns every literal match, unranked.
They're complementary.
