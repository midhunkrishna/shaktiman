---
title: From Sourcegraph / Cody
sidebar_position: 4
---

# Migrating from Sourcegraph / Cody

## Mental-model shift

Sourcegraph is a **hosted, organisation-wide, cross-repo** code-intelligence
platform. Cody is the AI layer on top. The core value props:

- One search over every repo in the org.
- Cloud indexing; no per-developer local footprint.
- Integrates with code-host permissions.

Shaktiman is **local-first, single-project, developer-machine-bound**:

- Indexes one project at a time.
- Lives on your laptop; no external service.
- Designed for the MCP ecosystem (Claude Code, Cursor, Zed).

These are different products solving different problems. You'd pick Shaktiman
when a single developer needs rich, agent-usable code intelligence for a
project they work on day-to-day, and you don't want (or can't have) a hosted
service in the loop.

## Feature parity table

| Capability | Sourcegraph / Cody | Shaktiman | Notes |
|---|---|---|---|
| Cross-repo search | ✓ | ✗ | Single-repo only. |
| Hosted, zero per-developer install | ✓ | ✗ | Shaktiman runs per-dev. |
| Respects host-level permissions | ✓ | N/A | Runs on your laptop; your permissions are yours. |
| Free for open-source repos | Partially | Always | MIT. |
| Ranked semantic search | ✓ | ✓ | Similar idea, different implementation. |
| AI chat over codebase | Cody | Claude Code / Cursor / Zed + Shaktiman | Substitute the client. |
| Precise cross-language references | ✓ | Partial | Shaktiman's graph is per-language. |
| Offline / air-gapped | ✗ | ✓ | Shaktiman has no external dep except optional Ollama. |
| No data leaves the machine | ✗ | ✓ (sans Ollama-as-a-service configurations) | Important for some orgs. |
| GraphQL API / integrations | ✓ | ✗ (MCP instead) | Different integration model. |

## Side-by-side workflow

**"Find every caller of `validateToken` across our org."**

Sourcegraph:

1. Open Sourcegraph UI or call the API.
2. `validateToken` → references panel → spans all repos.
3. Click through results.

Shaktiman:

1. You can only do this **per repo**. If `validateToken` exists in repo A and
   callers are in repo B, Shaktiman indexed on repo A doesn't see repo B.
2. Workaround: run Shaktiman on each repo, query each.

This is a genuine gap, not a workaround that closes it.

## Gaps (where Sourcegraph wins)

- **Cross-repo.** Full stop. If that's your need, Shaktiman is the wrong
  tool.
- **Permissions-aware.** Sourcegraph enforces who-can-see-what; Shaktiman runs
  as you, on your files.
- **Infrastructure for large orgs.** Shared indexes, shared tuning, shared
  observability. Shaktiman has none.
- **Cody as an integrated product.** Cody is the Sourcegraph AI layer. With
  Shaktiman you supply your own client (Claude Code, Cursor, etc.).

## Where Shaktiman wins

- **Local-first.** No server to stand up, no data egress, no licenses.
- **Zero cost** for open-source or internal use.
- **Fits the MCP ecosystem.** If your agent is Claude Code / Cursor / Zed,
  Shaktiman is native.
- **Budget-fitted context assembly.** `context` is explicitly designed to
  produce a token-bounded context package — a primitive Sourcegraph doesn't
  expose directly.

## When to keep Sourcegraph / Cody

- Your team works across many repos and needs unified search.
- Your org's security model requires hosted, permission-aware code access.
- You already have Sourcegraph as an infra decision and it serves you.

You can run both. Sourcegraph for cross-repo exploration; Shaktiman for
day-to-day, agent-driven work inside the repo you're currently editing.

## See also

- [Claude Code integration](/integrations/claude-code) — the equivalent of
  Cody-on-Sourcegraph.
- [Multi-instance concurrency](/guides/multi-instance) — how Shaktiman handles
  multiple Claude Code sessions on the same project.
