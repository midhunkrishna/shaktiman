---
title: Backend selection
sidebar_position: 2
---

# Backend selection

Two independent choices — metadata backend and vector backend — define the
floor for every other performance property. Pick these first.

## Metadata backends

| Backend | Query latency | Write throughput | Memory | Disk | Multi-host |
|---|---|---|---|---|---|
| **`sqlite`** (default) | Very low (in-process) | Moderate (single writer) | Low (~24 MB page cache) | 100–500 MB for typical repos | No — file-local |
| **`postgres`** | Low (network RTT) | High | None locally | Remote | Yes |

**Pick `sqlite` when:** one developer, one machine, local-first. This is the
intended default and almost always right.

**Pick `postgres` when:** shared team index, CI infrastructure, cross-host
deployment. Mandatory constraint: postgres forces the vector backend to be
`pgvector` or `qdrant` — see
[A12 in ADR-003](/design/adr-003-pluggable-backends).

Measurement: `shaktiman status` shows disk footprint; `time shaktiman search`
shows query latency.

## Vector backends

| Backend | Search latency @ 10k | @ 100k | @ 500k | Memory | Disk |
|---|---|---|---|---|---|
| **`brute_force`** (default) | <20 ms | ~30 ms | >100 ms | All vectors in RAM | One file |
| **`hnsw`** | <10 ms | ~10 ms | ~15 ms | Hot subset | One file (larger than brute_force) |
| **`pgvector`** | Network RTT + ~10 ms | + ~15 ms | + ~20 ms | None locally | In Postgres |
| **`qdrant`** | Network RTT + ~5 ms | + ~8 ms | + ~10 ms | None locally | Remote |

Numbers are indicative. Your mileage depends on embedding dimensionality
(768 assumed), network (for remote backends), and top-K.

**Pick `brute_force` when:** repo has under ~75k chunks (most repos), and
you don't mind the full vector set in RAM. Simplest, no surprises.

**Pick `hnsw` when:** repo is large (>100k chunks), you're on a single
machine, and you want sub-linear search time. Cost: approximate recall
instead of exact.

**Pick `pgvector` when:** you've already chosen `postgres` metadata and want
a single store.

**Pick `qdrant` when:** you need the best vector-search performance and are
willing to run a separate service (or use Qdrant Cloud).

## Trade-off table

| Choice | Pros | Cons | Recommended for |
|---|---|---|---|
| `sqlite` + `brute_force` | Zero setup, all local | Cold start on every process open (~10–30 ms) | Default. Works up to ~75k chunks. |
| `sqlite` + `hnsw` | Sub-linear search | Index file can corrupt on unclean shutdown | Large local repos. |
| `postgres` + `pgvector` | Single remote store | pgvector extension must be installed | Team deployments where latency tolerance is ~20 ms. |
| `postgres` + `qdrant` | Best vector performance | Two remote services to operate | Largest deployments, quality-sensitive retrieval. |

## Switching backends

Changing backends requires a reindex:

```bash
# Change [vector].backend in shaktiman.toml (or use --vector flag)
shaktiman reindex /path/to/project --embed --vector hnsw
```

See [Re-indexing](/guides/reindexing) for the full flow.

## See also

- [Configuration → Backends](/configuration/backends) — the TOML schema.
- [ADR-003 — Pluggable Storage Backends](/design/adr-003-pluggable-backends)
  — why four vector backends exist and the A12 constraint.
- [Scaling](./scaling) — when to move from local to externalised backends.
