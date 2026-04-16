---
title: Installation
sidebar_position: 1
---

# Installation

Build Shaktiman from source. The full path below takes ~1 minute on a warm cache and
produces two binaries — `shaktiman` (CLI) and `shaktimand` (MCP daemon).

:::info[Platform support]
Shaktiman is **POSIX-only**: macOS and Linux. The daemon relies on `flock`, Unix
domain sockets, and `os.TempDir()` semantics that don't translate cleanly to Windows.
On Windows, use **WSL 2** and follow the Linux instructions inside the WSL shell.
Pull requests that add native Windows support are welcome.
:::

## 1. Prerequisites

- **Go 1.25 or newer.** Check with `go version`.
- **Git.** For cloning the repository.
- **A C compiler** (gcc / clang). Required for the `sqlite` build tag.
  - **macOS:** install the Xcode Command Line Tools with `xcode-select --install`.
    Without them, `go build` fails at cgo with a `command not found: cc` error.
  - **Linux:** install `build-essential` (Debian/Ubuntu) or `gcc` + `make` (RHEL
    family) or the equivalent for your distro.
- **~500 MB free disk** for a mid-size repo (< 100k files). Larger repos scale
  roughly linearly — see [Scaling](/performance/scaling).
- **~1 GB free RAM** for the daemon on a mid-size repo; more if you enable the
  `brute_force` vector backend (it holds all vectors in memory).

### Optional (only for specific features)

- **[Ollama](https://ollama.com)** — required if you want vector embeddings. After
  installing, run `ollama pull nomic-embed-text` to fetch the default model.
  Without Ollama, search falls back to keyword mode and still works.
- **`jq`** — used by a few commands in the [Troubleshooting](/troubleshooting/overview)
  pages to filter `shaktimand.log`. Install via `brew install jq` (macOS) or
  `apt install jq` / `dnf install jq` (Linux).

For a postgres-only deployment without CGo, see [Backends](/configuration/backends).

## 2. Clone and build

```bash
git clone https://github.com/midhunkrishna/shaktiman.git
cd shaktiman

go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktiman ./cmd/shaktiman
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktimand ./cmd/shaktimand
```

This produces `./shaktiman` and `./shaktimand` in the repo root. Move them onto your
`PATH` if you'd like to call them from anywhere.

## 3. Verify

```bash
./shaktiman --help
./shaktimand
```

`shaktiman --help` prints the list of subcommands (`init`, `index`, `reindex`,
`status`, `search`, `context`, `symbols`, `deps`, `diff`, `enrichment-status`,
`summary`). `shaktimand` without arguments exits with a usage message — that's the
intended behaviour.

## Next

You've got the binaries. Now index a project and run your first query:

→ [Quickstart](./quickstart)
