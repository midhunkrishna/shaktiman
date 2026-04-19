<p align="center">
  <img src="website/static/img/logo.png" alt="Shaktiman" width="200">
</p>

<h1 align="center">Shaktiman</h1>

<p align="center">
  <a href="https://codecov.io/gh/midhunkrishna/shaktiman"><img src="https://codecov.io/gh/midhunkrishna/shaktiman/branch/master/graph/badge.svg?token=BZ7NUTRX30" alt="codecov"></a>
</p>

Local-first code context engine for coding agents.

Shaktiman indexes your codebase and exposes MCP tools (search, symbols, dependencies, context) that let Claude Code and other agents assemble exactly the right code context — fitted to a token budget.

- **Tree-sitter parsing** — functions, classes, symbols, imports, call graphs
- **Hybrid search** — keyword (FTS5) + semantic (vector) + structural + change signals
- **Budget-fitted context** — ask for 4K tokens, get 4K tokens of the most relevant code
- **Live updates** — file watcher re-indexes on save

📖 **Full documentation:** [shaktiman.dev](https://shaktiman.dev)

## Quick Start

```bash
git clone git@github.com:midhunkrishna/shaktiman.git
cd shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktimand ./cmd/shaktimand
```

Then wire it into Claude Code via `.mcp.json`. See [Getting Started](https://shaktiman.dev/docs/getting-started/quickstart) for the full walkthrough.

## Documentation

| | |
|---|---|
| [Installation](https://shaktiman.dev/docs/getting-started/installation) | Prerequisites, build tags, Ollama setup |
| [Claude Code setup](https://shaktiman.dev/docs/getting-started/claude-code-setup) | `.mcp.json` config + `CLAUDE.md` template |
| [MCP tools](https://shaktiman.dev/docs/reference/mcp-tools/overview) | `search`, `context`, `symbols`, `dependencies`, `diff`, `summary` |
| [CLI reference](https://shaktiman.dev/docs/reference/cli) | All commands and flags |
| [Configuration](https://shaktiman.dev/docs/configuration/config-file) | `.shaktiman/shaktiman.toml` options |
| [Backends](https://shaktiman.dev/docs/configuration/backends) | SQLite, PostgreSQL, pgvector, Qdrant, HNSW |
| [Integrations](https://shaktiman.dev/docs/integrations/claude-code) | Claude Code, Cursor, Zed, custom agents |

## Supported Languages

TypeScript, JavaScript, Python, Go, Rust, Java, Ruby, ERB, Shell (bash). See [supported languages](https://shaktiman.dev/docs/reference/supported-languages) for details and instructions on adding new ones.

## Contributing

```bash
# Build, test, vet (default backends)
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
go vet -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...
```

See [Contributing](https://shaktiman.dev/docs/contributing) for project structure, backend build tags, and language-addition steps.

## License

[MIT](https://github.com/midhunkrishna/shaktiman/blob/master/LICENSE)
