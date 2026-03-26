# Shaktiman MCP — CLAUDE.md Template

Copy this section into your project's CLAUDE.md to instruct Claude Code how to use the shaktiman MCP tools efficiently.

---

## MCP Tools (via shaktimand) — MANDATORY

**STOP RULE: Before calling Grep, Glob, or Bash grep/find/rg — ask: "Can `mcp__shaktiman__*` answer this?" If yes, use MCP. No exceptions.**

This applies to the main agent AND all subagents. When delegating to any subagent (code-explainer, codebase-reader, Explore, reviewer, implementer, etc.), explicitly instruct it to use `mcp__shaktiman__*` tools instead of Read/Grep/Glob. Subagents do not automatically inherit these instructions.

### Locate-first pattern

**Always search in locate mode first** (the default). This returns compact file pointers — paths, line ranges, symbols, and relevance scores — without source code. Then use the `Read` tool to read specific files you need.

This pattern minimizes context usage:
1. `mcp__shaktiman__search` → discover relevant locations (~125 tokens for 10 results)
2. `Read` → read only the files you actually need
3. `Edit` → make changes

Only use `mode="full"` when you need inline source code without a separate Read call.

### Subagent delegation template

When spawning any subagent that needs to read or search code, include this in the prompt:

> **HARD RULE: Do NOT call Grep or Glob. Use `mcp__shaktiman__search`, `mcp__shaktiman__symbols`, and `mcp__shaktiman__dependencies` for ALL code search, caller tracing, and file discovery. The ONLY exceptions are: reading a file by exact known path (use Read), finding non-code files (.md, .json, .yaml), or when MCP tools return no results.**

### Tool mapping

| Instead of | Use | For |
|---|---|---|
| Grep, Glob | `mcp__shaktiman__search` | Find code by keyword or concept (returns locations, not content). Use `path` param to scope to a directory. |
| Read (multi-file) | `mcp__shaktiman__search mode="full"` | Get source code inline when Read is inconvenient |
| Read (cross-file overview) | `mcp__shaktiman__context` | Multi-file understanding fitted to a token budget |
| Grep (definitions) | `mcp__shaktiman__symbols` | Look up function/class/type definitions by name |
| Grep (find all callers) | `mcp__shaktiman__dependencies direction:"callers"` | Find all call sites of a function — essential for refactoring |
| (no equivalent) | `mcp__shaktiman__dependencies` | Trace callers/callees of a symbol |
| (no equivalent) | `mcp__shaktiman__diff` | Recent file changes and affected symbols |

### Common mistakes — do NOT do these

| Bad (Grep/Glob) | Good (MCP) | Why |
|---|---|---|
| `Grep ".IndexProject("` to find callers | `dependencies symbol:"IndexProject" direction:"callers"` | Structured results, finds indirect callers too |
| `Grep "mock.*Ollama"` in a test file | `search query:"mock ollama" path:"internal/vector/"` | Semantic match, fewer tokens |
| `Glob "internal/daemon/*_test.go"` | `search query:"func Test" path:"internal/daemon/"` | Finds test code by content, not just filenames |
| `Grep "TestHealthy"` to find a test | `symbols name:"TestHealthy" kind:"function"` | Exact definition, not every mention |

### Workflow recipes

**Updating a function signature (find all callers):**
1. `mcp__shaktiman__dependencies symbol:"FuncName" direction:"callers"` → list of call sites
2. `Read` each file → `Edit` the call

**Checking if tests exist for a package:**
1. `mcp__shaktiman__search query:"func Test" path:"internal/daemon/"` → lists test functions

**Finding where a type/interface is used:**
1. `mcp__shaktiman__dependencies symbol:"TypeName" direction:"callers" depth:2`

**Understanding a function before modifying it:**
1. `mcp__shaktiman__symbols name:"FuncName"` → get file + line
2. `mcp__shaktiman__dependencies symbol:"FuncName"` → see what it calls and what calls it
3. `Read` the file → `Edit`

### Token efficiency tips

- **Keep `max_results` small** (5-10). You rarely need more than 10 results.
- **Use `min_score`** to filter noise. Default is 0.15; raise to 0.3+ for precise queries.
- **Use `context` sparingly** with small budgets (1024-2048 tokens). For single-file reading, use `Read` instead.
- **Prefer `symbols`** over `search` when you know the exact function/type name.
- **Use `path` param** on `search` to scope results to a directory (e.g. `path: "internal/mcp/"`) instead of using Grep with a path filter.

### Fallback to Grep/Glob — ONLY when ALL of these are true

- [ ] You tried MCP search/symbols/dependencies first and got insufficient results, OR
- [ ] The file is non-code (.md, .json, .yaml, .toml) that Shaktiman doesn't index, OR
- [ ] You need the exact file path for Read/Edit (not searching)
