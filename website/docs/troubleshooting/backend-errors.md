---
title: Backend errors
sidebar_position: 6
---

# Backend errors

Covers Postgres, Qdrant, pgvector, and HNSW connection / corruption issues.

## Symptom: `database.backend "postgres" requires postgres.connection_string`

### Cause

The config says `[database].backend = "postgres"` but no connection string is
configured. `ValidateBackendConfig` rejects the combination before the daemon
opens.

### Fix

Set one of:

- `[postgres].connection_string` in `.shaktiman/shaktiman.toml`.
- Env var `SHAKTIMAN_POSTGRES_URL`.
- CLI flag `--postgres-url` (for `index` / `reindex`).

Format: `postgres://user:pass@host:port/dbname?sslmode=...`.

## Symptom: `database.backend "postgres" is incompatible with vector.backend "brute_force"`

### Cause

Same validator rejects `postgres + brute_force` or `postgres + hnsw`. Both local
vector stores persist to `.shaktiman/embeddings.bin`; when multiple `shaktimand`
processes share a Postgres database, they race on that file. See
[ADR-003 A12](/design/adr-003-pluggable-backends) and
[Known Limitations](/reference/limitations#postgres-requires-pgvector-or-qdrant).

### Fix

Switch `[vector].backend` to `pgvector` (vectors in the same Postgres) or
`qdrant` (externalised). Remember to `reindex` if you already have embeddings in
the old backend.

## Symptom: pgvector extension missing

Error like `pgvector: extension "vector" is not available`.

### Fix

Install the pgvector extension on your Postgres server:

```sql
CREATE EXTENSION vector;
```

On managed services (RDS, Neon, Supabase) it's usually a one-click toggle. Check
your provider's docs.

## Symptom: Qdrant collection not found

Error like `qdrant: collection "shaktiman" does not exist`.

### Cause

The first index run normally creates the collection. If you see this error,
either the daemon lost permission mid-way, or the collection was deleted
externally.

### Fix

```bash
# Let a fresh reindex recreate the collection
shaktiman reindex /path/to/project --vector qdrant
```

If you set `[qdrant].collection` to a name you don't own, point it at one you do.

## Symptom: HNSW index file is corrupted

Error during load: `hnsw: failed to read index` or similar.

### Cause

HNSW persists to a disk file. An unclean shutdown (SIGKILL, power loss) during
a periodic save can leave the file partially written.

### Fix

`reindex` refuses to run while a `shaktimand` daemon holds the project lock,
so stop the daemon first (close the MCP client, or `kill "$(cat
.shaktiman/daemon.pid)"`). See
[Re-indexing → Daemon must be stopped first](/guides/reindexing#daemon-must-be-stopped-first).

```bash
# Delete the HNSW file and reindex vectors
rm /path/to/project/.shaktiman/embeddings.bin    # brute_force
rm /path/to/project/.shaktiman/hnsw.bin          # HNSW (if present)
shaktiman reindex /path/to/project --embed
```

Metadata in `index.db` is SQLite-WAL and crash-safe; you don't lose parse /
symbol / edge data, only the vectors.

## Symptom: `SQLITE_BUSY` in the log

### Likely causes

1. You ran `shaktiman index` while `shaktimand` was alive (shouldn't happen —
   the CLI refuses — but if somehow it did, both writers collide).
2. An external tool has `index.db` open (`sqlite3` CLI, a GUI DB browser).

### Fix

- Stop the external reader, or close the other writer.
- If it persists, check `ls /proc/<pid>/fd/` (Linux) or `lsof index.db`
  (macOS / Linux) for leftover handles.

## Symptom: `pgvector` / `qdrant` returns dimension-mismatch errors

### Cause

You changed `[embedding].model` (which changed `dims`) but didn't reindex. The
remote store still has vectors with the old dimensionality.

### Fix

`shaktiman reindex --embed` purges remote store contents and rebuilds with the
new dimensions. Stop the daemon first — `reindex` won't run while the project
lock is held.

If your backends differ from the daemon defaults, pass them explicitly so
`reindex` talks to the same store that errored:

```bash
shaktiman reindex /path/to/project --embed \
  --db postgres --vector pgvector \
  --postgres-url "$SHAKTIMAN_POSTGRES_URL"
```

See [`reindex` flags](/reference/cli#shaktiman-reindex-project-root) for the full
list.

## See also

- [Configuration → Backends](/configuration/backends) — which combinations are
  valid.
- [Known Limitations → Backend combinations](/reference/limitations#backend-combinations).
- [Guides → Re-indexing](/guides/reindexing) — the two-phase purge that handles
  both remote and local state.
