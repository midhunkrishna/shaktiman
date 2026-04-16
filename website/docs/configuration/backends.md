---
title: Backends
sidebar_position: 2
---

# Backends

Shaktiman supports multiple metadata and vector backends, selected at build
time (via Go build tags) and at runtime (via TOML / CLI flags / env).

For the performance implications of each combination, see
[Performance → Backend selection](/performance/backend-selection).

## Backend matrix

| Metadata | Vector | Build tags | Allowed? |
|---|---|---|---|
| `sqlite` | `brute_force` | `sqlite_fts5 sqlite bruteforce` | ✓ (default) |
| `sqlite` | `hnsw` | `sqlite_fts5 sqlite hnsw` | ✓ |
| `sqlite` | `qdrant` | `sqlite_fts5 sqlite qdrant` | ✓ |
| `sqlite` | `pgvector` | — | ✗ (pgvector requires postgres metadata) |
| `postgres` | `brute_force` | — | ✗ (A12 — file-based vector races) |
| `postgres` | `hnsw` | — | ✗ (A12 — file-based vector races) |
| `postgres` | `qdrant` | `postgres qdrant` | ✓ |
| `postgres` | `pgvector` | `postgres pgvector` | ✓ |

The `sqlite_fts5` tag is **always required** — it enables SQLite's FTS5
virtual table that keyword search relies on.

The constraint matrix is enforced at startup by `ValidateBackendConfig`
(`internal/types/config.go`); invalid combinations fail fast with a clear
error message.

## Build-tag cheatsheet

| Target | Build command |
|---|---|
| Default (sqlite + bruteforce + hnsw) | `go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktimand ./cmd/shaktimand` |
| Add Qdrant | `go build -tags "sqlite_fts5 sqlite bruteforce hnsw qdrant" -o shaktimand ./cmd/shaktimand` |
| Everything (sqlite + postgres + all vectors) | `go build -tags "sqlite_fts5 sqlite postgres bruteforce hnsw pgvector qdrant" -o shaktimand ./cmd/shaktimand` |
| Postgres-only, pure-Go (no CGo / C compiler) | `go build -tags "postgres pgvector" -o shaktimand ./cmd/shaktimand` |

The postgres-only build drops the CGo dependency entirely — handy for static
binaries in containers.

## Selecting at runtime

TOML (`.shaktiman/shaktiman.toml`):

```toml
[database]
backend = "sqlite"         # or "postgres"

[vector]
backend = "brute_force"    # or "hnsw", "qdrant", "pgvector"

[postgres]
connection_string = "postgres://..."   # required if database.backend = "postgres"

[qdrant]
url = "http://localhost:6334"          # required if vector.backend = "qdrant"
```

CLI flag overrides (for `index` and `reindex` only):

```bash
shaktiman index . --db postgres --vector pgvector
shaktiman index . --vector hnsw
shaktiman index . --qdrant-url http://my-qdrant:6334 --vector qdrant
```

Env var overrides for secrets:

```bash
SHAKTIMAN_POSTGRES_URL=postgres://...
SHAKTIMAN_QDRANT_API_KEY=...
```

Precedence (highest wins): CLI flags → env vars → TOML → defaults.

## Switching backends

Always requires a full reindex — the new backend starts empty and needs
repopulating from source:

```bash
shaktiman reindex /path/to/project --embed --vector hnsw
```

See [Re-indexing](/guides/reindexing) for the two-phase purge (remote
stores first, local files second) and what's preserved.

## See also

- [Performance → Backend selection](/performance/backend-selection) —
  the trade-offs, measurements, and when to pick what.
- [ADR-003 — Pluggable Storage Backends](/design/adr-003-pluggable-backends)
  — the decision record and the A12 constraint in full.
