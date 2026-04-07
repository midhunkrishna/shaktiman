# Contributing Guide

## Build Tags

Backends are selected at build time via Go build tags. The default local
development build includes SQLite and both in-process vector stores:

```
sqlite_fts5 sqlite bruteforce hnsw
```

| Tag | Category | What it includes |
|-----|----------|-----------------|
| `sqlite_fts5` | required | SQLite FTS5 full-text search support |
| `sqlite` | storage | SQLite metadata backend (requires CGo + C compiler) |
| `bruteforce` | vector | In-memory brute-force vector store |
| `hnsw` | vector | HNSW approximate nearest neighbor vector store |
| `postgres` | storage | PostgreSQL metadata backend |
| `pgvector` | vector | pgvector vector store (requires `postgres`) |
| `qdrant` | vector | Qdrant vector store |

Omitting `sqlite`, `bruteforce`, and `hnsw` produces a CGo-free binary
(useful for containerized postgres-only deployments).

## Running Tests

All commands require at least the `sqlite_fts5` build tag. The `-race`
flag enables Go's race detector for concurrency safety. Include the
backend tags for whichever backends you want to exercise.

### Full test suite (default backends)

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
```

### Backend matrix

The CI runs three configurations. To reproduce locally:

```bash
# SQLite + brute_force / hnsw (default)
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...

# SQLite + Qdrant
SHAKTIMAN_TEST_VECTOR_BACKEND=qdrant \
SHAKTIMAN_TEST_QDRANT_URL=http://localhost:6333 \
  go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw qdrant" ./...

# PostgreSQL + pgvector
SHAKTIMAN_TEST_DB_BACKEND=postgres \
SHAKTIMAN_TEST_VECTOR_BACKEND=pgvector \
SHAKTIMAN_TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/testdb?sslmode=disable \
  go test -race -p 1 -tags "sqlite_fts5 sqlite bruteforce hnsw postgres pgvector" ./...
```

Tests that target a specific backend skip gracefully when that backend
is not compiled in (e.g., running with only `postgres pgvector` tags
will skip all SQLite-specific tests).

### Single package

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/storage/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/storage/sqlite/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/vector/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/daemon/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/parser/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/core/
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/mcp/
```

### Single test

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" -run TestEmbedProject_LargeChunkCount ./internal/daemon/
```

### Verbose output

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" -v -run TestIntegration_IndexAndSearch ./internal/daemon/
```

### Integration tests

Integration tests use the `TestIntegration_` prefix and exercise the full pipeline (scan, parse, index, search) against real source files in `testdata/`. They create temporary databases and are safe to run in parallel.

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" -run 'TestIntegration_' ./internal/daemon/
```

### Embedding integration tests

These tests exercise the end-to-end embedding pipeline using mock Ollama servers. They cover large chunk counts, crash recovery, Ollama failure handling, and incremental re-embedding.

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" -run 'TestEmbedProject_' ./internal/daemon/
```

### Benchmarks

```bash
# All benchmarks
go test -tags "sqlite_fts5 sqlite bruteforce hnsw" -run='^$' -bench=Benchmark -benchmem ./internal/storage/sqlite/ ./internal/vector/

# Storage benchmarks (GetEmbedPage, MarkChunksEmbedded, CountChunksNeedingEmbedding)
go test -tags "sqlite_fts5 sqlite bruteforce hnsw" -run='^$' -bench=Benchmark -benchmem ./internal/storage/sqlite/

# Vector/embedding benchmarks (RunFromDB throughput and memory)
go test -tags "sqlite_fts5 sqlite bruteforce hnsw" -run='^$' -bench=Benchmark -benchmem ./internal/vector/
```

### Test coverage

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

### Build and vet

```bash
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
go vet -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...

# Postgres-only (no CGo)
go build -tags "postgres pgvector" ./cmd/shaktimand/
```
