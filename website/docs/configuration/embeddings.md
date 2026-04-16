---
title: Embeddings
sidebar_position: 4
---

# Embeddings

Shaktiman uses Ollama to generate vector embeddings for semantic search. This page
covers the knobs that actually ship, grouped by what they affect.

The authoritative schema is [`[embedding]` in Config File](/configuration/config-file#embedding).

## Provider

Today the only embedding client is Ollama (`internal/vector/ollama.go`). Point it
at any compatible Ollama-served model via:

```toml
[embedding]
ollama_url = "http://localhost:11434"
model = "nomic-embed-text"
dims = 768
```

`dims` **must match** what the model actually produces. Common combinations:

| Model | `dims` |
|---|---|
| `nomic-embed-text` | 768 |
| `mxbai-embed-large` | 1024 |
| `snowflake-arctic-embed-m` | 768 |

If `dims` and the model disagree you'll get insertion errors or silently wrong
results — there's no cross-check. See
[Known Limitations](/reference/limitations#embedding-dimensions-must-match-the-model).

## Task prefixes

Some embedding models — `nomic-embed-text` notably — are trained with task-specific
prefixes. Using them improves retrieval quality noticeably:

```toml
[embedding]
query_prefix = "search_query: "
document_prefix = "search_document: "
```

`query_prefix` is prepended to every search query before it's sent to Ollama.
`document_prefix` is prepended to every chunk before embedding. Leave both empty
for models that don't use prefixes.

## Batching and timeout

```toml
[embedding]
batch_size = 128
timeout = "120s"
```

- `batch_size` — how many chunks go into a single `/api/embed` request. Higher
  throughput on a fast GPU, more memory used on the Ollama side. Default 128 is
  safe for `nomic-embed-text` on most machines.
- `timeout` — HTTP timeout for a single batch. `120s` is generous; lower it if
  you'd rather fail fast. On failure the circuit breaker opens — see below.

## Circuit breaker

The embedding worker wraps the Ollama client in a circuit breaker:

| State | Behaviour |
|---|---|
| `closed` | Normal operation; batches flow through. |
| `open` | Embedding paused after repeated failures. Retries after a cooldown. |
| `half_open` | Probe batch in flight to see if Ollama recovered. |
| `disabled` | Permanently off — either by config (`embedding.enabled = false`) or after extended unavailability. Restart `shaktimand` to retry. |

Check the state with
[`enrichment_status`](/reference/mcp-tools/enrichment-status). When it's not
`closed`, semantic search automatically falls back to keyword ranking.

## Daemon vs CLI indexing

Shaktiman has two code paths that populate the index, and they handle
embeddings differently — worth knowing so the defaults don't surprise you.

| Path | What it is | Embedding behaviour |
|---|---|---|
| **Daemon** (`shaktimand`) | Launched automatically by your MCP client (Claude Code, Cursor, Zed, …). Owns the watcher and runs incremental re-indexes on file saves. | Honors `embedding.enabled` — **default `true`**. Starts the embedding worker on launch; new/changed chunks are embedded continuously in the background. |
| **CLI** (`shaktiman index`) | You run it by hand, e.g. in CI, scripting, or one-shot cold-indexing. | **Opt-in** — embedding is off unless you pass `--embed` (and have Ollama reachable). The `enabled` flag in the TOML is ignored here; the CLI flag wins. |

Both paths write to the same metadata store and vector store, so a CLI cold-index
without `--embed` followed by a daemon start is a valid sequence — the daemon
will embed the pending chunks on startup. Check progress with
[`enrichment_status`](/reference/mcp-tools/enrichment-status).

## Disabling embedding

If you don't have Ollama installed or want the keyword-only path (faster cold
index, no GPU required), disable embedding entirely:

```toml
[embedding]
enabled = false
```

This only affects the daemon path. For the CLI, simply don't pass `--embed`.

Keyword search, symbol lookup, dependencies, and `diff` all work without
embeddings. Only semantic ranking and the vector-based facets of `context` need
them.

## See also

- [`[embedding]` config reference](/configuration/config-file#embedding)
- [Backends → Vector](/configuration/backends)
- [Troubleshooting — Embedding failures](/troubleshooting/embedding-failures)
