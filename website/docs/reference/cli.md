---
title: CLI Reference
sidebar_position: 1
---

# CLI Reference

Shaktiman ships two binaries:

- **`shaktiman`** — direct CLI over the index. Every MCP tool is also available as a
  CLI subcommand that reads the SQLite index without involving the daemon.
- **`shaktimand`** — the MCP stdio server. Launched by your MCP client
  (e.g. Claude Code); you rarely invoke it by hand.

## Persistent flags (`shaktiman`)

```
--format <json|text>   Output format (default: json)
```

Every subcommand honors `--format`. Set `--format text` for human-readable output
(matches the format the MCP server returns); leave the default `json` when piping to
`jq` or other tools.

## Management commands

### `shaktiman init <project-root>`

Initialize a `.shaktiman/` config directory. Writes a sample `shaktiman.toml` if one
doesn't already exist; leaves existing config untouched.

```
shaktiman init /path/to/project
```

### `shaktiman index <project-root>`

Cold-index a project directory. Refuses to run if a `shaktimand` daemon is already
holding the project lock (the daemon handles indexing automatically).

| Flag | Default | Purpose |
|---|---|---|
| `--embed` | `false` | Also generate embeddings after indexing (requires Ollama). |
| `--vector <backend>` | from config | Vector backend: `brute_force`, `hnsw`, `qdrant`, `pgvector`. |
| `--db <backend>` | from config | Metadata backend: `sqlite`, `postgres`. |
| `--postgres-url <url>` | from config / env | Override the Postgres connection string. |
| `--qdrant-url <url>` | from config | Override the Qdrant URL. |

Progress is emitted on stdout (`Indexing: N/M files (X%)` / `Embedding: N/M chunks
(X%)`). On a TTY the lines redraw in place; on a pipe they're emitted every 10 %.

Ctrl-C during embedding flushes progress to disk; the next run resumes from where it
left off.

### `shaktiman reindex <project-root>`

Purge all indexed data and reindex from scratch. Keeps configuration. Destructive —
prompts for confirmation on TTYs.

All flags from `index`, plus:

| Flag | Default | Purpose |
|---|---|---|
| `--force` | `false` | Skip the confirmation prompt (required on non-interactive terminals). |

### `shaktiman status <project-root>`

Print a minimal index summary (file / chunk / symbol counts + parse errors +
per-language file counts). Reads the index directly; no daemon required.

## Query commands

Each query command wraps the MCP tool of the same name. Output is JSON by default;
use `--format text` for a human-readable rendering.

### `shaktiman search <query>`

See [`search` MCP tool](/reference/mcp-tools/search) for result semantics.

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root to read the index from. |
| `--max <int>` | from `[search].max_results` | Maximum results. |
| `--mode <locate\|full>` | from `[search].default_mode` | Result mode. |
| `--min-score <float>` | from `[search].min_score` | Minimum relevance score. |
| `--explain` | `false` | Include per-signal score breakdown (text format only). |
| `--path <prefix>` | — | Filter results to a file or directory prefix. |
| `--scope <impl\|test\|all>` | `impl` | Result scope. |

### `shaktiman context <query>`

See [`context` MCP tool](/reference/mcp-tools/context).

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |
| `--budget <int>` | from `[context].budget_tokens` | Token budget (256–32768). |
| `--scope <impl\|test\|all>` | `impl` | Result scope. |

### `shaktiman symbols <name>`

See [`symbols` MCP tool](/reference/mcp-tools/symbols).

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |
| `--kind <string>` | — | Filter by symbol kind (`function`, `class`, `method`, `type`, `interface`, `variable`). |
| `--scope <impl\|test\|all>` | `impl` | Result scope. |

### `shaktiman deps <symbol>`

See [`dependencies` MCP tool](/reference/mcp-tools/dependencies). Note the CLI name is
`deps`, not `dependencies`.

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |
| `--direction <callers\|callees\|both>` | `both` | Traversal direction. |
| `--depth <int>` | `2` | BFS depth (1–5). |
| `--scope <impl\|test\|all>` | `impl` | Result scope. |

### `shaktiman diff`

See [`diff` MCP tool](/reference/mcp-tools/diff).

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |
| `--since <duration>` | `24h` | Time window. Go duration syntax — no `"d"` or `"w"` units. Capped at `720h` internally. |
| `--limit <int>` | `50` | Maximum diffs. Out-of-range values (`<1` or `>500`) silently reset to the default. |
| `--scope <impl\|test\|all>` | `impl` | Result scope. |

### `shaktiman enrichment-status`

See [`enrichment_status` MCP tool](/reference/mcp-tools/enrichment-status).

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |

### `shaktiman summary`

See [`summary` MCP tool](/reference/mcp-tools/summary).

| Flag | Default | Purpose |
|---|---|---|
| `--root <path>` | `.` | Project root. |

## `shaktimand` (MCP daemon)

```
shaktimand <project-root>
```

Takes a single positional argument: the project root. The daemon:

1. Canonicalizes the project root path (`symlinks + relative → absolute`) so two
   daemons can't race by addressing the same directory differently.
2. Creates `.shaktiman/` if missing and opens `shaktimand.log` for structured logs
   (stdout is reserved for the MCP protocol, stderr for startup errors only).
3. Acquires a `flock` on `.shaktiman/daemon.pid`. If the lock is taken, enters
   **proxy mode** — forwarding MCP traffic to the leader via
   `/tmp/shaktiman-<hash>.sock` — and never rotates the shared log.
4. If it got the lock, loads the TOML config, validates the backend combination
   (see [Backends](/configuration/backends)), opens the socket for future proxies,
   and serves MCP over stdio.

`shaktimand` is not normally run by hand. Wire it via your MCP client's
configuration (for Claude Code, see [Claude Code Setup](/getting-started/claude-code-setup)).

## Source of truth

- `cmd/shaktiman/main.go` — `init`, `index`, `reindex`, `status`.
- `cmd/shaktiman/query.go` — `search`, `context`, `symbols`, `deps`, `diff`,
  `enrichment-status`, `summary`.
- `cmd/shaktimand/main.go` — daemon lifecycle and leader/proxy dispatch.
