---
title: From grep / ripgrep
sidebar_position: 2
---

# Migrating from grep / ripgrep

## Mental-model shift

`grep` and `ripgrep` are **enumeration** tools: give them a pattern, they
return every file where it matches, in whatever order the filesystem surfaces
them. You then triage the list yourself.

Shaktiman's `search` is a **ranking** tool: give it a concept, it returns the
top-N most relevant matches ordered by a blend of semantic, structural,
keyword, change, and session signals. You skip the triage step for conceptual
queries.

Neither is "better" — they solve different problems.

## Feature parity table

| Capability | ripgrep | Shaktiman equivalent | Notes |
|---|---|---|---|
| Exact-string search | `rg "pattern"` | — | Keep ripgrep. |
| Regex search | `rg -e "regex"` | — | Keep ripgrep. |
| Case-insensitive | `rg -i` | — | Keep ripgrep. |
| Concept / natural-language | — | `shaktiman search "concept"` | Shaktiman's strength. |
| Filename filter | `rg "x" path/` | `shaktiman search "x" --path path/` | Prefix match on path. |
| File-type filter | `rg -t go "x"` | Indirect via `--path` | No language filter today; the index already excludes binary / non-source. |
| Ranked top-N | — | `shaktiman search "x" --max 10` | ripgrep can't rank. |
| Compact output | — | `shaktiman search "x" --mode locate` | ~12 tokens/result; great for LLM prompts. |
| Exclude test files | `--glob '!*_test.go'` | `--scope impl` (default) | Language-aware test pattern. |

## Side-by-side workflow

**"Where is authentication handled in this codebase?"**

ripgrep:

```bash
rg -l "auth" | head -20                 # 47 files; eyeball the list
rg "middleware.*auth"                    # narrow to middleware
rg -l "validateToken|authenticate"       # look for verbs
# Now read ~5 files and reconstruct the picture
```

Shaktiman:

```bash
shaktiman search "authentication middleware" --root . --format text
# Top 10 files, ranked. Read the top 3.
```

Same goal, ~3 invocations → 1, and the output is aligned with what you
actually want.

## Gaps

- **Shaktiman has no regex search.** For `deadline\.Exceeded\b` or similar,
  always `rg`.
- **Shaktiman has no live grep-over-pipes.** `rg` can filter any stdin;
  Shaktiman queries its own index.
- **Shaktiman's `path` filter is a prefix, not a glob.** `--path "internal/"`
  works; `--path "**/auth*.go"` doesn't — use `rg` for globs.
- **Shaktiman requires an initial index.** First run costs 1–2 minutes on a
  mid-size repo; ripgrep starts instantly.

## When to keep ripgrep

- Every time you're looking for an **exact** string or regex.
- When grep-over-pipes or grep-over-not-a-git-repo is the right tool.
- When you want to scan a directory Shaktiman hasn't indexed.

Most Shaktiman users keep `rg` on their PATH and reach for whichever fits the
question. Conceptual → Shaktiman; literal → ripgrep.

## See also

- [Searching & navigating the index](/guides/searching) — the full picker.
- [`search`](/reference/mcp-tools/search) — every flag.
