---
title: Scaling
sidebar_position: 5
---

# Scaling

When to move from the default local-first setup to externalised backends,
and what changes.

## The scaling axes

Shaktiman is designed single-developer-first. You'd scale beyond defaults
when one of:

1. **Repo grows past ~1 M lines** — default `brute_force` vector search
   starts to crawl.
2. **Shared team infrastructure** — multiple developers want a common index
   rather than each maintaining their own.
3. **CI / scripted indexing** — indexing on ephemeral runners, with state
   living externally.
4. **Air-gapped / centralised control** — compliance reasons push state off
   developer laptops.

## Scaling-up paths

### Path 1: single dev, big repo → `hnsw`

The easiest scaling step. Stays local-first; only swaps the vector backend.

```toml
[vector]
backend = "hnsw"
```

Then `shaktiman reindex --embed --vector hnsw`. See
[Re-indexing](/guides/reindexing).

Gives you sub-linear vector search (log N) at the cost of approximate recall
(typically 95%+ of exact results, configurable via HNSW parameters). Memory
profile shifts from "all vectors in RAM" to "hot subset in RAM, rest on
disk".

**Don't do this for small repos.** `brute_force` is faster at small N
because the graph-traversal overhead of HNSW dominates. Break-even is
around 50k–100k chunks.

### Path 2: multi-dev, single-repo → Postgres + pgvector or Qdrant

When multiple developers want a common index (or a team CI pipeline wants
state that persists across runs):

1. **Create the schema once, up front.** Shaktiman's migrations create
   tables and indexes inside a schema — they do **not** create the schema
   itself. If you point Shaktiman at a non-existent schema, the first
   migration fails with `schema "..." does not exist`. Run this once against
   your Postgres (substitute your own schema name):

   ```sql
   CREATE SCHEMA IF NOT EXISTS shaktiman_myproject;
   ```

   The default `public` schema exists on a fresh Postgres, so you can skip
   this step if you're keeping the default.

2. **Point Shaktiman at it:**

   ```toml
   [database]
   backend = "postgres"

   [postgres]
   connection_string = "postgres://..."
   schema = "shaktiman_myproject"       # one schema per project

   [vector]
   backend = "pgvector"                  # or "qdrant"
   ```

**Constraint from [ADR-003](/design/adr-003-pluggable-backends) A12:**
Postgres metadata forbids `brute_force` / `hnsw` vector backends. The
validator rejects the combination at startup with a clear error.

Project isolation is via `project_id` column on every Postgres-backed
table — multiple projects can share a Postgres schema without stomping on
each other.

### Path 3: multi-repo, multi-team → one Qdrant, many Postgres schemas

Large orgs with many repos might centralise:

- One managed Postgres (RDS / Neon / Supabase) with a schema per repo.
- One managed Qdrant cluster with a collection per repo.

Each repo gets its own `shaktimand` config pointing at the shared
infrastructure. Indexes can be rebuilt on any machine (CI, dev laptop)
without copying state — the state lives in the managed services.

This isn't a scenario Shaktiman is explicitly optimised for, but the
building blocks are there:
[ADR-003](/design/adr-003-pluggable-backends) was written with this shape in
mind.

## What stays local even when scaled

- **Parsing.** Tree-sitter still runs on the machine doing the indexing. CPU
  cost doesn't disappear; it just shifts to whichever machine invoked
  `shaktiman index`.
- **Embedding batches.** Ollama (or whatever you use) runs wherever it runs;
  remote metadata / vector stores don't change the embedding path.
- **MCP server.** Each client (Claude Code, Cursor, Zed) launches its own
  `shaktimand`. The leader/proxy mechanism still applies per-project-root.

## Gotchas when scaling out

- **Embedding dimensions must stay consistent.** Changing `[embedding].dims`
  across a shared deployment breaks the vector store. Agree on the model up
  front.
- **Schema migrations** happen at `shaktimand` startup. If you're running
  multiple daemon versions against the same Postgres, the older one may see
  errors after the newer one runs migrations. Roll deployments together.
- **Qdrant collection naming** defaults to `"shaktiman"` — set
  `[qdrant].collection` explicitly to avoid collisions between projects
  sharing a Qdrant cluster.

## When not to scale

If the defaults work, leave them. Every additional piece of infrastructure
is more to operate. Single-dev Shaktiman on `sqlite` + `brute_force` is
plenty for most real workflows.

## See also

- [Configuration → Backends](/configuration/backends) — the validation rules.
- [ADR-003 — Pluggable Storage Backends](/design/adr-003-pluggable-backends)
  — the decision record behind these options.
- [Multi-instance concurrency](/guides/multi-instance) — how per-project
  leadership plays with shared state.
