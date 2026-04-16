# ADR-001: Code Review Capabilities for Shaktiman

**Status:** AMENDED
**Date:** 2026-03-31
**Deciders:** Shaktiman maintainers

> **Status (Today, 2026-04-16):** **NOT SHIPPED.** No code-review-specific MCP tool
> exists in `internal/mcp/tools.go` at time of writing. Retained as a design record.

---

## Context

Shaktiman is a local-first code context engine designed to reduce token usage during codebase exploration. Paradoxically, for the code review use case, Shaktiman currently *increases* token usage compared to a naive approach.

**The token economics problem:**

| Approach | Tokens for 3-file MR |
|---|---|
| Naive (git diff + Read) | ~5,000 |
| Current Shaktiman | ~8,850 |

The 77% overhead comes from six specific gaps:

1. **Diff tool cannot target a specific MR.** Only accepts `since: "24h"` time windows. Diffs are recorded from fsnotify watcher events during enrichment, not from git history. A reviewer must over-fetch by time window and mentally filter to relevant changes.

2. **`include_impact` is specced but not implemented.** The API specification (06-api-specification.md) defines `include_impact: true` with `impacted_callers` per changed symbol. The diff handler in `internal/mcp/tools.go:489-554` ignores this parameter entirely. The underlying `store.Neighbors()` BFS in `internal/storage/graph.go:148-201` could power it.

3. **Dependencies tool has no batch queries via MCP.** `BatchNeighbors()` exists in `internal/storage/metadata.go:797` but is only used internally by `computeStructuralScoresBatch()` in the ranker. Reviewing 5 changed functions requires 5 separate MCP round-trips.

4. **Dependencies tool does not surface edge kinds.** Response type `format.DepResult` in `internal/format/types.go:33-37` has `{name, kind, file_path, line}` but no edge_kind. The `Neighbors()` recursive CTE in `graph.go:158-166` discards edge kind via `SELECT DISTINCT id FROM reachable`. The DB schema has `kind TEXT NOT NULL CHECK (kind IN ('imports','calls','type_ref','inherits','implements'))` on the edges table.

5. **Search/context cannot scope to specific files.** Search has `path` (single prefix) but no multi-file support. No way to say "give me context for these 3 changed files." Change recency is 15% weight in the ranker (ranker.go:14), not controllable per-query.

6. **No unified review tool.** Getting a review picture requires 5-8 separate MCP calls: diff, then dependencies per symbol, then context/search. Each call has JSON overhead, redundant file path resolution, and the LLM must mentally join the results.

**Design tension:** The data model document (05-data-model.md) explicitly states "Shaktiman indexes the working tree, not git history." Adding git-based diff targeting must be reconciled with this philosophy.

---

## Decision

**We choose Alternative D: Hybrid -- enhance existing tools + add a thin `review` orchestration tool.**

This decomposes into three layers:

### Layer 1: Fix existing tools (Gaps 2, 3, 4)

These are missing features that the architecture already supports internally.

**1a. Implement `include_impact` on the diff handler.**
- Add `include_impact` boolean parameter to `diffToolDef()`.
- When true, for each changed symbol in `diff_symbols`, call `store.Neighbors(ctx, symbolID, 1, "incoming")` to find callers.
- Return `impacted_callers` array per changed symbol as specified in 06-api-specification.md.
- Cost: ~50 lines in `tools.go`. No schema change. No new dependency.

**1b. Add `symbols` (batch) parameter to dependencies tool.**
- Accept `symbols: ["FuncA", "FuncB", "FuncC"]` as alternative to single `symbol`.
- Internally route to `BatchNeighbors()` which already exists in metadata.go:797.
- Return results grouped by input symbol.
- Cost: ~30 lines in `tools.go`. Interface already exists.

**1c. Surface edge kind in dependencies and impact responses.**
- Modify `Neighbors()` CTE to return `(id, edge_kind)` pairs instead of just `id`.
- Define new type `NeighborResult{SymbolID int64, EdgeKind string}`.
- Update `format.DepResult` to include `EdgeKind string`.
- Cost: ~40 lines across `graph.go`, `types.go`, `tools.go`. Requires CTE rewrite from `SELECT DISTINCT id` to `SELECT DISTINCT id, kind`.

### Layer 2: Add git-aware diff source (Gap 1)

**2a. Add `commit_range` parameter to diff tool.**
- Accept optional `base` and `head` parameters (commit SHAs or branch names).
- When provided, shell out to `git diff --name-status <base>...<head>` to get the file list.
- For each changed file, look up its current symbols in the Shaktiman index (NOT the git blob content).
- Cross-reference with `diff_log` and `diff_symbols` for symbol-level change detail when available.
- Fallback: if `diff_log` has no matching entry (file changed outside the watcher window), report file-level change only.

**Why this preserves the "current working tree" philosophy:**
- Shaktiman still does not store historical file content.
- Git is used only as a *file list source* -- "which files differ between these commits?"
- All symbol lookups, impact analysis, and context assembly still use the current index.
- This is analogous to how the existing watcher uses fsnotify as a *file list source* -- we add git as a second source.

**2b. Add `files` parameter to search and context tools (Gap 5).**
- Accept `files: ["path/a.go", "path/b.go"]` to scope results.
- Implementation: filter FTS/vector results to matching file_ids before ranking.
- Cost: ~20 lines per handler. No schema change.

### Layer 3: Add thin `review` orchestration tool (Gap 6)

**3a. New `review` MCP tool.**
- Accepts `base` and `head` (commit range), plus optional `budget_tokens` (default 4096).
- Orchestrates internally within the Go process (no MCP round-trips):
  1. Get changed files via `git diff --name-status base...head`
  2. For each changed file, query `diff_symbols` for symbol-level changes
  3. For changed symbols, run `Neighbors(incoming, depth=1)` for impact
  4. Run `context` assembly scoped to changed files with the token budget
  5. Return a single unified response

**Response structure:**
```json
{
  "changed_files": [
    {
      "path": "internal/auth/login.go",
      "change_type": "modify",
      "symbols_changed": [
        {
          "name": "ValidateToken",
          "change_type": "signature_changed",
          "impacted_by": [
            {"name": "HandleLogin", "file": "internal/routes/auth.go", "edge_kind": "calls"}
          ]
        }
      ]
    }
  ],
  "context": [
    // Token-budgeted ranked chunks for changed files
  ],
  "summary": {
    "files_changed": 3,
    "symbols_changed": 5,
    "impacted_callers": 8,
    "context_tokens": 3500
  }
}
```

**Token economics of the `review` tool:**
- 1 MCP call instead of 5-8
- No redundant file path resolution
- Impact is co-located with changes (no mental joining)
- Context is pre-scoped to changed files
- Estimated: ~2,500-3,500 tokens for a 3-file MR (vs. 5,000 naive, 8,850 current)

---

## Alternatives Considered

### Alternative A: Incremental enhancement only (add params to existing tools one by one)

**Steelman:** This is the lowest-risk approach. Each change is small, testable, and independently deployable. It preserves the existing tool surface and avoids introducing a new tool that clients must learn. The MCP client (LLM) already knows how to compose existing tools -- improving each one benefits all use cases, not just review.

**Why not chosen:** Even with all enhancements, the review workflow still requires 3-5 MCP calls: enhanced diff (1) + batch dependencies (1) + scoped context (1) + optional search (1). Each call has ~200-400 tokens of JSON overhead. The LLM must still mentally join results across calls. This approach fixes the per-tool gaps but not the orchestration gap (Gap 6). Token savings would be ~30-40% vs current (to ~5,500 tokens), roughly matching naive -- but not beating it.

### Alternative B: Single new `review` tool only (no changes to existing tools)

**Steelman:** Maximum simplicity for the review use case. One tool, one call, one response. All orchestration logic is internal. The tool can be highly optimized for the specific workflow without backward compatibility constraints. Easier to document and explain.

**Why not chosen:** This duplicates logic that should live in existing tools. Impact analysis benefits `diff` users who don't use `review`. Batch dependencies benefits anyone tracing multiple symbols. File-scoped search benefits any targeted exploration. Building everything into `review` creates a monolithic handler that is hard to test in isolation and means non-review use cases don't benefit from the improvements. It also means the `diff`, `dependencies`, and `search` tools remain broken for their own use cases.

### Alternative C: Git-native diff rewrite (make Shaktiman fully git-aware)

**Steelman:** The most architecturally clean solution. Replace the fsnotify-based diff recording with git-native change tracking. Store git object hashes per file, compute diffs via `git diff-tree`, and support full commit range queries natively. This would make Shaktiman a true "git-aware code intelligence" tool rather than a "working tree index with change notifications."

**Why not chosen:** This is a large architectural change that contradicts the documented design philosophy. The current diff_log captures changes the watcher observes in real-time, including unsaved-file-level changes that git doesn't track. Replacing fsnotify with git would lose sub-commit granularity. It would also require storing git object hashes in the schema, running git commands during enrichment, and handling repos with no git history (new projects). The effort is 5-10x larger than Alternative D for marginal benefit: the review tool needs git only for file list discovery, not full git integration. Migration cost is high and the approach is over-fitted to one use case.

### Alternative E: MCP Composition Protocol (let the client orchestrate)

**Steelman:** Define a "review recipe" in the MCP server instructions that tells the LLM: "For code review, call diff with include_impact, then batch-dependencies, then scoped-context." This avoids adding any new tool and relies on the LLM's ability to follow multi-step protocols.

**Why not chosen:** Each MCP call has round-trip latency and JSON serialization overhead. The LLM cannot parallelize the calls (diff must complete before dependencies can be called). Intermediate results consume context window tokens that are discarded after the final synthesis. Protocol-based composition is fragile -- the LLM may skip steps, reorder them, or fail to correlate results. This approach has the worst token economics of all alternatives.

---

## Consequences

### Positive

1. **Token efficiency achieves the target.** The `review` tool delivers ~2,500-3,500 tokens for a typical 3-file MR, beating the naive approach by 30-50%.

2. **Existing tools improve independently.** `include_impact` on diff, batch queries on dependencies, and file-scoped search all benefit non-review workflows.

3. **No schema migration required.** All changes use existing tables (edges, diff_log, diff_symbols, chunks, symbols). The edge kind is already stored; we just need to surface it through the CTE.

4. **Design philosophy preserved.** Git is used only as a file list source, not as a content store. The "current working tree only" principle remains intact.

5. **Incremental delivery.** Layer 1 (fix existing tools) ships independently and provides value even without Layer 3.

6. **Small blast radius.** The `review` tool composes existing, tested primitives. New code is primarily orchestration, not new data access patterns.

### Negative

1. **New external dependency: `git` CLI.** Shaktiman currently has zero runtime dependencies on external CLIs. Shelling out to `git` introduces a process execution dependency, error handling for git-not-installed, and parsing of git output. This is mitigated by: git is universally available on developer machines; the dependency is isolated to a single `gitdiff` package.

2. **The `review` tool's response may be stale.** If a file was changed after the commit range but before the index was updated, the symbol information will reflect the current index, not the commit state. This is a consequence of the "current working tree only" design and is acceptable for the review use case (reviewer is typically on the branch being reviewed).

3. **Testing complexity increases.** The `review` tool's integration test requires a git repo fixture. Unit tests for Layer 1 changes are straightforward (they extend existing test patterns), but end-to-end review tests need git setup/teardown.

4. **MCP tool count increases.** Adding `review` brings the tool count from 7 to 8. Each tool adds to the system prompt size that LLMs must process.

5. **Partial coverage when diff_log is empty.** If the watcher hasn't observed changes for a commit range (e.g., user pulled remote changes), the review tool can report file-level changes but not symbol-level detail for those files. This degrades gracefully but may confuse users expecting full symbol-level analysis.

---

## Pre-Mortem: "6 months later, this failed -- why?"

### Failure Mode 1: Git output parsing breaks
**Scenario:** A new git version changes output format, or a repo uses an unusual configuration (e.g., `diff.mnemonicPrefix`), and the `git diff --name-status` parser produces garbage.
**Mitigation:** Use `git diff --name-status -z` (NUL-separated, format-stable). Pin to porcelain output. Add integration tests against actual git repos.

### Failure Mode 2: Review tool response is too large
**Scenario:** A large MR (30+ files, 100+ changed symbols) produces a response that itself consumes 10,000+ tokens, defeating the purpose.
**Mitigation:** The `budget_tokens` parameter caps context assembly. Add `max_files` parameter with default 20. Summarize impact beyond the first N callers per symbol. Include a `truncated: true` flag when limits are hit.

### Failure Mode 3: Stale index makes impact analysis wrong
**Scenario:** Developer runs `git checkout` to a different branch but the Shaktiman watcher hasn't re-indexed yet. The review tool reports impact based on symbols that no longer exist or have different signatures.
**Mitigation:** Check `files.content_hash` against the working tree before reporting. Add a staleness indicator (`"index_freshness": "stale"` or `"up_to_date"`) to the response. Document that users should wait for re-indexing after branch switches.

### Failure Mode 4: Nobody uses the `review` tool
**Scenario:** LLM agents default to `git diff` + `Read` because they're simpler and well-understood. The review tool goes unused despite being superior in token economics.
**Mitigation:** Add the review tool to the MCP server instructions with clear "when to use" guidance. Measure tool_calls metrics (the `tool_calls` table already exists in schema.go). Track adoption and iterate on discoverability.

### Failure Mode 5: Layer 1 ships but Layer 3 never does
**Scenario:** The team delivers the existing tool enhancements but deprioritizes the review orchestration tool. Token savings plateau at ~30% instead of reaching the 50%+ target.
**Mitigation:** Phase the work so Layer 3 is a single PR after Layer 1. Keep the review handler thin (orchestration only, no new data access). Set a delivery milestone for Layer 3.

---

## FMEA: Operational Risk Assessment

| Risk | Severity (1-10) | Occurrence (1-10) | Detection (1-10) | RPN | Mitigation |
|---|---|---|---|---|---|
| Git CLI not available on host | 8 | 2 | 2 | 32 | Check at startup; disable review tool gracefully; diff tool falls back to time-window mode |
| Git diff parsing produces incorrect file list | 7 | 2 | 3 | 42 | Use `-z` NUL-separated output; integration tests with real git repos |
| Stale index produces misleading impact analysis | 6 | 4 | 5 | 120 | Add freshness indicator to response; check content_hash before reporting |
| Large MR overwhelms response budget | 5 | 3 | 3 | 45 | `max_files` and `budget_tokens` caps; truncation flag in response |
| CTE rewrite for edge kinds causes performance regression | 4 | 3 | 2 | 24 | Benchmark before/after; the CTE already traverses edges, adding kind to SELECT is minimal overhead |
| review tool handler becomes a maintenance burden | 5 | 3 | 4 | 60 | Keep handler as pure orchestration; all data access via existing store methods |
| Partial diff_log coverage confuses users | 5 | 5 | 6 | 150 | Document clearly; add `"coverage": "full" | "partial"` to response; encourage waiting for re-index |

**Top risks by RPN:**
1. **Partial diff_log coverage (RPN 150):** Highest risk. When files changed outside the watcher window (git pull, branch switch), symbol-level data is missing. Must degrade gracefully and communicate clearly.
2. **Stale index (RPN 120):** Second highest. Branch switches create a window where the index doesn't match the working tree. Freshness indicators are essential.

---

## Phasing

### Phase 1: Fix existing tools (1-2 weeks)
**Impact: Immediately useful for all workflows, not just review.**

1. **Implement `include_impact` on diff handler** (Gap 2)
   - Add parameter to `diffToolDef()` and `diffHandler()`
   - For each diff symbol with a valid `symbol_id`, call `Neighbors(ctx, symbolID, 1, "incoming")`
   - Extend `format.DiffResult` with `ImpactedCallers []ImpactCaller`
   - Files: `internal/mcp/tools.go`, `internal/format/types.go`

2. **Surface edge kind in Neighbors() and DepResult** (Gap 4)
   - Define `NeighborResult{ID int64, EdgeKind string}`
   - Rewrite `Neighbors()` CTE to `SELECT DISTINCT id, kind FROM reachable`
   - Handle duplicate IDs with different edge kinds (keep all)
   - Update `dependenciesHandler()` and `format.DepResult`
   - Files: `internal/storage/graph.go`, `internal/format/types.go`, `internal/mcp/tools.go`

3. **Add batch `symbols` parameter to dependencies tool** (Gap 3)
   - Accept `symbols: [...]` as alternative to single `symbol`
   - Delegate to `BatchNeighbors()` in `metadata.go`
   - Return results grouped by input symbol name
   - Files: `internal/mcp/tools.go`

### Phase 2: Git-aware diff + file scoping (1 week)
**Impact: Enables commit-range targeting and file-scoped queries.**

4. **Add git diff file list source** (Gap 1)
   - New package `internal/git/diff.go`: run `git diff --name-status -z base...head`
   - Returns `[]FileChange{Path, Status}` (A/M/D/R)
   - Add `base` and `head` params to `diffToolDef()`
   - When present, use git file list instead of time-window query
   - Files: new `internal/git/diff.go`, `internal/mcp/tools.go`

5. **Add `files` parameter to search and context** (Gap 5)
   - Accept `files: ["path/a.go", ...]` on both tools
   - Filter candidates by file_id after FTS/vector retrieval
   - Files: `internal/mcp/tools.go`, `internal/core/engine.go`

### Phase 3: Review orchestration tool (1 week)
**Impact: Single-call review with optimal token economics.**

6. **New `review` tool** (Gap 6)
   - New handler in `internal/mcp/tools.go` (or split to `internal/mcp/review.go`)
   - Accepts `base`, `head`, `budget_tokens`, `max_files`, `scope`
   - Orchestrates: git file list -> diff_symbols lookup -> impact (Neighbors incoming) -> context assembly
   - All in-process, no MCP round-trips
   - Register in `server.go` alongside existing tools
   - Files: `internal/mcp/review.go`, `internal/mcp/server.go`, `internal/format/types.go`

---

## Component Design (C4 Component Level)

```
MCP Client (Claude Code / LLM Agent)
    |
    | MCP stdio
    |
    v
┌─────────────────────────────────────────────────────┐
│  MCP Server (internal/mcp)                          │
│                                                     │
│  ┌──────────┐ ┌─────────┐ ┌──────────┐ ┌────────┐  │
│  │ search   │ │ context │ │ diff     │ │symbols │  │
│  │ handler  │ │ handler │ │ handler  │ │handler │  │
│  └──────────┘ └─────────┘ └──────────┘ └────────┘  │
│  ┌──────────┐ ┌──────────────────────────────────┐  │
│  │ deps     │ │ review handler (NEW)             │  │
│  │ handler  │ │  - git file list                 │  │
│  └──────────┘ │  - diff symbol lookup            │  │
│               │  - impact via Neighbors          │  │
│               │  - context assembly (scoped)     │  │
│               └──────────────────────────────────┘  │
└──────────┬──────────────────────────────┬───────────┘
           |                              |
           v                              v
┌──────────────────────┐    ┌─────────────────────────┐
│ QueryEngine          │    │ Store (storage)          │
│ (internal/core)      │    │ (internal/storage)       │
│                      │    │                          │
│ - Search(...)        │    │ - GetRecentDiffs(...)    │
│ - Context(...)       │    │ - GetDiffSymbols(...)    │
│ - HybridRank(...)    │    │ - Neighbors(...)  [MOD]  │
│ - Assemble(...)      │    │ - BatchNeighbors(...)    │
│                      │    │ - GetSymbolByID(...)     │
│                      │    │ - GetSymbolByName(...)   │
└──────────────────────┘    └─────────────────────────┘
                                       |
                                       v
                            ┌─────────────────────────┐
                            │ SQLite DB               │
                            │ - files, chunks, symbols│
                            │ - edges (has kind col)  │
                            │ - diff_log, diff_symbols│
                            │ - chunks_fts            │
                            └─────────────────────────┘

┌────────────────────────────┐
│ git package (NEW)          │
│ (internal/git)             │
│                            │
│ - DiffFiles(base, head)    │
│   -> []FileChange          │
│ - uses: os/exec "git"     │
│ - output: -z NUL-separated│
│ - isolated, testable      │
└────────────────────────────┘
```

**Key data flows for the `review` tool:**

```
1. review(base, head, budget=4096)
      |
      v
2. git.DiffFiles(base, head)
      |  -> ["internal/auth/login.go" (M), "internal/auth/token.go" (M), "internal/routes/auth.go" (A)]
      v
3. For each file: store.GetRecentDiffs(ctx, {FileID: id})
      |  -> diff_log entries with symbol-level changes
      v
4. For each changed symbol with symbol_id:
   store.Neighbors(ctx, symbolID, 1, "incoming")
      |  -> [{ID: 42, EdgeKind: "calls"}, {ID: 55, EdgeKind: "type_ref"}]
      v
5. Assemble context scoped to changed files:
   engine.Context(ctx, {Query: <auto>, BudgetTokens: budget, FileIDs: changedFileIDs})
      |  -> ranked, deduplicated chunks within budget
      v
6. Format and return unified response
```

**Modified interfaces:**

```go
// graph.go - Neighbors returns edge kinds
type NeighborResult struct {
    SymbolID int64
    EdgeKind string
}
func (s *Store) NeighborsWithKind(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]NeighborResult, error)

// format/types.go - DepResult gains edge kind
type DepResult struct {
    Name     string `json:"name"`
    Kind     string `json:"kind"`
    FilePath string `json:"file_path"`
    Line     int    `json:"line"`
    EdgeKind string `json:"edge_kind,omitempty"`  // NEW
}

// format/types.go - New types for review response
type ReviewResult struct {
    ChangedFiles []ReviewFile    `json:"changed_files"`
    Context      []ScoredResult `json:"context,omitempty"`
    Summary      ReviewSummary  `json:"summary"`
    Coverage     string         `json:"coverage"` // "full" | "partial"
    Freshness    string         `json:"freshness"` // "up_to_date" | "stale"
}

type ReviewFile struct {
    Path        string               `json:"path"`
    ChangeType  string               `json:"change_type"`
    Symbols     []ReviewSymbolChange `json:"symbols_changed,omitempty"`
}

type ReviewSymbolChange struct {
    Name       string         `json:"name"`
    ChangeType string         `json:"change_type"`
    Impact     []ImpactCaller `json:"impacted_by,omitempty"`
}

type ImpactCaller struct {
    Name     string `json:"name"`
    FilePath string `json:"file_path"`
    EdgeKind string `json:"edge_kind"`
}

type ReviewSummary struct {
    FilesChanged    int `json:"files_changed"`
    SymbolsChanged  int `json:"symbols_changed"`
    ImpactedCallers int `json:"impacted_callers"`
    ContextTokens   int `json:"context_tokens"`
}

// git/diff.go - New package
type FileChange struct {
    Path   string
    Status string // "A", "M", "D", "R"
}
func DiffFiles(repoRoot, base, head string) ([]FileChange, error)
```

---

## Open Questions

1. **Should `review` support uncommitted changes?** Adding `--cached` and working-tree diffs would cover pre-commit review, but increases the surface area. Defer to a follow-up if there's demand.

2. **Should the context assembly in review use a specialized ranking?** The current 5-signal ranker weights change recency at 15%. For review, change recency should arguably be 100% (we already know which files changed). A review-specific ranking profile may be worth exploring.

3. **What is the right default `budget_tokens` for review?** 4096 is a starting guess. Need empirical data from real MRs to calibrate.

4. **Should `NeighborsWithKind` replace `Neighbors` or coexist?** Coexistence avoids breaking existing callers (ranker uses `Neighbors` and doesn't need edge kinds). But maintaining two methods adds surface area. Recommendation: add `NeighborsWithKind` as a new method, keep `Neighbors` for backward compatibility.

5. **Should the git package handle merge commits?** `git diff base...head` uses three-dot (merge-base) semantics, which is correct for branch-based review. Two-dot (`base..head`) shows all commits, which may include merge artifacts. Default to three-dot; document the behavior.

---

## References

- `/Users/minimac/p/shaktiman/internal/mcp/tools.go` -- MCP tool definitions and handlers
- `/Users/minimac/p/shaktiman/internal/mcp/server.go` -- Tool registration
- `/Users/minimac/p/shaktiman/internal/storage/graph.go` -- Neighbors() BFS, edge resolution
- `/Users/minimac/p/shaktiman/internal/storage/diff.go` -- GetRecentDiffs, ComputeChangeScores
- `/Users/minimac/p/shaktiman/internal/storage/metadata.go` -- BatchNeighbors (line 797)
- `/Users/minimac/p/shaktiman/internal/core/assembler.go` -- Token budget assembly
- `/Users/minimac/p/shaktiman/internal/core/ranker.go` -- 5-signal hybrid ranker
- `/Users/minimac/p/shaktiman/internal/daemon/writer.go` -- Diff recording during enrichment
- `/Users/minimac/p/shaktiman/internal/format/types.go` -- Response format types
- `/Users/minimac/p/shaktiman/internal/types/interfaces.go` -- MetadataStore, BatchMetadataStore interfaces
- `/Users/minimac/p/shaktiman/internal/storage/schema.go` -- DB schema (edges, diff_log, diff_symbols)
- `/Users/minimac/p/shaktiman/docs/design/06-api-specification.md` -- API spec (include_impact at line 526)
- `/Users/minimac/p/shaktiman/docs/design/05-data-model.md` -- "indexes current working tree only" (line 32)

---

## Amendment 1 — 2026-03-31: Git-native symbol extraction (no branch switch required)

### Context

The original decision proposed that the `review` tool would "look up current symbols in the Shaktiman index" for changed files. Workflow analysis revealed this creates a branch-coupling problem:

- If the reviewer is on branch C and reviewing branch B against trunk A, the index reflects C — not A or B.
- The original design implicitly assumed the reviewer is on the base branch, requiring a stash → checkout → wait-for-reindex → review → checkout-back → stash-pop workflow.
- This is worse UX than just reading the diff directly.

The root cause: the Shaktiman index is a single working-tree snapshot (by design, per 05-data-model.md). It cannot simultaneously reflect multiple branches.

### Decision

**Replace index-dependent symbol lookup with on-the-fly tree-sitter parsing of git objects for changed files.**

The `review` tool extracts symbols from the BASE side of the diff by reading file contents directly from git objects, not from the index:

```
1. git diff A...B                          → file list + hunk line ranges
2. git show A:path/to/file.go             → base file content (from git objects, no checkout)
3. tree-sitter parse(base file content)    → symbols + line ranges for base version
4. intersect hunk `-` ranges with symbols  → which symbols were changed
5. query INDEX dependency graph            → callers of changed symbols (blast radius)
```

**Key properties:**
- Step 2 reads from git's object store, not the working tree. No checkout needed.
- Step 3 uses Shaktiman's existing tree-sitter parsers (same code that the enrichment pipeline uses).
- Step 5 uses the current index's dependency graph. This is an approximation — the graph reflects whichever branch the reviewer is on, not necessarily trunk. For most reviews, caller sets are stable across branches. This tradeoff is acceptable.
- The tool works from ANY branch. No stashing, no checkout, no re-indexing wait.

**For the `+` side (new code in the review branch):**

```
git show B:path/to/file.go → parse → symbols in the new version
```

This gives the reviewer both perspectives: what existed before (base symbols) and what exists after (head symbols), without the index needing to match either branch.

### Consequences

**Positive:**
- Review tool works from any branch — no workflow prerequisites.
- No dependency on watcher timing or index freshness for symbol extraction.
- Deterministic — same git objects always produce the same symbols.
- Existing tree-sitter parser code is reused; no new parsing infrastructure needed.

**Negative:**
- On-the-fly parsing adds latency (~ms per file, negligible for typical 3-10 file MRs).
- Dependency graph is approximate (from current branch, not base branch). Callers that exist only on trunk but not on the reviewer's branch will be missed. This is rare in practice.
- The `internal/git` package now needs both `git diff` and `git show` support, plus tree-sitter integration for on-the-fly parsing. This is more code than the original "query the index" approach.

### Changes to Original Design

**Layer 2 (Section 2a) is replaced:**
- ~~"For each changed file, look up its current symbols in the Shaktiman index"~~
- Now: "For each changed file, `git show base:file` → tree-sitter parse → symbols"

**Layer 3 (review tool, step 2) is replaced:**
- ~~"For each changed file, query `diff_symbols` for symbol-level changes"~~
- Now: "For each changed file, parse base and head versions from git objects, diff the symbol lists"

**`diff_log`/`diff_symbols` role is narrowed:**
- No longer the primary mechanism for identifying changed symbols in the review tool.
- Still used for: change recency scoring in the ranker (15% signal weight), the existing `diff` MCP tool's time-window queries.

---

## Amendment 2 — 2026-03-31: Three-way compare (overlap detection with reviewer's branch)

### Context

When a reviewer on branch C reviews branch B against trunk A, there may be files or symbols that BOTH branches modified relative to trunk. This overlap is invisible in a two-way diff (A vs B) but represents:

- **Merge conflict risk** — both branches touched the same symbol, merging both into trunk will likely conflict.
- **Reviewer context advantage** — the reviewer has deep knowledge of overlapping areas because they changed the same code.
- **Amplified blast radius** — risk isn't just B's callers; it's the interaction between B's and C's changes to the same symbol.

### Decision

**Add optional three-way overlap detection to the `review` tool.**

When the reviewer is on a branch other than trunk, the tool computes a third diff:

```
git diff A...B → symbols changed in review branch (already computed)
git diff A...C → symbols changed in reviewer's branch (new)
intersect      → symbols modified in both branches relative to trunk
```

The same on-the-fly parsing mechanism from Amendment 1 applies — `git show A:file` parsed with tree-sitter, hunk ranges mapped to symbols.

**Response includes an overlap section (only when non-empty):**

```json
{
  "changed_files": [...],
  "context": [...],
  "overlap": [
    {
      "symbol": "ValidateToken",
      "file": "auth/login.go",
      "in_review_branch": "signature_changed",
      "in_current_branch": "modified",
      "risk": "merge_conflict_likely",
      "callers": [
        {"name": "HandleLogin", "file": "routes/auth.go:88", "edge_kind": "calls"}
      ]
    }
  ],
  "summary": {
    "files_changed": 3,
    "symbols_changed": 5,
    "impacted_callers": 8,
    "overlap_symbols": 1,
    "context_tokens": 3500
  }
}
```

**Behavior when overlap is empty:**
- Reviewer is on trunk (C = A): `git diff A...C` produces nothing. Overlap section omitted.
- No shared changes: overlap section omitted.
- Zero noise in the common case.

### Consequences

**Positive:**
- Surfaces merge conflicts before they happen — reviewer can flag compatibility issues during review.
- Highlights areas where the reviewer has the most context — naturally focuses review attention.
- Cheap to compute — same git diff + tree-sitter pipeline, just one additional diff.
- Graceful degradation — when there's no overlap, the section is simply absent.

**Negative:**
- Adds one more `git diff` + parse pass. For large branches with many changes on C, this could be non-trivial. Mitigated by: only parsing files that appear in BOTH diffs (intersection of file lists first, then parse only overlapping files).
- "merge_conflict_likely" is a heuristic (same symbol modified ≠ guaranteed conflict). Could produce false positives if both branches modified different parts of the same function.

### Changes to Original Design

**Layer 3 (review tool) gains a new step:**
- After computing B-vs-A changes, optionally compute C-vs-A and intersect.
- New parameter: `include_overlap: bool` (default: true when C ≠ A, false when C = A).

**`ReviewResult` type gains:**
```go
type ReviewResult struct {
    // ... existing fields ...
    Overlap []OverlapEntry `json:"overlap,omitempty"`
}

type OverlapEntry struct {
    Symbol          string         `json:"symbol"`
    FilePath        string         `json:"file"`
    InReviewBranch  string         `json:"in_review_branch"`
    InCurrentBranch string         `json:"in_current_branch"`
    Risk            string         `json:"risk"`
    Callers         []ImpactCaller `json:"callers,omitempty"`
}
```

---

## Amendment 3 — 2026-03-31: Trunk auto-detection and Level 1 git awareness

### Context

The `review` tool needs to know the repository's trunk branch to serve as the default `base` parameter. The original ADR required `base` to be provided explicitly. Additionally, Shaktiman's branch switch detection uses a fragile heuristic (>20 file changes in <2 seconds) that produces false positives on large batch operations and false negatives on small branch switches.

### Decision

**Add Level 1 git awareness: detect current branch and trunk, but do NOT maintain per-branch indexes.**

**Trunk auto-detection (in order of precedence):**

1. **User override**: `base` parameter on the `review` tool, or a `trunk` setting in Shaktiman project config.
2. **`git symbolic-ref refs/remotes/origin/HEAD`**: Returns the remote's default branch (e.g., `refs/remotes/origin/master`). Works for any normally-cloned repo.
3. **Fallback heuristic**: Check if `main` or `master` exists as a local branch. Prefer `main` if both exist.
4. **Error**: If none of the above resolve, require explicit `base` parameter.

**Current branch detection:**

- Read `.git/HEAD` to determine the current branch (or detached HEAD state).
- Watch `.git/HEAD` via fsnotify for reliable branch switch detection, replacing the ">20 files in <2s" heuristic.
- Emit branch switch signal with the old and new branch names for logging/diagnostics.

**What this does NOT include:**
- No per-branch index caching (Level 2).
- No multi-branch indexes (Level 3).
- No git history traversal.
- The single-index, current-working-tree model is preserved.

### Consequences

**Positive:**
- `review` tool "just works" without specifying `base` in the common case.
- Branch switch detection becomes reliable (file-based watch on `.git/HEAD`) instead of heuristic.
- Foundation for future git integration if needed, without committing to per-branch indexes.
- Minimal scope: one file watch (`.git/HEAD`) + one git command for trunk detection.

**Negative:**
- `git symbolic-ref` can fail for repos without a remote (local-only repos, fresh `git init`). Fallback heuristic handles this.
- Watching `.git/HEAD` adds one more fsnotify watch. Negligible overhead.
- The `internal/git` package grows in scope: now handles `diff`, `show`, `symbolic-ref`, and HEAD reading. Still contained and testable.

### Changes to Original Design

**Layer 2 (Section 2a) is updated:**
- `base` parameter becomes optional (default: auto-detected trunk).
- New `internal/git` functions: `DetectTrunk(repoRoot) (string, error)`, `CurrentBranch(repoRoot) (string, error)`.

**Branch switch detection (watcher) is updated:**
- ~~">20 source files changed in <2 seconds" heuristic~~
- Now: fsnotify watch on `.git/HEAD`, emit branch switch signal on content change.
- The heuristic is retained as a secondary signal for edge cases where `.git/HEAD` watch fails (e.g., worktrees with separate `.git` files).

**Review tool default behavior:**
```
review(head="feature-branch")
  → base auto-detected as "master" via git symbolic-ref
  → current branch detected as "some-other-branch" via .git/HEAD
  → three-way compare: trunk vs review-branch vs current-branch
```

**New `internal/git` package surface:**
```go
// git/detect.go
func DetectTrunk(repoRoot string) (string, error)
func CurrentBranch(repoRoot string) (string, error)

// git/diff.go
func DiffFiles(repoRoot, base, head string) ([]FileChange, error)
func DiffHunks(repoRoot, base, head, filePath string) ([]Hunk, error)

// git/show.go
func ShowFile(repoRoot, ref, filePath string) ([]byte, error)

// git/watch.go
func WatchHEAD(repoRoot string) (<-chan BranchChange, error)
```
