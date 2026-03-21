# API Specification — Addendum

> Addresses findings from the API specification critique round (reviewer + adversarial-analyst).
> These amendments are part of the API specification.

---

## AP-1: Missing `summary` Method

**Status: ACTIVE**

**Problem:** Architecture v3 defines 6 MCP tools including `summary(scope)`. The API spec omits it entirely. MCP compatibility layer cannot expose a tool with no ZMQ backing method.

**Fix: Add `summary` method.**

```
REQUEST:
{
  "method": "summary",
  "params": {
    "scope": "project",               // "project" | "module:src/auth/" | "file:src/auth/login.ts"
    "detail": "standard"              // "minimal" | "standard" | "detailed"
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "scope": "project",
    "overview": {
      "total_files": 342,
      "total_chunks": 2840,
      "total_symbols": 1920,
      "languages": { "typescript": 280, "python": 62 },
      "modules": [
        { "path": "src/auth/", "files": 12, "symbols": 85, "hotspot_rank": 1 },
        { "path": "src/payments/", "files": 8, "symbols": 52, "hotspot_rank": 2 }
      ]
    },
    "recent_activity": {
      "files_changed_24h": 5,
      "symbols_changed_24h": 12,
      "hotspots": ["src/auth/login.ts", "src/payments/retry.ts"]
    },
    "index_health": {
      "embedding_pct": 0.95,
      "stale_files": 0,
      "graph_state": "ready",
      "parse_errors": 0
    }
  },
  "meta": { "latency_ms": 5, "strategy": "index_lookup", "freshness": "up_to_date" }
}

LATENCY: <5ms (index lookups, no store fan-out).
TOKEN EFFICIENCY: ~200 tokens for project summary vs reading dozens of files.
```

**MCP Compatibility Layer addition:**

```
MCP resource read: shaktiman://workspace/summary
  → ShaktimanClient.summary("project", "standard")
  → Cached in Resource Manager, rebuilt on structural change or cold index completion

MCP tool call: summary(scope)
  → ShaktimanClient.summary(scope, "standard")
  → Serialize to MCP JSON-RPC response
```

**Method reference table addition:**

| Method | Category | Sync | Target Latency | Token Efficiency |
|---|---|---|---|---|
| `summary` | Query | Sync | <5ms | ~200 tokens (project overview) |

**SDK addition:**

```rust
pub fn summary(&self, scope: &str, detail: &str) -> Result<SummaryResponse>;
```

---

## AP-2: Registry Concurrency Protection + Daemon Startup Lock

**Status: SUPERSEDED -- MCP stdio eliminates daemon registry; MCP client manages server lifecycle**

**Problem:** Registry file has no locking. Multiple daemons or agents writing simultaneously corrupt it. Also, multiple agents can race to start a daemon for the same repo (thundering herd).

**Fix: Per-repo lock file + atomic registry writes.**

```
DAEMON STARTUP LOCK:

  Lock file: {runtime_dir}/{repo_id}.lock
  Protocol:
    1. Agent/CLI resolves repo_id
    2. Acquire flock({repo_id}.lock, LOCK_EX | LOCK_NB)
       • If acquired → proceed to start daemon
       • If blocked → another process is starting; wait on lock (LOCK_EX blocking)
       • When lock acquired → re-read registry to see if daemon is now running
       • If running → connect to existing daemon, release lock
       • If not → start daemon, write registry, release lock

  This eliminates the thundering herd problem entirely.
  The lock file is intentionally NEVER deleted — its existence is harmless,
  and deleting it would create a new race condition.

REGISTRY ATOMIC WRITES:

  Protocol:
    1. Acquire flock(shaktiman-registry.lock, LOCK_EX)
    2. Read current registry file
    3. Modify in memory
    4. Write to {registry}.tmp (same directory, same filesystem)
    5. rename({registry}.tmp, {registry})    — atomic on POSIX
    6. Release flock

  This ensures:
    • No partial writes (tmp file is complete before rename)
    • No reader sees corrupt state (rename is atomic)
    • Multiple writers are serialized via flock
```

---

## AP-3: Robust PID Liveness Detection

**Status: SUPERSEDED -- MCP stdio eliminates daemon process management; client spawns/manages server directly**

**Problem:** PID recycling defeats simple `kill(pid, 0)` liveness checks. Daemon dies, OS reuses PID for unrelated process, agent thinks daemon is alive.

**Fix: Verify process identity, not just PID existence.**

```
LIVENESS CHECK PROTOCOL:

  1. Read registry entry: { pid, started_at, rpc_socket }
  2. Check PID exists: kill(pid, 0)
     • If not exists → PID dead, clean registry entry
  3. Check process start time matches:
     • Linux: read /proc/{pid}/stat field 22 (starttime in clock ticks)
       convert to epoch, compare with registry started_at (±2s tolerance)
     • macOS: sysctl(KERN_PROC, KERN_PROC_PID, pid) → kp_proc.p_starttime
       compare with registry started_at (±2s tolerance)
     • If mismatch → PID recycled, clean registry entry
  4. Ping daemon via RPC socket (timeout 1000ms):
     • If pong → daemon alive, connect
     • If timeout → daemon unresponsive, clean registry entry + socket files

  ONLY after all 3 checks pass is the daemon considered alive.

SIGKILL / OOM-KILL RECOVERY:

  When agent detects dead daemon (any check fails):
    1. Unlink stale socket files (rpc + evt)
    2. Remove registry entry
    3. Acquire startup lock (AP-2)
    4. Start new daemon
```

---

## AP-4: repo_id Length Extension

**Status: PARTIALLY SUPERSEDED -- socket naming no longer needed (MCP stdio); repo_id concept may still be useful for CLI/index identification**

**Problem:** `SHA-256[:12]` = 48 bits. Birthday collision at ~16M repos. CI/CD farms with throwaway clones can reach this.

**Fix: Extend to 16 hex characters (64 bits) for socket names. Use full SHA-256 internally.**

```
REVISED repo_id SCHEME:

  Internal (registry, process identity):
    repo_id_full = SHA-256(canonical_absolute_path)     -- 64 hex chars
    Used in: registry keys, REPO_MISMATCH validation, logging

  Socket naming (filesystem constraint):
    repo_id_short = SHA-256(canonical_absolute_path)[:16]  -- 16 hex chars, 64 bits
    Used in: socket file names only ({repo_id_short}-rpc.sock)
    Birthday collision at ~4 billion repos — acceptable

  Path canonicalization:
    MUST use realpath() / fs::canonicalize() before hashing.
    This resolves symlinks, removes trailing slashes, normalizes case.
    Two paths to the same directory always produce the same repo_id.
```

---

## AP-5: Message Size Limits + Request Validation

**Status: ACTIVE**

**Problem:** No maximum message size. A malformed or malicious request can OOM the daemon. `budget_tokens` has no upper bound.

**Fix: Hard limits on incoming messages and parameters.**

```
MESSAGE SIZE LIMITS:

  Incoming request: max 1MB (1,048,576 bytes)
  If exceeded: daemon drops message without deserializing.
  Log: warn("Oversized request dropped: {size} bytes")

  Outgoing response: no hard limit (budget-fitted by design)
  Typical max: ~100KB for a large context response (8192 tokens × ~10 bytes/token)

PARAMETER VALIDATION:

  budget_tokens:     min=256, max=32768, default=8192
  max_results:       min=1, max=200, default=50
  depth (BFS):       min=1, max=3, default=2
  query (text):      max 10,000 chars
  files (array):     max 50 entries
  targets (enrich):  max 100 entries
  notify_change files: max 500 entries per call, max 10 calls/second (rate limit)
  scope (diff):      max 500 chars
  since:             must be valid ISO8601 or duration format

  Invalid params → INVALID_PARAMS error with specific field + reason.
```

---

## AP-6: `enrich(wait=true)` Timeout

**Status: ACTIVE**

**Problem:** `enrich(wait=true)` blocks indefinitely if enrichment stalls (parser hangs, Ollama timeout, file mutex deadlock).

**Fix: Explicit timeout with default.**

```
ENRICH TIMEOUT:

  Default: 10,000ms (10 seconds) for wait=true
  Configurable via params.timeout_ms (max 60,000ms)

  REQUEST:
  {
    "method": "enrich",
    "params": {
      "targets": [{ "file": "src/handler.ts" }],
      "priority": "high",
      "wait": true,
      "timeout_ms": 5000              // explicit timeout for wait=true
    }
  }

  If timeout exceeded:
    Return status "partial" with results for files that completed.
    Remaining files continue enrichment in background.

  RESPONSE (timeout, partial):
  {
    "status": "partial",
    "data": {
      "enriched": 1,
      "pending": 1,
      "results": [
        { "file": "src/handler.ts", "status": "complete", "chunks": 5 }
      ],
      "timed_out": [
        { "file": "src/large-file.ts", "status": "in_progress" }
      ]
    },
    "error": {
      "code": "ENRICHMENT_TIMEOUT",
      "message": "1 of 2 files completed within 5000ms. Remainder continuing in background.",
      "recoverable": true
    }
  }
```

---

## AP-7: Disconnect Detection + Idle Shutdown Mechanism

**Status: SUPERSEDED -- MCP stdio transport handles connection lifecycle; idle shutdown becomes process exit when MCP client disconnects**

**Problem:** ZMQ ROUTER over IPC does not natively detect peer disconnect. The 30-min idle shutdown has no reliable trigger.

**Fix: Application-level heartbeat + activity-based idle timer.**

```
HEARTBEAT PROTOCOL:

  Agent sends: ping() every 60 seconds while connected
  Daemon tracks: last_activity_timestamp per connected agent identity

  Idle timer logic (daemon):
    every 60s:
      if now() - last_activity_from_any_source > idle_shutdown_minutes * 60:
        initiate shutdown

    "activity" includes:
      • Any RPC request received (including ping)
      • Any enrichment write committed
      • Any file watcher event processed

  If daemon has no RPC activity for 30 minutes AND no background work:
    → auto-shutdown

  Agent-side:
    heartbeat_timer = spawn every 60s:
      response = ping(timeout=2000ms)
      if timeout:
        mark daemon as possibly dead
        trigger liveness check (AP-3)
        if dead → restart daemon

SHUTDOWN GRACE PERIOD (fixes race condition):

  Revised shutdown sequence:
    1. Set state = DRAINING
    2. For 5 seconds: accept new requests but return SHUTTING_DOWN error
    3. Drain in-flight requests (5s timeout)
    4. Publish daemon.shutting_down event
    5. Stop File Watcher
    6. Flush Session Store
    7. Drain Writer Thread (10s timeout)
    8. Close SQLite connections
    9. Unbind ROUTER + PUB sockets
    10. Unlink socket files
    11. Remove registry entry
    12. Exit

  This ensures new connections during the shutdown window get an error
  instead of silently timing out.
```

---

## AP-8: Scores Field -- Opt-In Only

**Status: ACTIVE**

**Problem:** Search response includes per-signal score breakdowns (`scores: { semantic, structural, ... }`) by default. Each chunk gains ~6 extra tokens. With 20 results = ~120 tokens wasted on every query.

**Fix: Move `scores` to `explain` mode only. Default response includes only final `score`.**

```
DEFAULT CHUNK FORMAT (scores excluded):
{
  "content": "...",
  "path": "src/auth/login.ts",
  "symbol_name": "validateToken",
  "kind": "method",
  "start_line": 89,
  "end_line": 112,
  "score": 0.87,                    // final weighted score ONLY
  "token_count": 284,
  "last_modified": "2026-03-20T09:15:00Z",
  "change_summary": "Modified 2h ago",
  "parse_quality": "full"
}

EXPLAIN MODE CHUNK FORMAT (explain=true):
{
  ... all above fields ...
  "scores": {
    "semantic": 0.92,
    "structural": 0.75,
    "change": 0.80,
    "session": 0.60,
    "keyword": 0.95
  }
}
```

---

## AP-9: Protocol Version Handling

**Status: SUPERSEDED -- MCP protocol has its own versioning mechanism**

**Problem:** `"v": 1` in every message but no handling for version mismatch.

**Fix: Explicit version rejection.**

```
VERSION HANDLING:

  Daemon checks request.v before deserializing params:
    if v > SUPPORTED_VERSION:
      return Error {
        code: "VERSION_UNSUPPORTED",
        message: "Protocol version {v} not supported. Daemon supports v1.",
        recoverable: false
      }
    if v < MIN_SUPPORTED_VERSION:
      return Error {
        code: "VERSION_UNSUPPORTED",
        message: "Protocol version {v} too old. Minimum supported: v{MIN}.",
        recoverable: false
      }

  For v1 → v2 migration:
    Daemon supports both v1 and v2 simultaneously for one release cycle.
    v1 requests get v1 responses. v2 requests get v2 responses.
    After deprecation period: MIN_SUPPORTED_VERSION bumped to 2.

  Error code added to Section 3.1:
    VERSION_UNSUPPORTED    Request protocol version not supported by daemon
```

---

## AP-10: Request ID Uniqueness

**Status: SUPERSEDED -- MCP JSON-RPC has its own request ID mechanism**

**Problem:** Monotonic counter can reset on agent restart, causing request_id collisions. Responses correlated to wrong requests.

**Fix: Require UUIDv4 for request_id. Daemon validates uniqueness.**

```
REQUEST ID SPECIFICATION:

  Format: UUIDv4 string (e.g., "550e8400-e29b-41d4-a716-446655440000")
  NOT: monotonic counters, sequential integers, or timestamps

  Daemon:
    Maintains dedup set of request_ids seen in the last 60 seconds.
    If duplicate request_id received → return INVALID_PARAMS error.
    Set is pruned every 60 seconds (remove entries older than 60s).

  Why UUIDv4, not monotonic:
    • Agent restarts reset counters → collision
    • Multiple agents to same daemon → counter space overlap
    • UUIDv4 has 122 bits of entropy → collision probability negligible
```

---

## AP-11: Socket File Permissions + Directory Security

**Status: SUPERSEDED -- no socket files with MCP stdio transport**

**Problem:** On macOS, `$TMPDIR` is per-user-per-session (e.g., `/var/folders/xx/.../T/`), which provides isolation. But the spec uses a shared `shaktiman/` subdirectory. Socket permissions are unspecified.

**Fix: Explicit directory and socket permissions.**

```
SOCKET FILE SECURITY:

  Runtime directory creation:
    mkdir -p {runtime_dir}
    chmod 0700 {runtime_dir}        // owner-only access

  Socket files:
    Created by zmq_bind() — inherits umask
    Daemon sets umask(0077) before bind → socket files are 0600

  macOS specifics:
    $TMPDIR is /var/folders/{hash}/{hash}/T/ — already per-user
    Socket path: $TMPDIR/shaktiman/{repo_id_short}-rpc.sock
    Per-user isolation is automatic

  Linux specifics:
    $XDG_RUNTIME_DIR is /run/user/$UID — per-user, tmpfs-backed
    Socket path: $XDG_RUNTIME_DIR/shaktiman/{repo_id_short}-rpc.sock
    Per-user isolation is automatic via directory permissions

  Fallback (/tmp):
    /tmp/shaktiman-$UID/ with chmod 0700
    Socket files with chmod 0600
```

---

## AP-12: PUB/SUB High Water Mark Configuration

**Status: SUPERSEDED -- MCP notifications replace PUB/SUB; no HWM configuration needed**

**Problem:** Default ZMQ HWM is 1000 messages. During reindex, thousands of `file.enriched` events can overflow the buffer. Agent subscribing to `""` amplifies this.

**Fix: Configure HWM + document event rate expectations.**

```
PUB SOCKET CONFIGURATION:

  ZMQ_SNDHWM = 100                  // max 100 pending messages per subscriber
  Overflow behavior: drop oldest (ZMQ default for PUB)

  This is intentionally low because:
    • Events are best-effort, not guaranteed delivery
    • Agent should query for current state, not replay event log
    • 100 messages is enough for normal operation (~3s of rapid file changes)

SUB SOCKET CONFIGURATION:

  ZMQ_RCVHWM = 100                  // match PUB to avoid asymmetric buffering

TOPIC SUBSCRIPTION GUIDANCE:

  Recommended: subscribe to specific prefixes
    agent.subscribe(["context.", "index."])    // ~2 events per user action

  NOT recommended: subscribe to ""
    Receives ALL events including per-file enrichment notifications
    During cold index of 10K files → 10K file.enriched events

  If agent needs per-file notifications:
    Subscribe to "file.enriched" but process in a non-blocking loop.
    Drop events if processing falls behind (they're informational).
```

---

## AP-13: Enrichment Level / Fallback Chain Naming Alignment

**Status: ACTIVE**

**Problem:** Two naming systems for the same concept: `enrichment_level` field uses strings (full, mixed, structural, keyword_only, degraded) while `strategy` uses level notation (hybrid_l0). Mapping is implicit.

**Fix: Explicit mapping table + use `strategy` as the canonical field.**

```
STRATEGY / ENRICHMENT LEVEL MAPPING:

  strategy field     │ enrichment_level │ Fallback Level │ Signals Available
  ───────────────────│──────────────────│────────────────│──────────────────
  "hybrid_l0"        │ "full"           │ Level 0        │ All 5
  "hybrid_l05"       │ "mixed"          │ Level 0.5      │ 4 (partial semantic)
  "structural_l1"    │ "structural"     │ Level 1        │ 3 (no semantic)
  "keyword_l2"       │ "keyword_only"   │ Level 2        │ 1 (FTS5 only)
  "filesystem_l3"    │ "degraded"       │ Level 3        │ 0 (raw file content)

  Both fields are ALWAYS present in meta:
    "meta": {
      "strategy": "hybrid_l0",            // machine-readable, for programmatic use
      "enrichment_level": "full",         // human-readable, for display
      ...
    }

  These are 1:1 — never independently set.
```

---

## AP-14: Cancel In-Flight Request

**Status: ACTIVE**

**Problem:** DEALER/ROUTER rationale claims "agent can cancel in-flight requests" but no cancel method exists.

**Fix: Add `cancel` method. Daemon makes best-effort to abort.**

```
REQUEST:
{
  "method": "cancel",
  "params": {
    "request_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "cancelled": true,                // true if request was found and cancellation attempted
    "request_state": "in_progress"    // "in_progress" | "completed" | "not_found"
  }
}

SEMANTICS:
  • Best-effort: daemon sets a cancellation flag for the request
  • Query pipeline checks flag between stages (after retrieval, after ranking)
  • If caught in time: query aborted, no response sent for original request
  • If too late: response may have already been sent (agent receives both cancel-ack and result)
  • Agent must handle receiving a response even after cancellation

  This is useful for:
    • Agent decides it doesn't need the result anymore (user typed new query)
    • Long-running context() call that the agent wants to short-circuit
```

---

## AP-15: `notify_change` Rate Limiting

**Status: ACTIVE**

**Problem:** `notify_change` with no rate limit can thrash the enrichment pipeline. Agent (or bug) calling it in a tight loop can starve legitimate background work.

**Fix: Server-side rate limiting + deduplication.**

```
RATE LIMITS FOR notify_change:

  Per-call: max 500 file entries per request
  Per-second: max 10 calls per second per agent
  Deduplication: same file path within 500ms window → coalesced into single event

  If rate exceeded:
    Return error:
    {
      "code": "RATE_LIMITED",
      "message": "notify_change rate limit exceeded. Max 10 calls/second.",
      "recoverable": true,
      "retry_after_ms": 1000
    }

  Error code added:
    RATE_LIMITED    Request rate exceeded for this method
```

---

## Summary

| # | Finding | Fix | Severity | Status |
|---|---|---|---|---|
| AP-1 | Missing `summary` method | Add method + MCP mapping + SDK | HIGH | ACTIVE |
| AP-2 | Registry file race condition + thundering herd | Per-repo flock + atomic registry writes | HIGH | SUPERSEDED |
| AP-3 | PID recycling defeats liveness checks | Process start-time verification | HIGH | SUPERSEDED |
| AP-4 | repo_id 48-bit collision space too small | Extend to 64-bit for sockets, full SHA-256 internal | HIGH | PARTIALLY SUPERSEDED |
| AP-5 | No max message size | 1MB limit + parameter validation bounds | HIGH | ACTIVE |
| AP-6 | `enrich(wait=true)` hangs indefinitely | 10s default timeout, partial results on timeout | HIGH | ACTIVE |
| AP-7 | ROUTER disconnect detection incorrect | Heartbeat protocol + activity-based idle timer | MEDIUM | SUPERSEDED |
| AP-8 | `scores` field wastes tokens by default | Move to explain-only | MEDIUM | ACTIVE |
| AP-9 | Protocol version handling unspecified | Explicit rejection + migration strategy | MEDIUM | SUPERSEDED |
| AP-10 | request_id collision on agent restart | Require UUIDv4, daemon dedup set | MEDIUM | SUPERSEDED |
| AP-11 | Socket file permissions unspecified | 0700 directory, 0600 sockets | MEDIUM | SUPERSEDED |
| AP-12 | PUB/SUB HWM unspecified | HWM=100, document subscription guidance | MEDIUM | SUPERSEDED |
| AP-13 | Enrichment level naming misalignment | Explicit mapping table | LOW | ACTIVE |
| AP-14 | Cancel claim with no cancel method | Add best-effort cancel method | LOW | ACTIVE |
| AP-15 | notify_change can thrash enrichment | Rate limiting + deduplication | LOW | ACTIVE |
