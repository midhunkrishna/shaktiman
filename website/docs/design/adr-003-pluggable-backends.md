---
title: ADR-003 — Pluggable Storage Backends
sidebar_position: 4
---

# ADR-003: Pluggable Storage Backends

**Status:** AMENDED (2 amendments). **Status today: SHIPPED.**

:::info[This is a summary]

The full ADR — context, alternatives, 10 sections of detailed design (config,
provider registry, package layout, per-backend specifics for Postgres, Qdrant,
pgvector), 5 implementation phases, pre-mortem, FMEA, and the two amendments
— lives in the repo:
[`docs/design/adr-003-pluggable-storage-backends.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-003-pluggable-storage-backends.md).
Read that before adding a backend or changing the registry.

:::

## Status today

**Shipped.** All backend combinations from the decision table are registered
and working:

- **Metadata:** `sqlite` (default), `postgres` (build tag `postgres`) —
  `internal/storage/registry.go`.
- **Vector:** `brute_force` (default), `hnsw`, `qdrant` (build tag `qdrant`),
  `pgvector` (build tag `pgvector`) — `internal/vector/registry.go`.
- **Constraint A12** (postgres rejects `brute_force`/`hnsw`) enforced in
  `ValidateBackendConfig` at `internal/types/config.go`.

## Context

Shaktiman's original storage layer hard-coded SQLite + an in-process
brute-force vector store. Teams sharing an index and larger repos needed
alternatives — but a big-bang rewrite risked destabilizing the working local-
first path. The design had to let new backends slot in without touching the
existing SQLite code path.

## Decision

**Provider pattern** — the Go `database/sql` driver model. Each backend
registers itself via an `init()` function into a central registry; a
config-driven factory constructs the right implementation at daemon startup.
No abstract factory hierarchy, no DI container.

### Supported combinations

| # | Metadata | Vector | Use case |
|---|---|---|---|
| 0 | SQLite (default) | BruteForce (default) | Local-first, zero setup |
| 1 | SQLite | HNSW | Local-first, faster vector search at scale |
| 2 | SQLite | Qdrant | Local relational, external vector search |
| 3 | PostgreSQL | Qdrant | Team / cloud relational, external vector search |
| 4 | PostgreSQL | pgvector | Fully external — team / cloud deployment |

`PostgreSQL + BruteForce` and `PostgreSQL + HNSW` were in the original table
and **removed by Amendment 2** (see constraint A12 below).

## Key constraints

- **A12 (postgres requires externalised vectors).** `brute_force` and `hnsw`
  are file-backed in `.shaktiman/embeddings.bin`; two daemons sharing a Postgres
  project would race on that file. The validator rejects the combination at
  daemon startup with an actionable error. Only `pgvector` or `qdrant` are
  permitted on the postgres metadata path.
- **pgvector requires postgres metadata.** pgvector tables live in the same
  Postgres as the relational data; `sqlite + pgvector` is invalid.
- **Default is untouched.** `sqlite + brute_force` is the zero-config path.
  Existing configs pinning postgres + brute_force/hnsw are an intentional
  breaking change, not a silent upgrade.
- **Build tags gate backends.** `postgres`, `pgvector`, `qdrant`, `hnsw`,
  `bruteforce`, `sqlite`, `sqlite_fts5` — see
  [Backends](/configuration/backends) for the matrix.

## When to revisit

- Adding a new backend (e.g., a different vector DB): register via the
  provider pattern, update the combinations table, run the compliance tests.
- If an external service offers a metadata+vector pair that wants its own
  provider (e.g., a single-service store), the registry can accommodate it
  without a rewrite.
- If A12 becomes too restrictive in practice — revisit only after a concrete
  user scenario shows it blocks a valid use case.
