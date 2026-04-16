---
title: From Claude's default tools
sidebar_position: 5
---

# Migrating from Claude's default tools

## Mental-model shift

Claude Code ships with `Grep`, `Glob`, and `Read` out of the box. You're
probably using a pattern like:

```
Grep "auth"             → 47 hits
Read file A              → partially relevant
Read file B              → relevant
Read file C              → not relevant
... 5 more Reads ...
```

Every `Read` spends several thousand tokens. Every `Grep` hit that turns out
irrelevant is wasted context.

Shaktiman adds **ranked discovery** on top of the same agent. The new loop:

```
mcp__shaktiman__search "auth"   → 5 ranked pointers, ~60 tokens
Read file A                      → highly relevant
(done)
```

The token savings are real and consistent — the
[architecture doc](/design/architecture) quotes a 45–65 % reduction in input
tokens for typical exploration tasks.

## Feature parity table

| Task | Default tool | Shaktiman equivalent | Why switch |
|---|---|---|---|
| Exact-string search | `Grep` | — | Keep `Grep`. |
| Regex match | `Grep` | — | Keep `Grep`. |
| Filename / path glob | `Glob` | — | Keep `Glob`. |
| Read a known file | `Read` | — | Keep `Read`. |
| Find code by concept | 3–5 `Grep`s + `Read`s | `mcp__shaktiman__search` | 70–90 % fewer tool calls. |
| Budget-fitted context for an LLM prompt | Manual: `Read` + trim | `mcp__shaktiman__context` | No manual trimming. |
| Find symbol definition | `Grep "func Foo"` + `Read` | `mcp__shaktiman__symbols name:"Foo"` | No false positives on comments / strings. |
| Trace callers | Multiple `Grep "Foo("` rounds | `mcp__shaktiman__dependencies symbol:"Foo"` | One call, transitive via `depth`. |
| Recent changes + affected symbols | `git log` + `Read` | `mcp__shaktiman__diff` | Symbol-level, not line-level. |
| Repo orientation | `Glob "**/*.md"` + `Read` | `mcp__shaktiman__summary` | One call, structured. |

## Side-by-side workflow

**"How does authentication work in this codebase?"**

Default tools:

```
1. Grep "auth"                        → 47 files
2. Grep "authenticate"                → 12 files (narrower)
3. Glob "**/auth*.go"                 → 3 files
4. Read internal/auth/middleware.go   → relevant
5. Read internal/auth/token.go        → relevant
6. Read internal/auth/session.go      → partially relevant
7. Read internal/server/handlers.go   → not relevant, turns out
Total: ~18k tokens of Read output.
```

Shaktiman:

```
1. mcp__shaktiman__search "authentication flow"   → 10 ranked pointers
2. Read internal/auth/middleware.go               → relevant
3. Read internal/auth/token.go                    → relevant
Total: ~6k tokens of Read output + ~250 tokens of search output.
```

## Gaps

- **Exact string / regex search.** Shaktiman ranks by relevance; if you need
  every literal match, `Grep` is correct.
- **Filename patterns.** Shaktiman indexes content, not filenames. `Glob
  "**/*.yaml"` has no direct equivalent — config files aren't even indexed.
- **Files Shaktiman doesn't index.** Markdown, YAML, JSON, Dockerfiles — none
  are parsed. `Read` directly.
- **Fresh checkouts with no daemon.** If you haven't started `shaktimand` on a
  project, there's no index yet — `Grep` is your only option.

## When to keep the default tools

Every time you need:

- Exact / regex match → `Grep`.
- Filename pattern → `Glob`.
- A known-path file → `Read`.
- Arbitrary non-source file (`.yaml`, `.md`, etc.) → `Read`.

The wins come from **adding** Shaktiman, not **replacing** the defaults.

## Telling Claude to use Shaktiman

Subagents don't inherit `CLAUDE.md` — you have to prompt them. See the
[Claude Code integration page](/integrations/claude-code#subagent-delegation)
for a copy-paste block that encodes "use Shaktiman for conceptual discovery;
fall back to Grep/Glob/Read for literal / filename / specific-file tasks".

## Token math

From the [architecture estimates](/design/architecture):

|  | Without Shaktiman | With Shaktiman | Reduction |
|---|---|---|---|
| Tool calls per task | 8–15 | 1–3 | 70–90% |
| Input tokens per task | 15k–30k | 5k–10k | 45–65% |
| Irrelevant content read | 25–40% | &lt;5% | 85–95% |

Your mileage varies by query type. Conceptual queries benefit most; literal
queries see no improvement (because `Grep` is already the right tool).

## See also

- [Claude Code integration](/integrations/claude-code) — the CLAUDE.md
  template telling Claude when to reach for which tool.
- [Searching & navigating the index](/guides/searching) — the full picker.
