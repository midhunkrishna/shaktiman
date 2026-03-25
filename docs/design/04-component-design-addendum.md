# Component Design — Addendum

> Addresses 20 findings from component design critique.
> These amendments are part of the component design specification.

---

## CA-1: Symbol Name → ID Resolution

**Problem:** C7 produces `EdgeRecord` with symbol names. C8/C3 need numeric IDs. Cross-file edges may reference symbols not yet indexed.

**Fix: Two-phase edge resolution in Writer Thread.**

```
EDGE RESOLUTION (inside Writer Thread write transaction):

  Phase 1: Resolve local edges
    For each EdgeRecord in the write job:
      src_id = lookup_symbol_id(src_symbol_name, file_id)  // same file — always exists
      dst_id = lookup_symbol_id(dst_symbol_name, file_id)  // try same file first
      if dst_id found → insert edge immediately

  Phase 2: Resolve cross-file edges
    For unresolved dst_symbol_name:
      dst_id = lookup_symbol_id_global(dst_symbol_name)    // search all files by name
      if dst_id found → insert edge
      if NOT found → insert into pending_edges table:
        pending_edges (src_symbol_id, dst_symbol_name, kind, created_at)

  Phase 3: Resolve pending (on every file enrichment write)
    After inserting symbols for a new file:
      SELECT * FROM pending_edges WHERE dst_symbol_name IN (newly inserted symbol names)
      For each match: insert resolved edge, delete from pending_edges

  Cleanup:
    pending_edges older than 24 hours are pruned (target symbol likely doesn't exist)

Schema addition:
  pending_edges (
    id INTEGER PRIMARY KEY,
    src_symbol_id INTEGER NOT NULL,
    dst_symbol_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at TEXT NOT NULL
  )
  CREATE INDEX idx_pending_name ON pending_edges(dst_symbol_name);
```

**EdgeRecord type updated:**

```
EdgeRecord {
  src_symbol_name: String,     // resolved to ID in Writer Thread Phase 1
  dst_symbol_name: String,     // resolved to ID in Writer Thread Phase 2/3
  kind: EdgeKind,
  is_cross_file: bool,         // hint for Phase 2 resolution
}
```

**EdgeBatch type defined (was missing from Section 11.1):**

```
EdgeBatch {
  additions: Vec<(SymbolId, SymbolId, EdgeKind)>,   // resolved IDs
  deletions: Vec<(SymbolId, SymbolId)>,
}
```

---

## CA-2: Query-Time Enrichment — Ephemeral Chunk IDs

**Problem:** Query-time enrichment returns chunks to C2 directly, but these chunks have no SQLite-assigned IDs. The Retrieval/Ranker/Assembler pipeline operates on `ChunkId`.

**Fix: Negative synthetic IDs for ephemeral chunks.**

```
EPHEMERAL ID SCHEME:

  SQLite auto-increment IDs are positive integers (1, 2, 3, ...).
  Query-time enrichment results use NEGATIVE IDs: -1, -2, -3, ...

  C7.enrich_file(is_query_time=true) returns:
    EnrichmentResult {
      chunks: Vec<ChunkRecord>,    // IDs assigned as -1, -2, -3, ...
      symbols: Vec<SymbolRecord>,
      edges: Vec<EdgeRecord>,
    }

  C2 merges ephemeral chunks into the retrieval candidate set:
    • Ephemeral chunks participate in ranking normally
    • semantic_score = 0 (no embedding exists yet)
    • structural_score = 0 (no graph edges yet)
    • keyword_score = computed via in-memory FTS (not SQLite FTS5)
    • change_score = 0 (no diff history)
    • session_score = 0

  Context Assembler:
    • Handles negative IDs transparently
    • Chunk content is available (it's in the EnrichmentResult)
    • token_count pre-computed during enrichment

  After query completes:
    • WriteJob enqueued to Writer Thread (P0)
    • SQLite inserts assign real IDs
    • Ephemeral IDs are never persisted

  Embedding:
    • Embedding queue entries use a (path, chunk_index) tuple instead of ChunkId
    • Embedding Worker resolves to real ChunkId after Writer Thread commits
    • Resolution: query C5 by (file_path, start_line, end_line)
```

---

## CA-3: Write Ordering Race — Hash Guard

**Problem:** P2 (watcher) write can overwrite newer P0 (query-time) write for the same file.

**Fix: Content hash guard in Writer Thread.**

```
WRITER THREAD — HASH GUARD:

  Before processing any WriteJob::Enrichment:
    current_hash = SELECT content_hash FROM files WHERE path = ?
    incoming_hash = job.file_record.content_hash

    if current_hash == incoming_hash:
      // File already indexed with this content — skip entire job
      // (Another enrichment already processed the same version)
      discard(job)
      return

    if current_hash IS NOT NULL:
      current_indexed_at = SELECT indexed_at FROM files WHERE path = ?
      if current_indexed_at > job.timestamp:
        // A newer version was already indexed — skip this stale job
        discard(job)
        return

  Proceed with normal write if hash is different and timestamp is newer.

COALESCING (enhanced):
  Burst coalescing already keeps only the latest P2 job per file_id.
  Hash guard adds a second safety layer at write time.
  Together: P0 writes commit first, P2 writes for same file are discarded.
```

---

## CA-4: Session Store — Full Component Specification

**Problem:** Session Store is listed as "cross-cutting" but lacks full specification. In-memory structures (LRU, decay HashMap) and ownership are undefined.

**Fix: Full specification as a named module.**

```
SESSION STORE

PURPOSE:
  Track agent access patterns, compute session scores, maintain working set.
  Prevent filter bubbles via exploration decay.

OWNER: C2 (Query Engine) owns the Session Store module.
  • Initialized at startup
  • Read during query scoring
  • Updated after each query response
  • Periodic flush to SQLite via Writer Thread

DATA STRUCTURES (in-memory):

  1. access_lru: LRU<ChunkId, AccessEntry>
     capacity: 10,000 entries
     AccessEntry { access_count: u32, last_accessed: Timestamp, operations: Vec<AccessOp> }
     Eviction: LRU (least recently accessed entries evicted first)

  2. decay_map: HashMap<ChunkId, u32>
     Maps ChunkId → queries_since_last_hit
     Updated on every query (in-memory, fast)
     Flushed to SQLite every 30 seconds OR every 10 queries (A10)

  3. working_set: HashSet<ChunkId>
     Derived from access_lru: chunks accessed ≥2 times in current session
     Recomputed after each query

OPERATIONS:

  record_access(chunk_ids: &[ChunkId], operation: AccessOp):
    for id in chunk_ids:
      access_lru.insert_or_update(id, |entry| {
        entry.access_count += 1
        entry.last_accessed = now()
      })
      decay_map.insert(id, 0)    // reset decay for accessed chunks
    recompute_working_set()

  get_session_scores(candidate_ids: &[ChunkId]) -> Map<ChunkId, f32>:
    for id in candidate_ids:
      if let Some(entry) = access_lru.get(id):
        decay = decay_map.get(id).unwrap_or(0)
        score = min(entry.access_count as f32 / 5.0, 1.0)
              * 0.9_f32.powi(decay as i32)
        result.insert(id, score)
      else:
        result.insert(id, 0.0)

  update_decay():
    // Called after each query
    let result_chunk_ids = current_query_results   // set by Retrieval Engine
    for (chunk_id, count) in &mut decay_map:
      if !result_chunk_ids.contains(chunk_id):
        *count += 1

  flush_to_sqlite():
    // Periodic: every 30s or every 10 queries
    let access_events: Vec<AccessEvent> = access_lru.drain_recent()
    let decay_flush: Vec<(ChunkId, u32)> = decay_map.iter().collect()
    writer_thread.enqueue(WriteJob::SessionUpdate {
      access_events,
      decay_flush: Some(decay_flush),
    }, P1)

STARTUP:
  1. Load last session's working_set from SQLite
  2. Populate access_lru with recent access_log entries (last session)
  3. Populate decay_map from working_set.queries_since_last_hit
  // If SQLite data is lost (crash, reindex), start with empty state — acceptable

CRASH RECOVERY:
  At most 30 seconds of session data lost.
  access_lru and decay_map are ephemeral — rebuilt from SQLite on restart.
  Partial loss is acceptable (session data is a ranking hint, not critical state).

WORKING SET SHIFT DETECTION (for push mode):
  After recompute_working_set():
    new_chunks = working_set - previous_working_set
    if new_chunks.len() > previous_working_set.len() * 0.30:
      notify(resource_update_channel, ResourceUpdateEvent::SessionShift)
    previous_working_set = working_set.clone()
```

---

## CA-5: Context Assembler — Oversized Chunk Handling

**Problem:** If all candidate chunks exceed `primary_budget`, the assembler returns an empty package silently.

**Fix: Truncation fallback + two-pass budget reclaim.**

```
ASSEMBLER ALGORITHM (revised):

  1. PRIMARY SELECTION (70% budget):
     for chunk in ranked_chunks:
       if line_overlap(chunk, selected) > 0.50 → skip
       if chunk.token_count <= remaining_primary:
         add chunk; subtract tokens
       elif chunk == ranked_chunks[0] && selected.is_empty():
         // FALLBACK: truncate the #1 chunk to fit budget
         truncated = truncate_to_tokens(chunk, remaining_primary)
         add truncated; subtract tokens
         truncated.metadata.note = "truncated to fit budget"

  2. STRUCTURAL EXPANSION (30% budget):
     for chunk in selected:
       neighbors = C3.bfs(chunk.symbol_id, depth=2, max=5)
       for neighbor in neighbors:
         if neighbor.token_count <= remaining_expansion && !already_selected:
           add neighbor; subtract from expansion_budget

  3. BUDGET RECLAIM (second pass):
     reclaimed = remaining expansion_budget
     if reclaimed > 0:
       for chunk in ranked_chunks (continuing where primary left off):
         if chunk.token_count <= reclaimed && !already_selected:
           add chunk; subtract from reclaimed

  4. METADATA ATTACHMENT (unchanged)

  INVARIANT: If candidates exist, the package always contains ≥1 chunk.
```

---

## CA-6: CSR Compaction — Safe Delta Clear

**Problem:** Clearing delta buffer after CSR build may discard edges committed during the build window.

**Fix: Snapshot-based clear.**

```
compact():
  snapshot_len = delta_buffer.len()        // snapshot BEFORE build
  new_csr = build_csr_from_sqlite()        // ~50-120ms
  atomic_swap(csr, new_csr)
  delta_buffer.drain(0..snapshot_len)       // clear only pre-snapshot entries
                                            // entries appended during build are preserved
  csr_state = READY
```

---

## CA-7: Startup Boot Sequence

**Problem:** No defined startup order. Queries arriving before stores are ready cause panics.

**Fix: Explicit boot sequence with readiness gates.**

```
STARTUP SEQUENCE:

  Phase 1: STORAGE
    1. Open SQLite database (WAL mode)
    2. Run schema migrations if needed
    3. Start Writer Thread (begin accepting write jobs)

  Phase 2: INDEX STORES
    4. Initialize Metadata Store (reader connection pool)
    5. Initialize Vector Store (brute-force default; see FD-3)
    6. Initialize Diff Store
    7. Start CSR build (background — C3; **deferred to Phase 3+** per FD-4)
    8. Initialize Session Store (load from SQLite)

  Phase 3: BACKGROUND WORKERS
    9. Start Embedding Worker (circuit breaker startup health check)
    10. Start File Watcher (C9 — begins emitting events)
    11. Start Enrichment Pipeline (C7 — begins consuming events)

  Phase 4: INTERFACE (last)
    12. Start MCP Server (begin accepting connections)
    13. Start CLI readiness (respond to commands)

  READINESS GATE:
    C1 (MCP Server) does NOT start until Phase 1-3 complete.
    If a query arrives before C3 CSR is READY:
      → structural scoring uses SQLite BFS fallback (already designed)
    If a query arrives before any files are indexed:
      → Level 3 (filesystem passthrough) — already designed

  SHUTDOWN SEQUENCE:
    1. Stop accepting new MCP connections
    2. Drain in-flight queries (max 5s timeout)
    3. Stop File Watcher
    4. Flush Session Store to SQLite
    5. Drain Writer Thread queue (max 10s timeout)
    6. Stop Embedding Worker
    7. Close SQLite connections
```

---

## CA-8: Channel Overflow Behavior

**Problem:** Overflow behavior undefined for 3 of 5 channels.

**Fix: Define overflow strategy per channel.**

```
CHANNEL OVERFLOW STRATEGIES:

  file_change_tx (capacity: 10,000):
    Overflow strategy: DROP_OLDEST
    If channel full, drop oldest events.
    Rationale: stale events are less useful. Periodic scan (5 min) catches dropped changes.
    Log: warn every 1000 dropped events.

  embed_queue_tx (capacity: 100,000):
    Overflow strategy: DROP_LOWEST_PRIORITY
    If channel full, discard P4 (FIFO un-embedded) entries to make room.
    P1-P3 entries are always accepted.
    Dropped P4 entries will be re-discovered by periodic scan or re-queued on model change.

  branch_switch_tx (capacity: 10):
    Overflow strategy: COALESCE
    If channel full, the latest event replaces the oldest.
    Multiple branch switches in rapid succession → only the latest matters.

  resource_update_tx (capacity: 100):
    Overflow strategy: COALESCE
    Debounce in C1 (500ms) already limits event rate.
    If overflow: latest event wins.

  write_job_tx (capacity: 5,000):
    Overflow strategy: BACK_PRESSURE (already defined in A1)
    P3 pauses at depth >1000. Never drops jobs.
    If capacity reached despite back-pressure: block sender (enrichment worker waits).
```

---

## CA-9: Stale Chunk/Embedding ID References

**Problem:** `stale_chunk_ids` and `EmbedQueueEntry.chunk_id` may reference IDs that no longer exist by the time the Writer Thread / Embedding Worker processes them.

**Fix: Defensive operations.**

```
WRITER THREAD — DEFENSIVE EMBEDDING INVALIDATION:

  invalidate_embeddings(stale_ids):
    DELETE FROM embeddings WHERE chunk_id IN (stale_ids)
    // If chunk_id doesn't exist → DELETE is a no-op (not an error)
    // SQLite handles this gracefully

EMBEDDING WORKER — DEFENSIVE INSERT:

  Before writing embedding:
    EXISTS = SELECT 1 FROM chunks WHERE id = ?
    If NOT EXISTS → discard embedding, log debug
    // This happens when chunk was replaced between queue and processing
    // It is expected and harmless — the new chunk will be queued separately

EMBED QUEUE ENTRY (revised):
  EmbedQueueEntry {
    file_path: String,       // stable identifier
    chunk_index: u32,        // position within file (0, 1, 2, ...)
    content: String,         // for embedding (no DB lookup needed)
    priority: EmbedPriority,
  }

  Embedding Worker resolution:
    After generating embedding:
      chunk_id = SELECT id FROM chunks
                 WHERE file_id = (SELECT id FROM files WHERE path = ?)
                 ORDER BY start_line LIMIT 1 OFFSET ?
    If resolved → enqueue WriteJob::EmbeddingInsert { chunk_id, vector }
    If not resolved → discard (chunk was replaced)
```

---

## CA-10: parent_chunk_id Resolution

**Problem:** Nested chunks (methods inside classes) need `parent_chunk_id` pointing to the class chunk, but IDs aren't known until SQLite insert.

**Fix: Positional index within the batch.**

```
WRITER THREAD — PARENT RESOLUTION:

  Chunks in a WriteJob are ordered: parent chunks first, then nested children.

  C7 (Chunk Splitter) assigns a batch-local index:
    chunks[0] = class Foo          parent_index: None
    chunks[1] = Foo.method_a       parent_index: Some(0)
    chunks[2] = Foo.method_b       parent_index: Some(0)
    chunks[3] = function bar       parent_index: None

  Writer Thread:
    id_map: Vec<ChunkId> = Vec::new()    // maps batch index → SQLite ID

    for (i, chunk) in chunks.enumerate():
      parent_id = chunk.parent_index.map(|idx| id_map[idx])
      inserted_id = INSERT INTO chunks (..., parent_chunk_id = parent_id) RETURNING id
      id_map.push(inserted_id)

  ChunkRecord type updated:
    ChunkRecord {
      ...
      parent_index: Option<usize>,    // batch-local, not parent_chunk_id
    }
```

---

## CA-11: Resource Update Trigger Sources

**Problem:** `resource_update_tx/rx` lists C8 and C9 as producers, but Session Store (working set shift) and internal timer (60s idle) are missing.

**Fix:**

```
RESOURCE UPDATE CHANNEL (revised):

  resource_update_tx/rx: C8, C9, Session Store, Timer → C1
  capacity: 100 events

  Producers:
    C8 (Writer Thread):     after enrichment commit → ResourceUpdateEvent::FileChanged
    C9 (Monitor):           branch switch detected  → ResourceUpdateEvent::BranchSwitch
    Session Store (in C2):  working set shift >30%  → ResourceUpdateEvent::SessionShift
    Internal timer (in C1): every 60s idle          → ResourceUpdateEvent::IdleRefresh

  ResourceUpdateEvent {
    kind: FileChanged | BranchSwitch | SessionShift | IdleRefresh,
    timestamp: Timestamp,
    affected_paths: Option<Vec<String>>,    // for FileChanged
  }

  C1 Resource Manager:
    Debounce: 500ms from last event
    Notification (context/changed): 3s min interval
    Notification payload: { event: "branch_switch" | "file_change" | "session_shift" }
```

---

## Summary

| # | Finding | Fix | Severity |
|---|---|---|---|
| CA-1 | Symbol name→ID resolution | Two-phase resolution + pending_edges table | HIGH |
| CA-2 | Query-time chunks have no IDs | Negative synthetic IDs, ephemeral lifetime | HIGH |
| CA-3 | P2 write overwrites newer P0 | Content hash guard in Writer Thread | HIGH |
| CA-4 | Session Store unspecified | Full module spec with in-memory structures | HIGH |
| CA-5 | Assembler empty on oversized chunks | Truncation fallback + two-pass reclaim | MEDIUM |
| CA-6 | CSR compaction loses delta entries | Snapshot-based drain | MEDIUM |
| CA-7 | Startup order undefined | Explicit 4-phase boot sequence | MEDIUM |
| CA-8 | Channel overflow undefined | Per-channel overflow strategies | MEDIUM |
| CA-9 | Stale chunk/embed ID references | Defensive operations + file-based embed queue | MEDIUM |
| CA-10 | parent_chunk_id resolution | Batch-local positional index | LOW |
| CA-11 | Resource update sources incomplete | Revised channel with all 4 producers | LOW |
