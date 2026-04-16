---
title: Troubleshooting
sidebar_position: 1
---

# Troubleshooting

Match your symptom to the relevant page. Each sub-page follows the same structure:
**Symptom → Likely causes (ranked) → Diagnostic commands → Fix.**

## Decision tree

| Symptom | Page |
|---|---|
| `shaktimand` won't start / "lock already held" / proxies appear frozen | [Daemon & leader election](/troubleshooting/daemon-and-leader) |
| Search returns no results or obviously-wrong results | [Empty or bad results](/troubleshooting/empty-or-bad-results) |
| Edits don't show up in search results — the index looks stale | [Indexing stuck](/troubleshooting/indexing-stuck) |
| Embedding % never reaches 100% / `enrichment_status` shows `open` / `disabled` | [Embedding failures](/troubleshooting/embedding-failures) |
| Queries are slow / high memory / high disk usage | [Performance problems](/troubleshooting/performance-problems) |
| Postgres or Qdrant connection errors / pgvector errors | [Backend errors](/troubleshooting/backend-errors) |

## Diagnostic basics

Three commands give you 90% of what you need before drilling in:

```bash
# 1. Is the index healthy?
shaktiman summary --root /path/to/project

# 2. Is embedding / enrichment making progress?
shaktiman enrichment-status --root /path/to/project

# 3. What's the daemon been up to?
tail -n 200 /path/to/project/.shaktiman/shaktimand.log
```

The daemon log is structured JSON by default — pipe it through `jq` for readability:

```bash
tail -f /path/to/project/.shaktiman/shaktimand.log | jq .
```

## When to reach for `reindex`

`shaktiman reindex` is always an option but rarely the right first step — it
discards your index and embeddings and rebuilds from scratch, which is slow on
large repos. Use it when:

- You've upgraded Shaktiman across parser or schema changes.
- You've changed `[embedding].model` or `dims`.
- `summary` shows obviously corrupted counts (files > 0, chunks = 0).

For everything else, the sub-pages below have targeted fixes that don't require a
rebuild.

See also: [Known Limitations](/reference/limitations) — some issues are *designed*,
not bugs (e.g. `postgres + brute_force` is rejected on purpose).
