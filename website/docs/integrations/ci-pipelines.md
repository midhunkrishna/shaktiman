---
title: CI pipelines
sidebar_position: 5
---

# CI pipelines

Shaktiman is primarily a dev-time tool, but the CLI has enough surface to be
useful in CI for things like "what symbols did this PR touch?", "does our code
still have a function named X?", or generating symbol-level inventories.

## What runs without a daemon

Every query subcommand (`search`, `context`, `symbols`, `deps`, `diff`,
`enrichment-status`, `summary`, `status`) reads the SQLite index directly. No
daemon needed. This makes them cheap to use in CI:

```bash
# Build shaktiman (or use a cached binary)
go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o /usr/local/bin/shaktiman ./cmd/shaktiman

# Index the working tree (CI usually has a fresh checkout)
shaktiman index "$GITHUB_WORKSPACE"

# Now run queries — JSON output by default, pipe to jq
shaktiman symbols "NewServer" --root "$GITHUB_WORKSPACE" --format json | jq .
```

Set `--format json` explicitly (it's the default) or `--format text` for
human-readable output. Both formats are stable for scripting.

## Exit codes

| Command | Success | Failure |
|---|---|---|
| `index` / `reindex` | `0` on full completion | `1` on error (config, parse-all-files, embedding failure with `--embed`) |
| Query commands (`search`, `context`, `symbols`, `deps`, `diff`) | `0` always when the query itself didn't error | `1` on invalid input, index missing, or backend error |

Query commands returning zero results are **not** treated as failures — check
the output if you want to gate on presence/absence.

## Embeddings in CI

Skipping embeddings is usually the right call:

```bash
shaktiman index "$GITHUB_WORKSPACE"     # no --embed: keyword + structural only
```

Pros:
- No Ollama dependency in your CI runner.
- Cold index completes much faster (no HTTP round-trips).
- `search`, `context`, and all other tools still work — they fall back to
  keyword ranking.

If you need semantic search in CI (e.g. for large-repo relevance gating), run
Ollama as a sidecar service and point `SHAKTIMAN_OLLAMA_URL` at it. Mind the
[known limitation](/reference/limitations#only-ollama-is-a-first-class-embedding-backend)
— only Ollama is supported today.

## Caching between runs

CI jobs usually start with a fresh checkout, so `.shaktiman/` doesn't persist
unless you explicitly cache it. On GitHub Actions:

```yaml
- uses: actions/cache@v4
  with:
    path: .shaktiman
    key: shaktiman-${{ runner.os }}-${{ hashFiles('**/*.go', '**/*.ts') }}
    restore-keys: shaktiman-${{ runner.os }}-
```

This caches the index keyed on source-file content. Mostly-incremental runs pick
up from cache in seconds.

## Example: PR-affected symbols

```yaml
jobs:
  affected-symbols:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Build shaktiman
        run: |
          go build -tags "sqlite_fts5 sqlite bruteforce hnsw" -o shaktiman ./cmd/shaktiman

      - name: Index
        run: ./shaktiman index .

      - name: List symbols touched by this PR
        run: |
          ./shaktiman diff --since "168h" --format json \
            | jq '.[] | {file: .file, changed_symbols: .changed_symbols}'
```

Adjust `--since` to your PR age, or compute it from `git log`'s oldest commit.

## Example: symbol inventory

```bash
for name in $(grep -r "func " --include="*.go" . | awk '{print $2}' | sort -u); do
  ./shaktiman symbols "$name" --root . --format json
done | jq -s 'flatten | unique_by(.path + .name)'
```

Clunky but effective for one-off audits — a single symbol-dump command isn't
exposed today (if you need this often, file an issue).

## Gotchas

- **Never run `index` or `reindex` while a daemon owns the lock.** The CLI
  refuses, which is what you want — but only if you ran it intentionally. In CI
  this should never happen (no persistent daemon).
- **`reindex` prompts for confirmation** on TTYs. Use `--force` in CI.
- **Duration strings use Go's `time.ParseDuration`** — no `"d"` or `"w"`. Use
  `"168h"` for a week.

## See also

- [CLI Reference](/reference/cli) — every flag.
- [Custom agents](/integrations/custom-agents) — scripting beyond CI.
- [Getting Started → Installation](/getting-started/installation) — building
  the binary.
