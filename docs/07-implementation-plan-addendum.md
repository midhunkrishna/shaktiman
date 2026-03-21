# Implementation Plan — Addendum

> Addresses findings from the implementation plan critique round (reviewer + adversarial-analyst).
> These amendments are part of the implementation plan.

---

## IP-1: ZMQ Server — Single-Goroutine Socket I/O

**Status: SUPERSEDED — ZMQ eliminated; MCP stdio used instead.**

**Problem:** The ZMQ server code spawns a goroutine per request, then calls `s.router.SendMessage()` from the spawned goroutine. libzmq sockets are NOT thread-safe — `RecvMessageBytes` in the main goroutine and `SendMessage` in spawned goroutines share the same socket. A Go mutex only serializes sends with each other, not sends with recvs. This causes message corruption, panics, or segfaults under concurrent requests. Both agents flagged this as HIGH.

**Fix: Single-goroutine socket I/O with response channel.**

```go
// internal/transport/server.go

type Server struct {
    router   *zmq4.Socket
    handler  RequestHandler
    respCh   chan outgoing    // handler goroutines send responses here
}

type outgoing struct {
    identity []byte
    payload  []byte
}

func (s *Server) Serve(ctx context.Context) error {
    poller := zmq4.NewPoller()
    poller.Add(s.router, zmq4.POLLIN)

    for {
        // Check for incoming requests (non-blocking, 10ms timeout)
        polled, _ := poller.Poll(10 * time.Millisecond)

        // Recv — same goroutine as send
        for _, p := range polled {
            if p.Socket == s.router {
                frames, err := s.router.RecvMessageBytes(zmq4.DONTWAIT)
                if err != nil {
                    continue
                }
                identity := frames[0]
                payload := frames[len(frames)-1]

                // Dispatch to handler goroutine — does NOT touch socket
                go func(id, data []byte) {
                    var req types.Request
                    if err := msgpack.Unmarshal(data, &req); err != nil {
                        s.respCh <- outgoing{id, s.makeError(req.RequestID, types.ErrInvalidRequest)}
                        return
                    }
                    resp := s.handler.Handle(ctx, &req)
                    respBytes, _ := msgpack.Marshal(resp)
                    s.respCh <- outgoing{id, respBytes}
                }(identity, payload)
            }
        }

        // Send — same goroutine as recv. Drain response channel.
        for {
            select {
            case out := <-s.respCh:
                s.router.SendMessage(out.identity, "", out.payload)
            default:
                goto done
            }
        }
    done:

        // Shutdown check
        if ctx.Err() != nil {
            return nil
        }
    }
}
```

All socket I/O happens in one goroutine. Handler goroutines never touch the socket.

---

## IP-2: Tree-Sitter Parser — Per-Worker Instances

**Status: ACTIVE**

**Problem:** `smacker/go-tree-sitter`'s `Parser` object is NOT safe for concurrent use. The enrichment pipeline runs N goroutines calling `e.parser.Parse()` concurrently. The C `TSParser` stores internal state — concurrent calls corrupt it. Flagged as HIGH by adversarial-analyst.

**Fix: One parser instance per enrichment goroutine.**

```go
// internal/daemon/enrichment.go

type EnrichmentPool struct {
    workers int
    jobCh   chan EnrichJob
}

func (p *EnrichmentPool) Start(ctx context.Context) {
    for i := 0; i < p.workers; i++ {
        go func() {
            // Each goroutine owns its own parser — never shared
            par := parser.NewParser()  // creates fresh TSParser + loads grammars
            defer par.Close()

            for {
                select {
                case job := <-p.jobCh:
                    result, err := enrichFile(ctx, par, job.FilePath)
                    // ...
                case <-ctx.Done():
                    return
                }
            }
        }()
    }
}
```

`parser.NewParser()` creates a new `sitter.Parser`, calls `SetLanguage()` as needed per file. Each worker goroutine owns its instance exclusively.

---

## IP-3: go-sqlite3 — Dual DB Instances

**Status: ACTIVE**

**Problem:** `database/sql` manages its own connection pool. A single `sql.DB` with `SetMaxOpenConns(5)` does NOT separate reader and writer connections — Go may hand any pooled connection to any query. PRAGMA settings (`query_only`, `cache_size`) set per-connection are lost on pool recycle. This can cause writes on "reader" connections or reads on the "writer" connection, violating WAL isolation guarantees. Flagged as HIGH by adversarial-analyst.

**Fix: Two separate `sql.DB` instances with connection hooks.**

```go
// internal/storage/db.go

type DB struct {
    writer *sql.DB  // MaxOpenConns=1
    reader *sql.DB  // MaxOpenConns=4
}

func Open(dbPath string) (*DB, error) {
    // Writer: single connection, no query_only
    writerDSN := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&cache=shared", dbPath)
    writer, err := sql.Open("sqlite3", writerDSN)
    if err != nil {
        return nil, err
    }
    writer.SetMaxOpenConns(1)
    writer.SetMaxIdleConns(1)

    // Set writer-specific PRAGMAs via connection init
    if _, err := writer.Exec("PRAGMA cache_size = -8000"); err != nil {  // 8MB
        return nil, err
    }

    // Reader: pool of 4, query_only enforced
    readerDSN := fmt.Sprintf("file:%s?_journal_mode=WAL&_query_only=true&_busy_timeout=5000&mode=ro&cache=shared", dbPath)
    reader, err := sql.Open("sqlite3", readerDSN)
    if err != nil {
        return nil, err
    }
    reader.SetMaxOpenConns(4)
    reader.SetMaxIdleConns(4)

    // Set reader-specific PRAGMAs on each connection
    reader.SetConnMaxLifetime(0)  // never expire — PRAGMAs stick

    return &DB{writer: writer, reader: reader}, nil
}

// All writes go through writer
func (db *DB) WithWriteTx(fn func(tx *sql.Tx) error) error {
    tx, err := db.writer.Begin()
    if err != nil {
        return err
    }
    if err := fn(tx); err != nil {
        tx.Rollback()
        return err
    }
    return tx.Commit()
}

// All reads go through reader pool
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
    return db.reader.QueryContext(ctx, query, args...)
}
```

---

## IP-4: Writer Goroutine Shutdown — Ordered Drain

**Status: ACTIVE**

**Problem:** The shutdown path `for len(ch) > 0 { <-ch }` is racy — producers may still be sending after `len(ch)` returns 0. Jobs sent after drain completes are lost. Also, callers blocked on `DoneCh` hang forever if the writer exits without responding. Both agents flagged this as HIGH.

**Fix: Ordered shutdown with WaitGroup + channel close.**

```go
// internal/daemon/writer.go

type WriterManager struct {
    ch        chan WriteJob
    producers sync.WaitGroup  // tracks all goroutines that send to ch
    done      chan struct{}    // closed when writer goroutine exits
}

// All producers must call AddProducer/RemoveProducer
func (wm *WriterManager) AddProducer()    { wm.producers.Add(1) }
func (wm *WriterManager) RemoveProducer() { wm.producers.Done() }
func (wm *WriterManager) Submit(job WriteJob) { wm.ch <- job }

func (wm *WriterManager) Run(ctx context.Context, db *storage.DB) {
    defer close(wm.done)

    // Normal operation: read from channel until context cancelled
    for {
        select {
        case job := <-wm.ch:
            processJob(db, job)
        case <-ctx.Done():
            goto drain
        }
    }

drain:
    // Wait for all producers to stop sending
    wm.producers.Wait()
    // Now safe to close — no more senders
    close(wm.ch)

    // Drain remaining jobs with timeout
    timeout := time.After(10 * time.Second)
    for job := range wm.ch {
        select {
        case <-timeout:
            slog.Warn("writer drain timeout, dropping remaining jobs", "remaining", len(wm.ch))
            return
        default:
            processJob(db, job)
        }
    }
}
```

---

## IP-5: Writer Channel — Capacity and Memory Budget

**Status: ACTIVE**

**Problem:** The writer channel has capacity 5000. Each `WriteJob` contains `Chunks []ChunkRecord` with full `content string`. A file with 50 chunks at 500 bytes each = ~25KB per job. 5000 jobs = ~125MB in channel buffer alone — far exceeding the 5MB budget for "Writer Thread queue" (DM-10). Flagged MEDIUM by adversarial-analyst.

**Fix: Reduce capacity + defer content reads.**

```
REVISED WRITER CHANNEL:

  Phase 1 (single priority):
    capacity: 500 (not 5000)
    Job contains: file path, content hash, pre-computed chunks+symbols
    Memory: 500 × ~25KB = ~12.5MB worst case (during cold index bursts)

  Phase 4 (priority lanes):
    P0: capacity 100
    P1: capacity 200
    P2: capacity 500
    P3: capacity 1000
    Total max: 1800 jobs × ~25KB = ~45MB worst case

  Alternative: Lazy content loading
    WriteJob stores file path + metadata only (~200 bytes/job)
    Writer goroutine reads file content from disk before writing to SQLite
    Memory: 5000 × 200B = ~1MB
    Trade-off: Adds 1 disk read per job (~0.1ms), but keeps within budget

  DECISION: Use reduced capacity (500 Phase 1, 1800 Phase 4).
  Content in-memory is acceptable — back-pressure prevents sustained max fill.
  Cold index batches of 200 files cap at ~200 jobs in-flight.
```

---

## IP-6: BFS Delta Buffer — Merge During Traversal

**Status: DEFERRED — CSR deferred to Phase 3+; SQLite CTEs used in Phase 2.**

**Problem:** The BFS code runs full BFS over CSR, then applies delta additions/deletions in a post-pass. This is logically wrong: (1) transitive edges through delta-added nodes are missed (A→B in CSR, B→C in delta — C is found but C→D in delta is not), (2) the delete logic removes nodes reachable through non-deleted paths. Both agents flagged this as HIGH.

**Fix: Overlay delta during BFS traversal.**

```go
// internal/graph/csr.go

func (g *GraphModule) BFS(symbolID uint32, maxDepth int) []uint32 {
    csr := g.current.Load()

    // Snapshot delta buffer (hold lock briefly)
    g.delta.mu.Lock()
    deltaAdds := make(map[uint32][]uint32)    // src → []dst
    deltaDeletes := make(map[[2]uint32]bool)  // (src, dst) → deleted
    for _, a := range g.delta.adds {
        deltaAdds[a.Src] = append(deltaAdds[a.Src], a.Dst)
    }
    for _, d := range g.delta.deletes {
        deltaDeletes[[2]uint32{d.Src, d.Dst}] = true
    }
    g.delta.mu.Unlock()

    // BFS with merged view
    visited := make(map[uint32]bool)
    queue := []bfsEntry{{id: symbolID, depth: 0}}

    for len(queue) > 0 {
        curr := queue[0]
        queue = queue[1:]
        if curr.depth >= maxDepth || visited[curr.id] {
            continue
        }
        visited[curr.id] = true

        // 1. CSR neighbors (skip deleted edges)
        if csr != nil && curr.id < uint32(len(csr.RowPtr)-1) {
            start, end := csr.RowPtr[curr.id], csr.RowPtr[curr.id+1]
            for i := start; i < end; i++ {
                dst := csr.ColIdx[i]
                if !deltaDeletes[[2]uint32{curr.id, dst}] {
                    queue = append(queue, bfsEntry{id: dst, depth: curr.depth + 1})
                }
            }
        }

        // 2. Delta-added neighbors (always included)
        for _, dst := range deltaAdds[curr.id] {
            queue = append(queue, bfsEntry{id: dst, depth: curr.depth + 1})
        }
    }

    delete(visited, symbolID)  // exclude the start node
    return maps.Keys(visited)
}
```

---

## IP-7: Reverse CSR for Bidirectional BFS

**Status: DEFERRED — CSR deferred to Phase 3+; SQLite CTEs support both directions natively.**

**Problem:** The CSR stores only forward edges (src → dst). The `dependencies()` API (DM-9, pattern 7.7) requires both callers (incoming) and callees (outgoing). Without a reverse index, incoming-edge queries require a full table scan of the CSR. Flagged as HIGH by reviewer.

**Fix: Build a reverse CSR alongside the forward CSR.**

```go
// internal/graph/csr.go

type CSRGraph struct {
    // Forward: src → dst (callees)
    FwdRowPtr   []uint32
    FwdColIdx   []uint32
    FwdEdgeKind []uint8

    // Reverse: dst → src (callers)
    RevRowPtr   []uint32
    RevColIdx   []uint32
    RevEdgeKind []uint8
}

func BuildCSR(edges []Edge, numSymbols int) *CSRGraph {
    g := &CSRGraph{}

    // Build forward CSR
    g.FwdRowPtr, g.FwdColIdx, g.FwdEdgeKind = buildOneDirection(edges, numSymbols, false)

    // Build reverse CSR (swap src/dst)
    g.RevRowPtr, g.RevColIdx, g.RevEdgeKind = buildOneDirection(edges, numSymbols, true)

    return g
}

// BFS uses Forward or Reverse based on direction parameter
func (g *GraphModule) BFS(symbolID uint32, maxDepth int, direction Direction) []uint32 {
    csr := g.current.Load()
    var rowPtr, colIdx []uint32
    if direction == Outgoing {
        rowPtr, colIdx = csr.FwdRowPtr, csr.FwdColIdx
    } else {
        rowPtr, colIdx = csr.RevRowPtr, csr.RevColIdx
    }
    // ... BFS using selected direction (same logic as IP-6)
}
```

**Memory impact:** Reverse CSR doubles CSR memory. At 1M lines: 17MB × 2 = 34MB. Revised budget:

```
Component                          │ 1M lines (revised)
───────────────────────────────────│──────────
CSR graph (forward + reverse)      │  34 MB   (was 17 MB)
SQLite page cache                  │  24 MB
Other                              │  14 MB
───────────────────────────────────│──────────
TOTAL                              │  ~72 MB  → ~89 MB   ✓ still within 100MB
```

**Task impact:** Add 1h to task 2.4 (CSR build). Task 2.6 (BFS) already has a `direction` parameter — just wire it up.

---

## IP-8: Priority Writer — Cascading Select (No Put-Back)

**Status: ACTIVE (Phase 4)**

**Problem:** The priority writer's back-pressure logic does `w.p3 <- job` (puts the job back on the channel) then sleeps. If P3 is full, this deadlocks. If not full, it creates a busy read-put-back loop. Both agents flagged as HIGH.

**Fix: Cascading select that skips P3 when back-pressure is active.**

```go
// internal/daemon/writer.go

func (w *PriorityWriter) Run(ctx context.Context) {
    backpressure := false

    for {
        // Check back-pressure condition
        totalQueued := len(w.p0) + len(w.p1) + len(w.p2) + len(w.p3)
        backpressure = totalQueued > 1000

        // Level 0: P0 always has highest priority
        select {
        case job := <-w.p0:
            w.process(job)
            continue
        default:
        }

        // Level 1: P1 next
        select {
        case job := <-w.p0:
            w.process(job)
            continue
        case job := <-w.p1:
            w.process(job)
            continue
        default:
        }

        // Level 2: P2 next
        select {
        case job := <-w.p0:
            w.process(job)
            continue
        case job := <-w.p1:
            w.process(job)
            continue
        case job := <-w.p2:
            w.process(job)
            continue
        default:
        }

        // Level 3: P3 only if no back-pressure
        if backpressure {
            // Wait for higher-priority work or timeout
            select {
            case job := <-w.p0:
                w.process(job)
            case job := <-w.p1:
                w.process(job)
            case job := <-w.p2:
                w.process(job)
            case <-time.After(50 * time.Millisecond):
                // Re-check back-pressure
            case <-ctx.Done():
                w.drain()
                return
            }
        } else {
            select {
            case job := <-w.p0:
                w.process(job)
            case job := <-w.p1:
                w.process(job)
            case job := <-w.p2:
                w.process(job)
            case job := <-w.p3:
                w.process(job)
            case <-ctx.Done():
                w.drain()
                return
            }
        }
    }
}
```

P3 jobs are never put back. When back-pressure is active, P3 is simply not selected — jobs wait in the channel until pressure subsides.

---

## IP-9: Phase 1 Missing Tasks

**Status: SUPERSEDED — Phase 1 tasks rewritten in final implementation plan; all items (full schema, repo_id, tokens, validation) incorporated.**

**Problem:** Several required tasks are absent from Phase 1. Reviewer flagged repo_id validation (MF-4), full schema (MF-5), and token counting (MF-7). Adversarial-analyst confirmed token counting as critical for budget compliance.

**Fix: Add 4 tasks to Phase 1.**

```
ADDED TASKS:

  1.X1: Create FULL SQLite schema (all tables from data model)          2h
    Create all tables: files, chunks, symbols, edges, pending_edges,
    diff_log, diff_symbols, access_log, working_set, config,
    schema_version. Empty tables for future phases cost nothing.
    Replaces task 1.4 scope (which only listed files/chunks/symbols/FTS5).

  1.X2: repo_id generation + validation + socket naming                 2h
    - SHA-256(canonical_path)[:16] for socket names (AP-4)
    - Full SHA-256 for internal identity
    - repo_id validation on every incoming request (REPO_MISMATCH error)
    - Socket path: {runtime_dir}/{repo_id_short}-rpc.sock

  1.X3: Token counting in enrichment pipeline                           1h
    - Use tiktoken-go (already in deps) to count tokens per chunk
    - Store in chunks.token_count (NOT NULL in schema)
    - Required for context assembler budget compliance (DC-7)

  1.X4: Request validation (AP-5)                                       1h
    - Max message size: 1MB (drop before deserializing)
    - Parameter bounds: budget_tokens [256, 32768], max_results [1, 200]
    - Return INVALID_PARAMS with specific field + reason

REVISED PHASE 1 TOTAL: 45h + 6h = ~51h (~1.7 weeks)
```

---

## IP-10: Phase 1 — PUB Socket + Symlink Handling

**Status: PARTIALLY SUPERSEDED — PUB socket superseded (MCP notifications instead); symlink handling and .shaktimanignore remain ACTIVE and incorporated into Phase 1.**

**Problem:** Multiple SHOULD-FIX items missing from Phase 1 that prevent clean Phase 4 integration.

**Fix: Adjust existing Phase 1 tasks.**

```
ADJUSTMENTS:

  Task 1.20 (daemon lifecycle) — ADD:
    - Bind PUB socket at startup alongside ROUTER (no-op publisher)
    - Register both socket paths in registry
    - Without this, Phase 4 PUB/SUB requires changing the daemon startup
      sequence and registry format

  Task 1.9 (file scanner) — ADD:
    - Resolve symlinks via filepath.EvalSymlinks() (DM-7)
    - Skip files outside project directory after resolution
    - Track inodes to skip hard link duplicates
    - Support .shaktimanignore alongside .gitignore (FR-18)

  Task 1.2 (shared types) — ADD:
    - Define VectorStore interface as no-op stub (SF-2)
    - Phase 3 provides Qdrant implementation
    - Query pipeline can reference the interface from Phase 1

NO NET TIME CHANGE — these fold into existing tasks.
```

---

## IP-11: Import Cycle Prevention — Type Placement

**Status: ACTIVE — simplified since transport package replaced by mcp package.**

**Problem:** The import graph shows `vector → core` but `core` needs to call `VectorStore.Search()` — creating a cycle if `VectorStore` is defined in `internal/vector`. Similarly, `ScoredResult` defined in `vector/store.go` but consumed by `core/retrieval.go` creates a dependency from `core` to `vector`.

**Fix: All shared interfaces and result types in `internal/types`.**

```
INTERFACE PLACEMENT:

  internal/types/interfaces.go:
    type VectorStore interface { ... }
    type GraphStore interface { ... }
    type MetadataStore interface { ... }

  internal/types/results.go:
    type ScoredResult struct { ... }
    type VectorEntry struct { ... }

  internal/vector/qdrant.go:
    // implements types.VectorStore
    type QdrantStore struct { ... }

  internal/core/retrieval.go:
    // consumes types.VectorStore (interface)
    func (r *Retrieval) semanticSearch(store types.VectorStore, ...) []types.ScoredResult

REVISED IMPORT GRAPH (no cycles):

  types  ←── parser
    ↑         ↑
    │         │
  storage ←──┘
    ↑
    │
  graph
    ↑
    │
  vector (implements types.VectorStore)
    ↑
    │
  core (depends on types.VectorStore interface, not vector package)
    ↑
    │
  transport ←── daemon ←── cmd/
```

---

## IP-12: Tree-Sitter Query Time Re-Estimation

**Status: PARTIALLY SUPERSEDED — TypeScript-only in Phase 1 (3h, not 10h). Languages added incrementally in Phases 2-3. Core finding (estimates too low) validated and incorporated.**

**Problem:** Both agents flagged tree-sitter query authoring as severely underestimated. Task 1.13 (4 languages, chunk+symbol queries, 3h) and tasks 2.1+2.2 (edge extraction, 6h) are unrealistic. Real estimate: 8-16h for symbol/chunk queries, 15-20h for edge queries.

**Fix: Revised estimates + phased language rollout.**

```
REVISED ESTIMATES:

  Phase 1 — Symbol/Chunk Queries:
    Task 1.13 SPLIT into:
      1.13a: TypeScript symbol/chunk queries               3h
      1.13b: Python symbol/chunk queries                   2h
      1.13c: Go symbol/chunk queries                       2h
      1.13d: Rust symbol/chunk queries                     3h
    TOTAL: 10h (was 3h)

  Phase 2 — Edge Extraction Queries:
    Task 2.1+2.2 SPLIT into:
      2.1a: TypeScript edge queries (imports, calls)       4h
      2.1b: Python edge queries                            3h
      2.1c: Go edge queries                                3h
      2.1d: Rust edge queries                              4h
    TOTAL: 14h (was 6h)

  RECOMMENDATION: Start with TypeScript only in Phase 1.
    Other languages can be added incrementally.
    TypeScript has the best tree-sitter grammar support.
    MVP is usable with 1 language.

REVISED PHASE 1 TOTAL: 51h + 7h = ~58h (~2 weeks)
REVISED PHASE 2 TOTAL: 39h + 8h = ~47h (~1.5 weeks)
REVISED PROJECT TOTAL: ~171h (~5.5 weeks)
```

---

## IP-13: MCP Compatibility Layer Re-Estimation

**Status: SUPERSEDED — MCP is now Phase 1 primary transport, properly estimated at 4h (tasks 1.20-1.22).**

**Problem:** Task 4.16 (MCP layer, 3h) is underestimated. MCP requires JSON-RPC over stdio/SSE, capabilities negotiation, tool registration, resource management, and notifications. This is a protocol bridge, not a wrapper.

**Fix: Revised estimate + phased approach.**

```
REVISED:

  Task 4.16 SPLIT into:
    4.16a: MCP server skeleton (stdio transport, JSON-RPC)    3h
    4.16b: Tool registration (search, context, symbols, deps, diff, summary)  2h
    4.16c: Resource registration (context/active, workspace/summary)  2h
    4.16d: MCP → ZMQ method translation + response mapping    3h
  TOTAL: 10h (was 3h)

  RECOMMENDATION: Use an existing Go MCP SDK if available
    (e.g., github.com/mark3labs/mcp-go).
    If SDK exists: 3-4h total (original estimate is fine).
    If from scratch: 10h.

  Add to go.mod:
    github.com/mark3labs/mcp-go v0.17.0   # if available and suitable

REVISED PHASE 4 TOTAL: 38h + 7h = ~45h (~1.5 weeks)
```

---

## IP-14: Vector Store Decision — Qdrant vs Embedded

**Status: RESOLVED — Brute-force in-process is default. Qdrant available as optional backend via VectorStore interface. DC-11/DC-13 satisfied.**

**Problem:** The architecture (DC-11, DC-13) specifies "no external database dependencies" and "embedded vector store." The implementation plan uses Qdrant, which is an external process. This contradicts two design constraints. Both agents flagged this.

**Fix: Document as an explicit decision with rationale and fallback.**

```
VECTOR STORE DECISION:

  OPTION A: Qdrant (current plan)
    Pros: mature HNSW, Go client, filtered search, built-in persistence
    Cons: external process, contradicts DC-11/DC-13, adds deployment complexity

  OPTION B: sqlite-vec (original architecture)
    Pros: embedded (no external process), DC-11/DC-13 compliant, single SQLite DB
    Cons: Go bindings via CGo extension loading (untested maturity), IVF not HNSW

  OPTION C: In-process brute-force
    Pros: zero dependencies, simple
    Cons: O(n) search, only viable up to ~50K chunks

  DECISION: Keep Qdrant as PRIMARY but add in-process brute-force as FALLBACK.

  The VectorStore interface (IP-11) enables this:
    - Phase 3 implements QdrantStore
    - Phase 3 also implements BruteForceStore (in-process, no external dep)
    - Config chooses: vector.backend = "qdrant" | "bruteforce" | "disabled"
    - If Qdrant is not running → automatic fallback to brute-force
    - brute-force is viable up to ~50K chunks (~500K lines) — covers most repos

  Task additions for Phase 3:
    3.X1: BruteForceStore implementation                     2h
      - In-memory []float32 slices
      - Cosine similarity scan
      - Adequate for repos up to 500K lines

  UPDATE DC-13 NOTE:
    DC-13 is satisfied by the brute-force fallback. Qdrant is an
    optional performance optimization, not a hard dependency.
    System is fully functional without Qdrant running.
```

---

## IP-15: Circuit Breaker — Mutex Instead of Atomics

**Status: ACTIVE**

**Problem:** The circuit breaker uses individual atomics (`state`, `consecutiveFail`, `openCycles`) that don't compose atomically. Between `Add(1)` and `state.Store()`, another goroutine can call `RecordSuccess()` and reset state. Flagged MEDIUM by adversarial-analyst.

**Fix: Use a single mutex for state transitions.**

```go
// internal/vector/embedding.go

type CircuitBreaker struct {
    mu              sync.Mutex
    state           int32
    consecutiveFail int32
    openCycles      int32
    lastOpenTime    time.Time
    cooldown        time.Duration
}

func (cb *CircuitBreaker) Allow() bool {
    cb.mu.Lock()
    defer cb.mu.Unlock()

    switch cb.state {
    case StateDisabled:
        return false
    case StateClosed:
        return true
    case StateOpen:
        if time.Since(cb.lastOpenTime) > cb.cooldown {
            cb.state = StateHalfOpen
            return true
        }
        return false
    case StateHalfOpen:
        return true
    }
    return false
}

func (cb *CircuitBreaker) RecordSuccess() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.consecutiveFail = 0
    cb.openCycles = 0
    cb.state = StateClosed
}

func (cb *CircuitBreaker) RecordFailure() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.consecutiveFail++
    if cb.consecutiveFail >= 3 {
        cb.state = StateOpen
        cb.lastOpenTime = time.Now()
        cb.openCycles++
        if cb.openCycles >= 3 {
            cb.state = StateDisabled
            slog.Warn("embedding model unreachable, disabling circuit breaker")
        }
    }
}
```

The circuit breaker is called infrequently (once per batch, not per-chunk), so mutex contention is negligible.

---

## IP-16: fsnotify macOS — File Descriptor Limits

**Status: ACTIVE**

**Problem:** macOS kqueue requires one file descriptor per watched file. Large repos (10K+ files) can hit `ulimit -n` limits. Flagged MEDIUM by adversarial-analyst.

**Fix: Document + directory-level watching.**

```
FSNOTIFY STRATEGY:

  macOS:
    Watch directories, not individual files.
    fsnotify on macOS uses kqueue which watches directory-level events.
    Add only the top-level project directories (recursive via filepath.WalkDir).
    FD usage: ~number of directories, not files.
    For 10K files in 500 directories: ~500 FDs.

  Linux:
    inotify watches are per-directory by default. No FD issue.

  Documentation:
    Add to CLI startup output: if FD limit < 4096, warn and suggest:
      "Run: ulimit -n 10240"

  Fallback:
    If fsnotify fails to add a watch (EMFILE):
      Log warning, fall back to periodic directory scan (every 30s).
      System still works, just with higher latency for change detection.
```

---

## Summary

| # | Finding | Fix | Severity | Source | Status |
|---|---|---|---|---|---|
| IP-1 | ZMQ socket not thread-safe | Single-goroutine socket I/O + response channel | HIGH | Both | SUPERSEDED |
| IP-2 | tree-sitter Parser not goroutine-safe | Per-worker parser instances | HIGH | Adversarial | ACTIVE |
| IP-3 | go-sqlite3 pool mixes reader/writer | Dual `sql.DB` instances | HIGH | Adversarial | ACTIVE |
| IP-4 | Writer shutdown drain race | WaitGroup + close + range drain | HIGH | Both | ACTIVE |
| IP-5 | Writer channel busts memory budget | Reduce capacity to 500 (Phase 1) | MEDIUM | Adversarial | ACTIVE |
| IP-6 | BFS delta applied post-traversal is wrong | Overlay delta during BFS traversal | HIGH | Both | DEFERRED |
| IP-7 | No reverse CSR for incoming-edge BFS | Build forward + reverse CSR | HIGH | Reviewer | DEFERRED |
| IP-8 | Priority writer put-back deadlocks | Cascading select, skip P3 under pressure | HIGH | Both | ACTIVE (Phase 4) |
| IP-9 | Missing Phase 1 tasks (schema, repo_id, tokens, validation) | Add 4 tasks (+6h) | HIGH | Reviewer | SUPERSEDED |
| IP-10 | Missing PUB socket, symlink, .shaktimanignore | Fold into existing Phase 1 tasks | MEDIUM | Reviewer | PARTIALLY SUPERSEDED |
| IP-11 | Import cycle risk with vector/core | All interfaces in `types/` package | MEDIUM | Both | ACTIVE |
| IP-12 | Tree-sitter query estimates 3× too low | Revised: 10h chunk/symbol, 14h edges | MEDIUM | Both | PARTIALLY SUPERSEDED |
| IP-13 | MCP layer 3× too low if from scratch | Use Go MCP SDK if available, else 10h | MEDIUM | Adversarial | SUPERSEDED |
| IP-14 | Qdrant contradicts DC-11/DC-13 | Keep Qdrant + add BruteForceStore fallback | MEDIUM | Both | RESOLVED |
| IP-15 | Circuit breaker atomics don't compose | Single mutex for state transitions | MEDIUM | Adversarial | ACTIVE |
| IP-16 | macOS fsnotify FD limits | Directory-level watching + fallback scan | LOW | Adversarial | ACTIVE |

**Revised timeline: ~171h (~5.5 weeks for a single focused engineer)**
