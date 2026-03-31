# Shaktiman

[![codecov](https://codecov.io/gh/midhunkrishna/shaktiman/branch/master/graph/badge.svg?token=BZ7NUTRX30)](https://codecov.io/gh/midhunkrishna/shaktiman)

Local-first code context engine for coding agents.

Shaktiman indexes your codebase and gives Claude Code (or any MCP client) tools to search, navigate, and assemble exactly the right code context — fitted to a token budget so you use fewer tokens and get better results.

- **Indexes code** using tree-sitter: functions, classes, symbols, imports, and call graphs
- **Hybrid search** combining keyword (FTS5), semantic (vector), structural, and change signals
- **Budget-fitted context** — asks for 4K tokens, gets exactly 4K tokens of the most relevant code
- **Live updates** — file watcher re-indexes on save, no manual reindexing needed

## Quick Start

```bash
# 1. Build
go build -tags sqlite_fts5 -o shaktiman ./cmd/shaktiman
go build -tags sqlite_fts5 -o shaktimand ./cmd/shaktimand

# 2. Add to Claude Code (see "Usage with Claude Code" below)

# 3. Start coding — Shaktiman tools appear automatically in Claude Code
```

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Go 1.25+** | CGo must be enabled (it is by default) |
| **C compiler** | Required by SQLite and tree-sitter (gcc/clang, included on macOS) |
| **Ollama** (optional) | Only needed for semantic/vector search. Without it, keyword search still works. |

## Installation

```bash
git clone https://github.com/shaktimanai/shaktiman.git
cd shaktiman

# Build both binaries (sqlite_fts5 tag is required)
go build -tags sqlite_fts5 -o shaktiman ./cmd/shaktiman
go build -tags sqlite_fts5 -o shaktimand ./cmd/shaktimand

# Verify
./shaktiman --help
```

### Optional: Ollama for Semantic Search

Shaktiman works without Ollama (keyword search only). To enable semantic search:

```bash
# Install Ollama (https://ollama.com)
ollama pull nomic-embed-text
```

Shaktiman connects to `http://localhost:11434` by default. If Ollama is unavailable, the system gracefully falls back to keyword search.

## Initializing a New Project

No manual setup is needed. Shaktiman initializes automatically on first run.

**With Claude Code (recommended):** Just add the MCP config (see next section). When Claude Code starts the daemon, it will:
1. Create `.shaktiman/` in your project root (SQLite database + embeddings)
2. Scan and index all supported source files
3. Start watching for changes

**With the CLI:**

```bash
# Option 1: Initialize config first, then index
./shaktiman init /path/to/your/project    # creates .shaktiman/shaktiman.toml
# Edit .shaktiman/shaktiman.toml to set vector.backend = "hnsw" etc.
./shaktiman index /path/to/your/project --embed

# Option 2: Index directly (uses defaults, or reads existing .shaktiman/shaktiman.toml)
./shaktiman index /path/to/your/project

# Option 3: Override vector backend at index time
./shaktiman index /path/to/your/project --embed --vector hnsw
```

This creates `.shaktiman/index.db` and indexes all source files. With `--embed`, it also generates vector embeddings for semantic search. Run it again at any time to re-index.

The `--vector` flag selects the vector store backend (`brute_force` or `hnsw`). Config resolution order: default → TOML → `--vector` flag.

Ctrl+C during embedding saves progress to disk. On the next run, only unembedded chunks are processed.

**What gets created:**

```
your-project/
  .shaktiman/
    shaktiman.toml     # Configuration (created by init or auto-generated)
    index.db           # SQLite database (symbols, chunks, FTS index, dependency graph)
    embeddings.bin     # Vector embeddings (only if Ollama is running)
```

Add `.shaktiman/` to your `.gitignore`.

**Excluding files:** Shaktiman respects `.gitignore`. For additional exclusions, create a `.shaktimanignore` file in your project root using the same pattern syntax:

```
# .shaktimanignore
generated/
*.pb.go
*_mock.go
```

## Usage with Claude Code

Create a `.mcp.json` file in the root of your project, and add:

```json
{
  "mcpServers": {
    "shaktiman": {
      "command": "/absolute/path/to/shaktimand",
      "args": ["/absolute/path/to/your/project"],
      "env": {
        "SHAKTIMAN_LOG_LEVEL": "DEBUG"
      }
    }
  }
}
```

We also need to tell Claude to use the mcp server. Add this to the projects `CLAUDE.md` file. A full template is available at [`docs/reference/sample_claude.md`](docs/reference/sample_claude.md).

```
## MCP Tools (via shaktimand)

Shaktiman is a pre-built code index that reduces context usage during exploration.
Use it to narrow down before reading files — not as a replacement for Grep or Glob.

### When to use shaktiman vs built-in tools

| Task | Tool | Why |
|---|---|---|
| Orient in unfamiliar codebase | `mcp__shaktiman__summary` | Codebase snapshot without reading files |
| Find code related to a concept | `mcp__shaktiman__search` | Ranked discovery — read only the top hits |
| Understand a topic across files | `mcp__shaktiman__context` | Token-budgeted chunks instead of reading many files |
| Find where a symbol is defined | `mcp__shaktiman__symbols` | Definition + signature without reading the file |
| Trace callers/callees | `mcp__shaktiman__dependencies` | Full call chain in one call |
| See what changed recently | `mcp__shaktiman__diff` | Symbol-level change tracking |
| Search for test code specifically | Any tool with `scope:"test"` | All tools exclude test files by default |
| Find exact string or regex | Grep | Shaktiman ranks by relevance, not pattern match |
| Find files by name/extension | Glob | Shaktiman indexes content, not filenames |
| Read a specific known file | Read | Direct file access |

### Signs you should use shaktiman instead

| You notice... | Use | Why |
|---|---|---|
| First time in a codebase, no context yet | `summary` | File count, languages, symbol count at a glance |
| Query is conceptual, not a literal pattern | `search` | Top-N ranked by relevance, not every literal match |
| Grep returned many matches, need to pick which to read | `search` with the same terms | Ranked results — read 3 files, not 30 |
| Need a function/type definition | `symbols name:"Name"` | File, line, signature without reading the file |
| Need callers, callees, or blast radius | `dependencies symbol:"Name"` | Structural call graph. Grep misses indirect calls. |
| Polyglot repo, Grep patterns differ by language | `symbols` or `dependencies` | Language-agnostic — same query regardless of syntax |

### Discovery workflow

1. `mcp__shaktiman__summary` → orient (size, languages, health)
2. `mcp__shaktiman__search` → narrow to relevant files (~12 tokens/result)
3. `Read` → read only the files that matter
4. `Edit` → make changes

### Subagent delegation

Subagents don't inherit these instructions. When spawning subagents that explore code, include:

> Use `mcp__shaktiman__search`, `mcp__shaktiman__symbols`, and `mcp__shaktiman__dependencies`
> for code discovery before reading files. All tools exclude test files by default —
> use `scope:"test"` when looking for test code. Use Grep for exact string/regex matching.
> Use Glob for finding files by name. Use Read for known file paths.
```

That's it. Claude Code will now have access to these tools:

### MCP Tools

| Tool | What it does | Key params |
|------|-------------|------------|
| `search` | Find code by keyword or natural language query | `query`, `mode` (locate/full), `max_results` (1-200), `min_score` (0.0-1.0) |
| `context` | Assemble ranked code context fitted to a token budget | `query`, `budget_tokens` (256-32768) |
| `symbols` | Look up functions, classes, types by name | `name`, `kind` (function/class/method/type) |
| `dependencies` | Show callers and callees of a symbol | `symbol`, `direction` (callers/callees/both), `depth` (1-5) |
| `diff` | Show recent file changes and affected symbols | `since` (e.g. "24h"), `limit` |
| `enrichment_status` | Check indexing and embedding progress | (none) |
| `summary` | Show workspace overview (files, languages, symbols, index health) | (none) |

### Example Prompts

You don't need to call tools directly. Just describe what you need:

> "Find all functions that handle authentication"

Claude Code uses the `search` tool to find relevant auth code.

> "Give me context for refactoring the payment flow"

Claude Code uses `context` with a token budget to assemble exactly the right amount of code.

> "What calls the `processOrder` function?"

Claude Code uses `dependencies` to trace the call graph.

> "What changed in the last 2 hours?"

Claude Code uses `diff` to show recent changes with affected symbols.

### How This Reduces Token Usage

Without Shaktiman, Claude Code reads entire files to build context. With Shaktiman:

1. **Locate-first search** — the default `locate` mode returns compact file pointers (path, line range, symbol, score) without source code. Claude reads only the files it needs via native tools. ~97% fewer tokens than returning full source.
2. **Budget-fitted assembly** — the `context` tool returns ranked code chunks that fit exactly within a token budget (default 4,096 tokens). No wasted tokens on irrelevant code.
3. **Chunk-level granularity** — returns individual functions and classes, not entire files.
4. **Score floor** — results below the minimum relevance threshold (default 0.15) are dropped, not returned.
5. **Deduplication** — overlapping code chunks are merged automatically.

## CLI Usage

All MCP tools are also available as CLI subcommands, reading the SQLite index directly without the MCP daemon.

### Output Format

All query commands support `--format` to control output:

- `--format json` (default) — pretty-printed JSON, suitable for piping to `jq` or other tools
- `--format text` — human-readable plain text, same format used by the MCP server

The `--format` flag is persistent and applies to all subcommands.

### Commands

| Command | What it does | Key flags |
|---------|-------------|-----------|
| `init <root>` | Initialize `.shaktiman/` config directory | (none) |
| `index <root>` | Index a project directory | `--embed` (generate embeddings), `--vector` (brute_force/hnsw) |
| `status <root>` | Show index status | (none) |
| `search <query>` | Search indexed code by keyword | `--root`, `--max`, `--mode` (locate/full), `--min-score`, `--explain` |
| `context <query>` | Assemble ranked code context fitted to a token budget | `--root`, `--budget` (256-32768) |
| `symbols <name>` | Look up functions, classes, types by name | `--root`, `--kind` |
| `deps <symbol>` | Show callers/callees of a symbol | `--root`, `--direction`, `--depth` (1-5) |
| `diff` | Show recent file changes and affected symbols | `--root`, `--since`, `--limit` |
| `enrichment-status` | Check indexing and embedding progress | `--root` |

### Examples

```bash
# Initialize config (optional — edit .shaktiman/shaktiman.toml before indexing)
./shaktiman init /path/to/project

# Index a project
./shaktiman index /path/to/project

# Index with embeddings (requires Ollama)
./shaktiman index --embed /path/to/project

# Index with HNSW vector backend
./shaktiman index --embed --vector hnsw /path/to/project

# Check index status
./shaktiman status /path/to/project

# Search for code (JSON output, default)
./shaktiman search "authentication middleware" --root /path/to/project --max 10

# Search with human-readable text output (MCP-style locate pointers)
./shaktiman search "authentication middleware" --root /path/to/project --format text

# Search with full source code in text format
./shaktiman search "authentication middleware" --root /path/to/project --format text --mode full

# Search with score breakdown
./shaktiman search "authentication middleware" --root /path/to/project --format text --explain

# Assemble context for a task (budget-fitted)
./shaktiman context "payment processing flow" --root /path/to/project --budget 4096

# Context in text format
./shaktiman context "payment processing flow" --root /path/to/project --format text

# Look up a symbol
./shaktiman symbols NewServer --root /path/to/project --kind function

# Trace callers of a function
./shaktiman deps processOrder --root /path/to/project --direction callers --depth 3

# See what changed in the last 2 hours
./shaktiman diff --root /path/to/project --since 2h

# Check embedding progress
./shaktiman enrichment-status --root /path/to/project
```

## Supported Languages

| Language | Extensions | Parser |
|----------|-----------|--------|
| TypeScript | `.ts`, `.tsx` | tree-sitter-typescript |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` | tree-sitter-javascript |
| Python | `.py` | tree-sitter-python |
| Go | `.go` | tree-sitter-go |
| Rust | `.rs` | tree-sitter-rust |
| Java | `.java` | tree-sitter-java |
| Groovy | `.groovy`, `.gradle` | tree-sitter-groovy |
| Shell | `.sh`, `.bash` | tree-sitter-bash |

Adding a new language: implement a `LanguageConfig` in `internal/parser/languages.go` with the AST node type mappings for your language.

## Architecture

```
Source Files
     |
     v
  Parser (tree-sitter)  -->  Chunks, Symbols, Edges
     |
     v
  SQLite (WAL + FTS5)   -->  Keyword index, dependency graph, change tracking
  Vector Store           -->  Semantic embeddings (optional, via Ollama)
     |
     v
  Query Engine           -->  Hybrid ranking, budget-fitted assembly
     |
     v
  MCP Server (stdio)     -->  Claude Code / any MCP client
```

**Retrieval levels** (automatic fallback):
1. **Hybrid** — semantic + keyword + structural + change + session signals (when embeddings are ready)
2. **Keyword** — FTS5 full-text search + structural ranking (default, no Ollama needed)
3. **Filesystem** — raw file reading (when index is empty, e.g. first run)

See `docs/` for detailed architecture documents.

## Configuration

All configuration uses sensible defaults. Optionally create `.shaktiman/shaktiman.toml` to override defaults. A sample config is auto-created on first daemon startup.

```toml
# .shaktiman/shaktiman.toml

[search]
# max_results = 10        # Max results per search (1-200)
# default_mode = "locate"  # "locate" (headers only) or "full" (with source code)
# min_score = 0.15         # Drop results below this relevance score (0.0-1.0)

[context]
# enabled = true           # Set false to disable the context tool entirely
# budget_tokens = 4096     # Default token budget for context assembly (256-32768)

[vector]
# backend = "brute_force"  # "brute_force" (default) or "hnsw"
```

### All Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `search.max_results` | 10 | Max results per search |
| `search.default_mode` | `locate` | `locate` (compact pointers) or `full` (with source) |
| `search.min_score` | 0.15 | Minimum relevance score threshold |
| `context.enabled` | true | Whether to register the context MCP tool |
| `context.budget_tokens` | 4,096 | Default token budget for context assembly |
| `vector.backend` | `brute_force` | Vector store backend: `brute_force` (in-memory, O(n)) or `hnsw` (disk-backed, O(log n) via hnswlib) |
| DB path | `.shaktiman/index.db` | SQLite database location |
| Watcher | enabled | Auto-reindex on file save |
| Watcher debounce | 200ms | Debounce window for file events |
| Ollama URL | `http://localhost:11434` | Embedding service endpoint |
| Embedding model | `nomic-embed-text` | Ollama model for embeddings |
| Embedding dims | 768 | Vector dimensionality |
| Embeddings | enabled | Set to false to disable vector search |
| Enrichment workers | 4 | Parallel parsing workers |

## Contributing

### Build and Test

```bash
# Build all packages
go build -tags sqlite_fts5 ./...

# Run tests with race detection
go test -race -tags sqlite_fts5 ./...

# Vet
go vet -tags sqlite_fts5 ./...
```

### Project Structure

```
cmd/
  shaktiman/           CLI tool (init, index, search, context, symbols, deps, diff, status)
  shaktimand/          MCP daemon (stdio server)
internal/
  types/               Shared types, config, interfaces
  storage/             SQLite backend (schema, FTS5, graph, diffs)
  parser/              Tree-sitter parsing, chunking, symbol extraction
  core/                Query engine, ranking, context assembly, fallback
  format/              Shared text formatters for CLI and MCP output
  daemon/              Lifecycle, writer, file watcher, enrichment pipeline
  vector/              In-memory vector store, Ollama client, circuit breaker
  mcp/                 MCP server, tool handlers, resources
  eval/                Evaluation harness (recall, precision, MRR)
docs/                  Architecture and design documents
testdata/              Test fixtures (TypeScript, Python, Go projects)
```

### Adding a New Language

1. Add a `LanguageConfig` in `internal/parser/languages.go` with AST node type mappings
2. Add the tree-sitter grammar import
3. Register file extensions in `internal/daemon/scan.go` and `internal/core/fallback.go`
4. Add test fixtures in `testdata/`
5. Run `go test -race -tags sqlite_fts5 ./...`

## Stack

| Component | Technology |
|-----------|-----------|
| Language | Go |
| Storage | SQLite (WAL mode, FTS5 full-text search) |
| Parsing | Tree-sitter via CGo |
| Protocol | MCP (Model Context Protocol) via `mcp-go` |
| Embeddings | Ollama (optional, `nomic-embed-text` default) |
| Tokenizer | `tiktoken-go` (cl100k_base) |
| File watching | `fsnotify` |

## License

[MIT](https://github.com/midhunkrishna/shaktiman/blob/master/LICENSE)
