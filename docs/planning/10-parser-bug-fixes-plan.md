# Parser Bug Fixes — Implementation Plan

**Date:** 2026-04-08
**Source:** [docs/review-findings/parser-bugs-from-recursive-chunking.md](../review-findings/parser-bugs-from-recursive-chunking.md)
**Scope:** All 13 bugs tracked in the review findings.
**Approach:** Red-Green-Refactor loop, one bug at a time.

---

## Context

ADR-004 (recursive AST-driven chunking) landed with 13 tracked follow-up
bugs in `docs/review-findings/parser-bugs-from-recursive-chunking.md`. They
span symbol extraction (#1-3), chunker architecture (#4-7, #11-12), schema
integration (#13), and edge extraction (#8-10).

We are fixing all 13. Two upfront decisions are locked in:

- **Bug #11 (size-gate):** Option 1 — fix the code. Add the early-return
  `tokens <= maxChunkTokens` at the top of `chunkNode` to match ADR-004
  §"Core Algorithm". Existing tests that assert 2+ chunks on small fixtures
  will be updated to use larger inputs.
- **Bug #4 (container/leaf conflation):** NodeMeta struct. Change
  `ChunkableTypes map[string]string` to `map[string]NodeMeta` where
  `NodeMeta = { Kind string, IsContainer bool }`. Single source of truth;
  `walkForSymbols` and `chunkNode` both read `IsContainer` instead of
  maintaining hardcoded whitelists.

---

## Implementation Loop (Red → Green → Refactor)

**Every bug fix must follow this loop.** Do not batch fixes. Do not skip
steps. The discipline is the point.

### Loop protocol

1. **RED** — Write a failing test case that demonstrates the bug.
   - Must fail on the current `master`/branch-head code.
   - Must assert on observable behavior (symbol list, chunk list, edge
     list, stored DB state, captured log output), not internal state.
   - Run the test. Confirm it fails **for the expected reason** — not a
     compile error, not a panic elsewhere. The failure message should name
     the symptom in the bug doc.
   - Commit the failing test with message
     `test(parser): red for bug #N — <short description>`.

2. **GREEN** — Apply the minimal fix that makes the test pass.
   - Change only what the failing test drives.
   - Run the new test → must pass.
   - Run the full affected package suite → must stay green.
     (`go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./internal/parser/... ./internal/daemon/... ./internal/storage/sqlite/...`)
   - Commit the fix with message `fix(parser): bug #N — <description>`.

3. **REFACTOR** — Clean up without changing behavior.
   - Only if the fix introduced duplication, awkward naming, or an obvious
     improvement.
   - Tests remain green throughout. No new assertions.
   - Commit (if changes were made) with message
     `refactor(parser): clean up after bug #N fix`.

4. **DOC** — Update `parser-bugs-from-recursive-chunking.md`.
   - Change the bug's status line to **RESOLVED (<commit-sha>)** and
     move it to a new "Resolved" section at the bottom of the doc, or
     strike through in place. Pick one style on the first fix and stick
     to it.
   - Amend the fix commit or add a separate doc commit.

5. **STOP** — Before starting the next bug, verify:
   - Full test suite is green.
   - Bugs doc is up to date.
   - Working tree clean.
   - If any of the above is false, resolve it before proceeding.

### Loop invariants

- **Never commit a red test without a green follow-up in the same session.**
- **Never merge fixes between bugs.** Each bug gets its own commit (or
  small commit chain: red test, fix, optional refactor, doc update).
- **If a fix breaks another test**, that's a signal the fix is too broad or
  surfaces another bug. Stop, investigate, decide whether to narrow the fix
  or file a new bug.

---

## Execution Order

Ordered for: (a) easy wins first to validate the RGR loop, (b) behavior
changes before refactors, (c) big refactors (#4) as dedicated phase, (d)
edge extraction cluster at the end.

| # | Bug | Category | Difficulty | Why this order |
|---|---|---|---|---|
| 1 | #12 | Depth guard off-by-one | Trivial | Validates RGR loop on the simplest possible fix |
| 2 | #13 | Namespace schema CHECK | Small | Needs a migration; full-pipeline test validates the new DB path |
| 3 | #11 | Size-gate contradiction | Medium | Behavior change — do before refactors so later tests target the correct output shape |
| 4 | #1 | extractName field vs type | Small | Unblocks field symbol extraction |
| 5 | #2 | Multi-declarator loss | Small | Builds on #1 for Java fields |
| 6 | #3 | Historical fields note | Doc-only | Resolved by #1 — mark closed in doc |
| 7 | #4 | NodeMeta refactor | Large | Touches all 9 language configs + chunker + symbols; tests from earlier fixes validate the refactor |
| 8 | #5 | findDeclarationChild whitelist | Medium | Builds on #4 — add `IsWrapper` / `WrappedKinds` to NodeMeta |
| 9 | #6 | Signature comment stripping | Medium | AST-walk rewrite of buildSignatureFromExtracted |
| 10 | #7 | Depth guard observability | Small | Structured log; assert via captured slog handler |
| 11 | #8 | Call receiver drop | Medium | edges.go — qualified call representation |
| 12 | #9 | Recursive call drop | Small | edges.go — remove the `callee != newOwner` filter carefully |
| 13 | #10 | Generic type args | Medium | edges.go — recurse into `type_arguments` instead of skipping |

---

## Per-Bug Fix Blueprints

Each blueprint is an **outline**, not a spec. During the RED phase you may
discover the actual test shape differs — adjust and document.

### Bug #12: Depth guard off-by-one

**RED:** Construct a synthetic AST scenario where `maxChunkDepth`
recursion is reached. Since building a deeply-nested real source is
impractical, assert the constant behavior via a unit test on
`chunker.go:139` — alternative: test a real deeply-nested Ruby module
tree and assert depth-10 nodes are emitted as chunks (not line-split).

**GREEN:** Change `if depth >= maxChunkDepth` → `if depth > maxChunkDepth`
at `chunker.go:139`. OR bump `maxChunkDepth` to 11. Document the chosen
bound in the comment.

**Files:** `internal/parser/chunker.go`, `internal/parser/parser_test.go`.

---

### Bug #13: Namespace schema CHECK constraint

**RED:** Write a test that exercises the full index pipeline on
`testdata/typescript_project/namespace.ts` and asserts
`store.GetSymbolByName(ctx, "Validators")` returns a symbol with
`Kind == "namespace"`. This test fails today with the CHECK constraint
error observed in `TestEnsureParserVersion_PurgesOnMismatch` logs.

**GREEN:** Create a new goose migration in
`internal/storage/migrations/sqlite/` and
`internal/storage/migrations/postgres/` that adds `"namespace"` to the
`symbols.kind` CHECK constraint.

- SQLite: rewrite the `symbols` table (SQLite doesn't support
  `ALTER TABLE ALTER CONSTRAINT`). Copy data, drop old, rename new.
- Postgres: `ALTER TABLE symbols DROP CONSTRAINT ... ADD CONSTRAINT ...`.

Verify against both backends (sqlite tests + postgres compliance tests).

**Files:**
- `internal/storage/migrations/sqlite/NNN_*.sql` (new)
- `internal/storage/migrations/postgres/NNN_*.sql` (new)
- `internal/daemon/daemon_test.go` (new test)

---

### Bug #11: Size-gate contradiction (decided: fix code)

**RED:** Write a new test `TestChunk_SmallContainerEmittedAsSingleChunk`
with a small Ruby/Java fixture (< 200 tokens) that has multiple methods.
Assert exactly 1 chunk is produced (plus header if applicable), not a
signature + member chunks.

**GREEN:** Add early-return at the top of `chunkNode`:

```go
content := node.Utf8Text(source)
tokens := p.tokens.Count(content)
if tokens <= maxChunkTokens {
    return emitAsSingleChunk(node, source, content, tokens, cfg)
}
```

Update tests that assert 2+ chunks on tiny fixtures
(`TestParse_RubyNestedModuleClass`, `TestParse_JavaInnerClass`,
`TestParse_PythonNestedClass`, `TestParse_RustTraitWithMethods`,
`TestParse_RustModuleWithItems`, `TestParse_JavaInterfaceWithDefaults`,
`TestParse_TypeScriptNamespace`, `TestParse_PythonDecoratedMethodsInClass`,
`TestParse_RubyNestedModuleClass`) to use fixtures large enough to
trigger real decomposition. Aim for ~1500+ token fixtures that force
`tokens > maxChunkTokens`.

**Files:**
- `internal/parser/chunker.go`
- `internal/parser/parser_test.go` (update ~8 tests, add 1 new test)

---

### Bug #1: extractName returns type name for fields

**RED:** Write `TestExtractName_JavaField` with a Java field declaration
`private final ExecutorService executor;` parsed to an AST. Assert
`extractName(node, source) == "executor"`. Today returns `"ExecutorService"`.

Also unblock: re-add `"field_declaration": "block"` to Java's
`SymbolKindMap` with kind `"variable"` so fields become indexed symbols.
Note: this means the test assertion can be end-to-end via
`result.Symbols`, not unit-level against `extractName`.

**GREEN:** In `extractName` at `chunker.go:426`:
- Prefer the `declarator` → `variable_declarator.name` field path for
  `field_declaration` and `local_variable_declaration`.
- Skip `type_identifier` children during the walk fallback.
- Keep existing behavior for classes/functions where the type walk was
  already correct.

**Files:** `internal/parser/chunker.go`, `internal/parser/languages.go`,
`internal/parser/parser_test.go`.

---

### Bug #2: Multi-declarator loss

**RED:** Test `TestParse_JavaMultiDeclaratorField` with
`public int x = 1, y = 2, z = 3;`. Assert 3 symbols with names `x`, `y`, `z`.

**GREEN:** Add case in `walkForSymbols` dispatch:

```go
case "field_declaration", "local_variable_declaration":
    p.extractJavaVariableSymbols(node, source, exported, chunks, symbols)
    return
```

Implement `extractJavaVariableSymbols` modeled on `extractVariableSymbols`
but walking `variable_declarator` children of Java
`field_declaration`/`local_variable_declaration`.

**Files:** `internal/parser/symbols.go`, `internal/parser/parser_test.go`.

---

### Bug #3: Historical note — fields never extracted (pre-existing)

**Doc-only:** After bugs #1 + #2 land, update the bugs doc to mark #3
as **RESOLVED by #1+#2**. No separate code change.

---

### Bug #4: NodeMeta refactor

This is the largest change. Allocate a full RGR cycle with extra care.

**RED:** Write two tests:
1. `TestSymbols_NewContainerTypeRecursesWithoutSourceEdit` — a harness test
   that registers a synthetic `NodeMeta{IsContainer: true}` in a test-only
   config and verifies `walkForSymbols` recurses into it without editing
   the `shouldRecurse` switch.
2. `TestSymbols_LeafNodeDoesNotRecurse` — verify `IsContainer: false` nodes
   (e.g., `field_declaration`, `const_item`) don't recurse to avoid
   extracting type identifiers as symbols.

**GREEN:** Refactor:
- `internal/parser/languages.go`: introduce
  ```go
  type NodeMeta struct {
      Kind        string // "function", "class", etc.
      IsContainer bool   // true if children should be recursed for symbols/chunks
      IsWrapper   bool   // true for export_statement, ambient_declaration, decorated_definition
  }
  ```
  Change all 9 language configs' `ChunkableTypes` to `map[string]NodeMeta`.
  Annotate each entry with correct `IsContainer` (classes, modules, traits,
  namespaces, impl blocks, extern blocks → true; functions, fields,
  consts, type aliases → false).
- `internal/parser/chunker.go`: update lookups. `kind` comes from
  `NodeMeta.Kind`. `findChunkableChildren` continues as-is; `chunkNode`
  reads `NodeMeta` instead of `string`.
- `internal/parser/symbols.go`: delete the hardcoded container case
  statement. `shouldRecurse = cfg.ChunkableTypes[nodeType].IsContainer`.

**REFACTOR:** Consolidate any duplicated `Kind` lookups. Ensure
`ChunkableTypes[x].Kind` is the single source for chunk kind.

**Files:**
- `internal/parser/languages.go` (all 9 configs)
- `internal/parser/chunker.go`
- `internal/parser/symbols.go`
- `internal/parser/parser_test.go`

---

### Bug #5: findDeclarationChild whitelist

**RED:** Write a test that adds a new wrapper node type in a test config
and verifies unwrap works without editing `findDeclarationChild`.

**GREEN:** Derive the whitelist from the union of all
`cfg.ChunkableTypes` where `NodeMeta.IsWrapper == false` (i.e., the
declaration types that can appear inside wrappers). Alternatively, use
the new `IsWrapper` flag from bug #4 — a wrapper unwraps to any non-wrapper
chunkable descendant of that config.

**Files:** `internal/parser/chunker.go`.

---

### Bug #6: Signature comment stripping via AST

**RED:** Write `TestSignature_PythonDocstringNotStripped` with a class
whose first method has a `"""docstring"""`. The docstring is a string
literal, not a comment. Assert the signature does not drop the method.

Also: `TestSignature_CBlockCommentNotLeaked` with a Java class containing
a `/* multi\n * line\n */` comment between methods. Assert the comment
text does not appear in the signature.

**GREEN:** Rewrite `buildSignatureFromExtracted` to walk the node's
tree-sitter children, skip `comment`-kind nodes, and reconstruct the
declaration from source bytes between non-comment tokens. Remove the
string-prefix fallback at `chunker.go:265-270`.

**Files:** `internal/parser/chunker.go`, `internal/parser/parser_test.go`.

---

### Bug #7: Depth guard observability

**RED:** Write `TestChunk_DepthGuardEmitsLog` that uses a custom
`slog.Handler` to capture output, parses a synthetic or real
deeply-nested source, and asserts a warning-level log is emitted
containing `max_chunk_depth` or similar key when the guard fires.

**GREEN:** In `chunker.go:139`, add a `slog.Warn` call before falling back
to line splitting:

```go
if depth > maxChunkDepth {
    slog.Warn("parser depth guard triggered",
        "node_type", nodeType, "depth", depth, "max", maxChunkDepth)
    return p.splitNodeByLines(node, source, name, kind)
}
```

Plumb file path if easily accessible through the parser context.

**Files:** `internal/parser/chunker.go`, `internal/parser/parser_test.go`.

---

### Bug #8: Call expression receiver drop

**RED:** Write `TestEdges_MemberCallPreservesReceiver` with a TypeScript
file `a.foo(); b.foo();` and a Go file `pkg1.Func(); pkg2.Func();`. Assert
distinct edges: `→ a.foo`, `→ b.foo`, `→ pkg1.Func`, `→ pkg2.Func`.

**GREEN:** In `resolveCallee` at `edges.go:389`, return a qualified name
`receiver.property` for `member_expression`, `selector_expression`, and
`attribute` node kinds. Collect the receiver identifier from the object
child; concatenate with `.`. If receiver is itself a member expression
(`a.b.c()`), recurse.

Note: this changes the shape of edge target names. Edge resolution in the
dependency graph uses name matching; qualified names may reduce false
positives but may also break some existing edge hits. Verify against the
`dependencies` MCP tool output.

**Files:** `internal/parser/edges.go`, `internal/parser/edges_test.go` (or
parser_test.go).

---

### Bug #9: Recursive call drop

**RED:** Write `TestEdges_RecursiveFunctionHasSelfEdge` with a source like
`func fact(n int) int { if n == 0 { return 1 }; return n * fact(n-1) }`.
Assert an edge exists from `fact → fact` with kind `calls`.

**GREEN:** Remove the `callee != newOwner` filter at `edges.go:110`. Audit
the same-name collision concern: when a function calls another function
with the same name (method overriding, same-name helpers), we may now
emit a self-edge that isn't actually recursive. That's a resolution-layer
concern — the parser should record the syntactic edge; the graph layer
can deduplicate.

**Files:** `internal/parser/edges.go`, `internal/parser/edges_test.go`.

---

### Bug #10: Generic type args

**RED:** Write `TestEdges_JavaGenericTypeArgumentsExtracted` with
`private Map<String, List<User>> users;`. Assert edges to `Map`, `String`,
`List`, `User` all exist from the enclosing class.

**GREEN:** In `extractHeritageTypeNames` at `edges.go:425-427`, remove the
`type_arguments` skip and recurse into its children. Same for any other
type-walk helpers that skip `type_arguments`.

Note: this may also apply to non-heritage type contexts (field types,
parameter types, return types). Check whether bug #10 is scoped to
heritage only or all type extraction. If broader, expand the test.

**Files:** `internal/parser/edges.go`, `internal/parser/edges_test.go`.

---

## Verification

### Per-bug
Each RGR cycle verifies itself by the test suite plus the bug's specific
test. Before moving on, run:

```bash
go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" \
    ./internal/parser/... \
    ./internal/storage/sqlite/... \
    ./internal/daemon/...
```

Postgres spot-check (needs Postgres available):
```bash
go test -tags "postgres pgvector" ./internal/storage/postgres/...
```

### End-to-end (after all 13)
1. Full test suite: `go test -race -tags "sqlite_fts5 sqlite bruteforce hnsw" ./...`
2. Postgres build: `go build -tags "postgres pgvector" ./...`
3. Index the shaktiman repo itself via the daemon CLI
   (`./shaktiman index` or equivalent). Verify:
   - Namespace symbols from any TypeScript test fixtures land in the DB.
   - Class fields are in the symbol index for Java fixtures.
   - Recursive call edges exist in the call graph for known recursive fns.
   - `mcp__shaktiman__symbols name:"..."` returns previously missing symbols.
4. Update `docs/review-findings/parser-bugs-from-recursive-chunking.md`
   with final status. Every bug should be marked RESOLVED or moved to a
   "Resolved" section at the bottom.

### Exit criteria

- 13/13 bugs marked RESOLVED in the bugs doc.
- Zero failing tests.
- Both sqlite and postgres builds clean.
- No new TODO or FIXME comments added without a tracking entry.
- `parser.ChunkAlgorithmVersion` bumped to `"3"` (since chunk boundaries
  may shift from bug #11 + #4 + #6) so deployed installs auto-reindex.

---

## Rollback strategy

Each bug fix is an independent commit. If a fix causes a regression
discovered later, `git revert` the specific commit. The RGR discipline
makes each commit small enough to revert cleanly without unraveling
unrelated work.

The one commit that is harder to revert is bug #4 (NodeMeta refactor)
because it touches every language config. Make that commit self-contained
— no other bug fixes in the same commit — so a revert is still surgical.

---

## Notes on RGR fidelity

This plan treats the RGR loop as **non-negotiable**. Temptations to skip:

- *"This fix is one character; do I really need a test first?"* — Yes.
  Bug #12 is the canonical one-character fix. It gets a RED test like any
  other, because the loop is training wheels as much as it is discipline.
- *"The refactor step is empty."* — Skip it for that bug. Don't invent
  refactors just to fill the slot.
- *"Two bugs are related; can I fix them together?"* — No. Each bug gets
  its own RED → GREEN → DOC cycle. You can batch the tests into the same
  file but keep commits separate.
- *"The test is hard to write."* — That's the bug telling you it's
  observability-hostile. Fix the observability first (separate RGR) or
  accept a coarser test (e.g., log capture instead of metric assertion).

---

## Open questions

None. Both open decisions (#11 direction, #4 shape) are resolved.
