# Data Model — Addendum

> Addresses findings from the data model critique round (reviewer + adversarial-analyst).
> These amendments are part of the data model specification.

---

## DM-1: FTS5 External Content Sync Triggers

**Problem:** FTS5 with `content=chunks, content_rowid=id` is external-content mode. SQLite does NOT auto-sync. Without triggers, keyword search returns stale/phantom results after any chunk insert or delete.

**Fix: Explicit sync triggers + cold index disable.**

```sql
-- FTS5 SYNC TRIGGERS (active during normal operation)

CREATE TRIGGER chunks_fts_insert AFTER INSERT ON chunks BEGIN
  INSERT INTO chunks_fts(rowid, content, symbol_name)
  VALUES (new.id, new.content, new.symbol_name);
END;

CREATE TRIGGER chunks_fts_delete AFTER DELETE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
  VALUES ('delete', old.id, old.content, old.symbol_name);
END;

CREATE TRIGGER chunks_fts_update AFTER UPDATE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, content, symbol_name)
  VALUES ('delete', old.id, old.content, old.symbol_name);
  INSERT INTO chunks_fts(rowid, content, symbol_name)
  VALUES (new.id, new.content, new.symbol_name);
END;
```

```
COLD INDEX FTS5 STRATEGY (A11 compatibility):

  Before cold index:
    DROP TRIGGER chunks_fts_insert;
    DROP TRIGGER chunks_fts_delete;
    DROP TRIGGER chunks_fts_update;

  After cold index:
    INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild');   -- full rebuild
    Re-create the 3 triggers above.

  During cold index:
    FTS5 returns empty results.
    HybridRanker redistributes keyword weight (0.10):
      sem += 0.05, struct += 0.03, change += 0.02
    This is already handled by the weight redistribution mechanism.
```

---

## DM-2: Stable Chunk Identity for Session & Embedding References

**Problem:** Chunk IDs are volatile — DELETE + re-INSERT on every file enrichment assigns new auto-increment IDs. This silently destroys:
- Session store entries (access_log, working_set) via CASCADE
- In-memory session LRU/decay keys
- Embedding worker resolution (chunk_id may point to wrong chunk)

**Fix: Use stable `(file_path, start_line, end_line)` keys for session tracking. Add content-hash guard for embedding resolution.**

```
SESSION STORE — STABLE KEYS:

  Problem: session tables use chunk_id FK → invalidated on every re-enrichment.

  Fix: working_set and access_log use (file_path, chunk_index) as stable identity.

  REVISED SCHEMA:

    working_set (
      session_id       TEXT NOT NULL,
      file_path        TEXT NOT NULL,         -- stable across re-enrichment
      chunk_index      INTEGER NOT NULL,      -- positional order within file
      access_count     INTEGER NOT NULL DEFAULT 1,
      last_accessed    TEXT NOT NULL,
      queries_since_last_hit INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (session_id, file_path, chunk_index)
    );

    access_log (
      id               INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id       TEXT NOT NULL,
      timestamp        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
      file_path        TEXT NOT NULL,         -- stable across re-enrichment
      chunk_index      INTEGER NOT NULL,
      operation        TEXT NOT NULL
        CHECK (operation IN ('search_hit', 'context_include', 'direct_read'))
    );
    CREATE INDEX idx_access_session ON access_log(session_id, timestamp);
    CREATE INDEX idx_access_file ON access_log(file_path, chunk_index);

  SESSION SCORE RESOLUTION (at query time):
    To compute session_score for a candidate chunk:
      1. Read chunk.file_id → files.path, chunk.chunk_index
      2. Lookup working_set WHERE file_path = ? AND chunk_index = ?
      3. If found → compute score from access_count and queries_since_last_hit
      4. If not found → session_score = 0.0

    This adds one JOIN per candidate but is O(1) with the PK index.

  IN-MEMORY SESSION STORE (revised CA-4):
    access_lru:   LRU<(String, u32), AccessEntry>     -- key = (file_path, chunk_index)
    decay_map:    HashMap<(String, u32), u32>          -- key = (file_path, chunk_index)
    working_set:  HashSet<(String, u32)>               -- stable across re-enrichment

    These survive re-enrichment because file_path and chunk_index
    are stable when the file content doesn't change chunk boundaries.
    When chunk boundaries DO change (new function added), the old
    (file_path, old_chunk_index) entries age out via exploration decay.
```

```
EMBEDDING WORKER — CONTENT HASH GUARD:

  Problem: OFFSET-based resolution can map embedding to wrong chunk
  when chunk ordering changes between embed generation and resolution.

  Fix: Content hash comparison after resolution.

  EmbedQueueEntry (revised):
    file_path:    String,
    chunk_index:  u32,
    content:      String,       -- for embedding generation
    content_hash: String,       -- SHA-256 of content (for validation)
    priority:     EmbedPriority,

  Embedding Worker resolution (revised):
    After generating embedding vector:
      chunk = SELECT id, content FROM chunks
              WHERE file_id = (SELECT id FROM files WHERE path = ?)
              AND chunk_index = ?

      if chunk IS NULL → discard (chunk was replaced)
      if sha256(chunk.content) != entry.content_hash → discard (stale)
      else → enqueue WriteJob::EmbeddingInsert { chunk_id: chunk.id, vector }

  This eliminates silent wrong-embedding attachment entirely.
```

---

## DM-3: CASCADE Delete Strategy — Controlled Blast Radius

**Problem:** CASCADE delete on file deletion bypasses the CSR delta buffer. Edges are deleted by SQLite cascades but the in-memory graph doesn't learn about them. CSR accumulates phantom edges.

**Fix: Replace file-level CASCADE on edges with explicit Writer Thread cleanup. Keep CASCADE for containment (chunks, symbols) but handle edge cleanup in application code.**

```
REVISED CASCADE STRATEGY:

  files:
    chunks.file_id → ON DELETE CASCADE           -- KEEP: chunks belong to file
    diff_log.file_id → ON DELETE SET NULL         -- CHANGED: preserve diff history (DM-5)
    symbols.file_id → ON DELETE CASCADE           -- KEEP: symbols belong to file

  chunks:
    symbols.chunk_id → ON DELETE CASCADE          -- KEEP: symbols belong to chunk
    chunks.parent_chunk_id → ON DELETE SET NULL   -- KEEP: orphan nested chunks gracefully

  symbols:
    edges.src_symbol_id → ON DELETE RESTRICT      -- CHANGED: prevent cascade
    edges.dst_symbol_id → ON DELETE RESTRICT      -- CHANGED: prevent cascade
    diff_symbols.symbol_id → ON DELETE SET NULL   -- KEEP: preserve diff history
    pending_edges.src_symbol_id → ON DELETE CASCADE -- KEEP: pending edge meaningless without src

  WHY RESTRICT on edges:
    The Writer Thread must explicitly delete edges BEFORE deleting symbols.
    This ensures the CSR delta buffer receives deletion entries for every edge removed.

  WRITER THREAD — FILE ENRICHMENT SEQUENCE (revised):

    begin_transaction()

    -- Step 1: Collect edges to delete (for CSR notification)
    stale_edges = SELECT src_symbol_id, dst_symbol_id
                  FROM edges
                  WHERE file_id = ?

    -- Step 2: Delete edges explicitly
    DELETE FROM edges WHERE file_id = ?

    -- Step 3: Now safe to delete old chunks/symbols (CASCADE handles the rest)
    DELETE FROM chunks WHERE file_id = ?
    -- CASCADE: symbols for this file deleted, which is safe because edges already gone

    -- Step 4: Insert new data
    INSERT INTO chunks ...
    INSERT INTO symbols ...
    INSERT INTO edges ...   (new edges from current parse)

    commit_transaction()

    -- Step 5: Post-commit CSR update
    graph_module.update(EdgeBatch {
      deletions: stale_edges,
      additions: new_edges,
    })

  FILE DELETION SEQUENCE:
    Same pattern: explicitly collect and delete edges first,
    then delete the file row (CASCADE handles chunks/symbols).
```

---

## DM-4: Missing UNIQUE Constraints

**Problem:** Logical unique identifiers declared in entity descriptions but not enforced in DDL. Allows silent duplicates that break edge resolution (CA-1) and embedding resolution (CA-9).

**Fix: Add UNIQUE constraints.**

```sql
-- Chunks: stable reference for session store and embedding resolution
ALTER TABLE chunks ADD CONSTRAINT uq_chunks_file_index
  UNIQUE (file_id, chunk_index);

-- Symbols: logical identity for edge resolution
ALTER TABLE symbols ADD CONSTRAINT uq_symbols_identity
  UNIQUE (file_id, name, kind);

-- Diff symbols: one entry per affected symbol per diff
ALTER TABLE diff_symbols ADD CONSTRAINT uq_diffsym_identity
  UNIQUE (diff_id, symbol_name);

-- Note: SQLite doesn't support ALTER TABLE ADD CONSTRAINT.
-- These must be in the original CREATE TABLE statements:

CREATE TABLE chunks (
  ...
  UNIQUE (file_id, chunk_index)
);

CREATE TABLE symbols (
  ...
  UNIQUE (file_id, name, kind)
);

CREATE TABLE diff_symbols (
  ...
  UNIQUE (diff_id, symbol_name)
);
```

---

## DM-5: Diff History Preservation for Deleted Files

**Problem:** `diff_log.file_id ON DELETE CASCADE` destroys all change history when a file is deleted. The `diff()` MCP tool cannot answer "what was recently deleted?" Also, file renames (delete + add) lose all diff history for the original file.

**Fix: Change CASCADE to SET NULL. Add file_path denormalization.**

```sql
-- REVISED diff_log schema:

CREATE TABLE diff_log (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id          INTEGER REFERENCES files(id) ON DELETE SET NULL,  -- CHANGED
  file_path        TEXT NOT NULL,              -- NEW: denormalized, survives file deletion
  timestamp        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  change_type      TEXT NOT NULL
    CHECK (change_type IN ('add', 'modify', 'delete', 'rename')),
  lines_added      INTEGER NOT NULL DEFAULT 0,
  lines_removed    INTEGER NOT NULL DEFAULT 0,
  magnitude        INTEGER GENERATED ALWAYS AS (lines_added + lines_removed) STORED,
  hash_before      TEXT,
  hash_after       TEXT,
  old_path         TEXT                        -- NEW: for rename tracking
);
```

```
REVISED BEHAVIOR:

  File deletion:
    1. INSERT diff_log { file_id, file_path, change_type='delete', ... }
    2. INSERT diff_symbols for each removed symbol
    3. DELETE FROM files WHERE id = ?
       → file_id in diff_log becomes NULL (SET NULL)
       → diff_log row preserved with file_path for queryability

  File rename:
    1. Detect: file A deleted + file B added with same content_hash within same batch
    2. INSERT diff_log { file_id=B.id, file_path=B.path, change_type='rename', old_path=A.path }
    3. UPDATE existing diff_log rows: SET file_id=B.id WHERE file_path=A.path AND file_id IS NULL
       → Previous diff history now linked to the new file ID
    4. No need to re-enrich — content unchanged, just path updated

  Query for deleted files:
    SELECT * FROM diff_log WHERE change_type = 'delete'
    AND file_path LIKE '%checkout%'
    ORDER BY timestamp DESC
    → Works even after file row is gone (file_path preserved)
```

---

## DM-6: `exports` Edge Kind Resolution

**Problem:** `exports` is defined as "Symbol A → File B" but `edges.dst_symbol_id` references `symbols(id)`, not files. This edge kind is unimplementable in the current schema.

**Fix: Redefine `exports` as Symbol → Module Symbol, and use `symbols.is_exported` for the simple case.**

```
EXPORTS EDGE KIND (revised):

  Option chosen: Derive export status from symbols.is_exported flag.
  Drop 'exports' from the edges table entirely.

  Rationale:
    • "What does this module export?" → SELECT * FROM symbols WHERE file_id = ? AND is_exported = 1
    • "Who uses this export?" → already captured by 'imports' edges (reverse direction)
    • The 'exports' edge would be redundant with the is_exported flag + imports edges

  REVISED EDGE KINDS:
    'imports' | 'calls' | 'type_ref' | 'extends' | 'implements'
    (5 kinds, not 6)

  REVISED CHECK CONSTRAINT:
    edges.kind CHECK (kind IN ('imports', 'calls', 'type_ref', 'extends', 'implements'))

  CSR edge_kind_enum mapping (revised):
    0 = imports
    1 = calls
    2 = type_ref
    3 = extends
    4 = implements
    (5 values, fits in 3 bits)
```

---

## DM-7: Symlink and Hard Link Handling

**Problem:** Symlinks/hard links can cause duplicate indexing, wasted resources, and potentially infinite loops in the watcher.

**Fix: Resolve symlinks during indexing. Detect and skip duplicates via content hash + inode.**

```
SYMLINK HANDLING:

  C9 (Monitor) — File Watcher:
    1. On event: resolve path to real path (realpath/canonicalize)
    2. Index using the REAL path, not the symlink path
    3. Maintain a symlink map: {symlink_path → real_path}
    4. If multiple symlinks point to same real file → index once

  files table:
    path column stores the REAL path (after symlink resolution)
    Symlink paths are not stored (they are ephemeral OS state)

  Hard links:
    Same inode, different paths. Detected via stat() inode comparison.
    First path encountered is indexed. Subsequent paths with same inode are skipped.

  Circular symlinks:
    realpath() detects and returns error. File is skipped, warning logged.

  Edge case: symlink target outside project directory → skip (not in project scope).
```

---

## DM-8: Index Cleanup — Remove Redundant Index

**Problem:** `files.path` has `UNIQUE NOT NULL`, which implicitly creates a unique index. The explicit `idx_files_path` is redundant.

**Fix: Remove redundant index.**

```
REMOVED:
  CREATE INDEX idx_files_path ON files(path);    -- redundant with UNIQUE constraint

KEPT: All other indexes remain necessary.
```

---

## DM-9: Missing Query Patterns for `symbols()` and `dependencies()` Tools

**Problem:** Section 7 covers 5 query patterns but omits explicit patterns for the `symbols()` and `dependencies()` MCP tools.

**Fix: Add query patterns 7.6 and 7.7.**

```
7.6 symbols(file) — "List all symbols in this file"

  NODES QUERIED:
    1. files WHERE path = ?                       → file_id
    2. symbols WHERE file_id = ?                  → all symbols
    3. chunks WHERE file_id = ?                   → line ranges for context

  SQL:
    SELECT s.name, s.kind, s.line, s.signature, s.visibility, s.is_exported,
           c.start_line, c.end_line, c.kind AS chunk_kind
    FROM symbols s
    JOIN chunks c ON c.id = s.chunk_id
    WHERE s.file_id = (SELECT id FROM files WHERE path = ?)
    ORDER BY s.line

  LATENCY: ~1ms (indexed lookups)

  TOKEN EFFICIENCY:
    Returns ~100 tokens (symbol list with signatures)
    vs ~2000 tokens for reading the entire file.
    95% token reduction.
```

```
7.7 dependencies(symbol) — "Who calls this? What does this call?"

  NODES QUERIED:
    1. symbols WHERE name = ? AND file_id = ?     → symbol_id
    2. edges WHERE src_symbol_id = symbol_id       → outgoing (callees)
    3. edges WHERE dst_symbol_id = symbol_id       → incoming (callers)
    4. symbols for each edge endpoint              → names, files, signatures

  SQL (callees):
    SELECT s.name, s.kind, f.path, s.signature, e.kind AS edge_kind
    FROM edges e
    JOIN symbols s ON s.id = e.dst_symbol_id
    JOIN files f ON f.id = s.file_id
    WHERE e.src_symbol_id = ?

  SQL (callers):
    SELECT s.name, s.kind, f.path, s.signature, e.kind AS edge_kind
    FROM edges e
    JOIN symbols s ON s.id = e.src_symbol_id
    JOIN files f ON f.id = s.file_id
    WHERE e.dst_symbol_id = ?

  ALSO (via CSR for speed):
    callers = C3.neighbors(symbol_id, direction=INCOMING)
    callees = C3.neighbors(symbol_id, direction=OUTGOING)
    → Faster than SQL for deep traversals

  LATENCY: ~3ms (CSR) or ~5ms (SQL)

  TOKEN EFFICIENCY:
    Returns ~150 tokens (caller/callee lists)
    vs 5-15 grep/read cycles × 500 tokens each = 2500-7500 tokens.
    94-98% token reduction.
```

---

## DM-10: Memory Budget Reconciliation

**Problem:** Data model memory table omits "Embedding worker buffer" (5MB) listed in architecture addendum A6.

**Fix: Add missing line item.**

```
REVISED MEMORY BUDGET (1M lines):

  Component                          │ 100K lines │ 1M lines
  ───────────────────────────────────│────────────│──────────
  SQLite page cache (8MB + 4×4MB)    │  24 MB     │  24 MB
  CSR graph                          │   0.6 MB   │  17 MB
  CSR delta buffer                   │  <0.1 MB   │  <0.1 MB
  Session LRU + decay + working set  │   0.7 MB   │   2 MB
  FTS5 auxiliary                     │   3 MB     │   8 MB
  Watcher state                      │   1 MB     │   3 MB
  Query embedding cache              │   3 MB     │   3 MB
  Embedding worker buffer            │   2 MB     │   5 MB    ← ADDED
  Writer Thread queue                │   2 MB     │   5 MB
  Per-query transient                │   3 MB     │   5 MB
  ───────────────────────────────────│────────────│──────────
  TOTAL                              │  ~39 MB    │  ~72 MB   ✓ within 100MB
```

---

## DM-11: Pending Edges — Re-creation on Source File Re-enrichment

**Problem:** When source file is re-enriched, old pending_edges are CASCADE-deleted (src_symbol_id deleted). But re-enrichment must re-produce pending_edges for still-unresolved cross-file references.

**Fix: Document explicit re-creation as part of the CA-1 enrichment flow.**

```
PENDING EDGES — COMPLETE LIFECYCLE:

  File A.ts enriched (first time or re-enrichment):
    Phase 1: Resolve local edges → insert into edges table
    Phase 2: For unresolved cross-file edges:
      → INSERT INTO pending_edges (src_symbol_id, dst_symbol_name, kind)
         using the NEW src_symbol_id from the current enrichment
      → Old pending_edges for old src_symbol_ids are CASCADE-deleted
         (old symbols deleted → CASCADE → old pending_edges deleted)
      → New pending_edges created with new src_symbol_ids
      → Net effect: pending_edges always reflect current enrichment state

  File B.ts enriched (resolves pending):
    Phase 3: After inserting symbols for B.ts:
      → Match pending_edges.dst_symbol_name against new symbol names
      → Resolve matches into real edges
      → Delete resolved pending_edges

  This is already correct by construction:
    - CA-1 Phase 2 runs on every enrichment
    - Old pending edges are implicitly cleaned by CASCADE
    - New pending edges are created from current parse
    - No explicit "re-creation" step needed — the normal enrichment flow handles it
```

---

## Summary

| # | Finding | Fix | Severity |
|---|---|---|---|
| DM-1 | FTS5 external content not synced | Explicit triggers + cold index disable/rebuild | HIGH |
| DM-2 | Chunk IDs volatile — breaks session, embeddings | Stable (file_path, chunk_index) keys + content hash guard | HIGH |
| DM-3 | CASCADE delete bypasses CSR delta buffer | RESTRICT on edges, explicit Writer Thread cleanup | HIGH |
| DM-4 | Missing UNIQUE constraints | Add to chunks, symbols, diff_symbols | HIGH |
| DM-5 | Diff history lost on file deletion/rename | SET NULL + file_path denormalization + old_path column | MEDIUM |
| DM-6 | `exports` edge kind unimplementable | Drop from edges, use symbols.is_exported flag | MEDIUM |
| DM-7 | Symlinks/hard links cause duplicates | Resolve to real path, skip duplicates via inode | MEDIUM |
| DM-8 | Redundant idx_files_path index | Remove | LOW |
| DM-9 | Missing query patterns for symbols/dependencies tools | Add patterns 7.6, 7.7 | LOW |
| DM-10 | Memory budget missing embedding worker buffer | Add 5MB line item | LOW |
| DM-11 | Pending edges re-creation on re-enrichment | Document lifecycle — already correct by construction | LOW |
