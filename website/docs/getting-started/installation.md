---
title: Installation
sidebar_position: 1
---

# Installation

Build Shaktiman from source. The full path below takes ~1 minute on a warm cache and
produces two binaries — `shaktiman` (CLI) and `shaktimand` (MCP daemon).

## 1. Prerequisites

- **Go 1.25 or newer.**
- **A C compiler** (gcc / clang). Required for the `sqlite` build tag. macOS has
  `clang` out of the box; Linux needs `build-essential` or equivalent.

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
