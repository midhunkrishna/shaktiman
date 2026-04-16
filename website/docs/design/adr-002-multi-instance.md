---
title: ADR-002 — Multi-Instance Concurrency
sidebar_position: 3
---

# ADR-002: Multi-Instance Concurrency Support for Shaktiman

**Status:** AMENDED — see Amendment 4 (2026-04-09). The original plan (D2–D5, D11–D14, D15) is **SUPERSEDED**. D1 survives as D1″: single-daemon + socket proxy.
**Date:** 2026-03-31
**Deciders:** Shaktiman maintainers

> **Status (Today, 2026-04-16):** **SHIPPED** as the single-daemon + socket-proxy
> variant. `flock` acquisition on `.shaktiman/daemon.pid` in `internal/lockfile/`;
> proxy bridge in `internal/proxy/` (see `cmd/shaktimand/main.go`); leader re-exec on
> promotion. The superseded sections (D2–D5, D11–D14, D15) describe paths that were
> evaluated and rejected.

---

## Context

Shaktiman runs as a per-project daemon (`shaktimand`) that indexes code, maintains an SQLite database at `.shaktiman/index.db`, and serves MCP queries over stdio. Two concurrency scenarios are unsupported today:

**Scenario A: Same directory, multiple instances.** A developer runs two or more Claude Code sessions against the same project directory. Each spawns its own `shaktimand`. Today, both open `index.db` with `MaxOpenConns=1` writer connections (`internal/storage/db.go:73`), leading to `SQLITE_BUSY` contention on writes and potential database corruption if WAL checkpointing races.

**Scenario B: Multiple git worktrees.** A developer uses `git worktree` to work on multiple branches simultaneously. Each worktree is a separate directory with its own `.shaktiman/index.db`. Today, each daemon independently cold-indexes the full project (~140K chunks, ~25K files), wasting disk (4x duplication) and CPU (4x enrichment).

### Scale targets

- 140K+ chunks, ~25K+ files per project
- 2-5 concurrent Claude Code sessions (same directory or across worktrees)
- Index database: ~200-500MB at scale

### Current architecture constraints

- `WriterManager` (`internal/daemon/writer.go:22`) serializes all writes through a single goroutine with `MaxOpenConns=1`
- Reader pool has 4 connections (`internal/storage/db.go:109`) via WAL mode
- Schema version 4 (`internal/storage/schema.go:9`) with tables: `files`, `chunks`, `symbols`, `edges`, `pending_edges`, `diff_log`, `diff_symbols`
- FTS5 external content table `chunks_fts` with insert/delete/update triggers (`internal/storage/schema.go:207-224`)
- `MetadataStore`, `BatchMetadataStore`, `VectorStore` interfaces (`internal/types/interfaces.go`) must remain unchanged
- Recursive CTE graph traversal in `Neighbors()` (`internal/storage/graph.go:148-201`) references `edges` table directly

---

## Decision

We adopt a layered concurrency strategy that handles both scenarios through DB-layer isolation, requiring zero changes to the `MetadataStore`, `BatchMetadataStore`, `VectorStore`, or `QueryEngine` interfaces. All scoping is transparent to the application layer.

### D1: Same directory, multiple instances -- flock-based leader election

Use `syscall.Flock` on `.shaktiman/index.db` at daemon startup.

- **Leader daemon** (acquires lock): runs normally -- read + write + enrichment + file watcher.
- **Follower daemons** (lock unavailable): run in read-only mode -- MCP query tools work (reader pool only), no enrichment pipeline, no `WriterManager`, no file watcher.
- Changes made by any Claude Code session are detected by the leader's `fsnotify` watcher (`internal/daemon/watcher.go`), which triggers incremental enrichment.
- On leader exit, followers can attempt to promote by retrying `flock`. Alternatively, a restart re-elects.

**Mechanism:**

```go
// In daemon.New(), before storage.Open():
lockPath := filepath.Join(cfg.ProjectRoot, ".shaktiman", "index.db.lock")
lockFD, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0644)
if err != nil {
    return nil, fmt.Errorf("open lock file: %w", err)
}
err = syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB)
isLeader := err == nil
// If !isLeader: skip WriterManager, watcher, enrichment pipeline
// Open DB with read-only writer (or nil writer)
```

**Follower behavior:**

- `storage.Open` still opens the reader pool (WAL allows concurrent readers).
- `WriterManager` is not started. `Submit()` returns `ErrWriterClosed`.
- `EnrichmentPipeline`, `ScanRepo`, and `Watcher` are not started.
- MCP server starts normally; all read-path tools (search, symbols, dependencies, context, diff, summary) work.
- Periodic retry of `flock` (every 30s) enables promotion if the leader exits.

### D2: Multiple worktrees (SQLite) -- UNION ALL overlay pattern

For worktree scenarios, each worktree maintains its own `.shaktiman/` directory with a lightweight overlay database. The overlay stores only worktree-specific changes; all other data is read from a shared base.

**Base table rename + overlay tables + merged views:**

```sql
-- Migration renames existing tables (one-time, schema version 5)
ALTER TABLE files RENAME TO _base_files;
ALTER TABLE chunks RENAME TO _base_chunks;
ALTER TABLE symbols RENAME TO _base_symbols;
ALTER TABLE edges RENAME TO _base_edges;

-- Overlay tables (identical schema, worktree-local changes only)
CREATE TABLE _overlay_files   (...same columns as _base_files...);
CREATE TABLE _overlay_chunks  (...same columns as _base_chunks...);
CREATE TABLE _overlay_symbols (...same columns as _base_symbols...);
CREATE TABLE _overlay_edges   (...same columns as _base_edges...);

-- Merged views with overlay priority (overlay wins on path conflict)
CREATE VIEW files AS
    SELECT * FROM _overlay_files
    UNION ALL
    SELECT b.* FROM _base_files b
    WHERE b.path NOT IN (SELECT path FROM _overlay_files);

CREATE VIEW chunks AS
    SELECT * FROM _overlay_chunks
    UNION ALL
    SELECT b.* FROM _base_chunks b
    WHERE b.file_id NOT IN (SELECT id FROM _overlay_files);

CREATE VIEW symbols AS
    SELECT * FROM _overlay_symbols
    UNION ALL
    SELECT b.* FROM _base_symbols b
    WHERE b.file_id NOT IN (SELECT id FROM _overlay_files);

CREATE VIEW edges AS
    SELECT * FROM _overlay_edges
    UNION ALL
    SELECT b.* FROM _base_edges b
    WHERE b.src_symbol_id NOT IN (
        SELECT id FROM _overlay_symbols
    );
```

**Key properties:**

- Views have the original table names (`files`, `chunks`, `symbols`, `edges`) so all existing queries work unchanged.
- `Neighbors()` recursive CTEs (`internal/storage/graph.go:158-166`) operate on `edges` view transparently.
- `KeywordSearch()` FTS5 queries operate on merged content.
- FTS5 supports views as external content tables -- the `content=` parameter can reference a view. This is the critical enabler that was validated during research.

### D3: Read/write separation

- **All reads** go through the merged views. Zero code changes in any read path: `QueryEngine`, `Assembler`, `Ranker`, tool handlers.
- **All writes** target `_overlay_*` tables. Two implementation options:
  - **Option A (preferred): INSTEAD OF triggers on views.** `INSERT INTO files ...` transparently routes to `_overlay_files`. Existing `processEnrichmentJob` (`internal/daemon/writer.go:227`) works unchanged.
  - **Option B: Direct targeting in WriterManager.** Modify `processEnrichmentJob` to write to `_overlay_*` tables directly. Single place to change since `WriterManager` is the only writer.

**INSTEAD OF trigger example:**

```sql
CREATE TRIGGER files_insert INSTEAD OF INSERT ON files
BEGIN
    INSERT INTO _overlay_files (path, content_hash, mtime, size, language,
        indexed_at, embedding_status, parse_quality, is_test)
    VALUES (NEW.path, NEW.content_hash, NEW.mtime, NEW.size, NEW.language,
        NEW.indexed_at, NEW.embedding_status, NEW.parse_quality, NEW.is_test);
END;

CREATE TRIGGER files_delete INSTEAD OF DELETE ON files
BEGIN
    -- Remove from overlay if present
    DELETE FROM _overlay_files WHERE path = OLD.path;
    -- Shadow base record: insert a tombstone or rely on overlay-only semantics
END;
```

### D4: Multiple worktrees (Postgres/Qdrant) -- file_versions + RLS

For the future Postgres backend, worktree scoping uses a different mechanism that leverages PostgreSQL-native features.

**Schema addition:**

```sql
CREATE TABLE worktrees (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    path        TEXT NOT NULL,
    label       TEXT,          -- e.g. "feature/auth"
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_trunk    BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE file_versions (
    worktree_id UUID NOT NULL REFERENCES worktrees(id),
    path        TEXT NOT NULL,
    file_id     BIGINT NOT NULL REFERENCES files(id),
    PRIMARY KEY (worktree_id, path)
);

-- Content-addressed files: same path + same content = shared row
-- files keyed by (path, content_hash)
CREATE UNIQUE INDEX idx_files_content_addr ON files(path, content_hash);
```

**RLS policies for transparent scoping:**

```sql
ALTER TABLE file_versions ENABLE ROW LEVEL SECURITY;

CREATE POLICY worktree_scope ON file_versions
    USING (worktree_id = current_setting('app.worktree_id')::uuid);

-- Scope set once per connection
SET app.worktree_id = '<worktree-uuid>';
```

**Content-addressed dedup:** `files` keyed by `(path, content_hash)`. If two worktrees have the same file with the same content, they share the same `files` row (and its chunks/symbols/edges). At 140K chunks across 25K files, typical branch divergence is <1% of files. 99.96% of data is shared.

**Worktree fork:** Creating a new worktree from trunk is one `INSERT...SELECT` of `file_versions` rows (~25K rows, completes in seconds).

### D5: Scoped edge resolution

**SQLite:** Views handle scoping transparently. The recursive CTEs in `Neighbors()` operate on the `edges` view, which already merges overlay + base with overlay priority. When a file is re-indexed in a worktree:
1. New `file_id` in `_overlay_files` generates new chunk IDs, symbol IDs, edge IDs in the overlay.
2. Old version's edges in `_base_edges` remain intact for the trunk worktree.
3. Overlay view priority ensures the worktree sees its own edges.

**Postgres:** Scope-filter JOIN via `file_versions`:

```sql
-- All graph queries implicitly scoped by RLS on file_versions
SELECT e.* FROM edges e
JOIN symbols s ON e.src_symbol_id = s.id
JOIN file_versions fv ON s.file_id = fv.file_id
-- fv automatically filtered by RLS policy
```

**Performance:** ~15-30% overhead on graph traversal from scoping JOINs/view UNION. Acceptable given current graph queries complete in <10ms.

### D6: Worktree identity

Each worktree generates a stable identity on first daemon start:

```
.shaktiman/worktree.id  -- contains a UUID v4
```

- Generated by daemon on first start if absent.
- Stable across restarts (persisted to file).
- Unique per worktree directory.
- Used as the `worktree_id` in Postgres `file_versions` and as an identifier in SQLite overlay semantics.
- Combined with filesystem path for human-readable identification.

### D7: Content-hash dedup for multi-daemon writes

The existing content hash guard in `processEnrichmentJob` (`internal/daemon/writer.go:233-249`) prevents redundant enrichment:

```go
// Content hash guard (CA-3): skip if already indexed with same hash
if job.ContentHash != "" {
    var currentHash string
    err := tx.QueryRowContext(ctx, "SELECT content_hash FROM files WHERE path = ?",
        job.FilePath).Scan(&currentHash)
    if err == nil && currentHash == job.ContentHash {
        return nil, nil  // skip -- already indexed
    }
}
```

For multi-daemon scenarios, this guard extends naturally:
- If the incoming `content_hash` matches what is already in the overlay (or base), skip enrichment entirely.
- Multiple daemons enriching the same file race to write; the first completes and subsequent ones skip via hash match.
- For Postgres with content-addressed `files(path, content_hash)`: INSERT ON CONFLICT DO NOTHING handles races at the DB level.

### D8: Stale worktree cleanup (Postgres)

**Registry and heartbeat:**

```sql
-- worktrees.last_seen updated by daemon every 60 seconds
UPDATE worktrees SET last_seen = now() WHERE id = current_setting('app.worktree_id')::uuid;
```

**Cleanup protocol (any surviving daemon):**

1. Find stale worktrees: `SELECT id FROM worktrees WHERE last_seen < now() - interval '1 hour' AND NOT is_trunk`.
2. Delete `file_versions` for stale worktrees.
3. Find orphaned files: `SELECT f.id FROM files f LEFT JOIN file_versions fv ON f.id = fv.file_id WHERE fv.file_id IS NULL`.
4. Cascade delete: orphaned files -> chunks -> symbols -> edges.
5. Clean up Qdrant embeddings for deleted chunks.

**Trunk protection:** Trunk worktree has `is_trunk = true` with extended TTL (24h), preventing accidental GC.

**SQLite:** Not needed. Deleting a worktree directory deletes `.shaktiman/` with it. No cross-worktree references exist in the SQLite model.

### D9: DB alternatives considered (research result)

SQLite with UNION ALL views was selected after evaluating:

| Alternative | Verdict | Reason |
|---|---|---|
| DuckDB | Rejected | OLAP engine: 7-10x slower on OLTP workloads; FTS does not auto-update; single-process writes only |
| libSQL / Turso | Rejected | Same ATTACH DATABASE limitations; less mature Go driver (`go-libsql`); no meaningful advantage for this use case |
| Embedded Postgres | Rejected | Not truly embedded -- spawns a server process; operational complexity for a local-first tool |
| KV stores (bbolt, badger) | Rejected | Would require building SQL, FTS, and graph traversal from scratch; net negative |
| SQLite ATTACH DATABASE | Rejected | Attached databases cannot share FTS5 virtual tables; triggers do not fire across attached schemas |

**Critical enabler:** FTS5 explicitly supports views as `content=` sources for external content tables. This was validated and is documented in the [SQLite FTS5 documentation](https://www.sqlite.org/fts5.html#external_content_tables). Without this, the UNION ALL overlay pattern would not work.

### D10: Interface transparency

No interface changes are required:

| Interface | Status | Scoping mechanism |
|---|---|---|
| `MetadataStore` | UNCHANGED | SQLite: views at `db.Open()` time. Postgres: RLS + session variable. |
| `BatchMetadataStore` | UNCHANGED | Same as above. |
| `VectorStore` | UNCHANGED | Chunk IDs are globally unique; scoping is pre-applied by DB queries that feed chunk IDs to vector search. |
| `QueryEngine` | UNCHANGED | Consumes `MetadataStore` interface; no awareness of scoping. |
| Tool handlers | UNCHANGED | Consume `QueryEngine`; no awareness of scoping. |

---

## Alternatives Considered

### A1: Same-directory concurrency

| Alternative | Steelman | Why not chosen |
|---|---|---|
| **PID file** | Simple, well-understood Unix pattern. Easy to implement. | Stale PID files after crashes require extra cleanup logic. Does not support follower promotion. `flock` is strictly superior -- automatically released on process exit. |
| **Unix domain socket** | Enables IPC between instances; followers could proxy writes to leader. | Significant complexity (socket server, protocol, error handling). Over-engineered for read-only followers. Could be added later as an enhancement. |
| **"Just let SQLite handle it"** (WAL + busy_timeout) | SQLite WAL mode does allow concurrent readers + one writer. `_busy_timeout=5000` is already set. | Multiple writer processes cause unpredictable `SQLITE_BUSY` errors under load. WAL checkpointing races between processes. No isolation of enrichment workload. The flock approach is deterministic. |
| **Advisory lock on DB file itself** | Avoid extra lock file. | Interferes with SQLite's own locking protocol. Documented as unsafe in SQLite docs. |

### A2: Worktree overlay mechanism

| Alternative | Steelman | Why not chosen |
|---|---|---|
| **ATTACH DATABASE** (separate overlay DB file) | Clean physical separation. Well-known SQLite pattern for multi-database access. | FTS5 virtual tables cannot span attached databases. Triggers do not fire across schemas. Would require duplicating FTS5 per worktree (140K+ rows each). |
| **Application-level routing** (if/else in every query) | No schema change needed. Could be done incrementally. | Violates the "zero interface changes" goal. Every query method in `MetadataStore` (20+ methods) and `BatchMetadataStore` (4 methods) would need routing logic. Maintenance burden is proportional to query count. |
| **DuckDB** | Superior analytical query performance. Native Parquet support for sharing base data. | OLAP engine: 7-10x slower on point lookups and small transactions that dominate Shaktiman's workload. FTS does not auto-update on INSERT. Single-process writer limitation is worse than SQLite's WAL mode. |
| **Schema-per-worktree** (SQLite: separate DB file per worktree, copy-on-create) | Complete isolation. Simple mental model. | Full duplication of 200-500MB per worktree. Cold index time (minutes) on worktree creation. No data sharing. |

### A3: Postgres scoping mechanism

| Alternative | Steelman | Why not chosen |
|---|---|---|
| **JOINs everywhere** (explicit file_versions JOIN in every query) | Transparent to DB config. No RLS dependency. Works on older Postgres versions. | Requires modifying every query in the Postgres storage implementation. ~30 queries need JOIN additions. Ongoing maintenance cost as queries evolve. |
| **Schema-per-tenant** (one Postgres schema per worktree) | Complete isolation. Simple queries. No RLS complexity. | 200-500MB duplication per worktree. `CREATE SCHEMA` + full data copy on worktree creation. Cross-schema queries for shared data are complex. |
| **Partition tags** (worktree_id column on every table, partition by it) | Native Postgres partitioning. Good for range scans. | Requires adding `worktree_id` to every table's primary key. Massive schema change. Partition pruning does not help with point lookups. Over-engineered for 2-5 worktrees. |

### A4: Stale worktree cleanup

| Alternative | Steelman | Why not chosen |
|---|---|---|
| **`git worktree list` reconciliation** | Uses ground truth (git knows which worktrees exist). No heartbeat overhead. | Requires git binary on PATH. Parsing `git worktree list` output is fragile. Does not handle non-git worktrees (if ever supported). The daemon already runs continuously, so heartbeat is nearly free. |
| **Reference counting** (track active connections per worktree) | Precise -- clean up exactly when last connection drops. | Crash leaves stale references. Requires distributed coordination for multi-process cleanup. Heartbeat + TTL is simpler and crash-safe. |

---

## Consequences

### Positive

1. **Multiple Claude Code sessions work concurrently** on the same project without `SQLITE_BUSY` errors or data corruption.
2. **Worktree branches share 99.96% of indexed data**, reducing disk usage from O(W * N) to O(N + W * delta) where W is worktree count and delta is typically <1% of files.
3. **Zero interface changes** means zero risk to existing MCP tool behavior, QueryEngine, Assembler, or Ranker.
4. **Follower daemons are useful immediately** -- all read-path MCP tools work, which is the primary use case (search, symbols, dependencies, context).
5. **FTS5 works transparently** through views, preserving full-text search quality and performance.
6. **Graph traversal works transparently** -- recursive CTEs operate on views without modification.
7. **Content-hash dedup** prevents redundant enrichment across daemons and worktrees.
8. **Postgres path** (D4) enables future cloud/team deployment with the same interface contract.
9. **Schema migration is reversible** -- views can be dropped and tables renamed back.

### Negative

1. **View overhead on reads.** UNION ALL views with NOT IN subqueries add overhead to every read query. Estimated 15-30% on graph traversal, less on simple lookups. Mitigated by: small overlay tables (typically <1% of rows), SQLite query planner optimizing NOT IN to index lookups.
2. **FTS5 rebuild complexity.** When overlay changes, FTS5 external content may need rebuild to reflect merged state. Mitigated by: FTS5 `rebuild` command is already implemented (`internal/storage/fts.go:89-96`).
3. **Schema migration is non-trivial.** Renaming base tables and creating views requires careful ordering within a transaction. Existing data must be preserved. FTS5 triggers must be recreated to target the correct tables.
4. **Follower daemons have stale reads** until the leader processes changes. Latency depends on watcher debounce (200ms default) + enrichment time. Acceptable for the search/context use case.
5. **flock is Unix-only.** Windows support would require a different locking mechanism (`LockFileEx`). Acceptable for current target platforms (macOS, Linux).
6. **Overlay table IDs may collide with base table IDs** unless AUTOINCREMENT ranges are coordinated. Mitigated by: using `INTEGER PRIMARY KEY AUTOINCREMENT` with different starting ranges, or accepting that overlay IDs are in a separate namespace and views handle dedup by path.
7. **INSTEAD OF triggers add write-path complexity.** Debugging write failures requires understanding the trigger chain. Mitigated by: triggers are simple INSERT/DELETE routing; `WriterManager` is the single writer.

---

## Pre-Mortem

*"It is 6 months from now and this design has failed. Why?"*

### PM-1: View performance degrades at scale

The UNION ALL views with NOT IN subqueries become the bottleneck when the overlay grows large (e.g., a long-lived feature branch with thousands of changed files). The NOT IN subquery on `_overlay_files.path` does a full scan of the overlay table for every base table row.

**Mitigation:** Monitor overlay table size. Add `EXPLAIN QUERY PLAN` tests to CI. If overlay exceeds 5% of base, consider periodic merge (copy overlay into base, clear overlay). The `NOT IN (SELECT path ...)` can be rewritten as `LEFT JOIN ... WHERE overlay.path IS NULL` if the planner prefers it.

### PM-2: FTS5 index diverges from merged view

After overlay writes, the FTS5 index reflects only the overlay state but MATCH queries should return results from both base and overlay. If FTS5 rebuild is not triggered correctly, search returns stale or incomplete results.

**Mitigation:** FTS5 with `content=` (external content) performs a content lookup on the referenced table/view at query time for snippet generation, but the index itself must be kept in sync via triggers or explicit rebuild. The existing trigger mechanism (`chunks_fts_insert/delete/update` in `schema.go:207-224`) must be adapted to fire on the merged view or on both base and overlay tables.

### PM-3: flock not released on unclean exit

If the leader daemon is killed with SIGKILL (not SIGTERM), the kernel releases the flock automatically (this is a property of `flock(2)`). However, if the process hangs (deadlock, infinite loop), the lock is held indefinitely.

**Mitigation:** Followers check leader liveness by inspecting the lock file's PID (written at lock acquisition time). If the PID is not running, followers can forcibly remove and reacquire the lock. Watchdog timeout: if a follower cannot promote after 5 minutes, log a warning.

### PM-4: Worktree identity file lost or duplicated

If `.shaktiman/worktree.id` is accidentally deleted, the daemon generates a new UUID, creating an orphaned worktree in the Postgres registry. If the file is copied (e.g., `cp -r` of the worktree), two worktrees share an ID.

**Mitigation:** For Postgres: heartbeat reconciliation detects orphans via `last_seen` TTL. For SQLite: worktree.id is advisory only (overlay is local to the directory). Add a check: if two daemons report the same worktree.id from different paths, log an error and regenerate.

### PM-5: Schema migration breaks existing installations

Users on schema version 4 upgrade to version 5. The migration renames tables, creates views, and recreates triggers. If the migration fails midway (power loss, disk full), the database is in an inconsistent state.

**Mitigation:** The entire migration runs within `db.WithWriteTx()` which is an atomic transaction. SQLite guarantees rollback on failure. Add a pre-migration backup: copy `index.db` to `index.db.v4.bak` before migration.

---

## FMEA (Failure Mode and Effects Analysis)

Risk scores: Severity (1-10) * Occurrence (1-10) * Detection (1-10) = RPN

| # | Failure Mode | Effect | Sev | Occ | Det | RPN | Mitigation |
|---|---|---|---|---|---|---|---|
| F1 | Overlay table grows unbounded (long-lived branch) | View queries slow down; FTS5 rebuild takes longer | 6 | 3 | 3 | 54 | Monitor overlay row count; periodic merge tool; alert at >5% of base |
| F2 | FTS5 index out of sync after overlay write | Search returns stale results or misses new content | 7 | 4 | 4 | 112 | Startup staleness check (`IsFTSStale` in `fts.go:100`); rebuild on mismatch; CI tests for FTS consistency |
| F3 | Leader daemon hangs holding flock | All followers remain read-only indefinitely | 5 | 2 | 5 | 50 | PID-based liveness check; follower promotion timeout; health endpoint |
| F4 | INSTEAD OF trigger bug routes write to wrong table | Data written to base instead of overlay (or lost) | 8 | 2 | 3 | 48 | Integration tests for trigger routing; SQLite `EXPLAIN` verification; manual review of trigger DDL |
| F5 | Schema migration fails midway | Database unusable; daemon cannot start | 9 | 2 | 2 | 36 | Atomic transaction; pre-migration backup; version check on startup |
| F6 | ID collision between overlay and base tables | JOIN/dedup logic produces incorrect results | 7 | 3 | 4 | 84 | Use AUTOINCREMENT with offset (overlay starts at 10B); or use path-based dedup in views |
| F7 | Concurrent flock attempts on NFS/network filesystem | flock semantics are unreliable on NFS | 6 | 2 | 6 | 72 | Document: local filesystem only; detect NFS mount and warn; fall back to PID file |
| F8 | Postgres RLS policy bypassed (missing SET session var) | Query returns data from all worktrees | 7 | 2 | 3 | 42 | Connection pool hook sets variable on checkout; integration test verifying RLS; FORCE ROW LEVEL SECURITY on role |
| F9 | Worktree cleanup deletes active worktree data | Data loss for a running daemon | 9 | 1 | 3 | 27 | Heartbeat TTL (1h) with generous margin; trunk protection (`is_trunk`); dry-run mode for cleanup |
| F10 | Vector store chunk IDs reference overlay IDs not visible to other worktrees | Vector search returns results that fail hydration | 5 | 3 | 4 | 60 | Vector search results are filtered through view-based hydration; missing chunks are silently dropped |

**Top risks by RPN:** F2 (FTS sync, 112), F6 (ID collision, 84), F7 (NFS flock, 72), F10 (vector/overlay ID mismatch, 60), F1 (overlay growth, 54).

---

## Phasing

### Phase 1: Same-directory multi-instance (D1)

**Scope:** flock-based leader/follower mode.
**Files changed:** `internal/daemon/daemon.go` (lock acquisition, conditional startup), new `internal/daemon/lock.go`.
**Effort:** ~200 LOC. No schema change. No migration.
**Risk:** Low. Additive behavior -- existing single-instance mode is the leader path.
**Ship criterion:** Two `shaktimand` processes on the same directory; leader indexes, follower serves queries without errors.

### Phase 2: Overlay schema migration (D2, D3)

**Scope:** Schema version 4 -> 5 migration. Table renames, overlay tables, merged views, INSTEAD OF triggers, FTS5 reconfiguration.
**Files changed:** `internal/storage/schema.go` (migration V4->V5), `internal/storage/db.go` (view creation at Open time), `internal/storage/fts.go` (trigger targets).
**Effort:** ~400 LOC migration + ~200 LOC tests.
**Risk:** Medium. Schema migration is the highest-risk change. Pre-migration backup mitigates.
**Ship criterion:** Existing index.db migrates successfully; all existing tests pass with views instead of tables; overlay writes are isolated.
**Dependency:** None (Phase 1 is independent).

### Phase 3: Worktree identity and overlay usage (D6, D7)

**Scope:** Worktree ID generation, overlay-aware `WriterManager`, content-hash dedup across overlay/base.
**Files changed:** `internal/daemon/daemon.go` (worktree ID), `internal/daemon/writer.go` (overlay targeting), new `internal/daemon/worktree.go`.
**Effort:** ~300 LOC.
**Risk:** Low-medium. Content-hash guard already exists; extending it to overlay is straightforward.
**Ship criterion:** Two worktrees sharing a base index; changes in one worktree are isolated; base data is shared.
**Dependency:** Phase 2 (overlay schema must exist).

### Phase 4: Postgres + RLS (D4, D5, D8)

**Scope:** Postgres storage backend with file_versions, RLS, heartbeat, cleanup.
**Files changed:** New `internal/storage/postgres/` package implementing `MetadataStore` interface.
**Effort:** ~1000 LOC (new backend).
**Risk:** Medium-high. New backend, new operational requirements (Postgres server).
**Ship criterion:** Full test suite passes against Postgres backend; worktree fork/cleanup works; RLS isolation verified.
**Dependency:** Phase 3 (worktree identity).

---

## Component Design

### Schema migration V4 -> V5

```sql
-- Step 1: Rename base tables
ALTER TABLE files RENAME TO _base_files;
ALTER TABLE chunks RENAME TO _base_chunks;
ALTER TABLE symbols RENAME TO _base_symbols;
ALTER TABLE edges RENAME TO _base_edges;
ALTER TABLE pending_edges RENAME TO _base_pending_edges;

-- Step 2: Create overlay tables (identical schema)
CREATE TABLE _overlay_files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    path             TEXT UNIQUE NOT NULL,
    content_hash     TEXT NOT NULL,
    mtime            REAL NOT NULL,
    size             INTEGER,
    language         TEXT,
    indexed_at       TEXT,
    embedding_status TEXT DEFAULT 'pending'
        CHECK (embedding_status IN ('pending', 'partial', 'complete')),
    parse_quality    TEXT DEFAULT 'full'
        CHECK (parse_quality IN ('full', 'partial', 'error', 'unparseable')),
    is_test          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE _overlay_chunks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id         INTEGER NOT NULL,
    parent_chunk_id INTEGER,
    chunk_index     INTEGER NOT NULL,
    symbol_name     TEXT,
    kind            TEXT NOT NULL
        CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'header', 'block')),
    start_line      INTEGER NOT NULL,
    end_line        INTEGER NOT NULL,
    content         TEXT NOT NULL,
    token_count     INTEGER NOT NULL,
    signature       TEXT,
    parse_quality   TEXT NOT NULL DEFAULT 'full'
        CHECK (parse_quality IN ('full', 'partial')),
    embedded        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE _overlay_symbols (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    chunk_id       INTEGER NOT NULL,
    file_id        INTEGER NOT NULL,
    name           TEXT NOT NULL,
    qualified_name TEXT,
    kind           TEXT NOT NULL
        CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant')),
    line           INTEGER NOT NULL,
    signature      TEXT,
    visibility     TEXT CHECK (visibility IN ('public', 'private', 'internal', 'exported')),
    is_exported    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE _overlay_edges (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id INTEGER NOT NULL,
    dst_symbol_id INTEGER NOT NULL,
    kind          TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    file_id       INTEGER,
    UNIQUE (src_symbol_id, dst_symbol_id, kind)
);

CREATE TABLE _overlay_pending_edges (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    src_symbol_id     INTEGER NOT NULL,
    dst_symbol_name   TEXT NOT NULL,
    dst_qualified_name TEXT DEFAULT '',
    kind              TEXT NOT NULL
        CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
    src_language      TEXT DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Step 3: Create merged views
CREATE VIEW files AS
    SELECT * FROM _overlay_files
    UNION ALL
    SELECT * FROM _base_files
    WHERE path NOT IN (SELECT path FROM _overlay_files);

CREATE VIEW chunks AS
    SELECT * FROM _overlay_chunks
    UNION ALL
    SELECT * FROM _base_chunks
    WHERE file_id IN (
        SELECT id FROM _base_files
        WHERE path NOT IN (SELECT path FROM _overlay_files)
    );

CREATE VIEW symbols AS
    SELECT * FROM _overlay_symbols
    UNION ALL
    SELECT * FROM _base_symbols
    WHERE file_id IN (
        SELECT id FROM _base_files
        WHERE path NOT IN (SELECT path FROM _overlay_files)
    );

CREATE VIEW edges AS
    SELECT * FROM _overlay_edges
    UNION ALL
    SELECT * FROM _base_edges
    WHERE src_symbol_id IN (
        SELECT id FROM _base_symbols
        WHERE file_id IN (
            SELECT id FROM _base_files
            WHERE path NOT IN (SELECT path FROM _overlay_files)
        )
    );

CREATE VIEW pending_edges AS
    SELECT * FROM _overlay_pending_edges
    UNION ALL
    SELECT * FROM _base_pending_edges
    WHERE src_symbol_id IN (
        SELECT id FROM _base_symbols
        WHERE file_id IN (
            SELECT id FROM _base_files
            WHERE path NOT IN (SELECT path FROM _overlay_files)
        )
    );

-- Step 4: Drop old FTS triggers (they reference table names that are now views)
DROP TRIGGER IF EXISTS chunks_fts_insert;
DROP TRIGGER IF EXISTS chunks_fts_delete;
DROP TRIGGER IF EXISTS chunks_fts_update;

-- Step 5: Recreate FTS5 with view as external content
DROP TABLE IF EXISTS chunks_fts;
CREATE VIRTUAL TABLE chunks_fts USING fts5(
    content,
    symbol_name,
    content=chunks,
    content_rowid=id
);

-- Step 6: Create FTS triggers on overlay table (writes only go to overlay)
CREATE TRIGGER chunks_fts_insert AFTER INSERT ON _overlay_chunks BEGIN
    INSERT INTO chunks_fts(rowid, content, symbol_name)
    VALUES (new.id, new.content, new.symbol_name);
END;

CREATE TRIGGER chunks_fts_delete AFTER DELETE ON _overlay_chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
    VALUES ('delete', old.id, old.content, old.symbol_name);
END;

CREATE TRIGGER chunks_fts_update AFTER UPDATE ON _overlay_chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
    VALUES ('delete', old.id, old.content, old.symbol_name);
    INSERT INTO chunks_fts(rowid, content, symbol_name)
    VALUES (new.id, new.content, new.symbol_name);
END;

-- Step 7: INSTEAD OF triggers for write routing
CREATE TRIGGER files_view_insert INSTEAD OF INSERT ON files BEGIN
    INSERT INTO _overlay_files (path, content_hash, mtime, size, language,
        indexed_at, embedding_status, parse_quality, is_test)
    VALUES (NEW.path, NEW.content_hash, NEW.mtime, NEW.size, NEW.language,
        NEW.indexed_at, NEW.embedding_status, NEW.parse_quality, NEW.is_test);
END;

CREATE TRIGGER files_view_delete INSTEAD OF DELETE ON files BEGIN
    DELETE FROM _overlay_files WHERE path = OLD.path;
END;

CREATE TRIGGER files_view_update INSTEAD OF UPDATE ON files BEGIN
    INSERT OR REPLACE INTO _overlay_files (path, content_hash, mtime, size,
        language, indexed_at, embedding_status, parse_quality, is_test)
    VALUES (NEW.path, NEW.content_hash, NEW.mtime, NEW.size, NEW.language,
        NEW.indexed_at, NEW.embedding_status, NEW.parse_quality, NEW.is_test);
END;

-- Step 8: Rebuild FTS to include base data
INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild');

-- Step 9: Recreate indexes on overlay tables
CREATE INDEX IF NOT EXISTS idx_overlay_files_language ON _overlay_files(language);
CREATE INDEX IF NOT EXISTS idx_overlay_files_embedding_status ON _overlay_files(embedding_status);
CREATE INDEX IF NOT EXISTS idx_overlay_chunks_file ON _overlay_chunks(file_id);
CREATE INDEX IF NOT EXISTS idx_overlay_chunks_file_index ON _overlay_chunks(file_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_overlay_chunks_symbol_name ON _overlay_chunks(symbol_name);
CREATE INDEX IF NOT EXISTS idx_overlay_chunks_embedded ON _overlay_chunks(embedded, id);
CREATE INDEX IF NOT EXISTS idx_overlay_symbols_name ON _overlay_symbols(name);
CREATE INDEX IF NOT EXISTS idx_overlay_symbols_qualified ON _overlay_symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_overlay_symbols_file ON _overlay_symbols(file_id);
CREATE INDEX IF NOT EXISTS idx_overlay_symbols_chunk ON _overlay_symbols(chunk_id);
CREATE INDEX IF NOT EXISTS idx_overlay_edges_src ON _overlay_edges(src_symbol_id);
CREATE INDEX IF NOT EXISTS idx_overlay_edges_dst ON _overlay_edges(dst_symbol_id);
CREATE INDEX IF NOT EXISTS idx_overlay_edges_file ON _overlay_edges(file_id);

-- Step 10: Record schema version
INSERT INTO schema_version (version) VALUES (5);
```

### flock mechanism (daemon.go addition)

```go
type lockResult struct {
    fd       int
    isLeader bool
}

func acquireInstanceLock(projectRoot string) (lockResult, error) {
    lockPath := filepath.Join(projectRoot, ".shaktiman", "index.db.lock")
    fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0644)
    if err != nil {
        return lockResult{}, fmt.Errorf("open lock: %w", err)
    }

    err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
    if err != nil {
        // Lock held by another process -- run as follower
        return lockResult{fd: fd, isLeader: false}, nil
    }

    // Write PID for liveness checking by followers
    _ = os.WriteFile(lockPath+".pid", []byte(strconv.Itoa(os.Getpid())), 0644)
    return lockResult{fd: fd, isLeader: true}, nil
}

func releaseInstanceLock(lr lockResult) {
    syscall.Flock(lr.fd, syscall.LOCK_UN)
    syscall.Close(lr.fd)
}
```

### Follower daemon startup path

```go
func New(cfg types.Config) (*Daemon, error) {
    lock, err := acquireInstanceLock(cfg.ProjectRoot)
    if err != nil {
        return nil, err
    }

    db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
    // ...existing setup...

    d := &Daemon{
        // ...existing fields...
        isLeader: lock.isLeader,
        lockResult: lock,
    }

    if lock.isLeader {
        // Full startup: writer, watcher, enrichment, embedding
        d.writer = NewWriterManager(store, cfg.WriterChannelSize, cfg.TestPatterns)
        // ...
    } else {
        slog.Info("running as follower (read-only mode)")
        // No writer, no watcher, no enrichment
        // Start leader promotion goroutine
        go d.tryPromoteLoop(ctx)
    }

    return d, nil
}
```

---

## Open Questions

1. **Overlay ID namespace.** Should overlay tables use a high AUTOINCREMENT start (e.g., `INSERT INTO sqlite_sequence VALUES('_overlay_files', 10000000000)`) to avoid ID collisions with base tables, or should views always resolve by path (not ID)?

2. **FTS5 rebuild frequency.** After how many overlay writes should FTS5 be rebuilt to ensure base+overlay consistency? Options: every N writes, on startup only, or use incremental merge.

3. **Follower promotion timing.** Should followers retry flock continuously (every 30s), or only on explicit user action (restart)?

4. **Windows support.** `flock` is Unix-only. If Windows support is needed, `LockFileEx` provides equivalent semantics but requires a separate code path. Defer until demand exists.

5. **Diff tracking tables.** `diff_log` and `diff_symbols` are not included in the overlay pattern above. These are append-only and may not need overlay semantics. Decision: keep them as-is (no rename, no overlay), or include for completeness?

---

## Amendment 1 — 2026-04-01: Pattern Detection and Satellite Mode for Ephemeral Worktrees

### Context

ADR-002 addresses two concurrency patterns: same-directory multi-instance (D1, flock) and user-managed worktrees (D2-D7, overlay). A third pattern was identified:

**Pattern C: Agent-spawned ephemeral worktrees.** A user on branch A asks Claude to perform work on branches B and C simultaneously. Claude spawns subagents that enter temporary worktrees via `EnterWorktree`. These worktrees have no `.shaktiman/` directory, no daemon, no index. All Shaktiman MCP tools are unavailable to the subagent.

This pattern differs from user-managed worktrees (Pattern A) in key ways:

- **Ephemeral** — worktrees are created and destroyed within a single Claude session.
- **No dedicated daemon** — spinning up a full `shaktimand` per ephemeral worktree is wasteful (~350MB memory each: 128MB SQLite reader cache + 215MB vector store + overhead).
- **Subagent inherits MCP connection** — the subagent's MCP tools still route to the main repo's daemon via the existing stdio pipe.

Running N daemons for N ephemeral worktrees is untenable at scale:

| Resource | Per daemon | 5 ephemeral worktrees |
|---|---|---|
| SQLite reader pool (4 × 32MB cache) | ~128MB | 640MB |
| In-memory vectors (140K × 384 dims × 4B) | ~215MB | 1.07GB |
| fsnotify watcher + parsers | ~10MB | 50MB |
| **Total** | **~350MB** | **~1.75GB** |

Additionally, neither the in-memory BruteForceStore nor HNSW indexes support overlay/fork semantics natively. Each daemon would load a full copy of the vector index.

### Decision

**D11: Dual operating modes — standalone and satellite.**

The `shaktimand` daemon supports two modes that coexist:

- **Standalone mode**: Daemon runs in a worktree with its own `.shaktiman/` directory, own index, own overlay. This is the existing ADR-002 model for user-managed worktrees (Patterns A and B from the original ADR).
- **Satellite mode**: The main repo's daemon serves queries on behalf of ephemeral worktrees that have no local daemon. No separate process, no separate index, no vector store duplication.

**D12: Startup-time detection — automatic mode inference.**

At daemon startup, `shaktimand` determines its context from filesystem signals. No user configuration required.

```
shaktimand starts in <projectRoot>
│
├── Is .git a FILE? (worktree indicator)
│   │
│   ├── YES → I'm in a git worktree
│   │   │
│   │   ├── .shaktiman/index.db exists?
│   │   │   ├── YES → Standalone mode (user-managed worktree, Pattern A)
│   │   │   │         Proceed with overlay model (D2-D7)
│   │   │   │
│   │   │   └── NO  → Fresh worktree, need to bootstrap
│   │   │       │
│   │   │       ├── Main repo has .shaktiman/index.db?
│   │   │       │   (locate via: git rev-parse --git-common-dir → parent)
│   │   │       │   │
│   │   │       │   ├── YES → Fast fork: copy base, create overlay tables
│   │   │       │   │         Incremental re-index of divergent files only
│   │   │       │   │
│   │   │       │   └── NO  → Cold start (full index from scratch)
│   │   │       │             Or refuse: "index main repo first"
│   │   │
│   │   └── flock on index.db held by another process?
│   │       ├── YES → Follower mode (D1: read-only, no enrichment)
│   │       └── NO  → Leader (acquire flock, full startup)
│   │
│   └── NO → .git is a DIRECTORY → I'm the main repo
│       └── Normal startup (current behavior)
│           Also: enable satellite query handling (D13)
```

**Key detection signals:**

| Signal | How to check | What it reveals |
|---|---|---|
| `.git` is file vs directory | `os.Stat(".git").Mode()` | Worktree vs main repo |
| `.shaktiman/` exists | `os.Stat(".shaktiman/index.db")` | Previously indexed vs fresh |
| Main repo's base index | `git rev-parse --git-common-dir` → `<parent>/.shaktiman/index.db` | Base available for fast fork |
| flock on `index.db` | `syscall.Flock(fd, LOCK_EX\|LOCK_NB)` | Another daemon already running |

**D13: Query-time detection — `cwd` parameter for satellite queries.**

All MCP tools gain an optional `cwd` parameter:

```json
{
  "method": "search",
  "params": {
    "query": "authentication flow",
    "cwd": "/path/to/ephemeral-worktree-B"
  }
}
```

When `cwd` is absent: query uses the daemon's own index (default behavior, no change).

When `cwd` is present, the daemon:

1. **Validates** the path is a git worktree of the same repository:
   ```
   git -C <cwd> rev-parse --git-common-dir
   → must match the daemon's own git-common-dir
   ```
   If it doesn't match (different repo), reject the query.

2. **Detects divergent files** between the daemon's index and the worktree:
   ```
   For each file in the index:
     compare content_hash(indexed) vs content_hash(file at <cwd>/path)
   ```
   This comparison is cached per worktree path (invalidated by mtime checks).

3. **Serves the query** with an in-memory overlay:
   - For non-divergent files (99.96%): serve from the existing index. Zero overhead.
   - For divergent files: on-the-fly tree-sitter parse of the worktree's version. Results are cached in memory for the session.

4. **Vector search** uses an `OverlayVectorStore` wrapper:
   ```go
   type OverlayVectorStore struct {
       base    VectorStore           // shared, main repo's store
       overlay map[int64][]float32   // worktree-specific vectors (<100 entries)
       hidden  map[int64]bool        // base vectors to exclude (changed chunks)
   }
   ```
   Search scans both base (excluding hidden) and overlay, merges by score. For BruteForceStore (linear scan), this adds negligible overhead. For HNSW, the overlay is searched separately and results are merged.

**D14: The `cwd` parameter is stateless and optional.**

- No session registration required. Each query is self-contained.
- The daemon does not track worktree lifecycle. No heartbeat needed for ephemeral worktrees.
- When the worktree is deleted, queries with that `cwd` simply stop arriving. No cleanup.
- Cached overlays can be evicted on LRU basis or after a timeout (e.g., 10 minutes of no queries for that `cwd`).
- The `cwd` parameter is ignored by standalone worktree daemons (they always use their own index).

### How the patterns coexist

| Pattern | Who starts daemon? | Daemon mode | Index source | Vector store |
|---|---|---|---|---|
| **A: Same-dir multi-instance** | Claude Code (auto via MCP config) | Leader + followers (D1) | Single shared `index.db` | Single shared in-memory store |
| **B: User-managed worktrees** | Claude Code (per worktree) | Standalone with overlay (D2-D7) | Own `index.db` with overlay tables | Own in-memory store (full load) |
| **C: Agent-spawned ephemeral** | Not started (uses main daemon) | Satellite via `cwd` param (D13) | Main daemon's index + in-memory overlay | Main daemon's store + `OverlayVectorStore` |

**From Claude Code's perspective:**
- Pattern A: transparent — MCP tools just work, flock handles contention.
- Pattern B: transparent — each Claude Code instance starts its own daemon.
- Pattern C: Claude/subagent passes `cwd` in MCP tool calls after entering a worktree. This is the only behavioral change.

### Consequences

**Positive:**
1. Ephemeral worktrees work without spawning new daemons — zero startup cost, ~0.5MB memory overhead per worktree (vs ~350MB for a full daemon).
2. No `.shaktiman/` directory created in ephemeral worktrees — clean worktree lifecycle, no stale data on deletion.
3. Detection is fully automatic at startup. No user configuration, no mode flags.
4. The `cwd` parameter is backward-compatible — absent means "use local index" which is the current behavior.
5. Vector store sharing eliminates the 215MB-per-worktree duplication for ephemeral cases.

**Negative:**
1. **`cwd` adds a parameter to all MCP tools.** While optional, it expands the API surface. Every tool handler needs to check for it and route accordingly.
2. **In-memory overlay cache has no persistence.** If the daemon restarts, cached parses for ephemeral worktrees are lost. Acceptable since queries rebuild the cache on demand.
3. **Divergent file detection has cost.** Comparing content hashes for 25K files against a worktree takes ~100-200ms on first query. Subsequent queries use the cached result (invalidated by mtime).
4. **Dependency graph is approximate for ephemeral worktrees.** Edges for divergent files reflect the main index, not the worktree's actual call graph. Same tradeoff accepted in ADR-001 Amendment 1 for the review tool.
5. **`cwd` validation requires `git rev-parse`** — one subprocess call per unique `cwd` (cached after first check).

### Changes to Phasing

Phase 1 (flock) and Phase 2 (overlay schema) are unchanged.

**New Phase 2.5: Satellite mode (D11-D14)**
- Add `cwd` parameter to all MCP tool definitions in `internal/mcp/tools.go`
- Add startup detection logic in `internal/daemon/daemon.go` (`.git` file check, git-common-dir resolution)
- Add `OverlayVectorStore` wrapper in `internal/vector/overlay.go`
- Add in-memory file overlay cache in `internal/daemon/satellite.go`
- **Effort:** ~500 LOC
- **Dependency:** None (can ship independently of Phase 2)

### FMEA Additions

| # | Failure Mode | Sev | Occ | Det | RPN | Mitigation |
|---|---|---|---|---|---|---|
| F11 | `cwd` points to a non-worktree directory or different repo | 3 | 3 | 2 | 18 | `git rev-parse --git-common-dir` validation; reject with clear error |
| F12 | Divergent file cache becomes stale (file changed after cache) | 4 | 4 | 3 | 48 | mtime-based invalidation; cache TTL of 60 seconds |
| F13 | Large divergence (>500 files) makes on-the-fly parsing slow | 5 | 2 | 3 | 30 | Cap on-the-fly parsing at N files; warn "consider standalone mode for large divergences" |
| F14 | Subagent doesn't pass `cwd` → queries return main branch results silently | 5 | 4 | 5 | 100 | Document in MCP tool instructions; Claude Code could auto-inject `cwd` when in a worktree |

**F14 is the highest new risk (RPN 100).** The most likely failure is the subagent simply forgetting to pass `cwd`, silently getting results from the wrong branch. Mitigation: Claude Code should auto-inject `cwd` into MCP tool calls when the working directory differs from the daemon's project root. This is a Claude Code integration, not a Shaktiman change.

---

## References

- `internal/storage/db.go` -- DB open, WAL config, dual connection pools
- `internal/storage/schema.go` -- Current schema V4, migration system
- `internal/storage/graph.go` -- `Neighbors()` recursive CTE, edge resolution
- `internal/storage/fts.go` -- FTS5 setup, triggers, rebuild, staleness check
- `internal/daemon/writer.go` -- `WriterManager`, `processEnrichmentJob`, content hash guard (line 233)
- `internal/daemon/daemon.go` -- Daemon lifecycle, cold index, watcher startup
- `internal/daemon/watcher.go` -- fsnotify watcher, debounce, branch switch detection
- `internal/types/interfaces.go` -- `MetadataStore`, `BatchMetadataStore`, `VectorStore` interfaces
- `internal/types/config.go` -- `DefaultConfig`, DB path at `.shaktiman/index.db`
- [SQLite FTS5 External Content Tables](https://www.sqlite.org/fts5.html#external_content_tables)
- [flock(2) man page](https://man7.org/linux/man-pages/man2/flock.2.html)

---

## Amendment 2: Bridge to ADR-003 Provider Pattern

**Date:** 2026-04-01
**Trigger:** ADR-003 (Pluggable Storage Backends) introduces the Provider Pattern with `MetadataStoreConfig` and `VectorStoreConfig` factories. The overlay/RLS concurrency mechanisms defined in this ADR must compose cleanly with the provider pattern. Adversarial analysis of ADR-003 identified this cross-ADR composition gap as a HIGH severity finding.

### Decision D15: Worktree Configuration Flows Through Provider Factories

**Context:** This ADR defines two concurrency strategies:
- SQLite: UNION ALL overlay with `_base_*`/`_overlay_*` tables, merged views, `INSTEAD OF` triggers (D3-D5).
- Postgres: Row-Level Security with `SET app.worktree_id` per connection (D6).

ADR-003 defines a provider registry where each backend registers a factory via `init()`. The factory receives `MetadataStoreConfig` and returns a `WriterStore` (or `MetadataStore`). The question: how does the factory know whether to apply overlay/RLS mode?

**Decision:** `MetadataStoreConfig` includes a `WorktreeConfig` struct (defined in ADR-003 Amendment 1, Decision A3):

```go
type WorktreeConfig struct {
    Enabled     bool   // whether overlay/RLS is active
    WorktreeID  string // unique identifier for this worktree instance
    BaseDBPath  string // path to the base SQLite DB (overlay mode only)
    IsSatellite bool   // satellite mode (read base + write overlay)
}
```

Each backend factory is responsible for applying its own concurrency strategy based on `WorktreeConfig`:

| Backend | `Worktree.Enabled = false` | `Worktree.Enabled = true` | `Worktree.IsSatellite = true` |
|---|---|---|---|
| SQLite | Standard schema (V4) | Apply overlay schema (V5): rename base tables → create overlay tables → create merged views → create `INSTEAD OF` triggers → reconfigure FTS5 `content=` on views | Open base DB read-only + overlay DB for writes. Merged views span both databases. |
| Postgres | Standard schema | Apply RLS policies on all tables. `SET app.worktree_id` on every connection acquired from pool via `pgxpool.Config.AfterConnect` hook. | Same as Enabled, but daemon runs in follower mode (MCP tools only, no enrichment). |

#### Implementation Location

- SQLite overlay logic: `internal/storage/sqlite/overlay.go` (schema V5 migration, view creation, trigger creation)
- Postgres RLS logic: `internal/storage/postgres/rls.go` (policy creation, connection init hook)
- Detection logic (D11 from Amendment 1): `internal/daemon/detect.go` (startup tree that populates `WorktreeConfig`)

#### Vector Store Composition

`VectorStoreConfig` also carries `WorktreeConfig`. When `IsSatellite = true`:
- BruteForce/HNSW: Wrapped with `OverlayVectorStore` (D12 from Amendment 1) — in-memory overlay map + hidden set over shared base store.
- Qdrant: Single shared collection. Satellite mode adds a `worktree_id` payload field for filtering. No overlay needed — server-side filtering handles isolation.
- pgvector: Shared table with `worktree_id` column. RLS policies auto-filter (same as MetadataStore).

#### Startup Detection Populates WorktreeConfig

The detection tree from D11 (Amendment 1) runs before factory creation and populates `WorktreeConfig`:

```
Is cwd inside a git worktree?
├─ Yes → WorktreeConfig{Enabled: true, WorktreeID: derived-from-path}
│   ├─ Is there a running shaktimand for the main repo?
│   │   ├─ Yes → IsSatellite = true, BaseDBPath = main repo's DB
│   │   └─ No  → IsSatellite = false (this instance owns the worktree)
│   └─ ...
└─ No  → WorktreeConfig{Enabled: false}
```

The populated `WorktreeConfig` is passed to both `MetadataStoreConfig` and `VectorStoreConfig` before calling the registry factories.

### Cross-Reference

- ADR-003 Amendment 1, Decision A3 defines `WorktreeConfig` and its role in `MetadataStoreConfig`.
- This ADR's D3-D5 define the SQLite overlay schema.
- This ADR's D6 defines the Postgres RLS approach.
- This ADR's D11-D14 define satellite mode and detection.
- ADR-003 Phase 2a (interface extraction) is a prerequisite for implementing worktree-aware factories.

---

## Amendment 3 — 2026-04-09: Re-scope after codebase audit + adversarial review

**Trigger:** The codebase drifted substantially between the original ADR (2026-03-31) and now. A directed re-audit, inversion analysis, and devil's-advocate review invalidate most of the original plan. This amendment records the drift, the analyses, and the revised scope.

### A3.1 Codebase drift since the original ADR

| Area | ADR assumption (2026-03-31) | Current reality (2026-04-09) |
|---|---|---|
| Storage package layout | Flat `internal/storage/{db,schema,graph,fts,metadata}.go` | Package-split behind build tags: `internal/storage/sqlite/` and `internal/storage/postgres/`, registered via `internal/storage/registry.go` (`RegisterMetadataStore`) |
| Schema evolution | Hand-rolled `schema_version` table + inline DDL | Goose-managed SQL migrations in `internal/storage/migrations/{sqlite,postgres}/*.sql`. There is no `V4`; the current SQLite schema is baselined at `001_base_schema.sql` + `002_symbols_kind_namespace.sql`. Postgres is at `001 → 006`. |
| Postgres isolation | D4: `file_versions` + RLS + content-addressed dedup keyed by `(path, content_hash)` | Shipped **differently**: `projects` table + `project_id` FK on `files` and `embeddings` (migration `006_add_project_id.sql`), application-level scoping, `PgStore.projectID` field. `EnsureProject(ctx, projectRoot)` canonicalizes via `filepath.EvalSymlinks`. No RLS, no content-addressed dedup, no `file_versions`. (Commits #47/#48, `ce34f92`.) |
| Postgres FTS | Reuse FTS5 triggers via views | `content_tsv` generated `tsvector` column on `chunks`; `KeywordSearch` JOINs through `files` for project scoping. FTS5 concerns do not apply to Postgres. |
| Vector backends | `internal/vector/{store,hnsw}.go` flat | Package-split: `bruteforce`, `hnsw`, `qdrant`, `pgvector` behind build tags. `pgvector` uses `project_id`; **Qdrant has zero multi-project isolation** (single collection, no payload tags) — `internal/daemon/daemon.go:81-84` logs a warning. `internal/vector/registry.go` mirrors the storage registry pattern. |
| Interfaces | `MetadataStore`, `BatchMetadataStore`, `VectorStore` | Composite `WriterStore` = `MetadataStore + DiffStore + GraphMutator + EmbeddingReconciler + EmbedSource + MetricsWriter + WithWriteTx`. New `StoreLifecycle` interface handles SQLite FTS trigger recovery; Postgres returns nil. |
| Branch switch handling | Planned to fall out of overlay view semantics | Already shipped: `Watcher.BranchSwitchCh` (`internal/daemon/watcher.go:63`) fires on >20 file flush, triggering a full re-scan + re-index in `daemon.go:536-563`. Single-worktree branch switches are a solved problem without overlays. |
| `WorktreeConfig` (ADR-002 Amend. 2 / ADR-003 A3) | Assumed existent | **Does not exist** in `internal/storage/registry.go` or anywhere else. D15 has a phantom dependency. |
| `cwd` MCP parameter (D13) | Planned | Not implemented. No tool in `internal/mcp/tools.go` accepts a `cwd` argument. |
| `flock` / lock files / `.shaktiman/worktree.id` | Planned | Not implemented. `cmd/shaktimand/main.go` opens the DB directly and even overwrites `.shaktiman/shaktimand.log` on every start — two concurrent daemons silently race over the log file. |
| Neighbors scoping invariant | Assumed views would handle it | `internal/storage/postgres/graph.go:143-144` explicitly relies on the invariant that *no cross-project edges can exist* because `InsertEdges` / `ResolvePendingEdges` scope symbol lookups by `project_id`. This invariant would be **broken** by the original D4's shared-file-row design. |

### A3.2 Inversion & devil's-advocate findings (summary)

High-signal findings from an adversarial + solution-fit review against the *current* code:

#### D1 — same-directory multi-instance (flock leader/follower)

**Verdict:** MARGINAL primitive (flock OK), WRONG architecture (leader/follower).

- **Inversion — silent stale reads.** The leader/follower split has no staleness signal. A follower serves arbitrarily stale results while the user believes the index is live. The `summary` tool does not return a "last indexed at" field. Worst-case correctness class: **stale-wrong-answer**.
- **WAL + mmap hazard on Darwin.** The reader pool (`internal/storage/sqlite/db.go:127`) sets `PRAGMA mmap_size = 268435456`. A follower holding an mmap on the WAL during the leader's passive checkpoint can hit SIGBUS if the WAL is truncated. The ADR never addresses mmap + multi-process. Unverified, HIGH-impact.
- **PID-based auto-promotion is unsafe.** Forcibly taking the lock from a "wedged" leader can create two writers → non-atomic FTS trigger sequences → index corruption. Invalidates ADR mitigation F3.
- **macOS flock semantics drift.** `syscall.Flock` on Darwin is BSD-flock; silently advisory on SMB/NFS/overlayfs. The ADR's F7 mitigation ("detect NFS and warn") is not implemented and is non-portable.
- **Devil's advocate — wrong layer.** The actual cause is *Claude Code spawning one `shaktimand` per MCP session on the same project*. The right fix is upstream: one long-lived daemon per project, reused across clients. Leader/follower degrades gracefully for a problem that should not be tolerated in the first place. A **single-instance refuse-to-start** ships the 90% value in ~50 LOC.

#### D2–D5 — SQLite UNION ALL overlay

**Verdict:** WRONG TOOL. Recommend **abandoning**.

- **Migration plan is dead on arrival.** The ADR prescribes `ALTER TABLE files RENAME TO _base_files` as an inline `V4 → V5` migration. Schema is now goose-managed; there is no `V4`. Any overlay must ship as a new numbered goose migration that runs on fresh installs too — the "renames existing tables" logic is nonsensical when `files` has never been a base table. The plan as written cannot be applied; it is a redesign, not a migration.
- **FTS5-on-view is a load-bearing unverified claim.** ADR lines 122, 280 claim "FTS5 supports views as `content=` sources... validated during research." This is **not** generally true: the view `chunks` defined at ADR lines 543-550 uses `UNION ALL` across two `AUTOINCREMENT` tables, producing potentially duplicate rowids. FTS5 external-content requires a stable unique `rowid` mapping. No integration test, no snippet, no driver-version evidence exists in the codebase. **If this assumption is wrong, the entire overlay plan collapses.** This must be a failing integration test *before* any other overlay work proceeds.
- **Overlay FK cascade gap.** The ADR's overlay DDL (ADR lines 483-523) does not re-declare FKs. The base tables all use `ON DELETE CASCADE`. When an `INSTEAD OF DELETE` trigger removes an overlay file, overlay chunks/symbols/edges are **not cascaded** and become orphans. Silent corruption over time.
- **ID-collision (F6, RPN 84) is unfixable by AUTOINCREMENT offset.** `INSERT INTO sqlite_sequence VALUES('_overlay_files', 10000000000)` only affects the *next* allocation; a manual lower INSERT still succeeds. `_overlay_edges.src_symbol_id` references symbol IDs spanning overlay + base with no FK — any collision silently mis-routes graph edges. `Neighbors()` recursive CTEs will traverse wrong edges with no loud failure.
- **Recursive CTE over a UNION ALL view is a performance trap.** At 8-level traversal × (overlay + `NOT IN` base scan) × 140K rows, the "15-30% overhead" estimate (ADR line 210) has no measurement behind it.
- **The dominant use case is already solved.** `Watcher.BranchSwitchCh` handles single-worktree branch switching with a full re-index. No overlay needed for the 95% case.
- **Better simpler alternative exists.** Each git worktree has its own `.shaktiman/index.db` (because `DBPath` is derived from `ProjectRoot`). The only problem the overlay solves is *cold-index cost on a fresh worktree*, which is one-time. A reflink clone (`cp -c` on macOS / `cp --reflink=auto` on Linux) of the base DB solves it in ~30 LOC with no schema change. For Postgres, **each worktree is a separate `project_id` already** — the entire "multi-worktree on shared backend" problem is solved in production today by simply pointing the daemon at a different path.

#### D4 — Postgres `file_versions` + RLS + content-addressed dedup

**Verdict:** SUPERSEDED. Reality shipped a simpler, strictly-better design.

- **Not what was built.** The Postgres backend is scoped by `projects.id` on `files` and `embeddings`. No `file_versions`, no RLS, no `(path, content_hash)` dedup. `PgStore.projectID` is a function-level parameter that cannot be accidentally forgotten — strictly more auditable than RLS's per-connection `SET app.worktree_id`.
- **Adopting D4 now would break an invariant the current code depends on.** `internal/storage/postgres/graph.go:143-144` explicitly notes that `Neighbors` does *not* filter by `project_id` because cross-project edges cannot exist. D4's shared-`files`-row model would violate this, introducing a cross-worktree data-leak path through `Neighbors` and forcing a full re-audit of every Postgres query to add join filters that were deliberately designed out.
- **RLS footguns.** `SET LOCAL app.worktree_id` with pgx pooling has documented variable-leakage modes; `BYPASSRLS` and `SECURITY DEFINER` interactions are audit burdens. The current `WHERE project_id = $1` pattern is greppable and code-reviewable.
- **"99.96% shared" is speculation.** No measurement. Dedup ignores that most divergent files touch imports or signatures, which propagate into `symbols`, `edges`, and embedded `chunks`.

#### D6 — `.shaktiman/worktree.id` UUID file

**Verdict:** WRONG TOOL. Delete the section.

- Redundant with `EnsureProject(ctx, projectRoot)`, which already uses canonicalized absolute path as the stable identity.
- Introduces a new failure mode (PM-4 "file copied with `cp -r`") that does not exist today. The current design is strictly safer.

#### D11–D14 — satellite mode + `cwd` MCP parameter

**Verdict:** WRONG TOOL. Recommend **deleting**.

- **F14 (RPN 100, silent wrong-branch results) has no real mitigation.** "Claude Code could auto-inject `cwd`" is wishful thinking outside this codebase's control. Shipping D13 creates a silent-default-to-wrong-answer failure mode: a subagent searches for "recent change" in ephemeral-worktree-B and silently gets results from branch A. Correctness class 1.
- **`OverlayVectorStore` does not compose with Qdrant.** `internal/vector/qdrant/store.go:30-50` creates one shared collection; upserts have no payload; searches have no filter. There is zero per-project scoping today. Retrofitting `worktree_id` payload filtering requires migrating every existing collection and adding payload code — not a one-line change as the ADR implies.
- **HNSW does not support hidden-set exclusion.** The "search overlay separately and merge" design degrades recall; no correctness proof exists.
- **`cwd` parameter is a breaking-API trap.** Once added to every MCP tool handler, every future tool must plumb it; removal is hard.
- **The problem can be solved upstream.** At the ADR's own claimed scale (25K files / 140K chunks), a second daemon's memory cost is dominated by the vector store. Satellite mode does not help the *primary* daemon's memory cost; it only helps the *secondary* case. A lightweight supervisor mode (one `shaktimand` per registered worktree, all sharing Postgres/Qdrant by `project_id`) reuses 100% of existing backend code for ~150 LOC of routing.

#### D15 — Amendment 2 bridge via `WorktreeConfig`

**Verdict:** BLOCKED on phantom dependency. Delete.

- `WorktreeConfig` does not exist in `internal/storage/registry.go`. The ADR-003 Amendment 1 / A3 reference is unfulfilled.
- Factory signature conflict: factories return `(WriterStore, StoreLifecycle, closer, error)`. Making `WriterStore` capabilities switch at runtime based on `WorktreeConfig.IsSatellite` breaks the composite interface contract — `GraphMutator`/`EmbeddingReconciler` would be no-ops in satellite mode, requiring runtime type switching inside factories.
- **Self-contradictory with D13.** D13 says satellite mode is per-query (`cwd` is per-request, in-memory overlay is per-session cached). D15 says satellite mode is chosen once at factory construction. One of these two decisions has to go.

### A3.3 Amended decisions

| ID | Original status | New status | Replacement / rationale |
|---|---|---|---|
| D1 | Proposed | **RETAINED, HARDENED** (see A3.4) | The only surviving decision. Reframed as single-instance enforcement. |
| D2 | Proposed | **SUPERSEDED** | Use reflink clone on worktree bootstrap or accept cold-index cost. |
| D3 | Proposed | **SUPERSEDED** | No overlay → no routing. |
| D4 | Proposed | **SUPERSEDED** | Postgres `project_id` scoping already shipped (migration 006). Worktree = project. |
| D5 | Proposed | **SUPERSEDED** | No overlay → no scoped edges. Neighbors invariant stays intact. |
| D6 | Proposed | **DELETED** | `ProjectRoot` canonical path is the stable identity. |
| D7 | Proposed | **RETAINED** (already exists) | Content-hash dedup guard in `processEnrichmentJob` is already the behavior (`internal/daemon/writer.go:215-232`). No change needed. |
| D8 | Proposed | **DELETED** | No `worktrees` registry to GC. |
| D9 | Research result | **RETAINED** as historical context | DB alternatives still reject for the same reasons, but the conclusions no longer drive a design. |
| D10 | Proposed | **N/A** | Interface transparency is moot when nothing is layered on top. |
| D11 | Proposed (Amend. 1) | **DELETED** | No satellite mode. |
| D12 | Proposed (Amend. 1) | **DELETED** | No mode detection tree. |
| D13 | Proposed (Amend. 1) | **DELETED** | `cwd` MCP parameter not shipped. |
| D14 | Proposed (Amend. 1) | **DELETED** | Stateless satellite cache not shipped. |
| D15 | Proposed (Amend. 2) | **DELETED** | Phantom `WorktreeConfig` dependency + contradiction with D13. |

### A3.4 Surviving decision: D1 (hardened)

**Scope reduction:** D1 is now *single-instance enforcement*, not leader/follower.

**D1′ Minimum viable same-directory safety.** At daemon startup, acquire `syscall.Flock(LOCK_EX|LOCK_NB)` on `.shaktiman/daemon.pid`. On success, write PID and proceed. On failure, **exit with a clear actionable error**: "another shaktimand is already running for this project (pid N); stop it or close the other MCP session." No follower mode, no auto-promotion, no liveness polling. `flock` is automatically released on any process exit (including SIGKILL).

**Location:** `cmd/shaktimand/main.go`, before `daemon.New(cfg)`. Roughly 40-60 LOC plus a test that a second start fails fast.

**Why this works:**
- The real contention mode is MCP spawn races, not two human users on one repo. Refusing to start with a clear message is the correct signal.
- `WriterManager.MaxOpenConns=1` + WAL + `_busy_timeout=5000` already handles the rare genuine concurrent case on the writer side. Follower mode's value was MCP read-path availability, which `BUSY`-retry or "start one daemon, serve many clients via MCP" solves without a second process.
- No staleness class of bugs: there is no follower serving stale reads.
- No mmap/checkpoint race: there is only one reader pool.
- No leader/follower state machine to get wrong.

**What is explicitly out of scope for D1′:**
- NFS / SMB / overlayfs support. The lock file is documented as local-filesystem-only; the error message should suggest the workaround ("run shaktimand on a local path").
- Windows support. `flock` is Unix-only; Windows users get a clean no-op (or a PID-file fallback) — deferred to when there is demand.
- Upstream MCP daemon reuse (one daemon per project, multiplex clients over stdio/socket). Out of scope for this ADR; may motivate a future ADR-007.

**Hardening required for D1′:**
1. Lock file path: `.shaktiman/daemon.pid` (not `index.db.lock`). Stays a simple PID file; on `flock` success, write PID for human diagnosis only.
2. Canonicalize `ProjectRoot` (via `filepath.EvalSymlinks`) before computing the lock path so two invocations with different relative paths to the same directory produce the same lock. Current `DBPath` derivation does *not* canonicalize — fix here.
3. Ensure the lock file is opened (not unlinked) for the daemon's lifetime; `defer syscall.Flock(fd, LOCK_UN); defer syscall.Close(fd)` in `main.go`.
4. Log rotation in `main.go:42-48` currently overwrites `.shaktiman/shaktimand.log` — a second start today clobbers the first's log. D1′ prevents this as a side-effect, but add a test that asserts the first daemon's log is not truncated.

**FMEA for D1′:**

| # | Failure Mode | Sev | Occ | Det | RPN | Mitigation |
|---|---|---|---|---|---|---|
| F1′ | flock held after crash (kernel releases — but delayed on NFS) | 4 | 2 | 4 | 32 | Documented as local-filesystem only; error message mentions NFS workaround |
| F2′ | User runs two MCP sessions → second shows error instead of silently degrading | 2 | 5 | 1 | 10 | Feature, not bug: the error message is the correct UX |
| F3′ | `ProjectRoot` not canonicalized → two daemons on the same directory via different relative paths | 7 | 2 | 2 | 28 | Canonicalize before lock path derivation (required hardening step 2) |
| F4′ | Log rotation race clobbers prior daemon's log | 3 | 5 | 2 | 30 | Eliminated as a side-effect of D1′ (second daemon cannot reach the rotation code) |

All new RPN values are well below the original plan's worst entries (F2=112, F14=100, F6=84).

### A3.5 Amended phasing

| Phase | Scope | Files touched | LOC | Risk | Dependency |
|---|---|---|---|---|---|
| P1 (ship first) | D1′ single-instance enforcement | `cmd/shaktimand/main.go` (+ small helper in `internal/daemon` or a new `internal/daemon/singleton.go`); test in `cmd/shaktimand` or `internal/daemon` | ~50 | Low | None |
| P2 (optional, follow-up) | Worktree bootstrap via reflink clone (`cp -c` mac / `cp --reflink=auto` linux), fallback to plain copy, gated behind `config.shaktiman.toml` flag | `internal/daemon/daemon.go` `New()` path; possibly a small utility in `internal/storage/sqlite/` | ~30-50 | Low | P1 |
| P3 (docs only) | Document that users with multiple worktrees on a shared Postgres should register each worktree as its own project (already works via `EnsureProject`) | `README.md`, `CLAUDE.md`, config sample | ~0 code | None | — |

Original Phases 2/3/4 from the initial ADR are **discarded**. Phases 2.5 and 4 from Amendment 1 are **discarded**.

### A3.5b Case F support matrix (post-amendment)

"Case F" = two Claude Code sessions running `shaktimand` concurrently on the same project directory.

| Backend combination | Case F status | Rationale |
|---|---|---|
| `sqlite + brute_force` (default) | **Unsupported today.** D1′ (P1 above) will convert this to "refuse to start with actionable error." | SQLite WAL + `_busy_timeout` masks races but does not eliminate them; in-memory vector store races on `embeddings.bin`. |
| `sqlite + hnsw` | Same as above. | Same. |
| `sqlite + qdrant` | Same as above (metadata layer is still SQLite). | Vector layer is safe (Qdrant is external), but metadata layer still races. |
| `sqlite + pgvector` | **Invalid combination.** Rejected by existing `ValidateBackendConfig` check. | pgvector requires Postgres. |
| `postgres + brute_force` | **Rejected by ADR-003 Amendment 2 (A12).** Daemon refuses to start. | Postgres MVCC handles the metadata layer, but `embeddings.bin` still races across daemons. |
| `postgres + hnsw` | Rejected by ADR-003 Amendment 2 (A12). | Same. |
| `postgres + pgvector` | **Supported.** Works today with zero new code. | Postgres MVCC for metadata; pgvector `ON CONFLICT (chunk_id) DO UPDATE` for vectors; both scoped by `project_id`. |
| `postgres + qdrant` | **Supported.** Works today with zero new code, modulo the existing Qdrant cross-project-isolation warning at `internal/daemon/daemon.go:81-84`. | Postgres MVCC for metadata; Qdrant server-side state with last-write-wins on identical chunk IDs. |

**Upshot:** users who want Case F support *today* with no code changes should run `database.backend = "postgres"` with either `vector.backend = "pgvector"` or `vector.backend = "qdrant"`. ADR-003 Amendment 2 (A12) makes this configuration internally consistent by **rejecting** the unsafe Postgres combinations at config validation time, so users cannot accidentally land in a silent-corruption state. D1′ (P1 above) remains the single-instance-enforcement story for SQLite users, which cannot be fixed by backend choice alone.

### A3.6 Open questions replaced / resolved

The original ADR's open questions are moot:
1. Overlay ID namespace → no overlay.
2. FTS5 rebuild frequency → no FTS5 overlay.
3. Follower promotion timing → no follower mode.
4. Windows support → deferred in D1′.
5. Diff tracking tables → unchanged.

**New open questions for follow-up ADRs (not this one):**
1. Should `shaktimand` expose a socket/named-pipe for MCP client reuse so a single daemon can serve multiple Claude Code sessions on the same project? (Would remove the problem that D1 exists to guard.)
2. For team/Postgres deployments, should `shaktimand` detect git worktrees automatically and call `EnsureProject` with the worktree path rather than the main repo path? Or is documenting the existing behavior sufficient?
3. Is cold-indexing a fresh worktree actually slow enough in practice to justify P2 (reflink clone), or is it a speculative optimization? **Measurement needed before P2 is prioritized.**

### A3.7 Review trail

This amendment was produced on 2026-04-09 from:
- A directed audit of the current codebase under `internal/storage/`, `internal/daemon/`, `internal/vector/`, `internal/types/`, and `cmd/shaktimand/`.
- An adversarial-analyst pass explicitly applying **inversion** ("what would make this plan fail catastrophically?") and **devil's advocate** ("argue against every recommendation on technical merit").
- A solution-fit analyst pass looking for simpler alternatives that reuse existing primitives (`project_id`, `BranchSwitchCh`, `filepath.EvalSymlinks`).

Both passes independently converged on the same high-level conclusion: **most of ADR-002 is solved or obsoleted by features that shipped between 2026-03-31 and 2026-04-09** — particularly the `projects` / `project_id` isolation in commits #47/#48 (`ce34f92`), the goose-managed schema, and the pre-existing `BranchSwitchCh` re-index path.

**Agent convergence (high-confidence findings cited by both reviewers):**
- D2–D5 overlay should be abandoned in favor of per-worktree DB files (reflink clone or accept cold-index).
- D4 is superseded by shipped `project_id` scoping; worktree-as-project is the model.
- D11–D14 `cwd`-based satellite mode creates a silent-wrong-answer failure mode with no real mitigation.
- D15 references `WorktreeConfig` that does not exist and contradicts D13.
- D1 is worth keeping but should degrade to **single-instance enforcement**, not leader/follower.

**Load-bearing assumptions that would need evidence before any resurrection of the overlay plan:**
- FTS5 `content=<view>` with a UNION ALL view and duplicate rowids works with `mattn/go-sqlite3 + sqlite_fts5`. No test exists. Must be a failing integration test written first.
- Recursive CTE over UNION ALL view stays within the "15-30% overhead" envelope at 140K rows. No benchmark exists.
- Real file divergence across real git branches is within 1-5% (the "99.96% shared" claim). No measurement exists.

Until those three data points exist, the overlay plan stays closed.

---

## Amendment 4 — 2026-04-09: D1″ Single-daemon + socket proxy

**Trigger:** Amendment 3's D1′ (refuse-to-start) was a safe minimum but leaves the second Claude Code session without MCP tooling entirely. Analysis of the `mcp-go` v0.45.0 library confirmed that `MCPServer` supports multiple concurrent sessions natively, and `StreamableHTTPServer` can serve over a Unix domain socket. This enables a design where one daemon serves all sessions on the same project, eliminating both the concurrency races AND the "second session is degraded" problem.

**Supersedes:** A3.4 (D1′ refuse-to-start). D1′ becomes the stepping-stone shipped first; D1″ is the target.

### A4.1 Decision: D1″ — single leader daemon, thin socket proxies

When multiple Claude Code sessions open on the same project directory, exactly **one** daemon owns the database, vector store, watcher, and enrichment pipeline. All other daemons become stateless proxies that bridge their Claude Code client's stdio to the leader's Unix domain socket.

**Invariant:** exactly one process touches `index.db`, `embeddings.bin`, and `shaktimand.log`. Proxies hold zero file descriptors on these files and zero in-memory state about them.

### A4.2 Architecture

```
┌────────────────┐  ┌────────────────┐  ┌────────────────┐
│ Claude Code #1 │  │ Claude Code #2 │  │ Claude Code #3 │
│  (first open)  │  │ (second open)  │  │ (third open)   │
└────────┬───────┘  └────────┬───────┘  └────────┬───────┘
         │                   │                   │
         │ MCP/stdio         │ MCP/stdio         │ MCP/stdio
         │                   │                   │
         ▼                   ▼                   ▼
  ╔═════════════╗     ┌─────────────┐     ┌─────────────┐
  ║ shaktimand  ║     │ shaktimand  │     │ shaktimand  │
  ║   LEADER    ║     │    PROXY    │     │    PROXY    │
  ║             ║     │             │     │             │
  ║ holds flock ║     │ no flock    │     │ no flock    │
  ║ owns DB     ║     │ no DB       │     │ no DB       │
  ║ owns vectors║     │ no vectors  │     │ no vectors  │
  ║ owns watcher║     │ no watcher  │     │ no watcher  │
  ╚══════╦══════╝     └──────┬──────┘     └──────┬──────┘
         │                   │                   │
         │                   │ HTTP/JSON-RPC     │
         │                   │                   │
         │           ┌───────▼───────────────────▼──────┐
         └──────────►│  .shaktiman/daemon.sock          │
                     │  (Unix domain socket)            │
                     └──────────────────────────────────┘
```

**Filesystem state:**

```
.shaktiman/
├── daemon.pid        ← flock target + PID for diagnostics
├── daemon.sock       ← Unix socket, created by leader, removed on exit
├── index.db          ← leader only
├── index.db-wal      ← leader only
├── embeddings.bin    ← leader only
└── shaktimand.log    ← leader only
```

### A4.3 Leader internal components

The leader process runs two MCP transports on the **same** `MCPServer`:

1. **`StdioServer.Listen(ctx, os.Stdin, os.Stdout)`** — serves the Claude Code client that spawned this process. Single session (mcp-go's `StdioServer` uses a package-level singleton session).

2. **`StreamableHTTPServer`** listening on `.shaktiman/daemon.sock` — serves proxy clients via HTTP-over-Unix-socket. Multi-session (each proxy gets its own session via mcp-go's `RegisterSession` / `sync.Map`).

Both transports share the same `MCPServer` → same tool handlers → same `QueryEngine` → same `Store` → same `VectorStore`. A query from proxy client #2 hits the same code path as a query from the leader's own stdio client.

```
┌─────────────────────────────────────────────────────────┐
│                   LEADER PROCESS                        │
│                                                         │
│  ┌─────────────────┐      ┌──────────────────────┐     │
│  │ StdioServer     │      │ StreamableHTTPServer │     │
│  │ (Claude Code #1)│      │ (daemon.sock)        │     │
│  └────────┬────────┘      └──────────┬───────────┘     │
│           │                          │                  │
│           └──────────┬───────────────┘                  │
│                      ▼                                  │
│           ┌──────────────────────┐                      │
│           │     MCPServer        │                      │
│           │  (tool dispatcher)   │                      │
│           └──────────┬───────────┘                      │
│                      │                                  │
│           ┌──────────┼──────────┐                       │
│           ▼          ▼          ▼                       │
│      QueryEngine  VectorStore  Store                    │
│           │          │          │                       │
│           └──────────┼──────────┘                       │
│                      ▼                                  │
│              .shaktiman/index.db                        │
│              .shaktiman/embeddings.bin                  │
│                                                         │
│  Background goroutines (leader-exclusive):              │
│   WriterManager, EnrichmentPipeline, Watcher,           │
│   EmbedWorker, periodicEmbeddingSave, MetricsRecorder   │
└─────────────────────────────────────────────────────────┘
```

### A4.4 Proxy

The proxy is a **stateless stdio-to-HTTP bridge**. It has no DB, no vector store, no goroutines beyond the bridge. Memory footprint: ~5-10 MB (Go runtime + stdio buffers + HTTP client).

```
┌─────────────────────────────────────────────────────────┐
│                   PROXY PROCESS                         │
│                                                         │
│  ┌─────────────────┐      ┌──────────────────────┐     │
│  │ stdin            │      │ HTTP client           │     │
│  │ (from Claude    │◄────►│ → daemon.sock         │     │
│  │  Code session)  │      │ (Unix socket dialer)  │     │
│  │ stdout           │      └──────────────────────┘     │
│  └─────────────────┘                                    │
│                                                         │
│  No other components. ~80 LOC.                          │
└─────────────────────────────────────────────────────────┘
```

**Protocol:** The proxy reads JSON-RPC lines from stdin, wraps each as an HTTP POST to the leader's `StreamableHTTPServer` endpoint on the Unix socket, reads the HTTP response, and writes the JSON-RPC result back to stdout.

Shaktiman sends **zero** server→client MCP notifications (verified: no calls to `Notify*`, `sendNotification`, or `notification` anywhere in `internal/mcp/`). The MCP communication is purely request-response. This means the proxy does not need to handle SSE or streaming — it is a simple request-response HTTP bridge.

### A4.5 Startup decision tree

```
shaktimand <project-root> starts
         │
         ▼
canonicalize ProjectRoot (filepath.EvalSymlinks)
         │
         ▼
open .shaktiman/daemon.pid (O_CREAT|O_RDWR)
         │
         ▼
flock(fd, LOCK_EX | LOCK_NB)
         │
    ┌────┴────┐
    │         │
 success   EWOULDBLOCK
    │         │
    ▼         ▼
 LEADER    check .shaktiman/daemon.sock
    │         │
    │    ┌────┴────┐
    │    │         │
    │  exists    missing
    │    │         │
    │    ▼         ▼
    │  connect   stale state:
    │  to sock   log warning,
    │    │       wait + retry
    │    ▼       (up to 5s),
    │  PROXY     then fail with
    │            actionable error
    │
    ▼
 write PID to daemon.pid
 listen on daemon.sock
 open DB, load vectors
 start watcher, writer, embed
 serve MCP on stdio + socket
```

### A4.6 Leader handoff on exit

```
t0  Leader (pid 100) running.
    Proxies 200, 300 bridging via socket.

t1  Claude Code #1 closes → leader receives EOF on stdio.
    Leader stops watcher, writer, embed.
    Leader removes daemon.sock.
    Leader releases flock (kernel does it on exit too).
    Leader exits.

t2  Proxy 200's HTTP client gets connection-refused/EOF.
    Proxy 200 retries flock on daemon.pid.
       │
       ├─ success → PROMOTE: open DB, listen(sock),
       │            start background goroutines,
       │            resume serving its own stdio client
       │            as the new leader.
       │
       └─ lost race → proxy 300 got it first;
                       reconnect to the new daemon.sock
                       and continue as proxy.
```

**Promotion is safe** because it is a cold start: the proxy has no in-flight writer tx, no cached state, no dangling mmap. It opens the DB from disk, loads vectors from disk, starts the watcher fresh. Indistinguishable from starting `shaktimand` for the first time.

**No auto-promotion during hangs.** If the leader holds the flock but stops responding on the socket, proxies return errors to their Claude Code clients. Recovery is "user kills the leader; a proxy then promotes." This avoids the two-writer corruption risk from the original ADR's PID-based forcible takeover.

### A4.7 `mcp-go` library support

Verified against `mark3labs/mcp-go v0.45.0` (current dependency in `go.mod`):

| Feature | Support | Location |
|---|---|---|
| `MCPServer` multi-session | Yes | `server/session.go:129-139` — `RegisterSession` uses `sync.Map` |
| `StdioServer` on arbitrary `io.Reader`/`io.Writer` | Yes | `server/stdio.go:521-524` — `Listen(ctx, stdin, stdout)` |
| `StdioServer` singleton session | **Limitation** | `server/stdio.go:386` — package-level `stdioSessionInstance`. Cannot serve multiple clients via `StdioServer`. |
| `StreamableHTTPServer` | Yes | `server/streamable_http.go:212` — multi-session, implements `http.Handler` |
| `StreamableHTTPServer` on `net.Listener` | Yes | Standard `http.Serve(listener, handler)` with any `net.Listener` including Unix socket |

The `StdioServer` singleton limitation is why the leader uses `StreamableHTTPServer` for socket clients, not a second `StdioServer`. Both transports register sessions on the same `MCPServer` instance.

### A4.8 Use case verification

| Case | Works? | How |
|---|---|---|
| **A** — single session, single project | Yes | Leader mode, identical to today |
| **B** — multiple sessions, different projects | Yes | Each project gets its own leader. Different `.shaktiman/` dirs, different sockets. No interaction. |
| **C** — git worktrees on SQLite | Yes | Each worktree has a different `ProjectRoot` → different `.shaktiman/` → each gets its own leader. |
| **D** — team on Postgres | Yes | Each daemon calls `EnsureProject` with its path. Same Postgres, different `project_id`. |
| **E** — branch switch in one worktree | Yes | Leader's `BranchSwitchCh` fires → re-index. All clients (stdio + proxies) see updated results immediately through the same `QueryEngine`. |
| **F** — two+ sessions, same project | **Yes** | First = leader, subsequent = proxies. All queries route to one daemon. Zero stale reads, zero double-enrichment, zero double-embedding. |
| **CLI (`cmd/shaktiman/`) concurrent with daemon** | **Caveat** | CLI opens its own DB connection via `daemon.New(cfg)` or `openStore(cfg)`. Read-only commands (search, status, symbols) are safe under WAL. The `index` subcommand starts its own `WriterManager` → races with the leader. Fix: CLI checks flock before indexing; if held, refuse with "daemon is running, use it for indexing." |
| **Ephemeral agent worktrees** | **Pre-existing gap** | Subagent inherits parent's MCP stdio pipe → queries go to main repo's leader → main-branch results. Not worse than today. Document: "ephemeral worktree subagents use grep/read directly; MCP tools reflect the main branch." |

### A4.9 Codebase changes (shape, not code)

```
cmd/shaktimand/main.go
    ├─ NEW: acquireInstanceLock()
    │       canonicalize ProjectRoot, flock on daemon.pid
    ├─ NEW: runAsProxy()
    │       detect socket, dial, bridge stdio ↔ HTTP
    ├─ CHANGED: on leader, create Unix socket listener,
    │           pass to daemon for StreamableHTTPServer
    └─ CHANGED: on leader exit, unlink daemon.sock

internal/daemon/daemon.go
    ├─ CHANGED: Start() accepts optional net.Listener
    │           for socket clients (alongside existing stdio)
    └─ CHANGED: Stop() closes the socket listener

internal/mcp/server.go
    └─ CHANGED: NewServer returns both *mcpserver.MCPServer
               and the tool registrations, so the leader can
               serve the same MCPServer on both transports

internal/proxy/bridge.go (NEW, ~80 LOC)
    └─ stdio ↔ HTTP bridge: read JSON-RPC from stdin,
       POST to Unix socket, write response to stdout

cmd/shaktiman/main.go (CLI)
    └─ CHANGED: index subcommand checks flock before
       opening WriterManager; refuses if daemon is running
```

**Nothing changes in:** `internal/storage/`, `internal/vector/`, `internal/core/`, `internal/types/`.

### A4.10 Comparison with alternatives

| Property | D1′ refuse-to-start | per-session DB | leader/follower | **D1″ single-daemon + proxy** |
|---|---|---|---|---|
| Second session gets MCP tools | No | Yes | Yes | **Yes** |
| Zero stale reads | n/a | No | No | **Yes** |
| Zero double-enrichment | Yes | No | Yes | **Yes** |
| Zero double-embedding | Yes | No | Yes | **Yes** |
| Zero silent races | Yes | Yes | No | **Yes** |
| One source of truth | Yes | No | Partial | **Yes** |
| Backend-agnostic | Yes | No | Yes | **Yes** |
| Lines of new code | ~50 | ~800+ | ~500 | **~250-350** |
| Schema changes | 0 | 0 | 0 | **0** |
| Interface changes | 0 | 0 | 0 | **0** |

### A4.11 Amended phasing (replaces A3.5)

| Phase | Scope | Files | LOC | Risk | Dep |
|---|---|---|---|---|---|
| **P1** (ship first) | D1′ flock + refuse-to-start | `cmd/shaktimand/main.go` | ~50 | Low | None |
| **P2** | D1″ leader socket listener | `internal/daemon/daemon.go`, `internal/mcp/server.go` | ~100 | Medium | P1 |
| **P3** | D1″ proxy bridge + promotion | `cmd/shaktimand/main.go`, new `internal/proxy/bridge.go` | ~120 | Medium | P2 |
| **P4** | CLI flock check | `cmd/shaktiman/main.go` (`index` subcommand) | ~20 | Low | P1 |
| **P5** (optional) | Worktree bootstrap via reflink clone | `internal/daemon/daemon.go` | ~30-50 | Low | — |
| **P6** (docs only) | Document Postgres multi-worktree-as-project, ephemeral worktree limitations | `CLAUDE.md`, README | ~0 code | None | — |

P1 is the stepping stone: it closes the safety gap immediately. P2+P3 upgrade the UX so that the second session works instead of being refused. P4 is a small hardening fix for the CLI. P5 and P6 are independent.

**Ordering rationale:** P1 ships fast and makes the second daemon safe (by blocking it). P2 is the leader-side socket server — no user-visible change yet, but the infrastructure exists. P3 adds the proxy and leader handoff — this is when users see "second session just works." P4 is a small independent fix.

### A4.12 FMEA for D1″

| # | Failure Mode | Sev | Occ | Det | RPN | Mitigation |
|---|---|---|---|---|---|---|
| F1″ | Leader exits uncleanly → stale `daemon.sock` on disk | 4 | 3 | 2 | 24 | Proxy detects connection-refused when dialing stale socket; retries flock; promotes if acquired. Socket unlinked by new leader on startup if flock succeeds. |
| F2″ | Proxy connects before leader's `StreamableHTTPServer` is ready | 3 | 3 | 2 | 18 | Proxy retries connection with backoff (100ms, 200ms, 500ms, 1s, 2s) before failing. Leader creates socket listener before starting MCP server. |
| F3″ | flock on NFS/SMB → silently advisory | 6 | 2 | 6 | 72 | Documented as local-filesystem only. Error message mentions this. Same as D1′. |
| F4″ | `ProjectRoot` not canonicalized → two leaders | 7 | 2 | 2 | 28 | Canonicalize via `filepath.EvalSymlinks` before lock path. Same as D1′. |
| F5″ | CLI `index` subcommand races with leader's writer | 5 | 3 | 3 | 45 | P4: CLI checks flock; refuses to index if daemon holds it. Read-only CLI commands are safe. |
| F6″ | Leader hangs (holds flock, socket unresponsive) | 5 | 2 | 5 | 50 | Proxies return errors to their Claude Code clients. No auto-promotion while flock held. User kills the wedged process. |

All RPN values are below the original plan's worst (F2=112, F14=100, F6=84). The highest new risk is F3″ (NFS flock, RPN 72) — same mitigation as before.

### A4.13 Open questions (updated from A3.6)

1. ~~Should `shaktimand` expose a socket/named-pipe for MCP client reuse?~~ **Answered: yes, via `StreamableHTTPServer` on `.shaktiman/daemon.sock`.**
2. For team/Postgres deployments, should `shaktimand` detect git worktrees automatically and call `EnsureProject` with the worktree path? Or is documenting it sufficient? **Still open — documentation-first for now (P6).**
3. Is cold-indexing a fresh worktree actually slow enough to justify P5 (reflink clone)? **Still open — measurement needed.**
4. **NEW:** Should the proxy support a `--timeout` flag for how long to wait for the leader's socket to become available before failing? Default proposal: 5 seconds with exponential backoff.
5. **NEW:** Should the leader's `StreamableHTTPServer` bind to `127.0.0.1:<port>` as a fallback for platforms where Unix sockets are unavailable (Windows)? Deferred until Windows demand exists.
