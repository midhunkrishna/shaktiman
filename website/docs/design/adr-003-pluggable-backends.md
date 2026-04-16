---
title: ADR-003 — Pluggable Storage Backends
sidebar_position: 4
---

# ADR-003: Pluggable Storage Backends

**Status:** AMENDED
**Date:** 2026-04-01
**Deciders:** Shaktiman maintainers

> **Status (Today, 2026-04-16):** **SHIPPED.** Metadata backends `sqlite` (default) and
> `postgres` (build tag `postgres`) are registered in
> `internal/storage/registry.go`. Vector backends `brute_force` (default), `hnsw`,
> `qdrant` (build tag `qdrant`), and `pgvector` (build tag `pgvector`) are registered
> in `internal/vector/registry.go`. Constraint A12 (postgres rejects
> `brute_force`/`hnsw` due to file-backed vector race) is enforced in
> `ValidateBackendConfig` (`internal/types/config.go`).

---

## Context

Shaktiman currently hard-codes SQLite as its only relational backend and offers two in-process vector backends (BruteForce, HNSW). This works well for the local-first, single-developer use case but blocks three adoption scenarios:

1. **Team/shared deployment.** Multiple developers indexing into a shared database requires a server-mode RDBMS.
2. **Large-corpus vector search.** In-process stores hit memory limits (~100K vectors for HNSW) and lack persistence durability for cloud workloads.
3. **Cloud-native deployment.** Managed infrastructure (RDS, Qdrant Cloud) is strongly preferred over embedded databases in production.

Additionally, several embedding configuration values are hardcoded in `internal/types/config.go` (Ollama URL, model, dims, batch size) and cannot be overridden via TOML or CLI flags.

### Current Architecture Snapshot

| Layer | Implementation | Location |
|---|---|---|
| Relational DB | SQLite (mattn/go-sqlite3), dual-conn WAL | `internal/storage/db.go` |
| Schema/Migrations | Versioned DDL (v4), FTS5 triggers | `internal/storage/schema.go` |
| MetadataStore | `storage.Store` (20+ methods) | `internal/storage/metadata.go` |
| VectorStore | BruteForceStore (in-memory, cosine) | `internal/vector/store.go` |
| VectorStore | HNSWStore (CGo hnswlib) | `internal/vector/hnsw.go` |
| Embedder | OllamaClient (HTTP) | `internal/vector/ollama.go` |
| Factory | `daemon.newVectorStore()` switch | `internal/daemon/daemon.go:456-466` |
| Config | `types.Config` struct + TOML | `internal/types/config.go` |

### Key Interfaces (internal/types/interfaces.go)

- `MetadataStore` -- 20+ methods for files, chunks, symbols, FTS, graph traversal
- `BatchMetadataStore` -- extends with batch queries (BatchNeighbors, BatchHydrateChunks, etc.)
- `VectorStore` -- Search, Upsert, UpsertBatch, Delete, Has, Count, Close
- `VectorPersister` -- optional SaveToDisk/LoadFromDisk for in-memory stores
- `EmbedSource` -- GetEmbedPage, MarkChunksEmbedded, CountChunksNeedingEmbedding

### Coupling Points

1. `storage.Store` directly wraps `storage.DB` (SQLite-specific dual-connection struct).
2. `daemon.New()` calls `storage.Open()` then `storage.Migrate()` -- both SQLite-specific.
3. `daemon.newVectorStore()` is a two-branch switch, not a registry.
4. `storage.Store` implements both `MetadataStore` and `EmbedSource` via the same SQLite connection.
5. FTS5 virtual table and triggers are SQLite-specific DDL.
6. Graph traversal (`Neighbors`) uses recursive CTEs that are portable to Postgres but not all databases.

---

## Decision

**Use the Provider Pattern (Go `database/sql` driver model) for both MetadataStore and VectorStore, with a config-driven factory.**

This is a minimalist registry: each backend registers itself via an `init()` function, and a central factory creates the correct implementation based on config. No abstract factory class hierarchy; no DI container.

### Target Backend Combinations

| # | Vector Backend | Relational Backend | Use Case |
|---|---|---|---|
| 0 | BruteForce (default) | SQLite (default) | Local-first, zero setup |
| 1 | HNSW | SQLite | Local-first, faster vector search at scale |
| 2 | Qdrant | SQLite | Local relational, external vector search |
| 3 | Qdrant | PostgreSQL | Team/cloud relational, external vector search |
| 4 | pgvector (via PostgreSQL) | PostgreSQL | Fully external — team/cloud deployment |

> Row 1 from the original table (BruteForce or HNSW on Postgres) was **removed** by Amendment 2 (2026-04-09). See "Constraint: Postgres requires an externalised vector backend" below.

### Constraint: pgvector requires Postgres relational backend

The pgvector vector store stores vectors in PostgreSQL tables alongside the relational data. Selecting `vector.backend = "pgvector"` with `database.backend = "sqlite"` is an invalid combination and must be rejected at config validation time.

### Constraint: Postgres requires an externalised vector backend (pgvector or qdrant)

Introduced by Amendment 2 (2026-04-09) to close the same-directory multi-instance concurrency gap ("Case F") identified in ADR-002 Amendment 3.

**Rule:** When `database.backend = "postgres"`, the only permitted `vector.backend` values are `"pgvector"` or `"qdrant"`. The combinations `postgres + brute_force` and `postgres + hnsw` are **rejected at config validation time**.

**Rationale:** The whole value of running `database.backend = "postgres"` in a multi-session context is that Postgres handles concurrent writers cleanly via MVCC — two `shaktimand` processes on the same project directory can both call `EnsureProject`, converge on the same `project_id`, and write through the same connection pool without `SQLITE_BUSY` or WAL races. However, `brute_force` and `hnsw` are **file-backed in the worktree**: each daemon loads its own in-memory copy from `.shaktiman/embeddings.bin` (per `VectorPersister.LoadFromDisk`) and races on the save path in `daemon.periodicEmbeddingSave` (`internal/daemon/daemon.go:367`). The Postgres backend therefore solves the metadata-layer race but *silently leaves the vector layer corruptible*. Banning the combination makes the Postgres path internally consistent: if you opt in to Postgres, your vector store must also be externalised.

`pgvector` and `qdrant` are both safe in this scenario:
- **pgvector** — same Postgres pool as the metadata store, scoped by `project_id`, `ON CONFLICT (chunk_id) DO UPDATE` resolves concurrent writes to the same chunk.
- **qdrant** — server-side state, no per-daemon file, last-write-wins on identical chunk IDs is acceptable.

**Error message (implementation target):**
> `config: database.backend "postgres" is incompatible with vector.backend %q — use "pgvector" or "qdrant". In-memory vector stores (brute_force, hnsw) persist to .shaktiman/embeddings.bin per daemon and will corrupt when multiple daemons share a Postgres project.`

**Non-goals:**
- This constraint does **not** restrict SQLite combinations. All four vector backends remain valid with SQLite (same-directory concurrency is handled separately — or not at all — in ADR-002).
- This constraint does **not** enable same-directory multi-instance support on Postgres automatically. It only ensures that *if* two daemons do run against the same Postgres-backed project, they will not silently corrupt the vector store. The non-code work (documenting that `postgres + pgvector`/`postgres + qdrant` is the supported multi-instance configuration) belongs with ADR-002.

**Interaction with the default config:** the default remains `sqlite + brute_force`. Users who opt in to Postgres must also opt in to a compatible vector backend. Existing configs that combine `postgres + brute_force` or `postgres + hnsw` will fail validation at daemon startup with an actionable error — intentional breaking change, not a silent upgrade.

---

## Alternatives Considered

### Alternative A: Abstract Factory

A `StorageFactory` interface with `NewMetadataStore()` and `NewVectorStore()` methods, with concrete factories per combination (e.g., `SqliteBruteForceFactory`, `PostgresPgvectorFactory`).

**Steelman:** Clean separation. Each factory encapsulates all wiring for a combination. Easy to test each combination in isolation.

**Why not chosen:** Combinatorial explosion -- 4 vector backends x 2 relational backends = 8 factory types, most of which differ only in one constructor call. Over-engineered for the actual variance (two axes of independent choice).

### Alternative B: Strategy Pattern (Injected at Construction)

Pass `MetadataStore` and `VectorStore` implementations directly into `Daemon.New()` as constructor parameters.

**Steelman:** Maximum flexibility. Caller controls wiring completely. Natural for testing (inject mocks). No registry overhead.

**Why not chosen:** Pushes wiring complexity to every call site (`cmd/shaktiman/main.go`, `cmd/shaktimand/main.go`, tests). Config-to-implementation mapping must be duplicated. Works well for testing but poorly for config-driven CLI usage.

### Alternative C: Full Dependency Injection Framework (wire, fx)

Use a DI framework to auto-wire backends based on config.

**Steelman:** Eliminates manual wiring. Scales to many components. Used successfully in large Go projects (Uber's fx).

**Why not chosen:** Adds a heavyweight dependency for a codebase with ~15 injectable components. Magic wiring obscures control flow. Build-time code generation (wire) or reflection (fx) adds complexity disproportionate to benefit.

### Alternative D: Provider Pattern (Chosen)

Each backend package registers a constructor via `init()`. A central factory resolves config to implementation. Modeled after `database/sql` driver registration.

**Steelman for alternatives:** Alternative B (strategy injection) is actually used *in combination* -- the provider pattern is the config-to-implementation bridge, and the resulting interfaces are injected into the Daemon. Tests bypass the registry and inject directly.

**Why chosen:**
- Idiomatic Go (`database/sql`, `image` package, `hash` package all use this pattern).
- Each backend is a self-contained package with no cross-backend imports.
- Adding a new backend requires only: (1) implement the interface, (2) add `init()` registration, (3) add a blank import in the build.
- Config validation happens in one place.
- No framework dependency.

---

## Detailed Design

### 1. Configuration

#### Enhanced shaktiman.toml

```toml
[database]
# backend = "sqlite"           # "sqlite" (default) or "postgres"

[postgres]
# connection_string = "postgres://user:pass@localhost:5432/shaktiman?sslmode=disable"
# max_open_conns = 10           # connection pool size (default: 10)
# max_idle_conns = 5            # idle connections (default: 5)
# schema = "public"             # Postgres schema (default: "public")

[vector]
# backend = "brute_force"       # "brute_force" (default), "hnsw", "qdrant", "pgvector"

[qdrant]
# url = "http://localhost:6334" # Qdrant HTTP API URL
# collection = "shaktiman"      # collection name (default: "shaktiman")
# api_key = ""                  # optional API key (prefer env var SHAKTIMAN_QDRANT_API_KEY)

[embedding]
# ollama_url = "http://localhost:11434"  # Ollama API base URL
# model = "nomic-embed-text"             # embedding model name
# dims = 768                             # vector dimensionality
# batch_size = 128                       # texts per batch request
# timeout = "120s"                       # HTTP timeout per request
```

#### Config Struct Changes (internal/types/config.go)

New fields added to `Config`:

```go
// Database backend configuration
DatabaseBackend string // "sqlite" (default) or "postgres"

// PostgreSQL configuration
PostgresConnString string
PostgresMaxOpen    int    // default: 10
PostgresMaxIdle    int    // default: 5
PostgresSchema     string // default: "public"

// Qdrant configuration
QdrantURL        string
QdrantCollection string // default: "shaktiman"
QdrantAPIKey     string // prefer env var

// Embedding (moved from hardcoded to configurable)
EmbedTimeout time.Duration // default: 120s
```

Fields `OllamaURL`, `EmbeddingModel`, `EmbeddingDims`, `EmbedBatchSize` already exist in Config but are currently hardcoded in `DefaultConfig()`. They become TOML-configurable via the new `[embedding]` section.

#### TOML Struct Changes

```go
type tomlConfig struct {
    Database  tomlDatabase  `toml:"database"`
    Postgres  tomlPostgres  `toml:"postgres"`
    Search    tomlSearch    `toml:"search"`
    Context   tomlContext   `toml:"context"`
    Vector    tomlVector    `toml:"vector"`
    Qdrant    tomlQdrant    `toml:"qdrant"`
    Embedding tomlEmbedding `toml:"embedding"`
    Test      tomlTest      `toml:"test"`
}
```

#### CLI Flags

```
shaktiman index <project-root> [flags]

Flags:
  --db string       Database backend: "sqlite" or "postgres" (default: from TOML)
  --vector string   Vector backend: "brute_force", "hnsw", "qdrant", "pgvector" (default: from TOML)
  --embed           Enable embedding worker
  --postgres-url    PostgreSQL connection string (overrides TOML)
  --qdrant-url      Qdrant URL (overrides TOML)
```

**Precedence:** CLI flags > environment variables > TOML > defaults.

Environment variables for secrets:
- `SHAKTIMAN_POSTGRES_URL` -- connection string (avoids password in TOML)
- `SHAKTIMAN_QDRANT_API_KEY` -- Qdrant authentication

#### Config Validation

Reject at startup:
- `vector.backend = "pgvector"` with `database.backend = "sqlite"` (pgvector requires Postgres)
- `database.backend = "postgres"` without `postgres.connection_string` (and no env var)
- `vector.backend = "qdrant"` without `qdrant.url`
- `embedding.dims` changed after vectors already exist (dimension mismatch)

### 2. Provider Registry

#### MetadataStore Registry (internal/storage/registry.go)

```go
package storage

import "fmt"

// MetadataStoreFactory creates a MetadataStore from config.
type MetadataStoreFactory func(cfg MetadataStoreConfig) (types.MetadataStore, func() error, error)

// MetadataStoreConfig holds backend-agnostic configuration.
type MetadataStoreConfig struct {
    Backend         string // "sqlite" or "postgres"
    SQLitePath      string
    PostgresConnStr string
    PostgresMaxOpen int
    PostgresMaxIdle int
    PostgresSchema  string
}

var metadataStoreFactories = map[string]MetadataStoreFactory{}

// RegisterMetadataStore registers a factory for a named backend.
func RegisterMetadataStore(name string, factory MetadataStoreFactory) {
    metadataStoreFactories[name] = factory
}

// NewMetadataStore creates a MetadataStore for the named backend.
// Returns the store and a closer function.
func NewMetadataStore(cfg MetadataStoreConfig) (types.MetadataStore, func() error, error) {
    factory, ok := metadataStoreFactories[cfg.Backend]
    if !ok {
        return nil, nil, fmt.Errorf("unknown metadata store backend: %q", cfg.Backend)
    }
    return factory(cfg)
}
```

Each backend registers in its `init()`:

```go
// internal/storage/sqlite/register.go
package sqlite

import "github.com/shaktimanai/shaktiman/internal/storage"

func init() {
    storage.RegisterMetadataStore("sqlite", NewStore)
}
```

#### VectorStore Registry (internal/vector/registry.go)

Same pattern for `VectorStoreFactory`, `VectorStoreConfig`, `RegisterVectorStore`, `NewVectorStore`.

### 3. Package Layout

```
internal/storage/
    registry.go          # MetadataStore registry (new)
    config.go            # MetadataStoreConfig type (new)
    sqlite/
        db.go            # refactored from current storage/db.go
        schema.go         # refactored from current storage/schema.go
        metadata.go       # refactored from current storage/metadata.go
        fts.go            # refactored from current storage/fts.go
        graph.go          # refactored from current storage/graph.go
        diff.go           # refactored from current storage/diff.go
        register.go       # init() registration
    postgres/
        db.go             # pgx connection pool
        schema.go          # Postgres DDL (tsvector, no FTS5)
        metadata.go        # MetadataStore implementation
        fts.go             # tsvector/tsquery full-text search
        graph.go           # recursive CTEs (same logic, Postgres syntax)
        diff.go            # diff tracking
        register.go        # init() registration
        migrate.go         # schema migrations

internal/vector/
    registry.go           # VectorStore registry (new)
    config.go             # VectorStoreConfig type (new)
    brute_force/
        store.go          # refactored from current vector/store.go
        register.go
    hnsw/
        store.go          # refactored from current vector/hnsw.go
        register.go
    qdrant/
        client.go         # Qdrant HTTP/gRPC client
        store.go          # VectorStore implementation
        register.go
    pgvector/
        store.go          # VectorStore over pgx
        register.go
```

### 4. Daemon Factory Changes (internal/daemon/daemon.go)

Replace the current `newVectorStore()` switch and hardcoded `storage.Open()` with registry calls:

```go
func New(cfg types.Config) (*Daemon, error) {
    // Validate backend combination
    if err := validateBackendCombination(cfg); err != nil {
        return nil, err
    }

    // Create MetadataStore via registry
    store, closer, err := storage.NewMetadataStore(storage.MetadataStoreConfig{
        Backend:         cfg.DatabaseBackend,
        SQLitePath:      cfg.DBPath,
        PostgresConnStr: cfg.PostgresConnString,
        PostgresMaxOpen: cfg.PostgresMaxOpen,
        PostgresMaxIdle: cfg.PostgresMaxIdle,
        PostgresSchema:  cfg.PostgresSchema,
    })
    // ...

    // Create VectorStore via registry
    vectorStore, err := vector.NewVectorStore(vector.VectorStoreConfig{
        Backend:    cfg.VectorBackend,
        Dims:       cfg.EmbeddingDims,
        QdrantURL:  cfg.QdrantURL,
        // ...
    })
    // ...
}
```

### 5. PostgreSQL-Specific Design

#### Connection Management

Use `pgx/v5` (not `database/sql`) for:
- Native Postgres type support (arrays, JSONB, inet, etc.)
- Connection pooling via `pgxpool`
- `COPY` protocol for bulk inserts (InsertChunks, InsertSymbols)
- Prepared statement caching

```go
pool, err := pgxpool.New(ctx, connString)
pool.Config().MaxConns = int32(cfg.PostgresMaxOpen)
```

#### Full-Text Search: tsvector/tsquery

Replace SQLite FTS5 with Postgres native FTS:

```sql
-- Column on chunks table
ALTER TABLE chunks ADD COLUMN content_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('english', content || ' ' || COALESCE(symbol_name, ''))) STORED;

CREATE INDEX idx_chunks_fts ON chunks USING GIN(content_tsv);

-- Query
SELECT id, ts_rank(content_tsv, query) AS rank
FROM chunks, plainto_tsquery('english', $1) query
WHERE content_tsv @@ query
ORDER BY rank DESC
LIMIT $2;
```

No triggers needed -- the generated column auto-updates. This replaces the three FTS5 triggers in `schema.go`.

#### Schema DDL

The logical schema (files, chunks, symbols, edges, pending_edges, diff_log, diff_symbols, access_log, working_set, tool_calls) maps directly to Postgres. Key differences:

| SQLite | PostgreSQL |
|---|---|
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGSERIAL PRIMARY KEY` |
| `TEXT` for timestamps | `TIMESTAMPTZ` |
| `REAL` for mtime | `DOUBLE PRECISION` |
| FTS5 virtual table + triggers | `tsvector` generated column + GIN index |
| `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')` | `NOW()` |
| `ON CONFLICT(path) DO UPDATE` | Same (Postgres supports upsert) |

#### Graph Traversal

The recursive CTE in `graph.go` (`WITH RECURSIVE reachable AS ...`) is portable to Postgres with no changes. Postgres recursive CTEs are well-optimized and support cycle detection via `CYCLE` clause (Postgres 14+).

#### Bulk Insert Performance

Use `COPY` protocol for InsertChunks/InsertSymbols (10-50x faster than individual INSERTs for large batches). pgx supports this natively via `pgx.Conn.CopyFrom()`.

### 6. Qdrant-Specific Design

#### Connection

Use HTTP REST API (port 6334) rather than gRPC:
- Simpler dependency footprint (no protobuf/grpc codegen)
- Sufficient for Shaktiman's throughput requirements (~100 vectors/sec during indexing)
- gRPC can be added later if throughput becomes a bottleneck

#### Collection Management

On startup, create collection if it does not exist:

```json
PUT /collections/shaktiman
{
    "vectors": {
        "size": 768,
        "distance": "Cosine"
    }
}
```

Dimension (`size`) comes from `cfg.EmbeddingDims`. Mismatch with existing collection is a fatal error.

#### Point ID Mapping

Qdrant uses UUID or integer point IDs. Use chunk ID (int64) directly as the point ID. This avoids a mapping table.

#### Metadata Filtering

Store `file_id` and `is_test` as payload fields for server-side filtering:

```json
{
    "id": 12345,
    "vector": [0.1, 0.2, ...],
    "payload": {
        "file_id": 42,
        "is_test": false
    }
}
```

Create payload index on `is_test` for scope filtering.

#### Delete Semantics

`Delete(ctx, chunkIDs)` maps to Qdrant's batch delete by point ID. Qdrant deletes are idempotent.

### 7. pgvector-Specific Design

#### Extension Setup

Requires `CREATE EXTENSION IF NOT EXISTS vector;` -- executed during Postgres schema migration.

#### Table Design

Add a vector column to a dedicated embeddings table (not on chunks directly, to avoid bloating the main table):

```sql
CREATE TABLE embeddings (
    chunk_id BIGINT PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
    embedding vector(768) NOT NULL
);

CREATE INDEX idx_embeddings_hnsw ON embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 200);
```

#### Index Type Decision

Use HNSW (not IVFFlat):
- HNSW has better recall at the same query speed
- No training step required (IVFFlat needs `CREATE INDEX` with training data)
- pgvector's HNSW supports concurrent inserts (since pgvector 0.5.0)
- M=16, ef_construction=200 matches current HNSW backend defaults

#### Search Query

```sql
SELECT chunk_id, 1 - (embedding <=> $1::vector) AS score
FROM embeddings
ORDER BY embedding <=> $1::vector
LIMIT $2;
```

`<=>` is cosine distance; `1 - distance` converts to similarity score matching `VectorResult.Score` semantics.

#### Integration with Postgres MetadataStore

When both `database.backend = "postgres"` and `vector.backend = "pgvector"`, they share the same `pgxpool.Pool`. The pgvector VectorStore receives the pool from the MetadataStore factory rather than creating its own connection. This is wired in the daemon factory:

```go
if cfg.VectorBackend == "pgvector" && cfg.DatabaseBackend == "postgres" {
    // Pass the pool from metadata store to pgvector store
    vectorCfg.PgPool = metadataStore.Pool()
}
```

### 8. Migration Strategy

#### SQLite to PostgreSQL Migration

**Approach:** Export-import via `shaktiman migrate` command.

```
shaktiman migrate --from sqlite --to postgres --project-root /path/to/project
```

Steps:
1. Open source SQLite database (read-only).
2. Connect to target Postgres (from TOML/CLI config).
3. Run Postgres schema DDL.
4. Stream data table-by-table using `COPY` protocol.
5. Regenerate FTS (tsvector columns auto-populate on insert).
6. Verify row counts match.
7. Update `shaktiman.toml` to `database.backend = "postgres"`.

**Estimated time:** ~30 seconds for 100K files (bottleneck is COPY throughput, not CPU).

**Rollback:** Source SQLite is never modified. Delete Postgres schema to roll back.

#### BruteForce/HNSW to Qdrant Migration

**Approach:** Full re-upload. No incremental path because the in-memory stores use a binary format incompatible with Qdrant.

```
shaktiman migrate --vector-from brute_force --vector-to qdrant --project-root /path/to/project
```

Steps:
1. Load existing vectors from disk (embeddings.bin or embeddings.hnsw).
2. Create Qdrant collection with correct dimensions.
3. Batch upsert all vectors to Qdrant (batches of 100 points).
4. Verify count matches.
5. Update `shaktiman.toml` to `vector.backend = "qdrant"`.

**Alternative:** Skip migration entirely and re-embed from source chunks (`shaktiman index --embed --vector qdrant`). This is slower but simpler and guaranteed correct.

#### BruteForce/HNSW to pgvector Migration

Same as Qdrant migration, but vectors are inserted into the `embeddings` table via SQL.

**Recommendation:** For projects under 50K chunks, prefer re-embedding over migration. The Ollama embedding step takes ~10 minutes for 50K chunks. Migration tooling has a higher bug surface than re-embedding, which reuses the existing, tested embedding pipeline.

#### Can Migration Be Incremental?

No. The relational schema migration (SQLite to Postgres) must be atomic -- partial migration leaves the system in an inconsistent state. Vector migration can technically be incremental (upload vectors in batches, resume on failure) but the full re-embed approach is preferred for simplicity.

### 9. Testing Strategy

#### Interface Compliance Tests

Create a shared test suite that any MetadataStore implementation must pass:

```go
// internal/storage/storetest/suite.go
func RunMetadataStoreTests(t *testing.T, factory func() types.MetadataStore) {
    t.Run("UpsertFile", func(t *testing.T) { ... })
    t.Run("InsertChunks", func(t *testing.T) { ... })
    t.Run("KeywordSearch", func(t *testing.T) { ... })
    t.Run("Neighbors", func(t *testing.T) { ... })
    // ... all MetadataStore methods
}
```

Similarly for VectorStore:

```go
// internal/vector/storetest/suite.go
func RunVectorStoreTests(t *testing.T, factory func() types.VectorStore) { ... }
```

#### Backend-Specific Tests

- SQLite tests run in CI with no external dependencies.
- Postgres tests require a Postgres instance (use `testcontainers-go` or skip if not available).
- Qdrant tests require a Qdrant instance (use `testcontainers-go` or skip).
- pgvector tests require Postgres with the vector extension.

#### Integration Tests

Test each valid backend combination end-to-end through `daemon.New()`.

### 10. Documentation Updates

- **README.md:** Add "Backend Configuration" section with quick-start per combination.
- **docs/setup-postgres.md:** PostgreSQL setup guide (install, create database, configure TOML).
- **docs/setup-qdrant.md:** Qdrant setup guide (Docker, cloud, configure TOML).
- **docs/migration.md:** Migration guide with step-by-step per migration path.
- **TOML sample config:** Update `sampleConfig` constant in `config.go` with all new sections.

---

## Implementation Phasing

### Phase 1: Configuration Enhancement (Low Risk)

**Goal:** Make hardcoded values configurable. No backend changes.

- Add `[embedding]` section to TOML (ollama_url, model, dims, batch_size, timeout).
- Add `[database]` section with `backend = "sqlite"` (forward declaration).
- Add `[qdrant]` and `[postgres]` sections (parsed but not yet used).
- Add config validation for backend combinations.
- Add `--db`, `--postgres-url`, `--qdrant-url` CLI flags (error if non-sqlite selected).
- Add environment variable support for secrets.
- Update `sampleConfig` constant.
- **Estimate:** 2-3 days. **Test coverage:** config parsing + validation unit tests.

### Phase 2: Storage Interface Refactoring (Medium Risk)

**Goal:** Extract SQLite implementation to sub-package without changing behavior.

- Create `internal/storage/registry.go` with provider registry.
- Move SQLite implementation to `internal/storage/sqlite/` sub-package.
- Create `internal/storage/sqlite/register.go` with `init()`.
- Update `daemon.go` to use `storage.NewMetadataStore()`.
- Create `internal/vector/registry.go` with vector provider registry.
- Move BruteForce to `internal/vector/brute_force/`, HNSW to `internal/vector/hnsw/`.
- Create shared test suites in `storetest/`.
- **Estimate:** 3-4 days. **Risk:** Refactoring breaks import paths. Mitigated by running full test suite after each move.

### Phase 3: PostgreSQL MetadataStore (High Value)

**Goal:** Implement Postgres backend for MetadataStore + BatchMetadataStore + EmbedSource.

- Add `pgx/v5` dependency.
- Implement `internal/storage/postgres/` package (all MetadataStore methods).
- Implement tsvector-based KeywordSearch.
- Implement Neighbors via recursive CTE (port from SQLite).
- Implement schema migrations (Postgres DDL).
- Add `shaktiman migrate --from sqlite --to postgres`.
- Write interface compliance tests + Postgres-specific tests.
- **Estimate:** 5-7 days. **Dependency:** Phase 2 complete.

### Phase 4: Qdrant VectorStore (Medium Value)

**Goal:** Implement Qdrant as an external vector backend.

- Implement `internal/vector/qdrant/` package (HTTP client).
- Collection lifecycle management (create, verify dimensions).
- Implement all VectorStore methods.
- Add vector migration command.
- **Estimate:** 3-4 days. **Dependency:** Phase 2 complete. Independent of Phase 3.

### Phase 5: pgvector VectorStore (Medium Value)

**Goal:** Implement pgvector for fully-external deployment.

- Implement `internal/vector/pgvector/` package.
- HNSW index creation with correct ops class.
- Connection pool sharing with Postgres MetadataStore.
- **Estimate:** 2-3 days. **Dependency:** Phase 3 complete (needs Postgres pool sharing).

---

## Pre-Mortem Analysis

### "The migration corrupted production data"

**Scenario:** SQLite-to-Postgres migration fails mid-stream. Half the tables are populated, half are empty.
**Mitigation:** Migration writes to a fresh Postgres schema. Source SQLite is never modified. Migration is atomic per-table (wrapped in a transaction). Verification step compares row counts before marking complete.
**Detection:** `shaktiman status` reports mismatched counts.

### "Qdrant went down and search stopped working"

**Scenario:** Qdrant is an external service. Network partition or Qdrant restart makes vector search unavailable.
**Mitigation:** VectorStore.Search returns an error; the query engine falls back to FTS-only ranking (keyword search still works via MetadataStore). Log warning. Do not crash the daemon.
**Detection:** Health check in daemon reports vector store status.

### "Someone changed embedding.dims after indexing 100K vectors"

**Scenario:** User changes model from nomic-embed-text (768d) to a 1024d model. Existing vectors are 768d. Search returns garbage.
**Mitigation:** Config validation compares `embedding.dims` against stored dimension in config table. Mismatch is a fatal startup error with clear message: "Embedding dimensions changed from 768 to 1024. Re-index with --embed or revert config."
**Detection:** Startup validation.

### "Postgres latency is 10x SQLite for local development"

**Scenario:** Developer configures Postgres for local dev. Network round-trip per query adds 1-5ms. SQLite was <0.1ms.
**Mitigation:** This is expected and documented. Postgres backend is for shared/team use, not local-first. Keep SQLite as default. Document latency expectations.
**Detection:** N/A -- by design.

### "The refactoring in Phase 2 broke the existing SQLite path"

**Scenario:** Moving files to sub-packages introduces import cycles or breaks the CGo build.
**Mitigation:** Phase 2 is a pure refactor with zero behavior change. Run full test suite (`go test -race -tags sqlite_fts5 ./...`) after each file move. Use `goimports` to fix import paths. The CGo dependency (mattn/go-sqlite3) stays in the sqlite sub-package.
**Detection:** CI test suite.

---

## FMEA (Failure Modes and Effects Analysis)

| Failure Mode | Severity | Likelihood | Detection | Mitigation |
|---|---|---|---|---|
| Postgres connection pool exhausted | High | Medium | Connection timeout errors, `pool.Stat()` metrics | Configure `max_open_conns` appropriately. Add pool stats to `shaktiman status`. |
| Qdrant collection dimension mismatch | High | Low | Fatal error on first upsert | Validate dimensions on collection creation. Check on startup. |
| FTS5-to-tsvector semantic difference | Medium | Medium | Different search results for same query | Document differences. Use `plainto_tsquery` (most similar to FTS5 default). Run search quality regression tests. |
| pgvector HNSW index OOM during build | High | Low | Postgres OOM killer | Set `maintenance_work_mem` appropriately. Document minimum memory. |
| Registry init() ordering conflicts | Low | Low | Test failures | Each backend registers independently. No ordering dependency. |
| SQLite sub-package refactor breaks CGo build tags | Medium | Medium | Build failure | Keep `sqlite_fts5` tag on sqlite sub-package only. Test with `go build -tags sqlite_fts5 ./...`. |
| Qdrant batch upsert exceeds payload limit | Medium | Low | HTTP 413 error | Cap batch size at 100 points. Retry with smaller batches. |
| Config TOML backward compatibility broken | Medium | High | Existing users' TOML rejected | All new sections are optional. Old TOML files parse unchanged. New fields have defaults. |

---

## Consequences

### Positive

1. **Unlocks team deployment.** Postgres backend enables shared indexing for teams of 5-50 developers.
2. **Scales vector search.** Qdrant and pgvector handle millions of vectors with sub-100ms latency. Removes the 100K vector ceiling.
3. **Cloud-native ready.** Managed Postgres (RDS) and managed Qdrant (Qdrant Cloud) eliminate ops burden for production deployments.
4. **Local-first preserved.** SQLite + BruteForce remains the default. Zero-setup experience unchanged.
5. **Embedding config exposed.** Users can point to remote Ollama, change models, tune batch sizes without code changes.
6. **Testable.** Shared test suites ensure all backends satisfy the same contract. Interface compliance is enforced, not assumed.
7. **Extensible.** Adding a new backend (e.g., DuckDB, Milvus, Weaviate) requires only implementing the interface and registering.

### Negative

1. **Increased surface area.** Four vector backends and two relational backends mean more code to maintain, more CI configurations, more documentation.
2. **FTS behavioral differences.** SQLite FTS5 and Postgres tsvector have different tokenization, stemming, and ranking. Search results will differ between backends. This is documented but may confuse users who switch.
3. **CGo remains for SQLite.** The mattn/go-sqlite3 dependency requires CGo. The Postgres path is pure Go (pgx). Mixed build requirements add CI complexity.
4. **Migration tooling is one-way.** Postgres-to-SQLite migration is not planned. Users who switch to Postgres cannot easily switch back.
5. **Phase 2 refactoring risk.** Moving files to sub-packages touches every import path. Short-term churn for long-term cleanliness.
6. **Qdrant/pgvector are external dependencies.** Users must install and manage them. Documentation and error messages must be excellent.

---

## Open Questions

1. **Should the Postgres MetadataStore also implement EmbedSource?** Currently `storage.Store` implements both. If Postgres MetadataStore also implements EmbedSource, the embed worker works unchanged. If not, we need a separate EmbedSource for Postgres. **Recommendation:** Yes, implement EmbedSource on Postgres MetadataStore -- the queries are simple (page through chunks where embedded=0).

2. **Should we support read replicas for Postgres?** The current SQLite architecture has a dual-connection model (writer + reader pool). Postgres natively supports multiple connections. A read-replica configuration adds complexity. **Recommendation:** Defer. Single Postgres instance is sufficient for initial deployment. Add read-replica support when a real scaling need emerges.

3. **Should Qdrant use gRPC or HTTP?** HTTP is simpler. gRPC is ~2x faster for batch operations. **Recommendation:** Start with HTTP. Add gRPC as an opt-in later if throughput matters.

4. **Should we add a `shaktiman backend status` command?** To show which backends are configured and their health (Postgres reachable, Qdrant collection exists, etc.). **Recommendation:** Yes, add in Phase 1 as part of config validation.

5. **Should pgvector use a separate `embeddings` table or add a column to `chunks`?** Separate table avoids bloating chunks table scans. Column on chunks simplifies JOINs. **Recommendation:** Separate table. The `chunks` table is scanned for FTS and iteration; adding a 768-float column would degrade those operations.

---

## References

- Go `database/sql` driver pattern: https://pkg.go.dev/database/sql#Register
- pgx v5 documentation: https://github.com/jackc/pgx
- Qdrant REST API: https://qdrant.tech/documentation/concepts/collections/
- pgvector: https://github.com/pgvector/pgvector
- Grafana Mimir storage backends (provider pattern example): https://github.com/grafana/mimir
- ADR-001: Code Review Capabilities (`docs/design/adr-001-code-review-capabilities.md`)
- ADR-002: Multi-Instance Concurrency (`docs/design/adr-002-multi-instance-concurrency.md`)

---

## Amendment 1: Adversarial Findings and Mitigations

**Date:** 2026-04-01
**Trigger:** Adversarial analysis revealed 3 critical, 4 high, and 6 medium findings. This amendment addresses concrete type leakage, uninterfaced methods, cross-ADR composition gaps, and underspecified failure modes.

### Adversarial Findings Summary

| # | Severity | Finding | Decision |
|---|---|---|---|
| F1 | CRITICAL | Concrete `*storage.Store` leakage across 17 files, 20+ uninterfaced methods | A1 |
| F2 | CRITICAL | FTS lifecycle methods not on any interface | A1 (StoreLifecycle adapter) |
| F3 | CRITICAL | EmbedSource + embedding reconciliation methods unaddressed | A1 |
| F4 | HIGH | Cross-ADR composition gap with ADR-002 overlay/RLS | A3 |
| F5 | HIGH | `LastInsertId()` doesn't exist in pgx | A4 |
| F6 | HIGH | Migration atomicity claim is false | A5 |
| F7 | HIGH | pgvector pool sharing creates deadlock risk | A6 |
| F8 | MEDIUM | `init()` + build tags interaction blocks pure-Go builds | A7 |
| F9 | MEDIUM | FTS5/tsvector semantic drift for code identifiers | A8 |
| F10 | MEDIUM | Vector search failure fallback unspecified | A9 |
| F11 | MEDIUM | Config accepts backend names before factories exist | A10 |
| F12 | MEDIUM | MetricsRecorder requires raw `*sql.DB` | A11 |
| F13 | MEDIUM | `*sql.Tx` params in diff/graph methods block backend abstraction | A2 |

---

### Decision A1: Interface Extraction (addresses F1, F2, F3)

**Context:** The original ADR assumed that `MetadataStore` + `BatchMetadataStore` + `EmbedSource` covered the full surface area of `storage.Store`. Adversarial audit found 20+ methods on `*storage.Store` not on any interface, and 17 files holding concrete type references.

#### Concrete Type Leakage Inventory

**Production files holding `*storage.Store`:**

| File | Usage | Required Change |
|---|---|---|
| `internal/daemon/daemon.go` | Field `store *storage.Store`, accessor `Store()` | Change to `WriterStore` interface |
| `internal/daemon/writer.go` | Field `store *storage.Store` | Change to `WriterStore` interface |
| `internal/daemon/enrichment.go` | Field `store *storage.Store` | Change to `WriterStore` interface |
| `internal/mcp/server.go` | Struct field `Store *storage.Store` | Change to `MetadataStore` interface |
| `internal/mcp/tools.go` | Handler params `*storage.Store` (5 sites) | Change to `MetadataStore` interface |
| `internal/mcp/resources.go` | Function param `*storage.Store` | Change to `MetadataStore` interface |
| `cmd/shaktiman/main.go` | `d.Store().GetIndexStats()` | Use interface accessor |
| `cmd/shaktiman/query.go` | Direct `storage.NewStore(db)` (6 sites) | Use registry factory |

**Production files holding `*storage.DB`:**

| File | Usage | Required Change |
|---|---|---|
| `internal/daemon/daemon.go` | Field `db *storage.DB` | Eliminate; fold into factory |
| `internal/mcp/metrics.go` | `MetricsRecorder.db *sql.DB` | New `MetricsWriter` interface (A11) |

#### New Interfaces

New interfaces in `internal/types/interfaces.go`:

```go
// DiffStore provides diff log and symbol change tracking.
type DiffStore interface {
    InsertDiffLog(ctx context.Context, tx TxHandle, entry DiffLogEntry) (int64, error)
    InsertDiffSymbols(ctx context.Context, tx TxHandle, diffID int64, symbols []DiffSymbolEntry) error
    GetRecentDiffs(ctx context.Context, input RecentDiffsInput) ([]DiffLogEntry, error)
    GetDiffSymbols(ctx context.Context, diffID int64) ([]DiffSymbolEntry, error)
}

// GraphMutator provides graph write operations (edges, pending edges).
// Extends the existing read-only GraphStore (Neighbors) with mutations.
type GraphMutator interface {
    InsertEdges(ctx context.Context, tx TxHandle, fileID int64, edges []EdgeRecord, symbolIDs map[string]int64, language string) error
    ResolvePendingEdges(ctx context.Context, tx TxHandle, newSymbolNames []string) error
    DeleteEdgesByFile(ctx context.Context, tx TxHandle, fileID int64) error
    PendingEdgeCallers(ctx context.Context, dstName string) ([]int64, error)
    PendingEdgeCallersWithKind(ctx context.Context, dstName string) ([]PendingEdgeCaller, error)
}

// StoreLifecycle provides backend-specific startup and bulk-write hooks.
// Returned as a separate value by the factory — not embedded in WriterStore.
// SQLite: manages FTS5 triggers and index rebuild.
// Postgres: nil (generated tsvector columns handle FTS automatically).
// Future backends can use these hooks for their own concerns (e.g.,
// Elasticsearch refresh intervals, DuckDB checkpointing).
type StoreLifecycle interface {
    // OnStartup performs crash recovery and index repair.
    // SQLite: EnsureFTSTriggers + IsFTSStale + RebuildFTS.
    OnStartup(ctx context.Context) error
    // OnBulkWriteBegin optimizes the store for batch inserts.
    // SQLite: disables FTS triggers to avoid per-row overhead.
    OnBulkWriteBegin(ctx context.Context) error
    // OnBulkWriteEnd restores normal operation after a bulk write.
    // SQLite: rebuilds FTS index + re-enables triggers.
    OnBulkWriteEnd(ctx context.Context) error
}

// EmbeddingReconciler provides embedding state management for crash recovery.
type EmbeddingReconciler interface {
    CountChunksEmbedded(ctx context.Context) (int, error)
    ResetAllEmbeddedFlags(ctx context.Context) error
    GetEmbeddedChunkIDs(ctx context.Context, afterID int64, limit int) ([]int64, error)
    ResetEmbeddedFlags(ctx context.Context, chunkIDs []int64) error
    EmbeddingReadiness(ctx context.Context, vectorCount int) (float64, error)
}

// WriterStore is the composite interface required by the daemon's write path.
// The daemon's writer.go and enrichment.go depend on all of these capabilities.
// Note: StoreLifecycle is NOT embedded here — it is returned separately by the
// factory and may be nil (e.g., Postgres needs no lifecycle hooks).
type WriterStore interface {
    MetadataStore
    DiffStore
    GraphMutator
    EmbeddingReconciler
    EmbedSource
    WithWriteTx(ctx context.Context, fn func(tx TxHandle) error) error
}
```

#### Decision

- `DiffStore`, `GraphMutator`, `EmbeddingReconciler`, `WriterStore` are added to `internal/types/interfaces.go`.
- `StoreLifecycle` is added to `internal/types/interfaces.go` as a **separate** interface — not embedded in `WriterStore`.
- The daemon holds `WriterStore` (not `*storage.Store`) and optionally `StoreLifecycle` (may be nil).
- The MCP layer holds `MetadataStore` (read-only, no write path needed).
- The `Store.DB()` accessor is removed. Transaction access goes through `WriterStore.WithWriteTx()`.
- The registry factory signature returns `StoreLifecycle` as a separate value:
  ```go
  func NewMetadataStore(cfg MetadataStoreConfig) (WriterStore, StoreLifecycle, func() error, error)
  ```
  Backends that need no lifecycle hooks return `nil` for `StoreLifecycle`.
- Callers that only need reads assign the `WriterStore` to a `MetadataStore` variable.
- **Phase 2 estimate revised from 3-4 days to 6-8 days** (see updated phasing below).

#### Daemon Usage

```go
store, lifecycle, closer, err := storage.NewMetadataStore(cfg)
if err != nil { return err }
defer closer()

// Startup crash recovery — only if backend needs it
if lifecycle != nil {
    if err := lifecycle.OnStartup(ctx); err != nil { return err }
}

// Cold indexing — bracket bulk writes with lifecycle hooks
if lifecycle != nil {
    if err := lifecycle.OnBulkWriteBegin(ctx); err != nil { return err }
    defer lifecycle.OnBulkWriteEnd(ctx)
}
// ... bulk insert chunks/symbols ...
```

#### SQLite StoreLifecycle Adapter

The SQLite factory returns a concrete adapter that wraps the existing FTS methods:

```go
type sqliteLifecycle struct {
    store *Store
}

func (l *sqliteLifecycle) OnStartup(ctx context.Context) error {
    if err := l.store.EnsureFTSTriggers(ctx); err != nil {
        return err
    }
    stale, err := l.store.IsFTSStale(ctx)
    if err != nil { return err }
    if stale {
        return l.store.RebuildFTS(ctx)
    }
    return nil
}

func (l *sqliteLifecycle) OnBulkWriteBegin(ctx context.Context) error {
    return l.store.DisableFTSTriggers(ctx)
}

func (l *sqliteLifecycle) OnBulkWriteEnd(ctx context.Context) error {
    if err := l.store.RebuildFTS(ctx); err != nil { return err }
    return l.store.EnableFTSTriggers(ctx)
}
```

The FTS methods (`EnsureFTSTriggers`, `DisableFTSTriggers`, etc.) remain on the SQLite `Store` struct as private implementation details — they are **not** on any interface. Only `StoreLifecycle` is exposed to the daemon.

#### Postgres Implementation Notes

- `StoreLifecycle`: Returns `nil`. Postgres uses generated `tsvector` columns — no triggers, no manual rebuild, no staleness detection needed. The daemon's `if lifecycle != nil` guard skips all lifecycle calls.
- `DiffStore`: Identical logic, Postgres syntax. `InsertDiffLog` uses `INSERT ... RETURNING id` instead of `LastInsertId()`.
- `GraphMutator`: Recursive CTEs are portable. `COPY` protocol can accelerate `InsertEdges` for bulk operations.
- `EmbeddingReconciler`: Simple flag queries, fully portable.

---

### Decision A2: Transaction Abstraction (addresses F13)

**Context:** 35+ call sites use `store.DB().WithWriteTx(func(tx *sql.Tx) error { ... })`. Several methods (`InsertDiffLog`, `InsertEdges`, `ResolvePendingEdges`, `DeleteEdgesByFile`) take `*sql.Tx` as a parameter, coupling them to `database/sql`.

**Decision:** Replace `*sql.Tx` with an opaque `TxHandle` interface.

```go
// TxHandle is an opaque transaction handle. Each backend defines its own
// concrete type (e.g., *sql.Tx for SQLite, pgx.Tx for Postgres).
// Methods that participate in transactions accept TxHandle.
type TxHandle interface {
    // Marker interface. Backends type-assert to their concrete transaction.
    txHandle()
}
```

#### How it works

1. `WriterStore.WithWriteTx(ctx, fn)` begins a transaction and passes the backend's concrete `TxHandle` to `fn`.
2. Methods like `InsertDiffLog(ctx, tx TxHandle, entry)` receive the handle.
3. Inside the SQLite implementation, `tx.(sqliteTxHandle).Tx` recovers the `*sql.Tx`.
4. Inside the Postgres implementation, `tx.(pgTxHandle).Tx` recovers the `pgx.Tx`.
5. Callers never import `database/sql` or `pgx` — they only pass the opaque handle through.

#### Why not `context.Context` for transaction propagation?

Embedding transactions in context (e.g., `context.WithValue(ctx, txKey, tx)`) is a common Go pattern but hides transaction boundaries, makes it easy to forget to commit/rollback, and complicates testing. Explicit `TxHandle` parameter makes transaction participation visible in function signatures.

---

### Decision A3: Cross-ADR Composition with ADR-002 (addresses F4)

**Context:** ADR-002 defines UNION ALL overlay (SQLite) and RLS (Postgres) for worktree isolation. ADR-003 defines the provider pattern with `MetadataStoreConfig`. Neither ADR specifies how worktree mode flows into the factory.

**Decision:** Add `WorktreeConfig` to `MetadataStoreConfig`.

```go
// WorktreeConfig controls worktree isolation (ADR-002).
type WorktreeConfig struct {
    Enabled     bool   // whether overlay/RLS is active
    WorktreeID  string // unique identifier for this worktree instance
    BaseDBPath  string // path to the base SQLite DB (overlay mode only)
    IsSatellite bool   // true = read base + write overlay (ephemeral worktree)
}

// MetadataStoreConfig (updated)
type MetadataStoreConfig struct {
    Backend         string
    SQLitePath      string
    PostgresConnStr string
    PostgresMaxOpen int
    PostgresMaxIdle int
    PostgresSchema  string
    Worktree        WorktreeConfig // ADR-002 bridge
}
```

#### Backend-Specific Behavior

| Config | SQLite Factory | Postgres Factory |
|---|---|---|
| `Worktree.Enabled = false` | Standard schema (current behavior) | Standard schema |
| `Worktree.Enabled = true` | Apply UNION ALL overlay schema (ADR-002 §D3-D5). Rename base tables, create overlay tables, create merged views with `INSTEAD OF` triggers. | `SET app.worktree_id = '<id>'` on every connection from pool. RLS policies auto-filter all queries. |
| `Worktree.IsSatellite = true` | Open base DB read-only + separate overlay DB. Merged views span both. | Same as `Enabled`, but daemon skips enrichment (read-only MCP tools only). |

#### Vector Store Composition

`VectorStoreConfig` also gains a worktree field:

```go
type VectorStoreConfig struct {
    Backend    string
    Dims       int
    // ... existing fields ...
    Worktree   WorktreeConfig
}
```

When `Worktree.IsSatellite = true`, the vector factory wraps the base `VectorStore` with `OverlayVectorStore` (ADR-002 §D12):

```go
if cfg.Worktree.IsSatellite {
    base := createBaseVectorStore(cfg)
    return NewOverlayVectorStore(base), nil
}
```

#### Cross-Reference

ADR-002 Amendment 2 (D15) references this decision. Both ADRs must be read together for the full worktree + pluggable backend picture.

---

### Decision A4: LastInsertId vs RETURNING (addresses F5)

**Context:** Every `INSERT` in the current SQLite `metadata.go` uses `res.LastInsertId()` (12+ call sites). The ADR specifies pgx (not `database/sql`) for Postgres. pgx does not support `LastInsertId()` — Postgres uses `INSERT ... RETURNING id`.

**Decision:** This is expected and by-design. Each backend has its own implementation of every `MetadataStore`/`WriterStore` method. The Postgres implementation uses `INSERT ... RETURNING id` with `pgx.Row.Scan()`. This is not a transliteration — it is a separate implementation that satisfies the same interface.

Example — `UpsertFile` in Postgres:

```go
func (s *PgStore) UpsertFile(ctx context.Context, file *types.FileRecord) (int64, error) {
    var id int64
    err := s.pool.QueryRow(ctx, `
        INSERT INTO files (path, language, size, mtime, content_hash, is_test)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (path) DO UPDATE SET
            language = EXCLUDED.language, size = EXCLUDED.size,
            mtime = EXCLUDED.mtime, content_hash = EXCLUDED.content_hash,
            is_test = EXCLUDED.is_test
        RETURNING id`, file.Path, file.Language, file.Size,
        file.Mtime, file.ContentHash, file.IsTest).Scan(&id)
    return id, err
}
```

This applies to all insert methods: `InsertChunks`, `InsertSymbols`, `InsertDiffLog`, `InsertEdges`.

---

### Decision A5: Migration Atomicity Correction (addresses F6)

**Context:** Section 8 claimed "migration is atomic per-table (wrapped in a transaction)." This is contradictory — either it is one big transaction (truly atomic) or per-table (resumable but not atomic). For 100K+ files, a single transaction causes Postgres WAL bloat and long lock hold times.

**Decision:** Replace with per-table streaming with checkpoint tracking.

#### Revised Migration Strategy

```
shaktiman migrate --from sqlite --to postgres --project-root /path/to/project
```

1. Create a fresh Postgres schema in a staging namespace (`_shaktiman_migrate`).
2. Run Postgres DDL in the staging schema.
3. Stream data table-by-table using `COPY` protocol. Each table is a separate transaction.
4. After each table completes, record the table name in a `_migrate_checkpoint` table.
5. On failure: resume from the last checkpoint. Already-copied tables are skipped.
6. After all tables complete: verify row counts match source.
7. Rename staging schema to target schema (`ALTER SCHEMA _shaktiman_migrate RENAME TO public`; or configured schema).
8. Update `shaktiman.toml` to `database.backend = "postgres"`.

#### Rollback

- Source SQLite is **never modified** (opened read-only).
- On failure: `DROP SCHEMA _shaktiman_migrate CASCADE` cleans up completely.
- On success: the old SQLite file remains as a backup until the user deletes it.

#### Resume on Failure

The checkpoint table tracks:

```sql
CREATE TABLE _migrate_checkpoint (
    table_name TEXT PRIMARY KEY,
    rows_copied BIGINT NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL
);
```

Re-running `shaktiman migrate` detects the checkpoint table and resumes from where it left off.

---

### Decision A6: Pool Sizing Guidance (addresses F7)

**Context:** pgvector and MetadataStore share a single `pgxpool.Pool` (default 10 connections). Under load (50 developers querying + embedding worker + vector upserts), pool exhaustion in one path blocks the other. Potential deadlock if a MetadataStore transaction holds a connection while the embedding worker needs one for vector upsert.

**Decision:** Add pool sizing model and optional pool separation.

#### Sizing Model

| Component | Connections Needed | Notes |
|---|---|---|
| MCP query handlers | 2 per concurrent user | Short-lived reads |
| Embedding worker | 2 | Cursor pagination + mark-embedded |
| Vector upsert (pgvector) | 1 | Batch upserts during enrichment |
| Enrichment writer | 1 | File/chunk/symbol inserts |
| Overhead buffer | 2 | Health checks, metrics |

**Formula:** `max_open_conns = max(10, 2 * expected_concurrent_users + 6)`

**Default TOML values updated:**

```toml
[postgres]
max_open_conns = 20    # was 10; sized for ~7 concurrent users
max_idle_conns = 10    # was 5
```

#### Pool Separation (Optional)

When `vector.backend = "pgvector"`, users can opt into separate pools:

```toml
[pgvector]
separate_pool = true      # default: false (share with metadata)
max_open_conns = 4        # dedicated vector pool size
```

When `separate_pool = true`, the pgvector factory creates its own `pgxpool.Pool` from the same connection string. This eliminates cross-path contention at the cost of more total connections.

**Default:** Shared pool. Separate pool is documented for high-concurrency deployments.

---

### Decision A7: Build Tag Strategy for Optional Backends (addresses F8)

**Context:** SQLite (`mattn/go-sqlite3`) and HNSW (`hnswlib`) require CGo. If their blank imports are unconditional in `daemon.go`, every build requires CGo — even a Postgres-only deployment. Users deploying to containers or CI environments may want a pure-Go binary.

**Decision:** Conditional blank imports via build tag files.

#### File Layout

```
cmd/shaktimand/
    main.go                    # no backend imports here
    imports_sqlite.go          # //go:build sqlite_fts5
    imports_hnsw.go            # //go:build hnsw
    imports_postgres.go        # //go:build postgres
    imports_qdrant.go          # //go:build qdrant
    imports_pgvector.go        # //go:build pgvector
```

Each import file contains only the blank import:

```go
//go:build sqlite_fts5

package main

import _ "github.com/shaktimanai/shaktiman/internal/storage/sqlite"
```

#### Build Profiles

| Profile | Command | Backends Available |
|---|---|---|
| Default (local-first) | `go build -tags sqlite_fts5 ./cmd/shaktimand` | SQLite + BruteForce |
| Full (all backends) | `go build -tags "sqlite_fts5 hnsw postgres qdrant pgvector" ./cmd/shaktimand` | All |
| Pure-Go (no CGo) | `go build -tags "postgres qdrant pgvector" ./cmd/shaktimand` | Postgres + Qdrant + pgvector |
| Postgres + local vectors | `go build -tags "postgres sqlite_fts5" ./cmd/shaktimand` | Postgres + SQLite + BruteForce |

#### Backward Compatibility

The existing `-tags sqlite_fts5` build command continues to work unchanged. The BruteForce vector backend requires no build tag (pure Go, always available). Only new backends require new tags.

#### Startup Validation

If a user configures `database.backend = "postgres"` but built without `-tags postgres`, the registry returns a clear error:

```
unknown metadata store backend: "postgres"
Hint: rebuild with -tags postgres to enable PostgreSQL support
```

This is implemented by A10 (config validation against registered factories).

---

### Decision A8: FTS Semantic Drift Mitigation (addresses F9)

**Context:** SQLite FTS5 and Postgres tsvector tokenize code identifiers differently. `handleRequest` is one token in FTS5 (Unicode61 tokenizer) but `to_tsvector('english', ...)` applies stemming and may split or stem differently. Users migrating from SQLite to Postgres will see different search results for the same queries.

**Decision:** Use `simple` dictionary for Postgres code search, plus a regression test suite.

#### Postgres FTS Configuration

Replace the `english` dictionary with `simple` for code-oriented search:

```sql
ALTER TABLE chunks ADD COLUMN content_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', content || ' ' || COALESCE(symbol_name, ''))) STORED;
```

The `simple` dictionary:
- No stemming (preserves exact tokens)
- Lowercases only
- No stop-word removal (important — code has meaningful short identifiers like `id`, `db`, `tx`)
- Closer to FTS5 Unicode61 behavior for code content

Trade-off: Natural-language queries ("authentication handling") work slightly worse with `simple` because there is no stemming. But Shaktiman indexes code, not prose — exact token matching is preferred.

#### Search Quality Regression Tests

Create `internal/storage/storetest/fts_regression_test.go`:

```go
var ftsRegressionCases = []struct {
    Query    string
    MustFind []string // file paths that must appear in top-5
}{
    {"handleRequest", []string{"internal/mcp/server.go"}},
    {"KeywordSearch", []string{"internal/storage/fts.go"}},
    {"UpsertFile", []string{"internal/storage/metadata.go"}},
    // ... canonical queries with known expected results
}
```

Run against both SQLite and Postgres backends. Results don't need to be identical (ranking differs) but the must-find files must appear in the top results.

---

### Decision A9: Vector Search Failure Fallback (addresses F10)

**Context:** The pre-mortem states "the query engine falls back to FTS-only ranking" when Qdrant is unavailable, but no such fallback exists in the current `QueryEngine`. If `VectorStore.Search` returns an error, the query currently fails.

**Decision:** Add graceful degradation to `QueryEngine` and a health check to `VectorStore`.

#### VectorStore Health Check

Add to `VectorStore` interface:

```go
type VectorStore interface {
    // ... existing methods ...
    // Healthy returns true if the store is reachable and operational.
    Healthy(ctx context.Context) bool
}
```

In-process backends (BruteForce, HNSW) always return `true`. Qdrant and pgvector perform a lightweight connectivity check (e.g., collection info for Qdrant, `SELECT 1` for pgvector).

#### QueryEngine Fallback

In `internal/core/engine.go`, the ranking pipeline changes:

```go
vectorResults, err := e.vectorStore.Search(ctx, queryVec, topK)
if err != nil {
    e.logger.Warn("vector search unavailable, falling back to FTS-only",
        "error", err)
    vectorResults = nil // proceed with FTS scores only
}
```

When `vectorResults` is nil, the ranker uses keyword scores + graph scores + change scores. Vector score weight becomes 0. This produces degraded but functional results.

#### Daemon Health Endpoint

`shaktiman status` reports vector store health:

```
Vector Store: qdrant (http://localhost:6334) — HEALTHY
Vector Store: qdrant (http://localhost:6334) — UNAVAILABLE (connection refused)
```

---

### Decision A10: Config Validation Against Registered Factories (addresses F11)

**Context:** Phase 1 adds new backend names ("qdrant", "pgvector", "postgres") to config parsing. But the factories for these backends don't exist until Phases 3-5. If a user writes `vector.backend = "qdrant"` in TOML during Phase 1, config parsing succeeds but the registry lookup fails with a confusing error.

**Decision:** Config validation checks factory registration, not just string validity.

```go
// ValidateBackends checks that configured backends have registered factories.
// Called after init() has run and before daemon startup.
func ValidateBackends(cfg types.Config) error {
    if !storage.HasMetadataStore(cfg.DatabaseBackend) {
        return fmt.Errorf("database backend %q is not available in this build; "+
            "rebuild with the appropriate build tag", cfg.DatabaseBackend)
    }
    if !vector.HasVectorStore(cfg.VectorBackend) {
        return fmt.Errorf("vector backend %q is not available in this build; "+
            "rebuild with the appropriate build tag", cfg.VectorBackend)
    }
    return nil
}
```

Registry helper:

```go
// HasMetadataStore returns true if a factory is registered for the named backend.
func HasMetadataStore(name string) bool {
    _, ok := metadataStoreFactories[name]
    return ok
}
```

This replaces the current hardcoded string validation in `LoadConfigFromFile`. The error message guides the user to the correct build tag.

---

### Decision A11: MetricsRecorder Abstraction (addresses F12)

**Context:** `MetricsRecorder` in `internal/mcp/metrics.go` holds `*sql.DB` directly, passed from `d.db.Writer()`. For Postgres, this is a `pgxpool.Pool`, not `*sql.DB`.

**Decision:** Define a `MetricsWriter` interface.

```go
// MetricsWriter persists MCP tool call metrics.
type MetricsWriter interface {
    RecordToolCall(ctx context.Context, record ToolCallRecord) error
}
```

- SQLite implementation: wraps `*sql.DB`, uses current INSERT logic.
- Postgres implementation: wraps `pgxpool.Pool`, uses `INSERT ... RETURNING` or fire-and-forget `INSERT`.
- `MetricsRecorder` accepts `MetricsWriter` in its constructor, not `*sql.DB`.
- The factory creates the appropriate `MetricsWriter` alongside the `MetadataStore` (same connection/pool).

---

### Updated FMEA

Additional failure modes from adversarial analysis:

| Failure Mode | Severity | Likelihood | Detection | RPN | Mitigation |
|---|---|---|---|---|---|
| Concrete type leakage discovered during Phase 2 | High (8) | Low (2) now | Code review (3) | 48 | A1 inventory eliminates surprise. All 17 files and 20+ methods documented. |
| `TxHandle` type assertion panic at runtime | High (8) | Low (2) | Test suite (2) | 32 | Interface compliance tests cover all transaction paths. Type assertion happens in one place per backend. |
| Build tag misconfiguration (missing backend at runtime) | Medium (5) | Medium (5) | Startup error (3) | 75 | A10 validates factory registration at startup with clear error message including build tag hint. |
| Pool exhaustion deadlock (pgvector shared pool) | High (8) | Medium (4) | Connection timeout (5) | 160 | A6 raises default pool size, documents sizing formula, offers optional pool separation. |
| FTS semantic drift causes user confusion on migration | Medium (5) | High (7) | User reports (7) | 245 | A8 uses `simple` dictionary (no stemming), adds regression test suite. Document remaining differences. |
| Migration resume corrupts data on retry | High (8) | Low (3) | Checkpoint verification (3) | 72 | A5 per-table checkpoints in staging schema. Each table is a separate transaction. Verify row counts. |

**Top 3 risks by RPN:**
1. FTS semantic drift (245) — Partially mitigated by `simple` dictionary. Residual risk: tokenization differences for compound identifiers.
2. Pool exhaustion deadlock (160) — Mitigated by higher defaults and optional pool separation.
3. Build tag misconfiguration (75) — Mitigated by startup validation with clear error message.

---

### Updated Implementation Phasing

#### Phase 2 (Revised): Storage Interface Refactoring — 6-8 days

Original estimate was 3-4 days. Revised after adversarial audit revealed the full scope of concrete type leakage.

**Phase 2a: Interface Extraction (3 days)**
- Add `DiffStore`, `GraphMutator`, `FTSManager`, `EmbeddingReconciler`, `WriterStore` to `interfaces.go`.
- Add `TxHandle` interface.
- Add `MetricsWriter` interface.
- Change `daemon.Daemon` to hold `WriterStore` instead of `*storage.Store`.
- Change MCP layer to accept `MetadataStore` instead of `*storage.Store`.
- Change `MetricsRecorder` to accept `MetricsWriter`.
- Remove `Store.DB()` accessor.
- **Test:** Full test suite passes with no concrete type imports outside `internal/storage/`.

**Phase 2b: SQLite Sub-Package Move (2 days)**
- Move `storage/*.go` to `internal/storage/sqlite/`.
- Create `internal/storage/sqlite/register.go` with `init()`.
- Create `cmd/shaktimand/imports_sqlite.go` with build tag.
- Update all import paths.
- **Test:** `go build -tags sqlite_fts5 ./...` and `go test -tags sqlite_fts5 ./...` pass.

**Phase 2c: Vector Sub-Package Move (1 day)**
- Move BruteForce to `internal/vector/brute_force/`.
- Move HNSW to `internal/vector/hnsw/`.
- Create registry and build tag import files.
- **Test:** Full test suite passes.

**Phase 2d: Shared Test Suites (2 days)**
- Create `internal/storage/storetest/suite.go` — `MetadataStore` compliance.
- Create `internal/storage/storetest/writer_suite.go` — `WriterStore` compliance.
- Create `internal/vector/storetest/suite.go` — `VectorStore` compliance.
- Create FTS regression test cases (A8).
- **Test:** SQLite and BruteForce pass all compliance tests.

#### Phases 3-5: Unchanged

Phase 3 (Postgres), Phase 4 (Qdrant), Phase 5 (pgvector) estimates remain as originally stated. The interface extraction in Phase 2 de-risks these phases — each new backend only needs to implement well-defined interfaces.

---

## Amendment 2 — 2026-04-09: Postgres requires an externalised vector backend

**Trigger:** ADR-002 Amendment 3 re-scoped multi-instance concurrency support. Analysis of which combinations of backends actually survive "two daemons on the same project directory" ("Case F") showed that Postgres closes the metadata-layer race but *silently leaves the vector layer corruptible* when paired with a file-backed in-memory vector store (`brute_force` or `hnsw`). This amendment records the constraint that makes the Postgres path internally consistent.

### Context

The `brute_force` and `hnsw` vector stores persist their state to `.shaktiman/embeddings.bin` in the project directory via the `VectorPersister` interface (`SaveToDisk`/`LoadFromDisk`). The daemon loads this file at startup (`internal/daemon/daemon.go:107`, `initEmbedding`) and writes it back on a timer (`periodicEmbeddingSave`, line 367) and on shutdown (line 439-448).

When two daemons run against the same project:
- **Metadata layer (Postgres):** MVCC and the `EnsureProject` canonical-path identity handle concurrent writes cleanly. No race.
- **Vector layer (brute_force/hnsw):** Each daemon holds its own in-memory copy. Both read the same file on startup, both write the same file on shutdown/timer → **last-writer-wins at the file level**, silently losing the other daemon's updates. This is a concurrency bug that Postgres alone cannot fix.

The problem does not exist for:
- `pgvector`, which stores vectors in Postgres tables scoped by `project_id` with `ON CONFLICT (chunk_id) DO UPDATE` on writes.
- `qdrant`, which stores vectors in a server-side collection with last-write-wins at the point level (acceptable because same-project, same-chunk-id writes converge).

### Decision A12: Reject `postgres + brute_force` and `postgres + hnsw` at config validation

**Rule:** `ValidateBackendConfig` (in `internal/types/config.go`) rejects the combination `database.backend = "postgres"` with `vector.backend ∈ {"brute_force", "hnsw"}`.

**Enforcement point:** `ValidateBackendConfig`, called from `cmd/shaktimand/main.go` before `daemon.New(cfg)`. The daemon refuses to start; the error message is actionable:

> `config: database.backend "postgres" is incompatible with vector.backend %q — use "pgvector" or "qdrant". In-memory vector stores (brute_force, hnsw) persist to .shaktiman/embeddings.bin per daemon and will corrupt when multiple daemons share a Postgres project.`

**Validation ordering:**
1. `pgvector requires postgres` (existing check, unchanged).
2. `postgres requires connection string` (existing check, unchanged).
3. **NEW:** `postgres requires pgvector or qdrant vector backend` (added by this amendment).
4. `qdrant requires url` (existing check, unchanged).

Ordering the new check after "postgres requires connection string" preserves the existing test's error expectations and gives the user the more fundamental error first when both are wrong.

### Consequences

**Positive:**
1. **Case F ("two sessions on one project") is supported on Postgres** with `pgvector` or `qdrant`, with zero new code paths beyond this validation. No flock, no leader/follower, no staleness signals.
2. **The Postgres backend is internally consistent** — opting in to Postgres means opting in to concurrency safety end-to-end.
3. **Failure is loud, not silent.** Users with misconfigured combinations get an actionable startup error instead of silent vector corruption that only surfaces as "search results went stale."

**Negative:**
1. **Breaking change** for any existing user running `postgres + brute_force` or `postgres + hnsw`. Mitigation: the error message names the two valid alternatives; `pgvector` is already packaged with the Postgres backend and requires no additional infrastructure.
2. **Expands the Postgres-user prerequisite surface.** A developer who wanted Postgres for the shared-metadata use case now must also run pgvector (via the same Postgres instance — no extra service) or stand up Qdrant. In practice pgvector is the path of least resistance and costs nothing extra.
3. **Target Backend Combinations table row 1** ("BruteForce or HNSW on Postgres") is removed. See the table at the top of this ADR.

**Neutral:**
- SQLite combinations are **unaffected**. All four vector backends remain valid with `database.backend = "sqlite"`. SQLite same-directory concurrency is handled separately in ADR-002 (refuse-to-start, if D1′ is ever implemented).
- Default config (`sqlite + brute_force`) is unaffected.

### Test surface

The existing test `TestValidateBackendConfig` in `internal/types/config_test.go` includes a case "postgres with connection string is valid" that implicitly relies on the default `VectorBackend = "brute_force"`. This case must be updated to specify a compatible vector backend (`pgvector` with appropriate conn string, or `qdrant` with URL) once A12 ships. New test cases must cover:
- `postgres + brute_force` → rejected with the new error.
- `postgres + hnsw` → rejected with the new error.
- `postgres + pgvector` → accepted (already covered).
- `postgres + qdrant` → accepted (new case).

### Sample config documentation

`sampleConfig` in `internal/types/config.go` should gain a comment near the `[database]` / `[vector]` sections explaining the constraint, so a user reading the generated TOML sees the rule before they hit the runtime error:

```toml
[database]
# backend = "sqlite"   # "sqlite" (default) or "postgres"
# Note: when backend = "postgres", vector.backend must be "pgvector" or "qdrant".
# File-backed vector stores (brute_force, hnsw) are not safe across multiple
# daemons sharing a Postgres project.

[vector]
# backend = "brute_force"   # "brute_force", "hnsw", "qdrant", or "pgvector"
```

### Cross-reference

- ADR-002 Amendment 3 (2026-04-09) defines Case F and the rationale for closing it via backend-combination restriction rather than in-process locking.
- ADR-002 is explicitly *not* affected by the implementation of A12: even without D1′ (single-instance refuse-to-start on SQLite), the Postgres path is made safe by this amendment alone.
