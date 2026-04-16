---
title: Performance problems
sidebar_position: 7
---

# Performance problems

Covers slow queries, high memory, high disk usage, and slow cold indexing.

## Symptom: queries take more than a second

### Likely causes (ranked)

1. **Cold index still in progress** — embeddings aren't populated, so ranking
   runs on the slower keyword + structural path.
2. **`search max_results` is very high** — you asked for 200 hits; the ranker
   scores 200×, assembly is proportional.
3. **`context budget_tokens` is maxed** — 32768 tokens of assembly with a large
   structural-expansion budget is genuinely more work.
4. **Very large repo + `brute_force` vector store** — brute force is `O(N)` per
   query. At ~75k chunks, queries cross ~30ms; past ~200k, they start to bite.

### Diagnostic

```bash
# Is embedding done?
shaktiman enrichment-status --root .

# Is the vector store the bottleneck?
time shaktiman search "your query" --root . --max 10     # keyword + structural
# vs.
time shaktiman search "your query" --root . --max 10 --mode full
```

### Fix

- Wait for embeddings if cold-indexing is active.
- Lower `max_results` to 10–20 for interactive use.
- Swap `brute_force` → `hnsw` if the repo is big. HNSW is `O(log N)` and
  persisted to disk.
- Use `locate` mode (the default) unless you specifically need inline source.

## Symptom: `shaktimand` memory grows steadily

### Likely causes

1. The embedding worker queue is deep (many unembedded chunks; each queued job
   holds the chunk text in memory briefly).
2. You're running `brute_force` — all vectors live in RAM by design.

### Diagnostic

```bash
ps -o rss,vsz,cmd -p $(cat /path/to/project/.shaktiman/daemon.pid)
shaktiman enrichment-status --root .
```

### Fix

- Let the queue drain (or pause edits until it does).
- For large repos, move to HNSW (disk-backed) or pgvector / qdrant (externalised).

## Symptom: disk usage is surprisingly high

### Breakdown (typical for 1M-line codebase)

- `index.db` (SQLite metadata + FTS5): ~150 MB
- `embeddings.bin` (768-dim f32 vectors, ~75k chunks): ~230 MB
- `index.db-wal` (SQLite write-ahead log, transient): up to ~20 MB
- `shaktimand.log` + rotated copies: variable

### Fix

- Set `[embedding].enabled = false` if you don't need semantic search — drops
  the 230 MB.
- Rotate older log files (the daemon does this on startup; proxies append and
  don't rotate).
- If `index.db` is much larger than expected: running
  `VACUUM` via `sqlite3 index.db "VACUUM;"` (with the daemon stopped) reclaims
  freelist space. Rare and usually unnecessary.

## Symptom: cold index is painfully slow

### Likely causes

1. Lots of files that shouldn't be indexed (`node_modules/`, `vendor/`, build
   output) aren't in `.gitignore`.
2. Very large generated files being parsed (e.g. bundled `.js`, generated
   protobuf).
3. Ollama is slow, and `--embed` was set so indexing waits on embedding.

### Diagnostic

```bash
# See what got indexed
shaktiman status --root .   # per-language file count — surprising?

# Time a sub-tree on its own
time shaktiman index /path/to/small-subtree
```

### Fix

- Add noise directories to `.gitignore` or `.shaktimanignore`.
- Index without `--embed` first (keyword-only runs much faster). Add `--embed`
  later once the metadata index is ready.
- If Ollama is the bottleneck, separate the runs: `shaktiman index .` then
  `shaktiman index . --embed` in the background.

## See also

- [Configuration → Backends](/configuration/backends) — backend trade-offs.
- [Configuration → Embeddings](/configuration/embeddings) — batch size, timeout.
- [Performance — Overview](/performance/overview) — the dedicated section (when
  it lands).
