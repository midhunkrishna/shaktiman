# Shaktiman MCP — CLAUDE.md Template

Copy this section into your project's CLAUDE.md to instruct Claude Code how to use the shaktiman MCP tools.

---

## MCP Tools (via shaktimand)

Shaktiman is a pre-built code index that reduces context usage during exploration. Use it to narrow down before reading files — not as a replacement for Grep or Glob.

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

### Discovery workflow

1. `mcp__shaktiman__summary` → orient (size, languages, health)
2. `mcp__shaktiman__search` → narrow to relevant files (~12 tokens/result in locate mode)
3. `Read` → read only the files that matter
4. `Edit` → make changes

Use `mode="full"` on search only when you need inline source code without a separate Read call.

### Signs you should use shaktiman instead

| You notice... | Use | Why |
|---|---|---|
| First time in a codebase, no context yet | `summary` | File count, languages, symbol count at a glance — no files to read |
| Your query is conceptual, not a literal pattern (e.g. "authentication", "database connection") | `search` | Returns top-N ranked by relevance. Grep returns every literal match, unranked. |
| A Grep returned many matches across many files and you need to pick which to read | `search` with the same terms | Ranked results tell you which files matter most — read 3 files, not 30 |
| You need a function or type definition | `symbols name:"Name"` | File, line, signature, visibility — without reading the file. Works across languages. |
| You need callers, callees, or blast radius of a function | `dependencies symbol:"Name"` | Structural call graph. Use `direction:"callers" depth:3` for impact analysis. Grep `"Name("` misses indirect calls and breaks on aliased imports. |
| The repo is polyglot and Grep patterns differ by language | `symbols` or `dependencies` | Language-agnostic. `func`, `def`, `function`, `fn` — same query works regardless of syntax. |

### Examples: when shaktiman helps

| Task | Tool call | Why this works |
|---|---|---|
| Find all callers of a function | `dependencies symbol:"FuncName" direction:"callers"` | Structured call graph, finds indirect callers too |
| See what a function depends on | `dependencies symbol:"FuncName" direction:"callees"` | Shows all functions it calls — useful for understanding scope of change |
| Map a function's full dependency neighborhood | `dependencies symbol:"FuncName" direction:"both" depth:3` | Bidirectional traversal — shows callers and callees up to 3 levels deep |
| Find where a function is defined | `symbols name:"FuncName" kind:"function"` | Exact definition, not every mention |
| Understand auth flow across files | `search query:"authentication flow"` | Conceptual match across files |
| Get context for refactoring | `context query:"payment processing" budget_tokens:2048` | Ranked chunks within budget |

### Examples: when Grep/Glob are the right tool

| Task | Tool | Why |
|---|---|---|
| Find exact string `".IndexProject("` | Grep | Literal pattern match |
| Find regex pattern `mock.*Server` | Grep | Regex matching |
| Find all TODO comments | Grep | Exact text pattern |
| Find test files by name | Glob `*_test.go` | Filename pattern |
| Find config files | Glob `*.yaml` | Non-code files shaktiman doesn't index |

### Subagent delegation

Subagents don't inherit CLAUDE.md instructions. When spawning subagents that explore code, include:

> Use `mcp__shaktiman__search`, `mcp__shaktiman__symbols`, and `mcp__shaktiman__dependencies`
> for code discovery before reading files. All tools exclude test files by default —
> use `scope:"test"` when looking for test code. Use Grep for exact string or regex matching.
> Use Glob for finding files by name. Use Read for known file paths.

### Token efficiency tips

- **Keep `max_results` small** (5-10). You rarely need more than 10 results.
- **Use `min_score`** to filter noise. Default is 0.15; raise to 0.3+ for precise queries.
- **Prefer `symbols`** over `search` when you know the exact function/type name.
- **Use `path` param** on `search` to scope results to a directory (e.g. `path: "internal/mcp/"`).
- **Use `context` with small budgets** (1024-2048 tokens). For single-file reading, use `Read`.
