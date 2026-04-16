---
title: Config File
sidebar_position: 1
---

# `.shaktiman/shaktiman.toml`

Shaktiman reads configuration from `.shaktiman/shaktiman.toml` in your project root.
All fields are optional — defaults (listed below) apply when a key is missing or the
file is absent.

**Precedence** (highest wins):

1. CLI flags (e.g. `--vector hnsw`)
2. Environment variables (`SHAKTIMAN_POSTGRES_URL`, `SHAKTIMAN_QDRANT_API_KEY`)
3. `.shaktiman/shaktiman.toml`
4. Built-in defaults

If the file doesn't exist, `shaktimand` writes a sample one on startup (commented out).
You can also generate it explicitly with `shaktiman init <project-root>`.

## Sections

### `[database]`

| Field | Default | Allowed | Notes |
|---|---|---|---|
| `backend` | `"sqlite"` | `"sqlite"`, `"postgres"` | Metadata backend. `postgres` requires the `postgres` build tag. |

### `[postgres]`

Used only when `[database].backend = "postgres"`.

| Field | Default | Validation | Notes |
|---|---|---|---|
| `connection_string` | — | required with postgres | Override via env `SHAKTIMAN_POSTGRES_URL`. |
| `max_open_conns` | `20` | `>= 1` | pgx pool max open connections. |
| `max_idle_conns` | `10` | `>= 0` | pgx pool max idle connections. |
| `schema` | `"public"` | — | Postgres schema name. |

### `[search]`

Defaults for the `search` MCP tool (and `shaktiman search` CLI).

| Field | Default | Validation | Notes |
|---|---|---|---|
| `max_results` | `10` | `1–200` | Also used as `DefaultMaxResults`. |
| `default_mode` | `"locate"` | `"locate"` or `"full"` | Clients may override per-call. |
| `min_score` | `0.15` | `0.0–1.0` | Relevance score floor; raise to reduce noise. |

### `[context]`

Defaults for the `context` MCP tool.

| Field | Default | Validation | Notes |
|---|---|---|---|
| `enabled` | `true` | bool | Set `false` to hide the `context` tool entirely. |
| `budget_tokens` | `4096` | `256–32768` | Default assembly budget. Also bounds `MaxBudgetTokens`. |

### `[vector]`

| Field | Default | Allowed | Notes |
|---|---|---|---|
| `backend` | `"brute_force"` | `"brute_force"`, `"hnsw"`, `"qdrant"`, `"pgvector"` | `qdrant` needs the `qdrant` build tag; `pgvector` needs the `pgvector` tag **and** postgres metadata. |

### `[qdrant]`

Used only when `[vector].backend = "qdrant"`.

| Field | Default | Notes |
|---|---|---|
| `url` | — | Required with qdrant. Example: `http://localhost:6334`. |
| `collection` | `"shaktiman"` | Collection name. |
| `api_key` | — | Override via env `SHAKTIMAN_QDRANT_API_KEY` (recommended). |

### `[embedding]`

| Field | Default | Validation | Notes |
|---|---|---|---|
| `ollama_url` | `http://localhost:11434` | — | Base URL for the Ollama HTTP API. |
| `model` | `"nomic-embed-text"` | — | Must match the model Ollama can serve. |
| `dims` | `768` | `1–4096` | Must match the model's embedding dimensionality. |
| `batch_size` | `128` | `>= 1` | Texts per `/api/embed` request. |
| `timeout` | `"120s"` | Go duration | HTTP timeout per batch. |
| `query_prefix` | `""` | — | Prepended to queries before embedding. Use `"search_query: "` with nomic-embed-text. |
| `document_prefix` | `""` | — | Prepended to chunks before embedding. Use `"search_document: "` with nomic-embed-text. |

### `[test]`

| Field | Default | Notes |
|---|---|---|
| `patterns` | per-language (see below) | Glob patterns (`*_test.go`) and directory prefixes (`testdata/`). Auto-populated after first index if absent. |

**Auto-populated language defaults** (merged across indexed languages):

| Language | Patterns |
|---|---|
| Go | `*_test.go`, `testdata/` |
| Python | `test_*.py`, `*_test.py` |
| TypeScript | `*.test.ts`, `*.spec.ts`, `*.test.tsx`, `*.spec.tsx`, `__tests__/` |
| JavaScript | `*.test.js`, `*.spec.js`, `*.test.jsx`, `*.spec.jsx`, `*.test.mjs`, `*.spec.mjs`, `__tests__/` |
| Java | `*Test.java`, `*Tests.java`, `src/test/` |
| Rust | `tests/` |
| Bash | `test_*.sh`, `*_test.sh` |
| Ruby | `*_test.rb`, `test_*.rb`, `*_spec.rb`, `spec/`, `test/` |

Patterns ending in `/` match directory prefixes; everything else is a basename glob.

## Fields not set via TOML

These ship from `DefaultConfig` and today are not tunable via `shaktiman.toml` (they're
either derived or intentionally fixed):

| Field | Default | What it's for |
|---|---|---|
| `DBPath` | `<root>/.shaktiman/index.db` | Metadata DB path (SQLite). |
| `EmbeddingsPath` | `<root>/.shaktiman/embeddings.bin` | Persisted vectors for local backends. |
| `MaxBudgetTokens` | `4096` | Bounded by `[context].budget_tokens` when set. |
| `DefaultMaxResults` | `10` | Overridden by `[search].max_results`. |
| `WriterChannelSize` | `500` | SQLite writer queue depth. |
| `EnrichmentWorkers` | `4` | Parallel parser workers. |
| `Tokenizer` | `cl100k_base` | Tokenizer for budget accounting. |
| `WatcherEnabled` | `true` | File-save re-index. |
| `WatcherDebounceMs` | `200` | Coalesce watcher events. |
| `EmbedEnabled` | `true` | Run the embedding worker (requires Ollama reachable). |

If you need to change these, the source of truth is
`internal/types/config.go:DefaultConfig` — add a TOML key there if a use case emerges.

## Environment variables

| Variable | Overrides |
|---|---|
| `SHAKTIMAN_POSTGRES_URL` | `[postgres].connection_string` |
| `SHAKTIMAN_QDRANT_API_KEY` | `[qdrant].api_key` |

Secrets belong in env, not in TOML.

## Backend-combination rules

`ValidateBackendConfig` runs after the config is fully merged and rejects unsupported
combinations:

- `[vector].backend = "pgvector"` → `[database].backend` must be `"postgres"`.
- `[database].backend = "postgres"` → `[postgres].connection_string` (or
  `SHAKTIMAN_POSTGRES_URL`) must be set.
- `[vector].backend = "qdrant"` → `[qdrant].url` must be set.
- `[database].backend = "postgres"` **forbids** `[vector].backend =
  "brute_force"` or `"hnsw"` — file-backed stores race on `embeddings.bin` when two
  daemons share the same Postgres database (ADR-003 A12).

## Source of truth

- `internal/types/config.go` — `Config` struct, `DefaultConfig`,
  `LoadConfigFromFile`, `ValidateBackendConfig`, `WriteSampleConfig`.
- `internal/types/config.go` — `langTestPatterns` for auto-populated test globs.
