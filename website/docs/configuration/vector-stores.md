---
title: Vector stores
sidebar_position: 3
---

# Vector stores

Per-backend settings for the four vector stores. For picking which to use,
see [Performance → Backend selection](/performance/backend-selection).

## `brute_force` (default)

In-memory cosine-similarity scan. Persisted to `.shaktiman/embeddings.bin`.

No configuration. Selected by:

```toml
[vector]
backend = "brute_force"
```

Storage: all vectors held in RAM, flushed to `embeddings.bin` periodically.
Search complexity: `O(N)` per query — fine up to ~75k chunks.

## `hnsw`

Hierarchical Navigable Small World approximate nearest neighbours.
Persisted to a disk-backed HNSW index file.

No configuration today beyond selection:

```toml
[vector]
backend = "hnsw"
```

HNSW-specific parameters (`M`, `efConstruction`, `ef`) are set to reasonable
defaults inside the store; they're not exposed via TOML. If you need to tune
them, you'll need to edit the source and rebuild.

Storage: on-disk index file; hot subset held in memory. Search complexity:
`O(log N)` per query with approximate recall (typically 95%+ at default
parameters).

## `qdrant`

Remote Qdrant service. Vectors live on Qdrant; Shaktiman holds no vector
state locally.

```toml
[vector]
backend = "qdrant"

[qdrant]
url = "http://localhost:6334"       # required
collection = "shaktiman"            # default; set per-project for isolation
api_key = ""                        # optional; prefer SHAKTIMAN_QDRANT_API_KEY env
```

**Isolation.** Each project's chunks are tagged with a `project_id` payload.
Multiple projects can share one collection, though for clarity separate
collections per project is cleaner.

**Connectivity.** Shaktiman starts up even if Qdrant is unreachable — queries
fail with a clear error. Embedding worker circuit-breaker also kicks in.

## `pgvector`

Vectors stored in Postgres via the [pgvector](https://github.com/pgvector/pgvector)
extension. Shares the same connection pool as the `postgres` metadata
backend.

```toml
[database]
backend = "postgres"

[postgres]
connection_string = "postgres://..."

[vector]
backend = "pgvector"
```

**Hard requirement:** `[database].backend = "postgres"` — `pgvector` refuses
to coexist with SQLite metadata. The validator rejects the combination at
startup.

**Extension install.** The `vector` extension must be enabled on the
Postgres server:

```sql
CREATE EXTENSION vector;
```

Most managed services (RDS, Neon, Supabase) expose this as a one-click
toggle.

## Changing backends mid-project

Any change to `[vector].backend` requires `shaktiman reindex --embed --vector
<new>`. The existing vectors aren't converted — the new backend starts
empty and is repopulated from source.

## Dimension compatibility

Every vector backend stores vectors at the fixed dimensionality set by
`[embedding].dims`. Changing dimensions (because you changed model) requires
reindex — incompatible vectors can't be converted.

## See also

- [Performance → Backend selection](/performance/backend-selection) —
  latency / throughput / memory trade-offs.
