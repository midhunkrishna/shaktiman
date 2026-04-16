---
title: MCP Tools Overview
sidebar_position: 1
---

# MCP Tools Overview

Shaktiman's daemon (`shaktimand`) exposes seven MCP tools:

| Tool | What it does |
|---|---|
| [`summary`](./summary) | Codebase snapshot (file count, languages, symbol count, index health) |
| [`search`](./search) | Ranked hybrid search with locate / full modes |
| [`context`](./context) | Token-budgeted context assembly |
| [`symbols`](./symbols) | Exact symbol lookup by name |
| [`dependencies`](./dependencies) | Callers / callees traversal |
| [`diff`](./diff) | Recent file changes with symbol-level attribution |
| [`enrichment_status`](./enrichment-status) | Indexing / embedding progress and circuit-breaker state |

All tools are read-only and exclude test files by default (pass `scope: "test"` to
include them, or `scope: "all"` for both).

:::note Placeholder

Per-tool parameter schemas — verified against `internal/mcp/tools.go` — land in Step 5.

:::
