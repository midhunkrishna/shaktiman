---
title: Indexing performance
sidebar_position: 3
---

# Indexing performance

Knobs that control how fast Shaktiman turns source files into indexed chunks
and vectors.

## The knobs

| Knob | Default | Affects | When to raise | When to lower |
|---|---|---|---|---|
| `enrichment_workers` | 4 | Parallel parse / extract throughput | CPU idle during index, many cores available | CPU-bound machine, other work competing |
| `watcher.debounce_ms` | 200 | Coalescing of rapid saves | Rapid-save workflows (editors that save on every keystroke) | Near-zero freshness requirement |
| `embedding.batch_size` | 128 | Embedding throughput | Fast Ollama (GPU), memory headroom on Ollama | CPU-bound Ollama, OOM risk |
| `embedding.timeout` | 120 s | Circuit-breaker sensitivity | Slow remote Ollama, occasional hiccups | You want to fail fast |
| `writer_channel_size` | 500 | SQLite writer backpressure | Very high write rate | Rarely — default is fine |

These are all top-level keys in the `Config` struct (`internal/types/config.go`);
most aren't yet exposed via TOML. If you need to tune them, edit
`DefaultConfig()` and rebuild for now. See the
[Config File reference](/configuration/config-file#fields-not-set-via-toml).

## Measurement recipe

### Cold-index speed

```bash
time shaktiman index /path/to/project
# Reports: Indexing: N/M files (100%) when done.
```

On a mid-size Go/TS repo with defaults, expect:

- 50k files: ~1–2 minutes
- 200k files: ~5–10 minutes
- 1M+ files: ~30 minutes to hours (embedding is the slow part)

Add `--embed` and the same run takes 2–10× longer — most of the work is
waiting on Ollama.

### Incremental speed

Save a file; check the log within a second:

```bash
tail -f .shaktiman/shaktimand.log | jq 'select(.component == "watcher" or .component == "enrichment")'
```

Expect ~50–200 ms per incremental re-index (parse → extract → write).
Embedding happens asynchronously and doesn't block the watcher.

## Trade-off patterns

### "My cold index is too slow."

Most common culprits in order:

1. **Unnecessary files being indexed.** `node_modules`, `vendor/`, build
   output. Add them to `.gitignore` or `.shaktimanignore` — the gitignore
   matcher runs before parse.
2. **Large generated files.** A bundled 5 MB JS file takes tree-sitter a
   while. Ignore it if possible.
3. **Embeddings enabled on a cold Ollama.** Disable `--embed` for the initial
   index, enable it once metadata is done.

### "Embedding is lagging behind edits."

Most common culprits:

1. **`batch_size` too small.** Each HTTP round-trip to Ollama has overhead;
   batching amortises it. Raise to 256 or 512 if your Ollama can handle it.
2. **Slow Ollama.** CPU-only Ollama is 5–10× slower than GPU. Worth moving
   Ollama to a machine with a GPU if embedding lag matters.
3. **Network latency.** Remote Ollama means you're paying RTT per batch.
   Collocate with `shaktimand` if possible.

### "My incremental re-index is jittery."

Mostly due to debounce windows colliding with large saves:

1. **Big editor auto-formats / `git checkout` / `npm install`** triggers a
   branch-switch detection (>20 files in one window). The enrichment pool
   processes in parallel but the watcher's buffer is bounded (capacity 100).
   If you see `"dropped file change event"` in the log, add noisy
   directories to `.shaktimanignore`.
2. **Periodic background embedding save** can briefly saturate disk —
   irrelevant for SSDs, visible on HDDs.

## When indexing is fast enough

If queries feel responsive, embedding % is climbing, and you don't see
`"dropped"` warnings — you're done. Don't tune further.

## See also

- [How indexing works](/guides/indexing) — the pipeline itself.
- [Embeddings](/configuration/embeddings) — embedding knobs in detail.
- [Troubleshooting → Indexing stuck](/troubleshooting/indexing-stuck) — when
  indexing isn't just slow but actually broken.
