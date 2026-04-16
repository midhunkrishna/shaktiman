---
title: Re-indexing
sidebar_position: 4
---

# Re-indexing

`shaktiman reindex` **purges every indexed artifact** for a project and rebuilds
from scratch. You rarely need it — the file watcher handles ordinary edits
incrementally. This page covers the handful of cases where you do, and what
happens under the hood.

## When to reindex

| Trigger | Why |
|---|---|
| Shaktiman upgrade with parser / schema changes | New parser fixes or schema migrations should apply across the whole corpus, not just edited files. |
| You changed `[embedding].model` or `[embedding].dims` | Existing vectors are incompatible with a new model; `reindex` regenerates them. |
| `.shaktiman/` looks corrupted | Impossible counts from `enrichment_status`, queries returning gibberish, SQLite errors in the log. |
| You want to switch vector backends | E.g. `brute_force` → `hnsw` — the new store starts empty and needs repopulating. |

**You don't need to reindex** after routine code edits — incremental indexing covers
those. You also don't need to reindex after upgrading Ollama unless the model
itself changed.

## How to run it

```bash
shaktiman reindex /path/to/project
```

On a TTY you'll be prompted to confirm:

```
This will delete ALL indexed data and reindex from scratch. Continue? [y/N]
```

In CI or any non-interactive context, pass `--force` to skip the prompt. Without
`--force`, `reindex` aborts on non-TTYs rather than hanging on stdin.

Optional flags (same as `index`):

| Flag | Purpose |
|---|---|
| `--embed` | Also generate embeddings (requires Ollama). |
| `--vector <backend>` | Switch to a different vector backend for the rebuild. |
| `--db <backend>` | Switch metadata backend. |
| `--postgres-url <url>` | Override the Postgres connection string. |
| `--qdrant-url <url>` | Override the Qdrant URL. |

## What gets purged vs. preserved

| Purged (rebuilt from scratch) | Preserved |
|---|---|
| `.shaktiman/index.db` (all metadata, chunks, symbols, edges, FTS5) | `.shaktiman/shaktiman.toml` |
| `.shaktiman/embeddings.bin` (local vectors) | `.shaktiman/.shaktimanignore` (if present) |
| Qdrant collection contents (when configured) | Your source files (obviously) |
| pgvector table rows (when configured) | |
| diff history | |
| Session / access history | |

Configuration is preserved on purpose — reindexing shouldn't require you to
reconfigure the project.

## Daemon must be stopped first

`reindex` refuses to run while `shaktimand` is holding the project lock. If the
daemon is running:

```
Error: a shaktimand daemon is running for this project; stop it before running reindex
```

Stop the daemon first (close the Claude Code window, or `kill` the `shaktimand`
process), then run `reindex`.

This matches `index`'s behaviour — both are writer operations, and having the
daemon's writer and the CLI's writer touch the same database would race.

## Two-phase purge

Under the hood `reindex` runs in phases:

1. **Open server-based backends and purge them.** For Postgres / Qdrant /
   pgvector, this issues explicit delete statements scoped to the current
   project. `EnsureProject` is used to resolve the project ID without creating
   spurious rows.
2. **Delete local files.** `.shaktiman/index.db`, `.shaktiman/embeddings.bin`,
   and the HNSW on-disk index are unlinked.
3. **Rebuild.** A fresh daemon is opened and `IndexProject` runs as normal,
   followed by `EmbedProject` when `--embed` is set.

Crucially, phase 1 runs *before* phase 2, so if your Postgres credentials are
wrong or Qdrant is unreachable, you'll fail out with local files intact — you
haven't dropped the useful part of the index before hitting the broken dependency.

Implementation: `cmd/shaktiman/main.go` → `reindexCmd`.

## Ctrl-C during reindex

Safe at any point. SQLite transactions won't commit unless they complete. The
embedding worker flushes progress to disk periodically; on the next run only the
un-embedded chunks are processed.

If you Ctrl-C between phase 1 (backend purge) and phase 2 (local file purge), your
server-side state is empty but local files still exist. The next `reindex` will
succeed; a plain `index` will not (the local metadata is now stale relative to the
empty remote vector store — easiest to re-run `reindex`).

## See also

- [`shaktiman reindex`](/reference/cli#shaktiman-reindex-project-root) — CLI flags.
- [How indexing works](/guides/indexing) — for the incremental path.
- [Backends](/configuration/backends) — which backend you're rebuilding into.
