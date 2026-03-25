# Shaktiman: Data Model & Schema Specification

> Complete data model for all entities, relationships, embeddings, diffs, and query patterns.
> Derived from [03-architecture-v3.md](./03-architecture-v3.md), [04-component-design.md](./04-component-design.md), and their addenda.

---

## 1. Core Entities (Nodes)

### 1.1 File

The root container. Every indexed source file is a File node.

```
FILE
  id:               INTEGER PRIMARY KEY (auto-increment)
  path:             TEXT UNIQUE NOT NULL          -- project-relative: "src/auth/login.ts"
  content_hash:     TEXT NOT NULL                 -- SHA-256 of file content (change detection)
  mtime:            REAL NOT NULL                 -- last modified timestamp (seconds since epoch)
  size:             INTEGER                       -- file size in bytes
  language:         TEXT                          -- "typescript" | "python" | "go" | "rust" | ...
  indexed_at:       TEXT                          -- ISO8601 timestamp of last enrichment
  embedding_status: TEXT DEFAULT 'pending'        -- 'pending' | 'partial' | 'complete'
  parse_quality:    TEXT DEFAULT 'full'           -- 'full' | 'partial' | 'error' | 'unparseable'

  UNIQUE IDENTIFIER: path (logical), id (internal)
  VERSIONING:        content_hash serves as the version. No historical file versions stored.
                     Each enrichment overwrites the previous state (current-state model).
                     Change history is captured in diff_log, not in file snapshots.
```

**Why current-state, not versioned snapshots:** Shaktiman indexes the working tree, not git history. The diff_log captures what changed and when. Storing full file snapshots per change would bust the 500MB disk budget at scale.

---

### 1.2 Chunk

The unit of retrieval. Code is split at semantic boundaries (functions, classes, methods, type declarations). Chunks are what the agent receives.

```
CHUNK
  id:               INTEGER PRIMARY KEY (auto-increment)
  file_id:          INTEGER NOT NULL → File.id    (CASCADE delete)
  parent_chunk_id:  INTEGER → Chunk.id            -- method → class nesting
  chunk_index:      INTEGER NOT NULL              -- positional order within file (0, 1, 2, ...)
  symbol_name:      TEXT                          -- "validateToken", "AuthService", NULL for headers
  kind:             TEXT NOT NULL                 -- 'function' | 'class' | 'method' | 'type'
                                                 -- | 'interface' | 'header' | 'block'
  start_line:       INTEGER NOT NULL
  end_line:         INTEGER NOT NULL
  content:          TEXT NOT NULL                 -- raw source code of this chunk
  token_count:      INTEGER NOT NULL              -- pre-computed via cl100k_base
  signature:        TEXT                          -- "fn validate(token: &str) -> Result<Claims>"
  parse_quality:    TEXT DEFAULT 'full'           -- 'full' | 'partial'

  UNIQUE IDENTIFIER: (file_id, chunk_index) is a stable reference.
                     id is the internal PK, but may change on re-enrichment.
  VERSIONING:        Chunks are replaced on each file enrichment. No chunk history.
                     The diff_log + diff_symbols track what changed.
```

**Chunk kinds explained:**

| Kind | Content | Typical Token Count |
|---|---|---|
| `function` | Standalone function / top-level fn | 50-500 |
| `class` | Class declaration without method bodies | 20-100 (shell only) |
| `method` | Method inside a class (parent_chunk_id → class chunk) | 50-400 |
| `type` | Type alias, struct, enum declaration | 20-200 |
| `interface` | Interface / trait / protocol definition | 30-150 |
| `header` | File-level: imports, constants, module docstring | 20-300 |
| `block` | Anonymous block, top-level statement group | 30-200 |

**Chunk splitting rules:**
- Primary: one chunk per top-level symbol boundary
- Nested: class methods become separate chunks with `parent_chunk_id` → class
- Oversized: chunks > 1024 tokens → split at logical sub-boundaries (inner blocks, statement groups)
- Minimum: chunks < 20 tokens → merge with adjacent chunk
- Header: imports + constants + top-level declarations → single `header` chunk per file

---

### 1.3 Symbol

A named code entity. Symbols are the nodes in the dependency graph. Every chunk has zero or more symbols; every symbol belongs to exactly one chunk.

```
SYMBOL
  id:               INTEGER PRIMARY KEY (auto-increment)
  chunk_id:         INTEGER NOT NULL → Chunk.id   (CASCADE delete)
  file_id:          INTEGER NOT NULL → File.id
  name:             TEXT NOT NULL                  -- "validateToken", "AuthService"
  qualified_name:   TEXT                           -- "auth.service.AuthService.validateToken"
  kind:             TEXT NOT NULL                  -- 'function' | 'class' | 'method' | 'type'
                                                  -- | 'interface' | 'variable' | 'constant'
  line:             INTEGER NOT NULL               -- definition line number
  signature:        TEXT                           -- full type signature
  visibility:       TEXT                           -- 'public' | 'private' | 'internal' | 'exported'
  is_exported:      BOOLEAN DEFAULT FALSE          -- whether exported from module

  UNIQUE IDENTIFIER: (file_id, name, kind) is the logical identity for resolution.
                     id is internal. Changes on re-enrichment.
  VERSIONING:        Symbols are replaced on each file enrichment (same as chunks).
                     Symbol history tracked via diff_symbols.
```

**Symbol vs Chunk:** A chunk is a retrieval unit (text the agent sees). A symbol is a graph node (thing that has relationships). A `class` chunk may contain 0 method bodies but define 1 class symbol. Each method has its own chunk AND its own symbol.

---

### 1.4 Diff (First-Class Entity)

Diffs are NOT metadata. They are full entities tracking what changed, when, and which symbols were affected. Diffs power the change signal for ranking and the `diff()` MCP tool.

```
DIFF_LOG
  id:               INTEGER PRIMARY KEY (auto-increment)
  file_id:          INTEGER NOT NULL → File.id    (CASCADE delete)
  timestamp:        TEXT NOT NULL                  -- ISO8601 of when change was detected
  change_type:      TEXT NOT NULL                  -- 'add' | 'modify' | 'delete' | 'rename'
  lines_added:      INTEGER DEFAULT 0
  lines_removed:    INTEGER DEFAULT 0
  magnitude:        INTEGER GENERATED ALWAYS AS (lines_added + lines_removed)  -- virtual column
  hash_before:      TEXT                           -- content_hash before change (NULL for 'add')
  hash_after:       TEXT                           -- content_hash after change (NULL for 'delete')

  ENRICHMENT STATUS is implicit from the data present:
    RAW:         diff_log row exists, no diff_symbols rows → hunk recorded, not analyzed
    STRUCTURAL:  diff_symbols rows exist with change_type set
    PARTIAL:     some diff_symbols rows, symbol resolution incomplete (pending_edges)
    FULL:        all diff_symbols resolved, downstream impact traced
```

```
DIFF_SYMBOLS
  id:               INTEGER PRIMARY KEY (auto-increment)
  diff_id:          INTEGER NOT NULL → DiffLog.id (CASCADE delete)
  symbol_id:        INTEGER → Symbol.id            (SET NULL on delete — symbol may be gone)
  symbol_name:      TEXT NOT NULL                   -- denormalized, survives symbol deletion
  change_type:      TEXT NOT NULL                   -- 'added' | 'modified' | 'removed'
                                                   -- | 'signature_changed' | 'moved'
  chunk_id:         INTEGER → Chunk.id              (SET NULL — links diff to the affected chunk)

  UNIQUE IDENTIFIER: (diff_id, symbol_name) — one entry per affected symbol per diff.
```

**Diff lifecycle:**

```
File saved at t=0
  │
  ├─ C9 (Monitor) detects change
  │
  ├─ C7 (Enrichment Pipeline):
  │    1. Parse new AST, extract new symbols/chunks
  │    2. Diff Engine compares OLD chunks (from C5) with NEW chunks
  │    3. Produces DiffRecord:
  │       diff_log:     { file_id, timestamp, 'modify', +12, -3, hash_old, hash_new }
  │       diff_symbols: [
  │         { symbol: "validateToken", change: "modified" },
  │         { symbol: "TokenClaims",   change: "signature_changed" },
  │         { symbol: "refreshToken",  change: "added" }
  │       ]
  │
  ├─ C8 (Writer Thread): persists to SQLite in single transaction
  │
  └─ Available for:
       • change_score in ranking: recency_decay(hours) × min(magnitude/50, 1.0)
       • diff() MCP tool: "what changed in auth/ since yesterday?"
       • Impact analysis: which chunks were recently modified?
```

**Diff connects to other entities:**

```
diff_log ──→ files         (which file changed)
diff_symbols ──→ symbols   (which symbols were affected)
diff_symbols ──→ chunks    (which chunks were affected — for change_score)
diff_log ──→ diff_log      (temporal ordering: query diffs by time range)
```

**Downstream impact via graph:** When `validateToken` is modified, the `diff()` tool can traverse edges to find callers:

```
diff_symbols(symbol: "validateToken")
  → edges WHERE dst_symbol_id = validateToken.id
    → symbols (callers) → chunks (caller code)
```

This answers "what might break if this changed?"

**Retention:** 30 days. Pruned on startup and every 6 hours idle. At 100 changes/day × 30 days = 3K entries = ~3MB. Heavy development: ~30MB.

---

### 1.5 Edge (Relationship Entity)

Directed edges between symbols. Persisted in SQLite, loaded into CSR for fast BFS.

```
EDGE
  id:               INTEGER PRIMARY KEY (auto-increment)
  src_symbol_id:    INTEGER NOT NULL → Symbol.id  (CASCADE delete)
  dst_symbol_id:    INTEGER NOT NULL → Symbol.id  (CASCADE delete)
  kind:             TEXT NOT NULL                  -- see Edge Kinds below
  file_id:          INTEGER → File.id              -- file where the edge was declared
                                                   -- (src file for calls, importing file for imports)

  UNIQUE CONSTRAINT: (src_symbol_id, dst_symbol_id, kind)  -- no duplicate edges
```

```
PENDING_EDGES (cross-file resolution — see CA-1)
  id:               INTEGER PRIMARY KEY (auto-increment)
  src_symbol_id:    INTEGER NOT NULL → Symbol.id
  dst_symbol_name:  TEXT NOT NULL                  -- unresolved target name
  kind:             TEXT NOT NULL
  created_at:       TEXT NOT NULL                  -- ISO8601, for 24h cleanup
```

---

> **Note (FD-3):** The embedding schema below describes the sqlite-vec virtual table format. Per FD-3, the default vector store is now **brute-force in-process** (`[]float32` slices in memory, persisted to binary file). The sqlite-vec DDL is retained for reference as an optional backend. The `VectorStore` interface abstracts both implementations.

### 1.6 Embedding (Vector Entity)

Vector embeddings for semantic search. Stored via sqlite-vec extension (or pluggable VectorStore).

```
EMBEDDING (virtual table via sqlite-vec)
  chunk_id:         INTEGER PRIMARY KEY → Chunk.id
  vector:           FLOAT32[768]                   -- 768 dimensions for nomic-embed-text
                                                   -- dimension configurable per model

  Stored via: CREATE VIRTUAL TABLE embeddings USING vec0(
    chunk_id INTEGER PRIMARY KEY,
    vector FLOAT32[768]
  );
```

**Model tracking (prevents mixed-model corruption — MF-1):**

```
CONFIG table:
  embedding_model_id:    "nomic-embed-text-v1.5"
  embedding_dimensions:  "768"

On model change:
  1. vector_store.invalidate_all()   — delete all vectors
  2. Update config
  3. Re-queue all chunks at P4
  4. System drops to Level 1 (structural+keyword) until re-embedded
```

---

### 1.7 Session Entities

Track agent access patterns for session-aware ranking.

```
ACCESS_LOG
  id:               INTEGER PRIMARY KEY
  session_id:       TEXT NOT NULL                  -- identifies the agent session
  timestamp:        TEXT NOT NULL                  -- ISO8601
  chunk_id:         INTEGER → Chunk.id             (CASCADE delete)
  operation:        TEXT NOT NULL                  -- 'search_hit' | 'context_include' | 'direct_read'

  RETENTION: 30 days, max 100K rows. Pruned on startup + every 6 hours.

WORKING_SET
  session_id:       TEXT NOT NULL
  chunk_id:         INTEGER NOT NULL
  access_count:     INTEGER DEFAULT 1
  last_accessed:    TEXT NOT NULL
  queries_since_last_hit: INTEGER DEFAULT 0        -- exploration decay (SF-9)
  PRIMARY KEY (session_id, chunk_id)
```

**In-memory counterparts (CA-4):**

```
access_lru:   LRU<ChunkId, AccessEntry>       capacity: 10,000
decay_map:    HashMap<ChunkId, u32>            queries_since_last_hit
working_set:  HashSet<ChunkId>                 chunks accessed ≥2 times

Flushed to SQLite every 30s or every 10 queries.
On crash: at most 30s of session data lost (acceptable).
```

---

### 1.8 System Entities

```
SCHEMA_VERSION
  version:          INTEGER NOT NULL
  applied_at:       TEXT NOT NULL                  -- ISO8601

CONFIG
  key:              TEXT PRIMARY KEY
  value:            TEXT NOT NULL

  Reserved keys:
    embedding_model_id       -- "nomic-embed-text-v1.5"
    embedding_dimensions     -- "768"
    embedding_enabled        -- "true" | "false"
    tokenizer                -- "cl100k_base"
    default_budget           -- "8192"
    schema_version           -- tracked in separate table but also cached here
```

---

## 2. Relationships (Edges)

### 2.1 Edge Kinds

| Edge Kind | Direction | Meaning | Example |
|---|---|---|---|
| `imports` | File A → Symbol B | A imports/uses module containing B | `import { validate } from './auth'` |
| `calls` | Symbol A → Symbol B | A calls B at runtime | `validate(token)` inside `handleRequest` |
| `type_ref` | Symbol A → Symbol B | A references B as a type | `claims: TokenClaims` in function signature |
| `inherits` | Symbol A → Symbol B | A extends/inherits B | `class Admin extends User` |
| `implements` | Symbol A → Symbol B | A implements interface B | `class AuthService implements IAuth` |

### 2.2 Edge Creation

Edges are created during enrichment by the Dep Extractor (C7):

```
EDGE CREATION TRIGGERS:

  1. File indexed for the first time (cold index or query-time enrichment):
     → Dep Extractor runs imports.scm and calls.scm queries against AST
     → All discovered edges are new additions

  2. File re-indexed (watcher-triggered or query-time stale re-enrichment):
     → Old edges for this file are deleted (DELETE FROM edges WHERE file_id = ?)
     → New edges from current parse are inserted
     → Net effect: full edge replacement per file

  3. Cross-file edge resolution (CA-1):
     → If dst_symbol not yet indexed → edge goes to pending_edges
     → When dst file is later indexed → pending_edges are resolved into real edges
     → pending_edges older than 24 hours are pruned
```

### 2.3 Edge Update Rules

```
EDGE LIFECYCLE:

  CREATE:  Dep Extractor produces EdgeRecord { src_name, dst_name, kind }
           Writer Thread resolves names → IDs (two-phase, CA-1)
           INSERT INTO edges (src_symbol_id, dst_symbol_id, kind, file_id)
           Append to CSR delta buffer

  DELETE:  When a file is re-enriched, all edges WHERE file_id = ? are deleted first
           Deletions added to CSR delta buffer
           New edges from re-parse are inserted

  UPDATE:  Edges are not updated in place. They are delete + re-insert.

  CASCADE: When a symbol is deleted (file re-enrichment or file deletion):
           ON DELETE CASCADE removes all edges to/from that symbol.
           CSR delta buffer gets deletion entries.
```

### 2.4 Containment Relationships (Implicit, Not Stored as Edges)

These relationships are derived from foreign keys, not stored in the `edges` table:

```
CONTAINS (implicit via foreign keys):

  File CONTAINS Chunks       → chunks.file_id → files.id
  File CONTAINS Symbols      → symbols.file_id → files.id
  Chunk CONTAINS Symbols     → symbols.chunk_id → chunks.id
  Chunk NESTS_IN Chunk       → chunks.parent_chunk_id → chunks.id

These are O(1) lookups via indexed foreign keys.
No need to store as graph edges — that would double the edge count for no benefit.

Query: "all symbols in file X"
  → SELECT * FROM symbols WHERE file_id = X

Query: "all methods in class Foo"
  → SELECT * FROM chunks WHERE parent_chunk_id = (chunk for Foo)
```

### 2.5 Diff-to-Entity Relationships

```
MODIFIES (diff_log → file):
  diff_log.file_id → files.id
  Direction: Diff → File
  Meaning: "this diff modified this file"
  Created: on every file change detection

AFFECTS (diff_symbols → symbol/chunk):
  diff_symbols.symbol_id → symbols.id
  diff_symbols.chunk_id → chunks.id
  Direction: Diff → Symbol, Diff → Chunk
  Meaning: "this diff affected this symbol/chunk"
  Created: by Diff Engine (post-parse), resolving changed line ranges to symbols

IMPACTS (derived — not stored):
  "What is impacted by this change?"
  diff_symbols → symbol → edges (reverse traversal) → downstream symbols
  Computed at query time via graph BFS, not pre-stored.
```

---

## 3. Complete SQLite Schema

```sql
-- ═══════════════════════════════════════════════════════════
-- SYSTEM TABLES
-- ═══════════════════════════════════════════════════════════

CREATE TABLE schema_version (
  version    INTEGER NOT NULL,
  applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE config (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- ═══════════════════════════════════════════════════════════
-- CORE ENTITIES
-- ═══════════════════════════════════════════════════════════

CREATE TABLE files (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  path             TEXT UNIQUE NOT NULL,
  content_hash     TEXT NOT NULL,
  mtime            REAL NOT NULL,
  size             INTEGER,
  language         TEXT,
  indexed_at       TEXT,
  embedding_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (embedding_status IN ('pending', 'partial', 'complete')),
  parse_quality    TEXT NOT NULL DEFAULT 'full'
    CHECK (parse_quality IN ('full', 'partial', 'error', 'unparseable'))
);

CREATE TABLE chunks (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id          INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  parent_chunk_id  INTEGER REFERENCES chunks(id) ON DELETE SET NULL,
  chunk_index      INTEGER NOT NULL,
  symbol_name      TEXT,
  kind             TEXT NOT NULL
    CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'header', 'block')),
  start_line       INTEGER NOT NULL,
  end_line         INTEGER NOT NULL,
  content          TEXT NOT NULL,
  token_count      INTEGER NOT NULL,
  signature        TEXT,
  parse_quality    TEXT NOT NULL DEFAULT 'full'
    CHECK (parse_quality IN ('full', 'partial'))
);

CREATE TABLE symbols (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  chunk_id         INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
  file_id          INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  qualified_name   TEXT,
  kind             TEXT NOT NULL
    CHECK (kind IN ('function', 'class', 'method', 'type', 'interface',
                    'variable', 'constant')),
  line             INTEGER NOT NULL,
  signature        TEXT,
  visibility       TEXT CHECK (visibility IN ('public', 'private', 'internal', 'exported')),
  is_exported      INTEGER NOT NULL DEFAULT 0
);

-- ═══════════════════════════════════════════════════════════
-- GRAPH (EDGES)
-- ═══════════════════════════════════════════════════════════

CREATE TABLE edges (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  src_symbol_id    INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  dst_symbol_id    INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL
    CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
  file_id          INTEGER REFERENCES files(id) ON DELETE CASCADE,
  UNIQUE (src_symbol_id, dst_symbol_id, kind)
);

CREATE TABLE pending_edges (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  src_symbol_id    INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  dst_symbol_name  TEXT NOT NULL,
  kind             TEXT NOT NULL
    CHECK (kind IN ('imports', 'calls', 'type_ref', 'inherits', 'implements')),
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- ═══════════════════════════════════════════════════════════
-- DIFF TRACKING
-- ═══════════════════════════════════════════════════════════

CREATE TABLE diff_log (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id          INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  timestamp        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  change_type      TEXT NOT NULL
    CHECK (change_type IN ('add', 'modify', 'delete', 'rename')),
  lines_added      INTEGER NOT NULL DEFAULT 0,
  lines_removed    INTEGER NOT NULL DEFAULT 0,
  hash_before      TEXT,
  hash_after       TEXT
);

CREATE TABLE diff_symbols (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  diff_id          INTEGER NOT NULL REFERENCES diff_log(id) ON DELETE CASCADE,
  symbol_id        INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
  symbol_name      TEXT NOT NULL,
  change_type      TEXT NOT NULL
    CHECK (change_type IN ('added', 'modified', 'removed', 'signature_changed', 'moved')),
  chunk_id         INTEGER REFERENCES chunks(id) ON DELETE SET NULL
);

-- ═══════════════════════════════════════════════════════════
-- VECTOR EMBEDDINGS
-- ═══════════════════════════════════════════════════════════

-- sqlite-vec virtual table (768-dim for nomic-embed-text)
CREATE VIRTUAL TABLE embeddings USING vec0(
  chunk_id INTEGER PRIMARY KEY,
  vector   FLOAT32[768]
);

-- ═══════════════════════════════════════════════════════════
-- FULL-TEXT SEARCH
-- ═══════════════════════════════════════════════════════════

CREATE VIRTUAL TABLE chunks_fts USING fts5(
  content,
  symbol_name,
  content=chunks,
  content_rowid=id
);

-- ═══════════════════════════════════════════════════════════
-- SESSION TRACKING
-- ═══════════════════════════════════════════════════════════

-- Revised per DM-2: uses (file_path, chunk_index) as stable keys
-- instead of chunk_id FK, so session data survives re-enrichment.
CREATE TABLE access_log (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id       TEXT NOT NULL,
  timestamp        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  file_path        TEXT NOT NULL,
  chunk_index      INTEGER NOT NULL,
  operation        TEXT NOT NULL
    CHECK (operation IN ('search_hit', 'context_include', 'direct_read'))
);
CREATE INDEX idx_access_session ON access_log(session_id, timestamp);
CREATE INDEX idx_access_file ON access_log(file_path, chunk_index);

CREATE TABLE working_set (
  session_id              TEXT NOT NULL,
  file_path               TEXT NOT NULL,
  chunk_index             INTEGER NOT NULL,
  access_count            INTEGER NOT NULL DEFAULT 1,
  last_accessed           TEXT NOT NULL,
  queries_since_last_hit  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, file_path, chunk_index)
);

-- ═══════════════════════════════════════════════════════════
-- INDEXES
-- ═══════════════════════════════════════════════════════════

-- Files
-- idx_files_path: removed per DM-8 — UNIQUE constraint on files(path) creates implicit index
CREATE INDEX idx_files_language ON files(language);
CREATE INDEX idx_files_embedding_status ON files(embedding_status);

-- Chunks
CREATE INDEX idx_chunks_file ON chunks(file_id);
CREATE INDEX idx_chunks_file_index ON chunks(file_id, chunk_index);
CREATE INDEX idx_chunks_symbol_name ON chunks(symbol_name);
CREATE INDEX idx_chunks_kind ON chunks(kind);

-- Symbols
CREATE INDEX idx_symbols_name ON symbols(name);
CREATE INDEX idx_symbols_qualified ON symbols(qualified_name);
CREATE INDEX idx_symbols_file ON symbols(file_id);
CREATE INDEX idx_symbols_chunk ON symbols(chunk_id);
CREATE INDEX idx_symbols_kind ON symbols(kind);

-- Edges
CREATE INDEX idx_edges_src ON edges(src_symbol_id);
CREATE INDEX idx_edges_dst ON edges(dst_symbol_id);
CREATE INDEX idx_edges_kind ON edges(kind);
CREATE INDEX idx_edges_file ON edges(file_id);

-- Pending edges
CREATE INDEX idx_pending_name ON pending_edges(dst_symbol_name);

-- Diff
CREATE INDEX idx_difflog_file_ts ON diff_log(file_id, timestamp);
CREATE INDEX idx_difflog_ts ON diff_log(timestamp);
CREATE INDEX idx_diffsym_symbol ON diff_symbols(symbol_id);
CREATE INDEX idx_diffsym_diff ON diff_symbols(diff_id);
CREATE INDEX idx_diffsym_chunk ON diff_symbols(chunk_id);

-- Session
CREATE INDEX idx_access_session ON access_log(session_id, timestamp);
CREATE INDEX idx_access_chunk ON access_log(chunk_id);
CREATE INDEX idx_working_set_chunk ON working_set(chunk_id);

-- ═══════════════════════════════════════════════════════════
-- CONNECTION CONFIGURATION
-- ═══════════════════════════════════════════════════════════

-- Writer connection (1):
--   PRAGMA journal_mode = WAL;
--   PRAGMA synchronous = NORMAL;
--   PRAGMA cache_size = -8000;           -- 8MB
--   PRAGMA wal_autocheckpoint = 1000;
--   PRAGMA foreign_keys = ON;

-- Reader connections (pool of 4):
--   PRAGMA journal_mode = WAL;
--   PRAGMA cache_size = -4000;           -- 4MB each
--   PRAGMA query_only = true;
--   PRAGMA foreign_keys = ON;

-- Total page cache: 8MB + (4 × 4MB) = 24MB
```

---

## 4. In-Memory Data Structures

These structures complement SQLite for performance-critical operations:

### 4.1 CSR Graph (C3)

```
CSR (Compressed Sparse Row) — read-optimized, rebuilt from SQLite:

  offsets: Vec<u32>           -- one entry per symbol node (N+1 entries)
  edges:   Vec<(u32, u8)>    -- (dst_symbol_id, edge_kind_enum)

  edge_kind_enum mapping:
    0 = imports
    1 = calls
    2 = type_ref
    3 = inherits
    4 = implements

  Memory at 1M lines (~2M edges):
    offsets: ~1MB    (250K symbols × 4 bytes)
    edges:   ~16MB   (2M edges × 8 bytes)
    Total:   ~17MB

  Delta Buffer (between compactions):
    additions: Vec<(u32, u32, u8)>    -- (src, dst, kind)
    deletions: HashSet<(u32, u32)>    -- (src, dst)
    Max size: 5000 entries before compaction trigger → ~60KB
```

### 4.2 Session Store (in-memory — CA-4)

```
access_lru:   LRU<ChunkId, AccessEntry>       capacity: 10,000 (~400KB)
  AccessEntry { access_count: u32, last_accessed: Timestamp }

decay_map:    HashMap<ChunkId, u32>            (~200KB for 10K entries)
  Maps ChunkId → queries_since_last_hit

working_set:  HashSet<ChunkId>                 (~40KB for 5K entries)
  Chunks accessed ≥2 times in current session
```

### 4.3 File Enrichment Mutex Map

```
enrichment_locks: ConcurrentHashMap<FilePath, Mutex>
  • Per-file lock preventing concurrent enrichment
  • Watcher: try_lock() — skip if held
  • Query-time: lock(timeout=50ms) — wait or degrade
```

### 4.4 Query Embedding Cache

```
query_embed_cache: LRU<String, Vec<f32>>      capacity: 100 entries (~3MB)
  • Key: query text
  • Value: embedding vector (768 floats × 4 bytes = 3KB per entry)
  • Hit rate: high for repeated/similar queries within a session
```

---

## 5. Embedding Strategy

### 5.1 What Gets Embedded

```
EMBEDDED:
  ✓ Functions         → each function chunk gets its own embedding
  ✓ Methods           → each method chunk gets its own embedding
  ✓ Classes           → class shell chunk (declaration without method bodies)
  ✓ Types/Interfaces  → type definition chunk
  ✓ Headers           → import/constant block chunk

NOT EMBEDDED:
  ✗ Entire files      → too coarse, wastes tokens on retrieval
  ✗ Individual lines  → too fine, no semantic coherence
  ✗ Symbols alone     → symbols are metadata, not content
  ✗ Edges             → structural, not semantic

RATIONALE:
  Chunking at symbol boundaries means embeddings capture semantic units
  that the agent will receive. Embedding at file level would require
  retrieving the entire file — defeating the purpose.
```

### 5.2 Granularity & Chunking

```
EMBEDDING UNIT = CHUNK

  Each chunk is embedded independently.
  Chunk content = raw source code (including comments, docstrings).
  Typical chunk: 50-500 tokens → 768-dim embedding.

  Oversized chunks (>1024 tokens):
    Split into sub-chunks before embedding.
    Each sub-chunk gets its own embedding.
    parent_chunk_id links sub-chunks to parent.

  Minimum chunks (<20 tokens):
    Merged with adjacent before embedding.
    Merged chunks still get one embedding.

  Average embedding count per file: 3-8 chunks.
  100K-line codebase: ~10K chunks → ~10K embeddings.
  1M-line codebase:  ~75K chunks → ~75K embeddings.
```

> **Note (FD-3):** Storage format below describes the sqlite-vec approach. Default implementation uses in-memory `[]float32` slices with binary file persistence (task 3.19). See architecture doc FD-3.

### 5.3 Storage Format

```
STORAGE: sqlite-vec virtual table within .shaktiman/index.db

  CREATE VIRTUAL TABLE embeddings USING vec0(
    chunk_id INTEGER PRIMARY KEY,
    vector   FLOAT32[768]
  );

  Disk per embedding: 768 × 4 bytes = 3,072 bytes (~3KB)
  10K chunks: ~30MB
  75K chunks: ~225MB
  100K chunks: ~300MB

  Pluggable via VectorStore trait:
    SqliteVecStore  — primary, transactionally consistent
    UsearchStore    — swap if sqlite-vec exceeds 80ms p95 at >100K chunks

SEARCH:
  ANN (Approximate Nearest Neighbor) via sqlite-vec.
  Query: SELECT chunk_id, distance FROM embeddings
         WHERE vector MATCH ? ORDER BY distance LIMIT ?
  Returns: Vec<(ChunkId, cosine_similarity)>
```

### 5.4 Embedding ↔ Graph Node Linkage

```
Embedding → Chunk → Symbol → Graph

  embeddings.chunk_id   →  chunks.id       (which chunk this vector represents)
  chunks.id             →  symbols.chunk_id (symbols defined in this chunk)
  symbols.id            →  edges.src/dst   (graph connectivity)

  Query flow:
    1. Embed query text → query vector
    2. ANN search → top-K chunk_ids with cosine scores
    3. For each chunk: look up symbols → look up edges → structural score
    4. Combine semantic + structural + keyword + change + session scores

  This linkage ensures:
    • Semantic results are enriched with structural context
    • Graph-adjacent chunks can be pulled in during expansion
    • A single chunk_id connects all stores
```

---

## 6. Versioning Model

### 6.1 Current-State Model (No Git History Indexing)

```
DESIGN DECISION: Shaktiman indexes the CURRENT working tree state only.
  • Not git history, not branches, not commits.
  • No repo@commit_hash addressing.
  • No immutable versioned nodes.

WHY:
  1. Agent operates on current code, not historical snapshots
  2. Storing per-commit snapshots at 1M lines → GB of disk (busts NFR-8)
  3. Git already stores history — Shaktiman shouldn't duplicate it
  4. Change history is captured by diff_log (lighter-weight)

WHAT CHANGES OVER TIME:
  • files table: UPSERT on each enrichment (content_hash changes)
  • chunks table: DELETE old + INSERT new on each file enrichment
  • symbols table: DELETE old + INSERT new on each file enrichment
  • edges table: DELETE old + INSERT new per file
  • diff_log: APPEND-only, captures what changed (30-day retention)
  • embeddings: DELETE stale + INSERT new when chunks change
```

### 6.2 How "What Changed?" Is Answered

```
Instead of diffing between commits, Shaktiman uses diff_log:

  "What changed in auth/ since yesterday?"
    SELECT dl.*, ds.symbol_name, ds.change_type, f.path
    FROM diff_log dl
    JOIN files f ON f.id = dl.file_id
    LEFT JOIN diff_symbols ds ON ds.diff_id = dl.id
    WHERE f.path LIKE 'src/auth/%'
      AND dl.timestamp > datetime('now', '-1 day')
    ORDER BY dl.timestamp DESC

  This returns file-level AND symbol-level change detail.
  No git interaction needed at query time.
```

### 6.3 Branch Handling

```
BRANCH SWITCHES (detected by C9):
  • >20 file changes in <2s → classified as branch switch
  • All changed files are re-enriched (batched, P2)
  • diff_log records these changes (change_type may be 'add' or 'modify')
  • Old diffs from the previous branch remain in diff_log (still useful)

BRANCH CONTEXT:
  • Shaktiman does NOT track which branch it's on
  • The index always reflects the current working tree
  • After branch switch: progressive re-enrichment updates the index
  • During re-enrichment window: stale data served with parse_quality flags

FUTURE V2 CONSIDERATION:
  • Branch-aware indexing (index per branch) is explicitly out of scope for V1
  • If needed: could store branch_name in files table and maintain parallel indexes
  • Not worth the complexity given the current-state model works for 95% of use cases
```

### 6.4 Content Hash as Version

```
Every file has a content_hash (SHA-256).
This serves as the version for:

  1. CHANGE DETECTION:
     C9 compares current hash with stored hash → triggers re-enrichment if different

  2. WRITE ORDERING (CA-3):
     Writer Thread checks incoming hash against stored hash.
     If hash matches → skip (already indexed with this content).
     If stored is newer → skip (stale write).

  3. DIFF TRACKING:
     diff_log stores hash_before and hash_after.
     Enables detecting "same change" vs "new change" (idempotency).

  4. EMBEDDING STALENESS:
     When content_hash changes → chunks change → old embeddings invalidated.
     embedding_status reset to 'pending' for re-embedding.
```

---

## 7. Query Patterns

### 7.1 "What breaks if I change this function?"

**Question:** User modifies `validateToken()`. What other code is affected?

```
NODES QUERIED:
  1. symbols WHERE name = 'validateToken'         → get symbol_id
  2. edges WHERE dst_symbol_id = symbol_id         → callers (reverse edges)
  3. symbols WHERE id IN (caller symbol_ids)       → caller details
  4. chunks WHERE id IN (caller chunk_ids)         → caller code

EDGES TRAVERSED:
  • calls (reverse): who calls validateToken?
  • type_ref (reverse): who references its return type?
  • extends/implements (reverse): who inherits from it?

IMPLEMENTATION (via C3 Graph + C5 Metadata):

  -- Step 1: Find the symbol
  SELECT id FROM symbols WHERE name = 'validateToken' AND file_id = ?

  -- Step 2: BFS reverse traversal (depth 2)
  C3.bfs(symbol_id, depth=2, direction=INCOMING)
  → Returns Map<SymbolId, depth>

  -- Step 3: Hydrate with chunk content
  SELECT c.content, c.symbol_name, f.path, s.signature
  FROM chunks c
  JOIN symbols s ON s.chunk_id = c.id
  JOIN files f ON f.id = c.file_id
  WHERE s.id IN (reachable symbol IDs)

  -- Step 4: Check recent diffs for cascading changes
  SELECT ds.symbol_name, ds.change_type, dl.timestamp
  FROM diff_symbols ds
  JOIN diff_log dl ON dl.id = ds.diff_id
  WHERE ds.symbol_id IN (reachable symbol IDs)
    AND dl.timestamp > datetime('now', '-7 days')

QUERY LATENCY:
  BFS (CSR, depth 2): ~5ms
  Hydration: ~3ms
  Diff check: ~2ms
  TOTAL: ~10ms
```

### 7.2 "Show all flows touching billing"

**Question:** Find all code related to billing across the codebase.

```
NODES QUERIED:
  1. chunks_fts MATCH 'billing'                    → keyword matches
  2. embeddings WHERE vector MATCH embed("billing") → semantic matches
  3. symbols WHERE name LIKE '%billing%' OR name LIKE '%invoice%' → name matches
  4. For each hit: graph BFS outward → related code

EDGES TRAVERSED:
  • calls (both directions): billing code calling and being called
  • imports: modules importing billing
  • type_ref: types used by billing code

IMPLEMENTATION (via search() MCP tool):

  -- Parallel fan-out:
  FTS5:    SELECT rowid, rank FROM chunks_fts WHERE chunks_fts MATCH 'billing'
  Vector:  SELECT chunk_id, distance FROM embeddings WHERE vector MATCH ?
  Symbols: SELECT chunk_id FROM symbols WHERE name LIKE '%bill%' OR name LIKE '%invoice%'

  -- Merge candidates, normalize scores, rank via 5-signal hybrid formula
  -- Expand top results via graph BFS (depth 2, max 5 neighbors per chunk)
  -- Assemble into token-budgeted context package

QUERY LATENCY:
  FTS5: ~5ms
  Vector search: ~30ms
  Symbol lookup: ~2ms
  Ranking: ~5ms
  Graph expansion: ~8ms
  Assembly: ~10ms
  TOTAL: ~60ms
```

### 7.3 "Find retry logic similar to this"

**Question:** Agent has a code snippet and wants to find similar patterns elsewhere.

```
NODES QUERIED:
  1. Embed the snippet → query vector
  2. embeddings ANN search → top-K semantically similar chunks
  3. chunks_fts MATCH 'retry OR backoff OR attempt' → keyword reinforcement
  4. For each hit: check structural neighbors (same file, callers/callees)

EDGES TRAVERSED:
  • calls: functions called by retry logic (sleep, backoff, etc.)
  • type_ref: error types handled in retry blocks

IMPLEMENTATION:

  -- Step 1: Embed query snippet
  query_vec = embedding_model.embed_single(snippet)

  -- Step 2: Semantic search
  SELECT chunk_id, distance FROM embeddings
  WHERE vector MATCH ? ORDER BY distance LIMIT 20

  -- Step 3: Keyword reinforcement
  SELECT rowid, rank FROM chunks_fts
  WHERE chunks_fts MATCH 'retry OR backoff OR exponential'

  -- Step 4: Merge and rank (semantic score heavily weighted for similarity queries)
  -- Step 5: For top results, BFS neighbors for structural context

QUERY LATENCY:
  Embed query: ~5ms (cache miss) / <0.1ms (cache hit)
  Vector search: ~30ms
  FTS5: ~5ms
  Merge + rank: ~5ms
  TOTAL: ~45ms
```

### 7.4 "What changed recently in checkout?"

**Question:** What code changed in the checkout module in the last 48 hours?

```
NODES QUERIED:
  1. diff_log + files WHERE path LIKE '%checkout%' AND recent
  2. diff_symbols → which specific symbols changed
  3. chunks → content of changed code
  4. edges → downstream impact (what might be affected by these changes)

EDGES TRAVERSED:
  • diff_log → files (which files changed)
  • diff_symbols → symbols (which symbols changed)
  • edges (reverse from changed symbols) → downstream callers

IMPLEMENTATION (via diff() MCP tool):

  -- Step 1: Recent changes in checkout
  SELECT dl.id, dl.change_type, dl.lines_added, dl.lines_removed,
         dl.timestamp, f.path
  FROM diff_log dl
  JOIN files f ON f.id = dl.file_id
  WHERE f.path LIKE '%checkout%'
    AND dl.timestamp > datetime('now', '-2 days')
  ORDER BY dl.timestamp DESC

  -- Step 2: Symbol-level detail
  SELECT ds.symbol_name, ds.change_type
  FROM diff_symbols ds
  WHERE ds.diff_id IN (diff IDs from step 1)

  -- Step 3: Impact analysis (optional, if agent asks)
  For each changed symbol:
    C3.bfs(symbol_id, depth=1, direction=INCOMING) → callers

  -- Step 4: Assemble DiffReport
  {
    files: ["checkout/cart.ts", "checkout/payment.ts"],
    changes: [
      { symbol: "processPayment", type: "modified", file: "payment.ts", when: "2h ago" },
      { symbol: "CartItem",       type: "signature_changed", file: "cart.ts", when: "5h ago" },
      { symbol: "applyDiscount",  type: "added", file: "cart.ts", when: "5h ago" }
    ],
    impact: ["OrderService.createOrder calls processPayment"]
  }

QUERY LATENCY:
  Diff query: ~3ms
  Symbol detail: ~2ms
  Impact BFS: ~5ms per changed symbol
  TOTAL: ~15ms for typical change set
```

### 7.5 "Assemble context for this task"

**Question:** Agent calls `context(files: ["src/auth/login.ts"], task: "fix rate limiting bug")`.

```
NODES QUERIED:
  1. Embed task description → query vector
  2. All 5 signal sources (parallel fan-out):
     • Vector search → semantic matches for "rate limiting"
     • FTS5 → keyword matches for "rate" AND "limit"
     • Graph BFS from symbols in login.ts → structural neighbors
     • Diff scores → recently modified chunks
     • Session scores → chunks the agent accessed recently
  3. Ranked chunks → Context Assembler → budget-fitted package

EDGES TRAVERSED:
  • imports from login.ts → dependencies
  • calls from login.ts symbols → callees
  • calls TO login.ts symbols → callers (who depends on this?)
  • type_ref → types used in rate limiting logic

IMPLEMENTATION (full query pipeline):

  QueryRouter:
    1. Check: is login.ts indexed? → if not, trigger enrichment (80ms budget)
    2. Check: is login.ts stale? → if so, trigger re-enrichment
    3. Select strategy: Level 0 (full hybrid) if embeddings ready

  RetrievalEngine (parallel):
    sem_results   = C4.search(embed("fix rate limiting bug"), top_k=50)
    struct_results = C3.bfs(symbols_in_login_ts, depth=2)
    kw_results    = C5.fts_search("rate AND limit", limit=50)
    change_results = C6.compute_change_scores(candidate_ids)
    session_results = session_store.get_session_scores(candidate_ids)

  HybridRanker:
    Normalize all scores to [0,1]
    Apply weights: 0.40×sem + 0.20×struct + 0.15×change + 0.15×session + 0.10×kw
    Sort descending

  ContextAssembler:
    effective_budget = 8192 × 0.95 = 7782 tokens
    Primary selection: top chunks fitting 70% budget (~5447 tokens)
    Expansion: graph neighbors fitting 30% budget (~2335 tokens)
    Two-pass reclaim if expansion budget unused
    Metadata: ~12 tokens per chunk

QUERY LATENCY:
  Router + enrichment check: ~3ms
  Parallel fan-out: ~35ms (max of individual stores)
  Ranking: ~5ms
  Assembly: ~12ms
  TOTAL: ~55ms
```

---

## 8. Token Efficiency Considerations

### 8.1 Minimizing Context Size

```
STRATEGY                              TOKEN SAVINGS

1. Chunk-level retrieval               ~80% fewer tokens than whole-file reads
   (return function, not file)         Agent asks for 1 fn → gets 200 tokens, not 2000

2. Pre-computed token counts           Zero runtime tokenization cost
   (stored in chunks.token_count)      Assembly is pure integer arithmetic

3. Budget-fitted assembly              100% budget compliance guaranteed
   (greedy pack with safety margin)    Agent never receives more than requested

4. Metadata overhead capped at 4%      ~12 tokens per chunk
   (path, symbol, lines, score)        300-token chunk → 12 tokens metadata → 4%

5. Deduplication via line-range overlap Prevents returning overlapping code
   (>50% overlap → skip)              Method + containing class → only the method

6. Structural expansion capped at 30%  Prevents graph bloat
   (max 5 neighbors per chunk)         Most budget goes to directly relevant code
```

### 8.2 Selective Retrieval

```
The 5-signal ranking ensures the MOST relevant chunks fill the budget:

  1. SEMANTIC (0.40): chunks whose meaning matches the query
     → "rate limiting" finds throttle logic even if named "requestGuard"

  2. STRUCTURAL (0.20): chunks connected via call/import graph
     → if login.ts is the focus, its dependencies get boosted

  3. CHANGE (0.15): recently modified chunks
     → active development area → likely what the agent needs

  4. SESSION (0.15): chunks the agent already accessed
     → agent's working set → continuity across queries

  5. KEYWORD (0.10): exact term matches
     → catches specific identifiers the agent is looking for

  Weight redistribution when signals are unavailable prevents
  over-reliance on any single signal.
```

### 8.3 Avoiding Redundant Fetching

```
MECHANISM                              HOW IT HELPS

1. Session-aware ranking               Agent doesn't re-discover code it already has.
                                       session_score boosts working set chunks.

2. Push mode resources                 Agent gets context BEFORE asking.
   (shaktiman://context/active)        4K pre-assembled tokens at task start.

3. diff() tool                         ~50 tokens to learn "what changed"
                                       vs ~2000 tokens to re-read the file.

4. symbols() tool                      ~100 tokens for symbol list + signatures
                                       vs ~2000 tokens for full file read.

5. dependencies() tool                 ~150 tokens for caller/callee list
                                       vs 5-15 grep/read cycles × 500 tokens each.

6. Exploration decay (SF-9)            Prevents filter bubble — agent discovers
                                       new code instead of re-fetching familiar code.

NET EFFECT:
  • 60-90% fewer tool call round-trips (NFR-6 target: ≥60%)
  • 40-60% fewer input tokens per task (NFR-5 target: ≥40%)
```

---

## 9. Entity-Relationship Diagram

```
┌─────────────┐         ┌──────────────┐         ┌──────────────┐
│   CONFIG    │         │SCHEMA_VERSION│         │ ACCESS_LOG   │
│             │         │              │         │              │
│  key (PK)   │         │  version     │         │  id (PK)     │
│  value      │         │  applied_at  │         │  session_id  │
└─────────────┘         └──────────────┘         │  timestamp   │
                                                  │  chunk_id ──────┐
                                                  │  operation   │  │
                                                  └──────────────┘  │
                                                                    │
┌──────────────────────────────────────────────────────────────┐    │
│                         FILES                                 │    │
│                                                               │    │
│  id (PK)  path (UNIQUE)  content_hash  mtime  size           │    │
│  language  indexed_at  embedding_status  parse_quality        │    │
└────────────────┬─────────────────────────────────────────────┘    │
                 │ 1:N                                              │
      ┌──────────┼───────────────────┐                              │
      │          │                   │                              │
      ▼          ▼                   ▼                              │
┌───────────┐ ┌───────────────┐ ┌───────────────┐                  │
│ DIFF_LOG  │ │    CHUNKS     │ │   SYMBOLS     │                  │
│           │ │               │ │               │                  │
│ id (PK)   │ │ id (PK) ◄────────── chunk_id   │                  │
│ file_id ──┤ │ file_id ──────┤ │ file_id ──────┤                  │
│ timestamp │ │ parent_chunk  │ │ name          │                  │
│ change_   │ │ chunk_index   │ │ qualified_name│                  │
│   type    │ │ symbol_name   │ │ kind          │                  │
│ lines_add │ │ kind          │ │ line          │                  │
│ lines_rem │ │ start_line    │ │ signature     │                  │
│ hash_before│ │ end_line      │ │ visibility    │                  │
│ hash_after│ │ content       │ │ is_exported   │                  │
│           │ │ token_count   │ └───────┬───────┘                  │
│           │ │ signature     │         │                          │
│           │ │ parse_quality │         │                          │
│           │ └───────┬───────┘         │                          │
│           │         │                 │                          │
│           │         │ 1:1             │ N:M                      │
│           │         ▼                 ▼                          │
│           │  ┌───────────────┐ ┌───────────────┐                │
│           │  │  EMBEDDINGS   │ │    EDGES      │                │
│           │  │  (sqlite-vec) │ │               │                │
│           │  │               │ │ id (PK)       │                │
│           │  │ chunk_id (PK) │ │ src_symbol ───┤                │
│           │  │ vector[768]   │ │ dst_symbol ───┤                │
│           │  └───────────────┘ │ kind          │                │
│           │                    │ file_id ──────┤                │
└─────┬─────┘                    └───────────────┘                │
      │ 1:N                                                       │
      ▼                                                           │
┌───────────────┐         ┌───────────────┐                       │
│ DIFF_SYMBOLS  │         │ WORKING_SET   │                       │
│               │         │               │                       │
│ id (PK)       │         │ session_id(PK)│                       │
│ diff_id ──────┤         │ chunk_id (PK) ◄───────────────────────┘
│ symbol_id ────┤         │ access_count  │
│ symbol_name   │         │ last_accessed │
│ change_type   │         │ queries_since │
│ chunk_id ─────┤         └───────────────┘
└───────────────┘

┌───────────────┐
│ PENDING_EDGES │
│               │
│ id (PK)       │
│ src_symbol_id │
│ dst_symbol_name│
│ kind          │
│ created_at    │
└───────────────┘
```

---

## 10. Disk & Memory Budget at Scale

### 10.1 Disk Usage Estimates

| Component | 100K lines | 1M lines | Formula |
|---|---|---|---|
| files table | ~0.3MB | ~1MB | ~100 bytes/file × file_count |
| chunks table | ~15MB | ~110MB | ~1.5KB/chunk avg × chunk_count |
| symbols table | ~2MB | ~15MB | ~200 bytes/symbol × symbol_count |
| edges table | ~2MB | ~16MB | ~40 bytes/edge × edge_count |
| embeddings | ~30MB | ~225MB | ~3KB/embedding × chunk_count |
| chunks_fts | ~10MB | ~75MB | ~1x chunk content (FTS5 overhead) |
| diff_log + diff_symbols | ~3MB | ~10MB | 30-day window, activity-dependent |
| session tables | ~1MB | ~3MB | capped at 100K rows |
| indexes | ~5MB | ~40MB | ~30% of base table sizes |
| WAL file | ~4MB | ~4MB | auto-checkpoint at 1000 pages |
| **TOTAL** | **~72MB** | **~499MB** | |

### 10.2 Memory Usage Estimates

| Component | 100K lines | 1M lines |
|---|---|---|
| SQLite page cache (writer 8MB + 4×4MB readers) | 24MB | 24MB |
| CSR graph | ~0.6MB | ~17MB |
| CSR delta buffer | <0.1MB | <0.1MB |
| Session LRU + decay + working set | ~0.7MB | ~2MB |
| FTS5 auxiliary | ~3MB | ~8MB |
| Watcher state | ~1MB | ~3MB |
| Query embedding cache | ~3MB | ~3MB |
| Writer Thread queue | ~2MB | ~5MB |
| Per-query transient | ~3MB | ~5MB |
| **TOTAL** | **~37MB** | **~67MB** |

Both within NFR-7 (memory <100MB) and NFR-8 (disk <500MB).

---

## Status

**Step 5: Data Model & Schema** — Complete. Awaiting critique validation and confirmation before next step.
