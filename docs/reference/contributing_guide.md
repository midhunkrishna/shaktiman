# Contributing Guide

## Running Tests

All commands require the `sqlite_fts5` build tag. The `-race` flag enables Go's race detector for concurrency safety.

### Full test suite

```bash
go test -race -tags sqlite_fts5 ./...
```

### Single package

```bash
go test -race -tags sqlite_fts5 ./internal/storage/
go test -race -tags sqlite_fts5 ./internal/vector/
go test -race -tags sqlite_fts5 ./internal/daemon/
go test -race -tags sqlite_fts5 ./internal/parser/
go test -race -tags sqlite_fts5 ./internal/core/
go test -race -tags sqlite_fts5 ./internal/mcp/
```

### Single test

```bash
go test -race -tags sqlite_fts5 -run TestEmbedProject_LargeChunkCount ./internal/daemon/
```

### Verbose output

```bash
go test -race -tags sqlite_fts5 -v -run TestIntegration_IndexAndSearch ./internal/daemon/
```

### Integration tests

Integration tests use the `TestIntegration_` prefix and exercise the full pipeline (scan, parse, index, search) against real source files in `testdata/`. They create temporary databases and are safe to run in parallel.

```bash
go test -race -tags sqlite_fts5 -run 'TestIntegration_' ./internal/daemon/
```

### Embedding integration tests

These tests exercise the end-to-end embedding pipeline using mock Ollama servers. They cover large chunk counts, crash recovery, Ollama failure handling, and incremental re-embedding.

```bash
go test -race -tags sqlite_fts5 -run 'TestEmbedProject_' ./internal/daemon/
```

### Benchmarks

```bash
# All benchmarks
go test -tags sqlite_fts5 -run='^$' -bench=Benchmark -benchmem ./internal/storage/ ./internal/vector/

# Storage benchmarks (GetEmbedPage, MarkChunksEmbedded, CountChunksNeedingEmbedding)
go test -tags sqlite_fts5 -run='^$' -bench=Benchmark -benchmem ./internal/storage/

# Vector/embedding benchmarks (RunFromDB throughput and memory)
go test -tags sqlite_fts5 -run='^$' -bench=Benchmark -benchmem ./internal/vector/
```

### Test coverage

```bash
go test -race -tags sqlite_fts5 -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

### Build and vet

```bash
go build -tags sqlite_fts5 ./...
go vet -tags sqlite_fts5 ./...
```
