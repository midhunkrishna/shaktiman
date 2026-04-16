---
title: How indexing works
sidebar_position: 1
---

# How indexing works

When Shaktiman indexes a project, it parses every supported source file, splits each
one into semantic chunks, extracts the symbols and call edges, and — if embeddings
are enabled — sends the chunks to Ollama in batches to get vectors back. Everything
ends up in `.shaktiman/index.db` (SQLite) and `.shaktiman/embeddings.bin` (when the
default `brute_force` vector backend is used).

This page covers the behaviour you'll actually notice: cold vs. incremental indexing,
the watcher, branch-switch detection, and when to force a rebuild.

## The pipeline

For each source file the indexer runs:

1. **Parse** — tree-sitter produces an AST. Parse errors don't abort; partial ASTs
   are allowed through.
2. **Chunk** — the parser recursively walks the AST, emitting chunks at the nodes
   listed in each language's `ChunkableTypes` (see
   [Supported Languages](/reference/supported-languages) and
   [ADR-004](/design/adr-004-recursive-chunking)).
3. **Extract symbols** — one row per function / class / method / type.
4. **Extract edges** — `imports`, `calls`, `type_ref`, `extends`, `implements`,
   `exports`.
5. **Write** — a single SQLite writer serialises everything into `index.db`,
   updating the FTS5 index for keyword search as it goes.
6. **Embed (optional)** — if `embedding.enabled` is `true` and Ollama is reachable,
   the embedding worker drains chunks from the queue in batches (`embed_batch_size`,
   default 128) and writes vectors to the configured vector store.

Steps 1–5 are fast — for a mid-size Go/TypeScript repo the full cold index completes
in a minute or two. Step 6 runs asynchronously and can take much longer on a large
repo, but the index is **queryable in keyword-fallback mode as soon as step 5
completes**. You don't have to wait for embedding.

## Cold index vs. incremental

| Mode | When | How to trigger |
|---|---|---|
| **Cold** | First run on a project, after `reindex`, or after clearing `.shaktiman/` | `shaktiman index .` or start `shaktimand` (indexes on launch) |
| **Incremental** | Every save of a watched file while `shaktimand` is running | Automatic via the file watcher |
| **Manual** | CI, scripting, running outside of Claude Code | `shaktiman index .` (refuses if a daemon is already running — it handles indexing itself) |

## The file watcher

When `shaktimand` is the leader for a project, it starts an
[fsnotify](https://github.com/fsnotify/fsnotify)-backed watcher over the project
root. The watcher:

- **Watches directories, not individual files** — conserves file descriptors on
  large repos.
- **Debounces events by `watcher.debounce_ms`** (default 200 ms). Rapid saves of the
  same file collapse into one re-index.
- **Respects `.gitignore`** via the same glob logic the indexer uses. It also
  honours `.shaktimanignore` for Shaktiman-specific exclusions (e.g. generated
  protobuf files you want off the index without adding them to `.gitignore`).

Implementation: `internal/daemon/watcher.go`.

## Branch-switch detection

If more than ~20 file changes arrive in a single debounce window, the watcher
classifies it as a branch switch (common cause: `git checkout`, `git pull`, or a
large refactor landing). It sends a one-shot signal on `branchSwitchCh` so the
enrichment pipeline can treat the batch as a single coherent event rather than a
cascade of per-file re-indexes.

## When to re-index explicitly

The incremental watcher handles edits automatically. You only need to force a
rebuild when:

- You've **upgraded Shaktiman** and want new parser fixes / schema to apply across
  the whole corpus. `shaktiman reindex .` is the right call.
- The index appears **corrupted** (rare — `enrichment_status` shows impossible
  counts, or queries return gibberish). `reindex` fixes this by purging and
  rebuilding.
- You've **changed your embedding model or dimensions**. Existing vectors are
  incompatible with the new model; `reindex` regenerates them.

See [Re-indexing](/guides/reindexing) for the flow and what's preserved.

## Progress and Ctrl-C

The indexer streams progress to stdout (`Indexing: N/M files`, `Embedding: N/M
chunks`). Progress redraws in place on a TTY and batches per 10 % when output is
piped.

Ctrl-C during embedding is **safe** — the embedding worker periodically flushes
vectors to disk (a background goroutine calls `RunPeriodicEmbeddingSave`). On the
next run only unembedded chunks are processed.

Ctrl-C during **metadata indexing** also works — the SQLite transaction model means
a partial index is internally consistent, and the next run will pick up unchanged
files quickly (content-hash check skips re-parsing).

## See also

- [`shaktiman index`](/reference/cli#shaktiman-index-project-root) — CLI flags.
- [`shaktiman enrichment-status`](/reference/mcp-tools/enrichment-status) — progress
  & embedding state.
- [Multi-instance concurrency](/guides/multi-instance) — why only one process
  indexes at a time.
- [Embeddings](/configuration/embeddings) — tuning the batch size, timeout, and
  prefixes.
