---
title: Backends
sidebar_position: 2
---

# Backends

Shaktiman supports multiple metadata and vector backends, selected at build time via
Go build tags and at runtime via `[database]` / `[vector]` in `shaktiman.toml`.

:::note Placeholder

The per-backend trade-off discussion lands in Step 5b. For now:

- **Metadata** — `sqlite` (default) or `postgres`.
- **Vector** — `brute_force` (default), `hnsw`, `qdrant`, `pgvector`.
- `postgres` rejects `brute_force` and `hnsw` — see
  [Known Limitations](/reference/limitations).
- Full TOML schema: [Config File](/configuration/config-file).

:::
