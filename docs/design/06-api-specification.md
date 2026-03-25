# Shaktiman: API & Communication Protocol Specification

> ZeroMQ-based IPC protocol for agent-daemon communication.
> Repo-aware, low-latency, token-efficient.
> Derived from architecture v3, component design, and data model specifications.

---

> **Implementation Decision (Post-Solution-Fit Analysis):** The ZeroMQ transport described in this document has been replaced by an **MCP stdio server** for the implementation. API methods defined here become MCP tools. PUB/SUB events become MCP notifications. The message envelope, error codes, parameter validation, and method semantics remain valid — only the wire transport changes. See `07-implementation-plan.md` §0 for rationale.
>
> **Mapping:** `search` → MCP tool `search` | `context` → MCP tool `context` | `symbols` → MCP tool `symbols` | `dependencies` → MCP tool `dependencies` | `diff` → MCP tool `diff` | `summary` → MCP tool `summary` | `enrich` → MCP tool `enrich` | `ping` / `status` → MCP resource `workspace/summary` | PUB/SUB events → MCP `notifications/resources/updated`

---

## 1. Transport Architecture

### 1.1 Overview

```
Coding Agent (Claude Code)                     Shaktiman Daemon
══════════════════════════                     ════════════════════

  ┌──────────────┐                              ┌──────────────────┐
  │  ZMQ DEALER  │──── ipc://rpc.sock ────────▶│  ZMQ ROUTER      │
  │  (async req) │◀───────────────────────────  │  (async reply)   │
  └──────────────┘                              └──────────────────┘
                                                         │
  ┌──────────────┐                              ┌────────┴─────────┐
  │  ZMQ SUB     │◀─── ipc://evt.sock ─────────│  ZMQ PUB         │
  │  (events)    │                              │  (notifications) │
  └──────────────┘                              └──────────────────┘
```

### 1.2 Socket Pair Design

**Two socket pairs per daemon instance:**

| Socket | Pattern | Direction | Purpose |
|---|---|---|---|
| RPC | DEALER → ROUTER | Bidirectional | Request-reply for all queries and commands |
| Events | PUB → SUB | Daemon → Agent | Push notifications (context changed, index progress) |

**Why DEALER/ROUTER over REQ/REP:**
- Non-blocking: agent can fire multiple queries without waiting for replies
- No deadlock risk from missed messages (REQ/REP envelope trap)
- Agent can cancel in-flight requests
- Correlation via request_id in payload, not ZMQ identity frames

**Why separate PUB/SUB for events:**
- Notifications are fire-and-forget (no reply expected)
- Topic-based filtering (agent subscribes to topics it cares about)
- Different reliability semantics: missed notifications are acceptable (agent queries current state on reconnect)

### 1.3 Transport Configuration

```
SOCKET PATHS:

  Runtime directory:
    Linux:  $XDG_RUNTIME_DIR/shaktiman/    (typically /run/user/$UID/shaktiman/)
    macOS:  $TMPDIR/shaktiman/              (typically /tmp/shaktiman/)
    Fallback: /tmp/shaktiman-$UID/

  Per-repo sockets:
    RPC:    {runtime_dir}/{repo_id}-rpc.sock
    Events: {runtime_dir}/{repo_id}-evt.sock

  repo_id derivation:
    repo_id = SHA-256(canonical_absolute_path_of_repo_root)[:12]
    Example: /Users/dev/projects/myapp → repo_id = "a3f8b2c1e9d0"

  Socket file cleanup:
    • Daemon checks for stale .sock files on startup (liveness ping)
    • Stale files are unlinked before bind
    • On clean shutdown: daemon unlinks both .sock files
```

### 1.4 Serialization

```
PRIMARY: MessagePack (via rmp-serde in Rust)
  • ~4x smaller than JSON on the wire
  • ~2x faster serialize/deserialize vs JSON
  • Schemaless — works directly with serde derive macros
  • Sub-microsecond overhead for typical message sizes

DEBUG MODE: JSON (via --debug-json daemon flag)
  • Human-readable for development/troubleshooting
  • Same message structure, different encoding
  • Toggle at daemon startup, not per-message

WIRE FORMAT:
  [2-byte frame header (ZMQ)] [MessagePack payload]
  Typical request:  50-200 bytes
  Typical response: 500-50,000 bytes (depending on context package size)
```

---

## 2. Repo Awareness

### 2.1 Isolation Model

```
STRICT REPO ISOLATION:

  • ONE daemon process PER repo
  • ONE SQLite database PER repo (.shaktiman/index.db within the repo)
  • ONE socket pair PER repo ({repo_id}-rpc.sock, {repo_id}-evt.sock)
  • NO cross-repo queries — enforced at process level
  • NO shared state between daemon instances

  This is the strongest isolation guarantee possible:
    Process isolation → separate memory spaces
    Database isolation → separate .shaktiman/ directories
    Socket isolation → separate IPC endpoints
    There is no code path that could accidentally cross repos.
```

### 2.2 Repo Registry

```
REGISTRY FILE: {runtime_dir}/shaktiman-registry.json

{
  "repos": {
    "a3f8b2c1e9d0": {
      "path": "/Users/dev/projects/myapp",
      "rpc_socket": "/tmp/shaktiman/a3f8b2c1e9d0-rpc.sock",
      "evt_socket": "/tmp/shaktiman/a3f8b2c1e9d0-evt.sock",
      "pid": 12345,
      "started_at": "2026-03-20T10:30:00Z",
      "status": "ready"
    }
  }
}

OPERATIONS:
  • Daemon writes its entry on startup, removes on clean shutdown
  • Agent reads registry to discover the socket for a given repo path
  • CLI `shaktiman list` reads registry to show all active daemons
  • Stale entries (dead PID) are cleaned on any read
```

### 2.3 repo_id in Every Request

```
EVERY RPC request includes repo_id:

  {
    "request_id": "req-001",
    "repo_id": "a3f8b2c1e9d0",       ← MANDATORY
    "method": "search",
    ...
  }

DAEMON VALIDATION:
  On every request:
    if request.repo_id != daemon.repo_id:
      return Error { code: REPO_MISMATCH, message: "..." }

  This is a safety check — if the agent connects to the wrong socket,
  it gets an immediate error rather than silently querying the wrong repo.
```

### 2.4 Repo Lifecycle

```
REGISTER / START:
  $ shaktiman start /path/to/repo
  1. Resolve canonical path
  2. Compute repo_id = SHA-256(path)[:12]
  3. Check registry: if daemon already running for this repo → return socket info
  4. Start daemon process (daemonize)
  5. Daemon runs boot sequence (CA-7): Storage → Index Stores → Workers → Interface
  6. Daemon writes to registry
  7. Return: { repo_id, rpc_socket, evt_socket }

STOP:
  $ shaktiman stop /path/to/repo
  1. Lookup repo_id in registry
  2. Send SHUTDOWN command via RPC socket
  3. Daemon runs shutdown sequence (CA-7): drain queries → flush → close
  4. Daemon removes registry entry, unlinks socket files

MISSING REPO INDEX:
  If agent connects but .shaktiman/index.db doesn't exist:
    Daemon starts cold index automatically (progressive, background)
    Queries during cold index use fallback chain:
      Level 3 → Level 2 → Level 1 → Level 0 (as stores become ready)
    Status queries return index progress percentage

STALE REPO STATE:
  If agent connects after repo was modified while daemon was down:
    File Watcher (C9) detects stale files on startup via mtime/hash comparison
    Periodic scan (every 5 min) catches anything the watcher missed
    Queries return results with freshness indicator:
      "freshness": "up_to_date" | "reindexing" | "stale"
```

---

## 3. Message Protocol

### 3.1 Envelope Format

Every RPC message follows this envelope:

```
REQUEST ENVELOPE:
{
  "v": 1,                          // protocol version
  "request_id": "req-abc123",      // correlation ID (UUID or monotonic counter)
  "repo_id": "a3f8b2c1e9d0",      // mandatory repo scope
  "method": "search",              // API method name
  "params": { ... }               // method-specific parameters
}

RESPONSE ENVELOPE:
{
  "v": 1,
  "request_id": "req-abc123",      // echoed from request
  "status": "ok",                  // "ok" | "error" | "partial"
  "data": { ... },                // method-specific response (present if ok/partial)
  "error": { ... },               // error details (present if error)
  "meta": {                        // always present
    "latency_ms": 47,
    "strategy": "hybrid_l0",
    "freshness": "up_to_date"
  }
}

ERROR FORMAT:
{
  "code": "INDEX_NOT_READY",
  "message": "Cold index in progress (42% complete). Using keyword-only fallback.",
  "recoverable": true,
  "fallback_used": "keyword_only"
}

ERROR CODES:
  REPO_MISMATCH          Agent connected to wrong daemon
  REPO_NOT_FOUND         No index exists for this repo
  INDEX_NOT_READY        Cold index in progress (partial results available)
  ENRICHMENT_TIMEOUT     Query-time enrichment exceeded 80ms budget
  INVALID_PARAMS         Malformed request parameters
  SYMBOL_NOT_FOUND       Requested symbol/function does not exist in index
  FILE_NOT_FOUND         Requested file does not exist in index
  INTERNAL_ERROR         Unexpected daemon error
  SHUTTING_DOWN          Daemon is shutting down, reject new requests
```

### 3.2 Event Envelope (PUB/SUB)

```
EVENT ENVELOPE:
{
  "v": 1,
  "repo_id": "a3f8b2c1e9d0",
  "topic": "context.changed",       // topic string for SUB filtering
  "timestamp": "2026-03-20T10:35:00Z",
  "data": { ... }                   // topic-specific payload
}

ZMQ TOPIC FILTERING:
  PUB socket sends: [topic_bytes][delimiter][msgpack_payload]
  SUB socket subscribes to topic prefixes:
    "context."     → context.changed, context.rebuilt
    "index."       → index.progress, index.complete, index.error
    "file."        → file.changed, file.deleted
    ""             → all events (empty prefix = subscribe to everything)
```

---

## 4. API Surface

### 4.1 Context Retrieval

#### `search` — Hybrid 5-signal search

Replaces: 5-15 grep/glob/read cycles.

```
REQUEST:
{
  "method": "search",
  "params": {
    "query": "retry logic in payments",
    "budget_tokens": 4096,                // optional, default 8192
    "options": {
      "semantic_weight": null,            // null = use defaults
      "include_paths": ["src/payments/"], // optional path filter
      "exclude_paths": [],
      "kinds": ["function", "method"],    // optional chunk kind filter
      "max_results": 20                   // optional, default 50
    }
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "chunks": [
      {
        "content": "async function retryPayment(txId: string, ...) { ... }",
        "path": "src/payments/retry.ts",
        "symbol_name": "retryPayment",
        "kind": "function",
        "start_line": 42,
        "end_line": 78,
        "score": 0.87,
        "scores": {
          "semantic": 0.92,
          "structural": 0.75,
          "change": 0.80,
          "session": 0.60,
          "keyword": 0.95
        },
        "last_modified": "2026-03-20T09:15:00Z",
        "change_summary": "Modified 2h ago: added timeout parameter",
        "parse_quality": "full",
        "token_count": 284
      }
    ],
    "budget_used": 3840,
    "budget_limit": 4096,
    "total_candidates": 127,
    "enrichment_level": "full"
  },
  "meta": {
    "latency_ms": 62,
    "strategy": "hybrid_l0",
    "freshness": "up_to_date"
  }
}
```

#### `context` — Task-oriented context assembly

Replaces: Manual codebase exploration. Used at task start.

```
REQUEST:
{
  "method": "context",
  "params": {
    "task": "Fix the rate limiting bug in the auth middleware",
    "files": ["src/middleware/auth.ts"],    // optional active file hints
    "budget_tokens": 8192,
    "include_deps": true,                  // include dependency context
    "include_diffs": true                  // include recent change context
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "chunks": [ ... ],                     // same ChunkWithMetadata format as search
    "workspace_summary": {
      "total_files": 342,
      "indexed_files": 342,
      "languages": { "typescript": 280, "python": 62 },
      "embedding_pct": 0.95,
      "recent_hotspots": ["src/middleware/", "src/auth/"]
    },
    "recent_changes": [
      {
        "path": "src/middleware/auth.ts",
        "symbol": "rateLimit",
        "change_type": "modified",
        "when": "3h ago",
        "magnitude": 15
      }
    ],
    "budget_used": 7650,
    "budget_limit": 8192,
    "enrichment_level": "full"
  },
  "meta": {
    "latency_ms": 89,
    "strategy": "hybrid_l0",
    "freshness": "up_to_date"
  }
}
```

#### `symbols` — File symbol listing

Replaces: Reading entire files to find function names. ~95% token reduction.

```
REQUEST:
{
  "method": "symbols",
  "params": {
    "file": "src/middleware/auth.ts",
    "include_signatures": true,            // default true
    "include_visibility": true             // default true
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "file": "src/middleware/auth.ts",
    "language": "typescript",
    "symbols": [
      {
        "name": "AuthMiddleware",
        "kind": "class",
        "line": 15,
        "signature": "class AuthMiddleware",
        "visibility": "exported",
        "children": ["authenticate", "rateLimit", "validateToken"]
      },
      {
        "name": "authenticate",
        "kind": "method",
        "line": 22,
        "signature": "async authenticate(req: Request, res: Response): Promise<void>",
        "visibility": "public",
        "parent": "AuthMiddleware"
      },
      {
        "name": "rateLimit",
        "kind": "method",
        "line": 55,
        "signature": "rateLimit(req: Request, limit: number): boolean",
        "visibility": "public",
        "parent": "AuthMiddleware"
      }
    ],
    "parse_quality": "full"
  },
  "meta": { "latency_ms": 2, "strategy": "index_lookup", "freshness": "up_to_date" }
}
```

#### `dependencies` — Call graph traversal

Replaces: 5-15 grep/read cycles to trace callers/callees. ~94-98% token reduction.

```
REQUEST:
{
  "method": "dependencies",
  "params": {
    "symbol": "validateToken",
    "file": "src/middleware/auth.ts",      // disambiguation if symbol name is common
    "direction": "both",                   // "callers" | "callees" | "both"
    "depth": 2,                            // BFS depth, max 3
    "include_code": false                  // if true, include chunk content
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "symbol": {
      "name": "validateToken",
      "kind": "method",
      "file": "src/middleware/auth.ts",
      "line": 89,
      "signature": "validateToken(token: string): Promise<TokenClaims>"
    },
    "callers": [
      {
        "name": "authenticate",
        "kind": "method",
        "file": "src/middleware/auth.ts",
        "line": 22,
        "edge_kind": "calls",
        "depth": 1
      },
      {
        "name": "handleWebSocket",
        "kind": "function",
        "file": "src/ws/handler.ts",
        "line": 14,
        "edge_kind": "calls",
        "depth": 1
      }
    ],
    "callees": [
      {
        "name": "verify",
        "kind": "function",
        "file": "node_modules/jsonwebtoken/index.d.ts",
        "line": 42,
        "edge_kind": "calls",
        "depth": 1
      },
      {
        "name": "TokenClaims",
        "kind": "type",
        "file": "src/types/auth.ts",
        "line": 8,
        "edge_kind": "type_ref",
        "depth": 1
      }
    ],
    "graph_state": "ready"
  },
  "meta": { "latency_ms": 5, "strategy": "csr_bfs", "freshness": "up_to_date" }
}
```

### 4.2 Diff & Change Handling

#### `diff` — Query recent changes

Replaces: `git log` + manual file reads. ~90% token reduction.

```
REQUEST:
{
  "method": "diff",
  "params": {
    "scope": "src/auth/",                  // path prefix filter
    "since": "24h",                        // "1h" | "24h" | "7d" | ISO8601 timestamp
    "include_impact": true,                // trace downstream callers of changed symbols
    "max_results": 50
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "changes": [
      {
        "file": "src/auth/login.ts",
        "change_type": "modify",
        "timestamp": "2026-03-20T08:30:00Z",
        "lines_added": 12,
        "lines_removed": 3,
        "magnitude": 15,
        "symbols_affected": [
          {
            "name": "validateCredentials",
            "change_type": "modified",
            "detail": "Added rate limiting check"
          },
          {
            "name": "LoginAttempt",
            "change_type": "signature_changed",
            "detail": "Added 'attempt_count' field"
          }
        ]
      }
    ],
    "impact": [
      {
        "changed_symbol": "validateCredentials",
        "impacted_callers": [
          { "name": "handleLogin", "file": "src/routes/auth.ts", "edge_kind": "calls" },
          { "name": "loginTest", "file": "tests/auth.test.ts", "edge_kind": "calls" }
        ]
      }
    ],
    "total_changes": 3,
    "time_range": { "from": "2026-03-19T10:35:00Z", "to": "2026-03-20T10:35:00Z" }
  },
  "meta": { "latency_ms": 8, "strategy": "diff_query", "freshness": "up_to_date" }
}
```

#### `notify_change` — Inform daemon of external file change

For when the agent modifies files directly (bypassing the OS watcher debounce).

```
REQUEST:
{
  "method": "notify_change",
  "params": {
    "files": [
      { "path": "src/auth/login.ts", "change_type": "modify" },
      { "path": "src/auth/types.ts", "change_type": "modify" }
    ]
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "accepted": 2,
    "enrichment_queued": true
  },
  "meta": { "latency_ms": 1, "strategy": "enqueue", "freshness": "reindexing" }
}
```

### 4.3 Enrichment Control

#### `enrich` — Request enrichment of specific files/symbols

```
REQUEST:
{
  "method": "enrich",
  "params": {
    "targets": [
      { "file": "src/new-module/handler.ts" },
      { "file": "src/new-module/types.ts" }
    ],
    "priority": "high",                    // "high" (P0) | "normal" (P2) | "background" (P3)
    "wait": false                          // if true, block until enrichment complete
  }
}

RESPONSE (wait=false):
{
  "status": "ok",
  "data": {
    "enqueued": 2,
    "estimated_ms": 150
  }
}

RESPONSE (wait=true, with timeout):
{
  "status": "ok",
  "data": {
    "enriched": 2,
    "results": [
      { "file": "src/new-module/handler.ts", "chunks": 5, "symbols": 4, "edges": 12 },
      { "file": "src/new-module/types.ts", "chunks": 3, "symbols": 3, "edges": 0 }
    ]
  },
  "meta": { "latency_ms": 95 }
}
```

#### `enrichment_status` — Check enrichment state of files/symbols

```
REQUEST:
{
  "method": "enrichment_status",
  "params": {
    "targets": [
      { "file": "src/new-module/handler.ts" }
    ]
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "files": [
      {
        "path": "src/new-module/handler.ts",
        "indexed": true,
        "stale": false,
        "parse_quality": "full",
        "embedding_status": "partial",
        "chunks_count": 5,
        "symbols_count": 4,
        "last_indexed": "2026-03-20T10:30:00Z"
      }
    ]
  }
}
```

### 4.4 Repo Management

#### `status` — Daemon and index status

```
REQUEST:
{
  "method": "status",
  "params": {}
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "repo": {
      "repo_id": "a3f8b2c1e9d0",
      "path": "/Users/dev/projects/myapp",
      "uptime_seconds": 3600
    },
    "index": {
      "state": "ready",                   // "cold_indexing" | "ready" | "reindexing"
      "progress_pct": 100,
      "total_files": 342,
      "indexed_files": 342,
      "stale_files": 0,
      "total_chunks": 2840,
      "total_symbols": 1920,
      "total_edges": 5600
    },
    "stores": {
      "metadata": "ready",
      "graph": "ready",                    // "building" | "ready"
      "vector": "ready",
      "fts": "ready",
      "diff": "ready"
    },
    "embedding": {
      "status": "closed",                  // circuit breaker: "disabled"|"closed"|"open"|"half_open"
      "model": "nomic-embed-text-v1.5",
      "completion_pct": 0.95,
      "queue_depth": 42
    },
    "writer_thread": {
      "queue_depth": 3,
      "queue_depth_by_priority": { "p0": 0, "p1": 0, "p2": 3, "p3": 0 }
    },
    "memory_mb": 45,
    "disk_mb": 72
  }
}
```

#### `reindex` — Trigger full reindex

```
REQUEST:
{
  "method": "reindex",
  "params": {
    "scope": "full",                       // "full" | "stale" | "embeddings"
    "force": false                         // if true, drop and rebuild from scratch
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "action": "reindex_started",
    "scope": "full",
    "estimated_files": 342,
    "subscribe_topic": "index.progress"    // agent can SUB to this for progress
  }
}
```

#### `shutdown` — Gracefully stop daemon

```
REQUEST:
{
  "method": "shutdown",
  "params": {
    "drain_timeout_ms": 5000               // max wait for in-flight queries
  }
}

RESPONSE:
{
  "status": "ok",
  "data": {
    "drained_queries": 1,
    "flushed_session": true
  }
}
```

### 4.5 Developer Ergonomics

#### `ping` — Liveness check

```
REQUEST:
{
  "method": "ping",
  "params": {}
}

RESPONSE:
{
  "status": "ok",
  "data": { "pong": true, "uptime_seconds": 3600 },
  "meta": { "latency_ms": 0 }
}
```

#### Request-Level Options (available on all query methods)

```
COMMON OPTIONS (available in any request's params):

  "options": {
    "force_refresh": false,        // bypass all caches, re-query stores
    "explain": false,              // include detailed scoring breakdown in response
    "freshness_max_age_ms": 5000,  // accept cached results up to this age
    "timeout_ms": 200,             // max query time, return partial on timeout
    "session_id": "sess-abc"       // explicit session ID for session scoring
  }
```

**`force_refresh`:**
```
When true:
  • Bypass query embedding cache
  • Bypass resource cache
  • Re-read from all stores (not from any in-memory cache)
  • Does NOT trigger re-enrichment (use `enrich` for that)

Use case: agent suspects stale results after rapid file changes.
```

**`explain`:**
```
When true, response includes detailed scoring:
  "explanation": {
    "query_embedding_cached": false,
    "stores_queried": ["vector", "graph", "fts5", "diff", "session"],
    "fallback_level": 0,
    "weight_redistribution": null,
    "candidates_before_rank": 127,
    "candidates_after_rank": 50,
    "top_chunk_score_breakdown": {
      "semantic_raw": 0.84, "semantic_norm": 0.92,
      "structural_raw": 2, "structural_norm": 0.33,
      "change_raw": 0.72, "change_norm": 0.72,
      "session_raw": 3, "session_norm": 0.60,
      "keyword_raw": 12.5, "keyword_norm": 0.95,
      "weighted_final": 0.87
    },
    "enrichment_triggered": false,
    "timing": {
      "router_ms": 2,
      "embed_query_ms": 0,
      "vector_search_ms": 28,
      "graph_bfs_ms": 8,
      "fts5_ms": 5,
      "diff_score_ms": 4,
      "session_score_ms": 1,
      "rank_ms": 3,
      "assemble_ms": 11
    }
  }

Use case: debugging ranking quality. Not for production queries (adds ~50 tokens overhead).
```

---

## 5. Event Topics (PUB/SUB)

### 5.1 Topic Taxonomy

```
TOPICS:

  context.changed          Working set shifted, resource re-assembled
  context.rebuilt           Full context resource rebuilt (branch switch, reindex)

  index.progress           Cold index or reindex progress update
  index.complete           Index operation finished
  index.error              Index operation failed

  file.enriched            Single file enrichment completed
  file.deleted             File removed from index

  embedding.progress       Embedding completion percentage changed
  embedding.status         Circuit breaker state changed

  daemon.shutting_down     Daemon is about to shut down
```

### 5.2 Event Payloads

```
CONTEXT CHANGED:
{
  "topic": "context.changed",
  "data": {
    "event": "file_change",               // "file_change"|"branch_switch"|"session_shift"
    "affected_files": ["src/auth/login.ts"],
    "working_set_size": 24
  }
}

INDEX PROGRESS:
{
  "topic": "index.progress",
  "data": {
    "operation": "cold_index",             // "cold_index"|"reindex"|"reindex_embeddings"
    "files_done": 150,
    "files_total": 342,
    "pct": 44,
    "elapsed_ms": 12000
  }
}

EMBEDDING STATUS:
{
  "topic": "embedding.status",
  "data": {
    "circuit_breaker": "closed",
    "completion_pct": 0.72,
    "queue_depth": 2100
  }
}

DAEMON SHUTTING DOWN:
{
  "topic": "daemon.shutting_down",
  "data": {
    "reason": "user_request",              // "user_request"|"signal"|"error"
    "drain_timeout_ms": 5000
  }
}
```

---

## 6. Performance & Latency Design

### 6.1 Latency Classifications

| Category | Target | Methods |
|---|---|---|
| **Ultra-fast** (<5ms) | Index lookups, no compute | `ping`, `symbols`, `enrichment_status`, `status` |
| **Fast** (<50ms) | Single-store queries | `dependencies`, `diff`, `notify_change` |
| **Standard** (<200ms) | Multi-store fan-out + ranking | `search`, `context` |
| **Async** (no wait) | Background operations | `enrich(wait=false)`, `reindex`, `shutdown` |

### 6.2 Latency Budget Breakdown (search/context)

```
TOTAL BUDGET: 200ms p95

  ZMQ IPC overhead:        0.1-0.2ms
  MessagePack deser:       0.05ms
  Request validation:      0.1ms
  Query Router:            2-5ms        (flag checks, enrichment decision)
  Query embedding:         0-8ms        (cache hit: 0ms; miss: ~8ms via Ollama)
  Parallel store fan-out:  25-60ms      (max of 5 parallel reads)
    ├ Vector search:       20-50ms
    ├ CSR BFS:             3-12ms
    ├ FTS5:                4-8ms
    ├ Diff scores:         4-8ms
    └ Session scores:      1-2ms
  Normalize + rank:        3-7ms
  Context assembly:        10-25ms
  MessagePack ser:         0.1-0.5ms
  ───────────────────────────────────
  TYPICAL:                 50-105ms
  p95:                     <150ms
  p99:                     <200ms
```

### 6.3 Synchronous vs Asynchronous Operations

```
SYNCHRONOUS (agent blocks waiting for response):

  search          → full pipeline, returns ContextPackage
  context         → full pipeline + workspace summary + recent diffs
  symbols         → index lookup, trivial
  dependencies    → CSR BFS or SQL, fast
  diff            → SQL query, fast
  enrichment_status → index lookup, trivial
  status          → in-memory reads, trivial
  ping            → no-op

  All sync operations must complete within timeout_ms (default 200ms).
  If timeout exceeded: return partial results with status "partial".

ASYNCHRONOUS (agent fires and monitors via events):

  enrich(wait=false)  → queues enrichment jobs, returns immediately
  reindex             → starts background reindex, returns immediately
  notify_change       → queues watcher events, returns immediately
  shutdown            → starts drain, returns immediately

  Agent monitors progress via PUB/SUB events:
    index.progress, file.enriched, embedding.progress, etc.
```

### 6.4 Caching Strategy

```
CACHE LAYER 1: Query Embedding Cache (in-daemon)
  LRU<query_text, Vec<f32>>, capacity 100 entries (~3MB)
  Hit: skip Ollama call, save ~8ms
  Invalidation: LRU eviction only (embeddings are deterministic per model)

CACHE LAYER 2: Resource Cache (in-daemon)
  Pre-assembled ContextPackage for shaktiman://context/active
  Rebuilt on: file change, session shift, branch switch, 60s idle
  Debounce: 500ms from last trigger event
  Served via: search/context with freshness_max_age_ms option

CACHE LAYER 3: SQLite Page Cache (per-connection)
  Writer: 8MB, Readers: 4MB each (total 24MB)
  Managed by SQLite internally

CACHE LAYER 4: CSR Graph (in-memory)
  Entire graph cached in CSR format (~17MB at 1M lines)
  Delta buffer for incremental updates
  Compaction: when delta > 5000 entries OR every 60s idle

NO CACHE for:
  • Diff scores (time-dependent, must be fresh)
  • Session scores (mutation-dependent, always live)
  • Enrichment status (must reflect current state)

force_refresh=true BYPASSES:
  Layer 1 (query embedding cache)
  Layer 2 (resource cache)
  Does NOT bypass Layer 3/4 (those are internal to stores)
```

### 6.5 Avoiding Enrichment Blocking

```
PRINCIPLE: Queries NEVER wait for enrichment to complete.

QUERY-TIME ENRICHMENT (sync, bounded):
  • Budget: 80ms max for single-file enrichment
  • If file is small (<2000 lines) and within budget → enrich inline
  • Results passed directly to query pipeline (not through Writer Thread)
  • Ephemeral chunk IDs (negative) for unwritten chunks
  • Writer Thread persist happens async AFTER query returns

ENRICHMENT QUEUED (async, unbounded):
  • Large files, recursive deps, embedding generation
  • Queued to Writer Thread at appropriate priority
  • Query returns best-available results NOW
  • Response includes enrichment_level to indicate quality:
    "full"         All stores had data
    "mixed"        Some chunks lacked embeddings
    "structural"   No semantic, structural + keyword only
    "keyword_only" FTS5 only
    "degraded"     Filesystem passthrough, minimal ranking

CALLER PATTERN:
  Agent calls search() → gets results in <200ms
  Agent can call enrichment_status() to check if better data is coming
  Agent can subscribe to file.enriched events for updates
  Agent can re-query after enrichment for improved results
```

---

## 7. Token Efficiency

### 7.1 Response Structure Principles

```
PRINCIPLE 1: SUMMARIES BEFORE CODE
  Responses lead with metadata (symbol name, path, score).
  Raw code content is included but positioned for truncation.
  Agent can read metadata and decide whether to look at the code.

PRINCIPLE 2: NODE REFERENCES
  For graph results (dependencies), return symbol references, not full code.
  Agent uses references to make targeted follow-up queries.

  Compact reference: { "name": "validate", "file": "auth.ts", "line": 42 }   ~15 tokens
  Full chunk:        "function validate(token: string): ..."                   ~200 tokens

  Agent gets the reference list first (~150 tokens for 10 deps),
  then fetches specific chunks only if needed.

PRINCIPLE 3: BUDGET ENFORCEMENT
  Every response fits within the requested budget_tokens.
  Pre-computed token_count per chunk eliminates runtime counting.
  Safety margin: 95% of stated budget used as effective limit.

PRINCIPLE 4: INCREMENTAL DETAIL
  symbols()        → ~100 tokens (names + signatures only)
  dependencies()   → ~150 tokens (references, no code)
  search()         → ~4000 tokens (code + metadata, budget-fitted)
  context()        → ~8000 tokens (full context package)

  Agent starts cheap, drills down only when needed.
```

### 7.2 Compact Chunk Format

```
FULL FORMAT (default for search/context):
{
  "content": "...",                // raw source code
  "path": "src/auth/login.ts",
  "symbol_name": "validateToken",
  "kind": "method",
  "start_line": 89,
  "end_line": 112,
  "score": 0.87,
  "token_count": 284,
  "last_modified": "2026-03-20T09:15:00Z",
  "change_summary": "Modified 2h ago",
  "parse_quality": "full"
}
Overhead: ~12 tokens metadata per chunk (~4% of 300-token avg chunk)

REFERENCE FORMAT (for dependencies, impact lists):
{
  "name": "validateToken",
  "kind": "method",
  "file": "src/auth/login.ts",
  "line": 89,
  "edge_kind": "calls"
}
~15 tokens per reference. 10 references = ~150 tokens.

SUMMARY FORMAT (for workspace overview):
{
  "file": "src/auth/login.ts",
  "symbols": 8,
  "recent_changes": 2,
  "hotspot_rank": 3
}
~10 tokens per file summary. 20 files = ~200 tokens.
```

### 7.3 Pagination

```
NOT USED FOR MOST QUERIES:
  Budget-fitting in the Context Assembler already limits response size.
  If you ask for 4096 tokens, you get at most 4096 tokens.
  There is no "page 2" — the budget IS the pagination.

USED FOR:
  diff() with large change sets:
    Default: max_results=50
    If total_changes > max_results:
      response includes "has_more": true, "next_cursor": "..."
      Agent sends: { "method": "diff", "params": { "cursor": "..." } }

  dependencies() with high-fan-in symbols:
    Default: depth=2, which caps results naturally via BFS
    If a symbol has 200+ callers at depth 1:
      Response capped at 50, includes "truncated": true
      Agent can request depth=1 with include_code=true for specific callers
```

---

## 8. Failure & Fallback Strategy

### 8.1 Daemon Unavailable

```
AGENT BEHAVIOR WHEN DAEMON IS DOWN:

  1. Agent tries to connect to RPC socket.
  2. ZMQ DEALER socket connect succeeds even if daemon is down
     (ZMQ connects asynchronously).
  3. Agent sends request. No response within timeout (default 2000ms).
  4. Agent detects: daemon unavailable.
  5. FALLBACK: agent operates without Shaktiman.
     • Uses standard file system tools (grep, glob, read)
     • No ranking, no context assembly, no push mode
     • Agent should log: "Shaktiman unavailable, using filesystem fallback"
  6. Agent can retry periodically (every 30s) or on next task.

AGENT-SIDE PSEUDOCODE:
  response = zmq_send_with_timeout(request, timeout=2000ms)
  if response is None:
    log("Shaktiman unavailable, falling back to filesystem")
    return filesystem_fallback(request)
```

### 8.2 Repo Not Indexed

```
DAEMON BEHAVIOR WHEN INDEX IS MISSING:

  1. Agent sends request for a repo with no .shaktiman/index.db.
  2. Daemon detects: no index exists.
  3. Daemon starts cold index automatically (background, progressive).
  4. Daemon responds with partial results:

  {
    "status": "partial",
    "data": {
      "chunks": [ ... ],                   // filesystem passthrough results (Level 3)
      "enrichment_level": "degraded"
    },
    "error": {
      "code": "INDEX_NOT_READY",
      "message": "Cold index started (0% complete). Serving filesystem fallback.",
      "recoverable": true,
      "fallback_used": "filesystem_passthrough"
    },
    "meta": {
      "strategy": "filesystem_l3",
      "freshness": "stale"
    }
  }

  5. Agent subscribes to index.progress events for updates.
  6. When index reaches Level 2 readiness (FTS5 built):
     Agent re-queries, gets keyword results.
  7. Progressive improvement: Level 3 → 2 → 1 → 0.5 → 0.
```

### 8.3 Stale Index

```
DAEMON BEHAVIOR WHEN INDEX IS STALE:

  1. Agent query arrives. Daemon checks freshness.
  2. If files changed since last index:
     a. Freshness = "reindexing" (watcher has detected, enrichment queued)
     b. Daemon serves CURRENT index data (stale but available)
     c. Response includes freshness: "reindexing"
  3. Agent decision: use stale results or wait.
     If agent needs fresh data:
       Call enrich(targets=[...], priority="high", wait=true)
       Then re-query.

  FRESHNESS FIELD (always present in meta):
    "up_to_date"    Index matches working tree (no pending changes)
    "reindexing"    Changes detected, enrichment in progress
    "stale"         Changes detected but not yet queued (rare, transient)
```

### 8.4 Partial Store Failures

```
INDIVIDUAL STORE FAILURES:

  Vector store timeout → exclude semantic score, redistribute weights
  CSR BFS timeout     → exclude structural score, redistribute weights
  FTS5 error          → exclude keyword score, redistribute weights
  Diff store error    → exclude change score, redistribute weights
  Session store error → exclude session score, redistribute weights

  Response always includes which stores were queried:
  "meta": {
    "strategy": "hybrid_l0",
    "stores_queried": ["vector", "fts5", "diff", "session"],  // graph missing
    "stores_failed": ["graph"],
    "weight_redistribution": {
      "semantic": 0.50,
      "structural": 0.00,          // graph failed
      "change": 0.15,
      "session": 0.15,
      "keyword": 0.20
    }
  }
```

---

## 9. Connection Lifecycle

### 9.1 Agent Connection Flow

```
AGENT STARTUP:

  1. Read repo path from current working directory (or config)
  2. Compute repo_id = SHA-256(canonical_path)[:12]
  3. Check registry file for existing daemon
     a. If daemon exists + PID alive → connect to existing sockets
     b. If daemon exists + PID dead → clean registry, start new daemon
     c. If no entry → start new daemon
  4. Connect ZMQ DEALER to rpc socket
  5. Connect ZMQ SUB to evt socket, subscribe to "context." and "index."
  6. Send ping() to verify connectivity
  7. If ping fails → start daemon, retry
  8. Ready.

AGENT DISCONNECT:

  1. Close ZMQ sockets (DEALER + SUB)
  2. Daemon detects disconnect (ROUTER notices identity gone)
  3. Daemon continues running (serves other potential connections, CLI)
  4. After 30 minutes with no connections → daemon auto-shuts down
     (configurable via config.idle_shutdown_minutes, default 30)
```

### 9.2 Daemon Lifecycle

```
DAEMON STARTUP (CA-7 boot sequence):

  Phase 1: STORAGE
    Open SQLite (WAL mode), run migrations, start Writer Thread

  Phase 2: INDEX STORES
    Initialize all stores, start CSR build (background)

  Phase 3: BACKGROUND WORKERS
    Start Embedding Worker, File Watcher, Enrichment Pipeline

  Phase 4: INTERFACE
    Bind ZMQ ROUTER to rpc socket
    Bind ZMQ PUB to evt socket
    Write registry entry
    Begin accepting requests

  READINESS:
    Daemon accepts queries as soon as Phase 4 starts.
    If stores aren't fully ready: fallback chain handles gracefully.

DAEMON SHUTDOWN:

  1. Receive shutdown command (or SIGTERM)
  2. Publish daemon.shutting_down event
  3. Stop accepting new requests (unbind ROUTER)
  4. Drain in-flight queries (5s timeout)
  5. Stop File Watcher
  6. Flush Session Store
  7. Drain Writer Thread (10s timeout)
  8. Stop Embedding Worker
  9. Close SQLite connections
  10. Unlink socket files
  11. Remove registry entry
```

---

## 10. Complete Method Reference

| Method | Category | Sync | Target Latency | Token Efficiency |
|---|---|---|---|---|
| `ping` | System | Sync | <1ms | N/A |
| `status` | System | Sync | <5ms | N/A |
| `shutdown` | System | Async | <5ms | N/A |
| `reindex` | System | Async | <5ms (enqueue) | N/A |
| `search` | Query | Sync | <200ms | Budget-fitted, ranked chunks |
| `context` | Query | Sync | <200ms | Full task context + summary |
| `symbols` | Query | Sync | <5ms | ~100 tokens (names + signatures) |
| `dependencies` | Query | Sync | <50ms | ~150 tokens (references) |
| `diff` | Query | Sync | <50ms | Symbol-level change summary |
| `notify_change` | Mutation | Async | <5ms (enqueue) | N/A |
| `enrich` | Mutation | Both | <5ms async / <200ms sync | N/A |
| `enrichment_status` | Query | Sync | <5ms | N/A |

---

## 11. Agent SDK Interface (Rust)

Thin client library the agent uses to communicate with the daemon:

```rust
/// Shaktiman client — connects to a running daemon via ZMQ IPC.
pub struct ShaktimanClient {
    rpc: zmq::Socket,       // DEALER
    events: zmq::Socket,    // SUB
    repo_id: String,
    timeout_ms: u64,
}

impl ShaktimanClient {
    /// Connect to daemon for the given repo path.
    /// Starts daemon if not running.
    pub fn connect(repo_path: &Path) -> Result<Self>;

    /// Liveness check.
    pub fn ping(&self) -> Result<PingResponse>;

    /// Hybrid 5-signal search.
    pub fn search(&self, query: &str, opts: SearchOptions) -> Result<SearchResponse>;

    /// Task-oriented context assembly.
    pub fn context(&self, task: &str, files: &[&str], opts: ContextOptions) -> Result<ContextResponse>;

    /// List symbols in a file.
    pub fn symbols(&self, file: &str) -> Result<SymbolsResponse>;

    /// Call graph traversal.
    pub fn dependencies(&self, symbol: &str, file: &str, opts: DepsOptions) -> Result<DepsResponse>;

    /// Query recent changes.
    pub fn diff(&self, scope: &str, since: &str, opts: DiffOptions) -> Result<DiffResponse>;

    /// Notify daemon of file changes.
    pub fn notify_change(&self, files: &[FileChange]) -> Result<NotifyResponse>;

    /// Request enrichment.
    pub fn enrich(&self, targets: &[EnrichTarget], opts: EnrichOptions) -> Result<EnrichResponse>;

    /// Check enrichment status.
    pub fn enrichment_status(&self, targets: &[EnrichTarget]) -> Result<EnrichStatusResponse>;

    /// Index/daemon status.
    pub fn status(&self) -> Result<StatusResponse>;

    /// Subscribe to events. Returns a stream/receiver.
    pub fn subscribe(&self, topics: &[&str]) -> Result<EventReceiver>;

    /// Trigger reindex.
    pub fn reindex(&self, scope: ReindexScope) -> Result<ReindexResponse>;

    /// Graceful shutdown.
    pub fn shutdown(&self) -> Result<ShutdownResponse>;
}

/// Options available on all query methods.
pub struct CommonOptions {
    pub force_refresh: bool,
    pub explain: bool,
    pub timeout_ms: Option<u64>,
    pub session_id: Option<String>,
}
```

---

## 12. MCP Compatibility Layer

The ZeroMQ protocol is the primary IPC layer. MCP is a thin adapter on top:

```
MCP SERVER (C1) — TRANSLATION LAYER:

  MCP tool call: search(query, budget?)
    → ShaktimanClient.search(query, SearchOptions { budget_tokens: budget })
    → Serialize ContextPackage to MCP JSON-RPC response

  MCP resource read: shaktiman://context/active
    → ShaktimanClient.context(task="", files=[], ContextOptions { budget: 4096 })
    → Cached in Resource Manager, served on read

  MCP prompt: task-start(task_description)
    → ShaktimanClient.context(task=task_description, ContextOptions { budget: 8192 })
    → Format as system prompt

  MCP notification: context/changed
    → EventReceiver.recv() for "context.changed" topic
    → Forward as MCP notification

  The MCP server is a thin TRANSLATOR, not a separate system.
  It connects to the daemon via the same ZMQ client as any other consumer.
  This means: CLI, MCP, and any future interface share the same protocol.
```

---

## Status

**Step 6: API & Communication Protocol** — Complete. Awaiting critique validation and confirmation before next step.
