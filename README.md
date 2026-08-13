<p align="center">
  <img src="website/static/img/logo.png" alt="Shaktiman" width="200">
</p>

<h1 align="center">Shaktiman</h1>

<p align="center">
  <a href="https://github.com/midhunkrishna/shaktiman/actions/workflows/ci.yml"><img src="https://github.com/midhunkrishna/shaktiman/actions/workflows/ci.yml/badge.svg?branch=master" alt="CI"></a>
  <a href="https://codecov.io/gh/midhunkrishna/shaktiman"><img src="https://codecov.io/gh/midhunkrishna/shaktiman/branch/master/graph/badge.svg?token=BZ7NUTRX30" alt="codecov"></a>
  <a href="https://github.com/midhunkrishna/shaktiman/blob/master/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
</p>

Local-first code context engine for coding agents.

Coding agents burn most of their context window rediscovering your codebase — grep after grep, whole files read for one relevant function. Shaktiman indexes your codebase once and exposes MCP tools (search, symbols, dependencies, context) that let Claude Code and other agents assemble exactly the right code context — fitted to a token budget. Everything runs on your machine; no code leaves it.

<p align="center">
  <img src="website/static/img/demo.gif" alt="Terminal demo: indexing a repository, hybrid search, symbol dependencies, and budget-fitted context assembly" width="800">
</p>

- **Tree-sitter parsing** — functions, classes, symbols, imports, call graphs
- **Hybrid search** — keyword (FTS5) + semantic (vector) + structural + change signals
- **Budget-fitted context** — ask for 4K tokens, get 4K tokens of the most relevant code
- **Live updates** — file watcher re-indexes on save

📖 **Full documentation:** [shaktiman.dev](https://shaktiman.dev)

## Quick Start

```bash
git clone https://github.com/midhunkrishna/shaktiman.git
cd shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktiman ./cmd/shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktimand ./cmd/shaktimand
```

Then wire it into Claude Code via `.mcp.json`. See [Getting Started](https://shaktiman.dev/getting-started/quickstart) for the full walkthrough.

## Documentation

| | |
|---|---|
| [Installation](https://shaktiman.dev/getting-started/installation) | Prerequisites, build tags, Ollama setup |
| [Claude Code setup](https://shaktiman.dev/getting-started/claude-code-setup) | `.mcp.json` config + `CLAUDE.md` template |
| [MCP tools](https://shaktiman.dev/reference/mcp-tools/overview) | `search`, `context`, `symbols`, `dependencies`, `diff`, `summary` |
| [CLI reference](https://shaktiman.dev/reference/cli) | All commands and flags |
| [Configuration](https://shaktiman.dev/configuration/config-file) | `.shaktiman/shaktiman.toml` options |
| [Backends](https://shaktiman.dev/configuration/backends) | SQLite, PostgreSQL, pgvector, Qdrant, HNSW |
| [Integrations](https://shaktiman.dev/integrations/claude-code) | Claude Code, Cursor, Zed, custom agents |
| [Architecture & ADRs](https://shaktiman.dev/design/architecture) | System design, decision records |

## Supported Languages

TypeScript, JavaScript, Python, Go, Rust, Java, Ruby, ERB, Shell (bash). See [supported languages](https://shaktiman.dev/reference/supported-languages) for details and instructions on adding new ones.

## Contributing

```bash
# Build, test, vet (default backends)
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
go vet -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
```

See [Contributing](https://shaktiman.dev/contributing) for project structure, backend build tags, and language-addition steps.

## License

[MIT](https://github.com/midhunkrishna/shaktiman/blob/master/LICENSE)
