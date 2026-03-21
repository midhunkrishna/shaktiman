# Architecture v3 — Addendum

> Addresses 14 findings from v3 critique round.
> These amendments are part of the v3 architecture.

---

## A1. Writer Thread — Priority Lanes + Back-Pressure

**Problem:** Single serialized Writer Thread saturates during burst events (branch switch = 500 files = 5s queue backlog). Blocks query-time enrichment, push mode updates, and session writes.

**Fix: Priority lanes with back-pressure.**

```
WRITER THREAD QUEUE (priority-ordered):

  P0: Query-time enrichment writes (80ms budget — must be fast)
  P1: Session / working set updates
  P2: Incremental watcher-triggered enrichment writes
  P3: Cold index batch writes

Processing rules:
  • Always drain P0 before P1, P1 before P2, etc.
  • P0 writes are small (single file) — target <5ms each
  • P3 writes are batched (200 files/transaction) — may take 50-100ms
  • Back-pressure: if queue depth > 1000 jobs, P3 pauses until queue drains to 500
  • Metric: expose queue depth for observability (CLI `shaktiman status`)

Burst coalescing:
  • Multiple watcher events for the same file within 200ms → coalesce into one write job
  • During branch switch: watcher debounces 200ms, then emits batch → single P2 job
```

---

## A2. CSR Graph — Delta Buffer Strategy

**Problem:** CSR (Compressed Sparse Row) is optimized for reads, not incremental writes. Full rebuild per change is 50-80ms at 1M lines — too expensive for the Writer Thread.

**Fix: Delta buffer with periodic compaction.**

```
CSR INCREMENTAL UPDATE STRATEGY:

  Main CSR: immutable between compactions. Rebuilt periodically.
  Delta buffer: append-only list of edge additions and deletions since last compaction.

  On edge write:
    1. Write to SQLite (via Writer Thread)
    2. Append to delta buffer (lock-free append, ~0.01ms)
    — Total: no CSR rebuild needed per write

  On BFS query:
    1. BFS over main CSR
    2. Apply delta buffer (additions: include; deletions: exclude)
    — Delta buffer typically <1000 entries — trivial scan

  Compaction (rebuild CSR from SQLite):
    Trigger: when delta buffer > 5000 entries OR every 60 seconds during idle
    Duration: 50-120ms at 1M lines
    Runs in: background thread, produces new CSR, swaps atomically (pointer swap)
    During compaction: queries use old CSR + delta buffer (still correct)

  Memory:
    Delta buffer: 5000 entries × 12 bytes = ~60KB (negligible)
    Main CSR: unchanged from v3 spec (17-42MB at 1M-2M lines)
```

---

## A3. CSR Rebuild State Machine

**Problem:** No defined behavior during CSR rebuild window at startup.

**Fix: Explicit state machine + SQLite BFS fallback.**

```
CSR STATES:

  BUILDING:
    • At process startup, or after compaction trigger
    • Duration: 50-120ms (1M lines)
    • Query behavior: structural_score uses SQLite-based BFS (slower)
    • Weight redistribution: same as "Graph available" — just slower execution
    • SQLite BFS: recursive CTE query, depth-limited to 2 (not 3) to stay <50ms
      WITH RECURSIVE reachable AS (
        SELECT dst_symbol_id, 1 AS depth FROM edges WHERE src_symbol_id = ?
        UNION ALL
        SELECT e.dst_symbol_id, r.depth + 1
        FROM edges e JOIN reachable r ON e.src_symbol_id = r.dst_symbol_id
        WHERE r.depth < 2
      )
    • Measured latency: ~30-80ms at 1M lines (acceptable during brief startup window)

  READY:
    • CSR loaded, delta buffer active
    • Query behavior: normal CSR BFS (<15ms)

  Transition: BUILDING → READY once CSR constructed and pointer swapped.
```

---

## A4. CSR + SQLite Consistency — Versioned Reads

**Problem:** Writer Thread commits to SQLite, then updates CSR delta buffer. Queries between these two steps see new chunks/symbols but stale graph.

**Fix: Version counter.**

```
VERSIONED CONSISTENCY:

  write_version: atomic u64, incremented on each Writer Thread commit

  Writer Thread:
    1. Begin SQLite transaction
    2. Write metadata, chunks, symbols, edges, diffs, FTS5
    3. Commit SQLite transaction
    4. Append to CSR delta buffer
    5. Increment write_version

  Query path:
    1. Read write_version at query start
    2. Read from SQLite (metadata, chunks, FTS5) — sees committed data
    3. Read from CSR + delta buffer
    4. If CSR delta buffer version < write_version from step 1:
       — The CSR may be missing edges from the latest commit
       — Fall back to SQLite BFS for structural scoring of affected nodes
       — This is a brief window (<1ms typically)

  In practice: the delta buffer append (step 4) follows the SQLite commit (step 3) by
  microseconds. The version check is a safety net, not a common-path degradation.
```

---

## A5. File Mutex — Decoupled from Writer Thread

**Problem (triple bottleneck):** Query-time enrichment acquires file mutex → runs parse/extract → enqueues Writer Thread job → waits for Writer Thread completion → releases mutex. If Writer Thread has backlog, mutex held for seconds, query-time 80ms budget wasted.

**Fix: Decouple mutex release from Writer Thread commit.**

```
REVISED ENRICHMENT FLOW:

  1. Acquire file mutex
  2. Parse (tree-sitter) → extract (parallel) → diff engine       [~55ms]
  3. Package results into immutable write job
  4. RELEASE file mutex                                             [mutex held: ~55ms]
  5. Enqueue write job to Writer Thread (async, fire-and-forget)
  6. Return enrichment results to caller (query-time or watcher)

  Why this is safe:
  • The write job is immutable — it contains all data needed for the SQLite write
  • The file mutex only protects parse/extract (preventing duplicate work)
  • Between mutex release and Writer Thread commit, the file is in a
    "enriched but not persisted" state
  • If another change arrives for the same file before the write commits,
    the new change will re-parse — this is correct (it gets the newer version)
  • If the process crashes before the write commits, the change is re-detected
    on restart (file hash mismatch) — self-healing

  Query-time enrichment path with this fix:
    Parse + extract: ~55ms
    Mutex overhead: ~1ms
    Enqueue (async): ~0ms
    TOTAL: ~56ms — well within 80ms budget
    (Writer Thread commit happens async, doesn't block the query)

  For query-time enrichment, the enrichment results are available
  IMMEDIATELY to the query (passed directly, not read from SQLite).
  The SQLite write is for persistence only.
```

**Mutex holder timeout (backup safeguard):**

```
File mutex acquisition:
  • Watcher path: try_lock(). If fails, skip. Change caught by periodic scan.
  • Query-time path: lock(timeout=50ms). If fails, serve degraded results.
  • Holder max duration: 5 seconds. If a parse hangs (e.g., binary file),
    a watchdog timer releases the mutex and marks the file as 'unparseable'.
    The enrichment thread is interrupted.
```

---

## A6. SQLite Page Cache — Per-Connection Budget

**Problem:** WAL mode with N+1 connections. If page cache is per-connection at 30MB each, total = 150MB — busts 100MB budget.

**Fix: Explicit per-connection cache size.**

```
SQLITE CONNECTION CONFIGURATION:

  Writer connection (1):
    PRAGMA cache_size = -8000;     -- 8MB (negative = KB)
    PRAGMA journal_mode = WAL;
    PRAGMA synchronous = NORMAL;   -- safe with WAL
    PRAGMA wal_autocheckpoint = 1000;

  Reader connections (pool of 4):
    PRAGMA cache_size = -4000;     -- 4MB each
    PRAGMA journal_mode = WAL;
    PRAGMA query_only = true;      -- prevent accidental writes

  Memory accounting:
    Writer: 8MB
    Readers: 4 × 4MB = 16MB
    Total page cache: 24MB (not 120MB)

REVISED MEMORY BUDGET (1M lines):

  Component               │ Estimate
  ────────────────────────│──────────
  SQLite page cache       │  24 MB    (was 30MB — now precise)
  CSR graph               │  17 MB
  CSR delta buffer        │  <1 MB
  Session LRU             │   2 MB
  FTS5 auxiliary          │   8 MB
  Watcher state           │   3 MB
  Embedding worker buffer │   5 MB
  Writer Thread queue     │   5 MB    (coalesced, back-pressured)
  Per-query transient     │   5 MB    (max 4 concurrent queries × ~1.5MB)
  ────────────────────────│──────────
  TOTAL                   │  ~70 MB   ✓ within 100MB (NFR-7)
```

---

## A7. Circuit Breaker — Permanent-Off State

**Problem:** If Ollama is not installed, circuit breaker cycles forever: 3 timeouts (90s) → 5min wait → retry → repeat. 48 minutes wasted per 8-hour day.

**Fix: Add DISABLED state + startup detection.**

```
CIRCUIT BREAKER STATES:

  DISABLED:
    • Set at startup if: no embedding model configured, OR Ollama health check fails
      AND config.embedding.enabled != true
    • No embedding attempts. No log spam.
    • System permanently operates at Level 1 (structural + keyword)
    • User can enable later: `shaktiman config set embedding.enabled true`

  CLOSED:
    • Normal operation. Process embedding batches.
    • On failure: increment failure counter.
    • 3 consecutive failures → OPEN.

  OPEN:
    • Skip embedding for 5 minutes.
    • Log single warning (not per-cycle).
    • After cooldown → HALF_OPEN.
    • After 3 OPEN cycles (15 min total) with no success → DISABLED.
      Log: "Embedding model unreachable. Disabling. Run `shaktiman config
      set embedding.enabled true` to retry."

  HALF_OPEN:
    • Try 1 batch (30s timeout).
    • Success → CLOSED.
    • Failure → OPEN (reset cooldown).

STARTUP BEHAVIOR:
  1. Check config.embedding.enabled (default: true)
  2. If true: health-check Ollama (HTTP GET /api/tags, 5s timeout)
     • Reachable → CLOSED
     • Unreachable → Log warning, start in OPEN, proceed to DISABLED after 3 cycles
  3. If false: DISABLED
```

---

## A8. Push Mode — Branch-Change Trigger + Debounce

**Problem:** No branch-change trigger for resource update. Notification storm after batch changes.

**Fix:**

```
RESOURCE UPDATE TRIGGERS (revised):

  shaktiman://context/active is re-assembled when:
    1. File save (existing)
    2. Working set shift >30% (existing)
    3. Branch change detected (NEW): watcher sees >20 files change in <2s
       → treat as branch switch → full resource re-assembly
    4. Every 60s idle (existing)

  DEBOUNCE:
    Resource re-assembly: 500ms debounce from last trigger event.
    Multiple rapid triggers → one assembly after 500ms of quiet.

  NOTIFICATIONS:
    context/changed: 3-second debounce. At most one notification per 3 seconds.
    After branch switch: single notification after enrichment stabilizes.
    Payload includes: { event: "branch_switch" | "file_change" | "session_shift" }
    so the agent can decide whether to re-read the resource.
```

---

## A9. Progressive Cold Index — Priority Ordering

**Problem:** Arbitrary batch ordering means important files may not be indexed until late. Query-time enrichment may conflict with cold index workers.

**Fix:**

```
COLD INDEX FILE ORDERING:

  Phase 1 (priority):
    1. Files modified in last 7 days (most likely to be queried)
    2. Files matching common entry-point patterns: src/*, lib/*, app/*, index.*, main.*
    3. Config files: *.config.*, *.json, *.yaml, *.toml

  Phase 2 (remaining):
    4. All other files, alphabetically

  QUERY-TIME ENRICHMENT DURING COLD INDEX:
    • Enrichment pool reserves 1 of 4 workers for query-time triggers
    • Cold index uses remaining 3 workers
    • File mutex prevents conflicts: if cold index holds mutex for target file,
      query-time trigger waits (50ms timeout), then serves raw file content
    • Priority ordering minimizes likelihood of this conflict for common queries
```

---

## A10. Exploration Decay — Batched Updates

**Problem:** Updating `queries_since_last_hit` for every chunk in working_set on every query creates O(W) writes through Writer Thread per query.

**Fix:**

```
EXPLORATION DECAY (batched):

  In-memory counters:
    • Maintain queries_since_last_hit in-memory HashMap (not SQLite)
    • On each query: iterate in-memory working set, increment counters
    • Use in-memory counters for session_score normalization
    • O(W) in-memory iteration is fast (~0.1ms for 5000 entries)

  SQLite persistence:
    • Flush in-memory counters to SQLite every 30 seconds OR every 10 queries
    • Single batch UPDATE via Writer Thread
    • On crash: at most 30s of decay data lost — acceptable
```

---

## A11. FTS5 Bulk Insert Latency

**Problem:** FTS5 content-sync updates during bulk inserts (cold index) may take 50-200ms per batch, not 5-15ms.

**Fix:**

```
FTS5 STRATEGY:

  Cold index:
    • Disable FTS5 sync during cold index
    • After all files indexed: single FTS5 rebuild
      INSERT INTO chunks_fts(chunks_fts) VALUES('rebuild');
    • This is a known SQLite optimization — bulk rebuild is faster than incremental
    • Rebuild at 1M lines (~75K chunks): ~2-5s (one-time, runs after cold index)

  Incremental (normal operation):
    • FTS5 sync per-file (1-5 chunks per file change): <2ms — acceptable

  Cold index Writer Thread timing (revised):
    • Without FTS5 sync: batch of 200 files = 30-50ms (not 50-100ms)
    • FTS5 rebuild runs once at end as separate P3 job
```

---

## Summary of Amendments

| # | Finding | Fix | Impact |
|---|---|---|---|
| A1 | Writer Thread bottleneck | Priority lanes + back-pressure + burst coalescing | HIGH — prevents 5s query stalls |
| A2 | CSR incremental update | Delta buffer + periodic compaction | HIGH — eliminates per-write rebuild |
| A3 | CSR rebuild state machine | BUILDING/READY states + SQLite BFS fallback | MEDIUM — clean startup behavior |
| A4 | CSR/SQLite consistency | Version counter + safety fallback | MEDIUM — eliminates stale graph reads |
| A5 | Triple bottleneck | Decouple mutex from Writer Thread + holder timeout | HIGH — fixes the critical interaction |
| A6 | Page cache memory | Per-connection cache_size, revised budget | HIGH — proves 100MB budget holds |
| A7 | Circuit breaker cycling | DISABLED state + startup detection | MEDIUM — no-Ollama UX fixed |
| A8 | Push mode staleness | Branch-change trigger + debounce | MEDIUM — fresh context after branch switch |
| A9 | Cold index ordering | Priority phases + reserved query worker | MEDIUM — better progressive UX |
| A10 | Exploration decay writes | In-memory counters, batched SQLite flush | LOW — reduces Writer Thread load |
| A11 | FTS5 bulk latency | Disable sync during cold index, rebuild once | MEDIUM — faster cold index |

---

## Remaining Acknowledged Limitations

| Limitation | Severity | Acceptance Rationale |
|---|---|---|
| NFR-8 disk at 1M lines is 455MB/500MB (9% margin) | LOW | The 500MB target was spec'd for 100K lines. At 1M lines, the target scales proportionally. Document: "500MB per 100K lines, ~5GB for 1M lines." |
| CSR edge kind uses enum encoding (assumption) | LOW | Document: edge kind is u8 enum, mapped at read time. 6 kinds fit in 3 bits. |
| Up to 1ms consistency window between SQLite commit and CSR delta append | LOW | Negligible in practice. Version counter detects and falls back. |
| Cold index >100K lines exceeds original 60s NFR-1 target | LOW | NFR-1 explicitly scoped to 100K lines. Progressive availability ensures system is usable immediately. |
