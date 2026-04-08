# Parser Bugs Uncovered During Recursive Chunking Implementation

**Date:** 2026-04-08
**Context:** Bugs found while implementing ADR-004 (recursive AST-driven chunking). Some are pre-existing and were hidden by the old whitelist-driven traversal; others are conceptual issues exposed by the new recursive approach. None are language-specific — they affect the parser architecture as a whole.

---

## Symbol Extraction Bugs

### 1. `extractName` returns type name instead of variable name for field-like declarations — **RESOLVED**

**Status:** Fixed in `internal/parser/chunker.go` — `extractName` now:

1. Keeps the existing `ChildByFieldName("name"|"property"|"function")` fast path that already covers Python function_definition, Java class_declaration, Rust struct_item, Go type_spec, etc.
2. Explicitly looks for a nested `variable_declarator` child **before** falling back to the generic walk. Java's `field_declaration` / `local_variable_declaration` nest the variable name inside `variable_declarator`, so this delegates to extractName on the declarator which then hits the `name` field path.
3. In the top-level walk, excludes `type_identifier` children because they are almost always type annotations in the contexts that reach the walk. Legitimate type-name contexts (e.g. Go `type_spec`) already succeed via `ChildByFieldName("name")` above.
4. Preserves a final-fallback loop that still accepts a sibling `type_identifier` for uncatalogued corner cases, so the change is strictly additive for existing well-formed paths.

Regression covered by `TestExtractName_JavaField` which calls `extractName` directly on a parsed Java `field_declaration` AST node and asserts `"executor"` is returned instead of `"ExecutorService"`. Bug #2 (multi-declarator handling) will build on this — once Java `field_declaration` is re-added to `SymbolKindMap` with a specialized extractor, every variable in `int x = 1, y = 2;` gets its own symbol.

### 2. Multi-declarator declarations lose all but the first name — **RESOLVED (Java)**

**Status:** Fixed for Java `field_declaration`. Added an `extractJavaFieldSymbols` specialized extractor in `internal/parser/symbols.go` that iterates every `variable_declarator` child and emits one symbol per declarator. Wired into `walkForSymbols`'s dispatch switch alongside the existing TypeScript/JavaScript `extractVariableSymbols` and Go `extractGoVarSymbols` handlers.

Details:
- `internal/parser/languages.go` — Java config re-registers `field_declaration` in `SymbolKindMap` (unblocked by bug #1 fix).
- `internal/parser/symbols.go` — `extractJavaFieldSymbols` dispatches per `variable_declarator`, derives visibility from the `modifiers` child (public/private/protected → public/private/internal), and indexes `static final` fields as `"constant"` while regular fields are `"variable"`.
- Regression covered by `TestParse_JavaMultiDeclaratorField` which asserts all five declarators in
  ```java
  public int x = 1, y = 2, z = 3;
  private static final String name = "counter", label = "cnt";
  ```
  are indexed (with `String` NOT showing up as a spurious type-name symbol, guarding against bug #1 regression).

**Still missing handlers for:**
- Rust grouped `const_item` / `static_item` (if they can be grouped — tree-sitter-rust usually emits one `const_item` per declaration, so this may be moot).
- Java `local_variable_declaration` inside method bodies. Not currently reached because `walkForSymbols` does not recurse into `method_declaration`, consistent with Go and TypeScript behavior. Can be added if per-method-scope symbol indexing becomes a requirement.

### 3. Class fields were never extracted as symbols at all (pre-existing, hidden) — **RESOLVED by bugs #1 + #2**

**Status:** Resolved transitively. Bug #1 fixed `extractName` so `variable_declarator` wins over `type_identifier`. Bug #2 re-registered Java `field_declaration` in `SymbolKindMap` with a specialized multi-declarator extractor. Class fields are now indexed through the recursive `walkForSymbols` path (which can reach them via the ADR-004 rewrite).

The pre-existing root cause — `walkForSymbols` gating class body recursion on a `ClassBodyTypes` whitelist that excluded field types — was already fixed by ADR-004 itself. Bug #3 existed as a standalone entry to track that even after the recursion fix, fields were still disabled due to bug #1. With bugs #1 and #2 both resolved, fields are live in the index.

Regression covered by `TestParse_JavaMultiDeclaratorField`.

---

## Chunker / Architecture Bugs

### 4. `ChunkableTypes` conflates two different concepts — **RESOLVED**

**Status:** Fixed. `ChunkableTypes` is now `map[string]NodeMeta` with a `NodeMeta` struct carrying both the chunk kind and an `IsContainer` flag. `walkForSymbols` reads `IsContainer` directly — the hardcoded kind-string switch and the `case "impl_item", "mod_item", ..."` whitelist are gone.

- `internal/parser/languages.go` — `NodeMeta{Kind, IsContainer}` struct added. All 9 language configs rewritten to use it. Containers flagged: Java `class_declaration`, `interface_declaration`, `record_declaration`, `static_initializer`; TypeScript `class_declaration`, `abstract_class_declaration`, `interface_declaration`, `internal_module`, `export_statement`, `ambient_declaration`; Python `class_definition`, `decorated_definition`; Rust `trait_item`, `impl_item`, `mod_item`, `foreign_mod_item`; JavaScript `class_declaration`, `export_statement`; Ruby `class`, `module`, `singleton_class`.
- `internal/parser/chunker.go` — `kind := cfg.ChunkableTypes[nodeType].Kind` replaces the old `string` lookup.
- `internal/parser/symbols.go` — the hardcoded whitelist and kind-string switch are replaced with `shouldRecurse = cfg.ChunkableTypes[nodeType].IsContainer`. Adding a new container kind to a language config no longer requires touching the walker.
- Regression covered by `TestSymbols_ContainerRecursionDrivenByIsContainerFlag` which mutates Java's cached config at runtime, renames `class_declaration`'s chunk kind to `"custom_container"` while keeping `IsContainer: true`, and asserts that method symbols inside the class body are still extracted.

### 5. `decorated_definition` wrapper kind resolution depends on `findDeclarationChild` — **RESOLVED**

**Status:** Fixed. `findDeclarationChild` now takes a `*LanguageConfig` parameter and uses `cfg.ChunkableTypes` as the single source of truth for unwrap targets. The hardcoded `declTypes` map is gone.

- `internal/parser/chunker.go` — `findDeclarationChild(node, cfg)` first checks tree-sitter's `declaration` field (used by `export_statement`), then scans named children for any kind registered in `cfg.ChunkableTypes`, skipping wrapper kinds (`cfg.ExportType`, `cfg.AmbientType`, and empty-kind entries like Python `decorated_definition`) so nested wrappers unwrap correctly.
- All five call sites (`chunkFile`, `chunkNode`, `findChunkableChildren`) now pass `cfg`.
- Regression covered by `TestFindDeclarationChild_UnwrapsViaConfig` which parses `@staticmethod def foo(x): ...`, asserts `decorated_definition` does NOT expose a `declaration` field, then verifies `findDeclarationChild` returns the inner `function_definition`. A follow-up assertion deletes `function_definition` from the cached cfg and verifies the helper returns nil — proof that the lookup reads from cfg and not a hardcoded list.

### 6. Signature builder strips comments via string prefix matching

**Location:** `internal/parser/chunker.go` — `buildSignatureFromExtracted`

**Current implementation:**
```go
if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") ||
    strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
    strings.HasPrefix(trimmed, "\"\"\"") || strings.HasPrefix(trimmed, "'''") {
    continue
}
```

**Problem:** Line-prefix comment detection is brittle. A multi-line string starting with `"""` is a Python docstring, not a comment in the AST sense. A C-style block comment spanning multiple lines won't all start with `*`. Edge cases can leak comment text into signatures.

**Proper fix:** Walk the node's children via tree-sitter, skip `comment` nodes, and reconstruct the declaration from the source bytes between the non-comment tokens.

### 7. Depth guard silently falls back to line splitting

**Location:** `internal/parser/chunker.go` — `chunkNode`, `maxChunkDepth`

**Problem:** When recursion depth exceeds `maxChunkDepth` (10), the node is fallback-split by lines. No log, no metric, no warning. If a pathological AST triggers this, chunk quality silently degrades with no visibility.

**Proper fix:** Emit a structured log or metric when the depth guard triggers, including file path and node type.

### 11. Size-gate contradiction: `chunkNode` decomposes small containers eagerly — **RESOLVED**

**Status:** Fixed by adding an early-return size gate at the top of `chunkNode`:

```go
if tokens <= maxChunkTokens {
    return []types.ChunkRecord{{ ... single chunk for this node ... }}
}
```

This runs before `findChunkableChildren` so small containers stay whole instead of being replaced with a signature summary + child chunks. Matches ADR-004 §"Core Algorithm" and §"Key behaviors" (*"A 20-line module with a 15-line class doesn't decompose"*).

- `internal/parser/chunker.go` — size gate added immediately after name extraction, before the depth guard.
- `internal/parser/parser_test.go`:
  - Regression covered by new `TestChunk_SmallContainerEmittedAsSingleChunk`, which asserts a ~60-token Ruby class with three methods emits as exactly one `class` chunk containing full method source.
  - `TestParse_ClassWithMethods` — chunk-based method assertions converted to symbol assertions; the small TS class now emits one chunk.
  - `TestParse_RubyNestedModuleClass` — dropped the `classChunks >= 2` assertion; symbol assertions remain the real coverage for nested container recursion.
  - `TestParse_JavaStaticInitializer` — reworded to assert the static initializer body survives inside the parent class chunk (instead of requiring a separate `block` chunk).
  - `TestParse_DepthGuardBoundary` — enlarged the fixture with a 120-line `FILLER_DATA` array inside `L11` so every nesting level inherits > 1024 tokens, preserving the bug #12 depth-recursion regression test under the new size gate.

### 12. Depth guard off-by-one — **RESOLVED**

**Location:** `internal/parser/chunker.go` — `chunkNode` depth guard.

**Status:** Fixed. The check now reads `if depth > maxChunkDepth` matching ADR-004 §6 pseudocode. With `maxChunkDepth = 10` the chunker handles 11 levels of nested chunkable containers before the fallback kicks in. Covered by `TestParse_DepthGuardBoundary` which uses 11 levels of nested Ruby modules and asserts the innermost method is extracted as a function chunk.

### 13. Schema CHECK constraint rejects `namespace` symbol kind — **RESOLVED**

**Status:** Fixed by adding `'namespace'` to the `symbols.kind` CHECK constraint in both sqlite and postgres schemas.

- `internal/storage/migrations/sqlite/002_symbols_kind_namespace.sql` — rewrites the `symbols` table with the expanded CHECK (SQLite can't ALTER a CHECK in place).
- `internal/storage/migrations/postgres/005_symbols_kind_namespace.sql` — `DROP CONSTRAINT` + `ADD CONSTRAINT` with the expanded list.

Regression covered by `TestIndex_TypeScriptNamespaceSymbolPersists` which indexes `testdata/typescript_project/namespace.ts` through the full daemon pipeline and asserts `store.GetSymbolByName(ctx, "Validators")` returns a symbol with `kind == "namespace"`.

---

## Edge Extraction Bugs (Pre-existing, Out of Scope for ADR-004)

Noted during research for ADR-004 but not addressed. These affect the dependency graph and MCP `dependencies` tool accuracy.

### 8. Call expression resolution drops the receiver

**Location:** `internal/parser/edges.go:389` — `resolveCallee`

**Problem:** `obj.method()` becomes an edge to `method`, losing `obj`. `a.foo()` and `b.foo()` are indistinguishable in the call graph. Same for Go selectors (`pkg.Func`) and Python attributes (`obj.method`).

**Impact:** False positive connections in the dependency graph. Method calls on different receivers merge into a single edge target.

### 9. Recursive calls are silently dropped

**Location:** `internal/parser/edges.go:110`

**Code:**
```go
if callee != "" && callee != newOwner {
    ctx.addEdge(newOwner, callee, "calls")
}
```

**Problem:** The `callee != newOwner` filter drops self-calls, which means recursive functions show no self-edge in the call graph. Also catches cases where a function calls a different function with the same name (method overriding, same-name helpers in different scopes).

### 10. Generic type arguments are skipped entirely

**Location:** `internal/parser/edges.go:425-427`

**Code:**
```go
if n.Kind() == "type_arguments" {
    return
}
```

**Problem:** `List<String>` misses `String`, `Box<MyType>` misses `MyType`, `HashMap<K, V>` misses both. Rust and Java lose significant type dependency information.

---

## Priority for Follow-Up

**Highest impact:**
- ~~Bug #1 + #2: Field and multi-declarator symbol extraction~~ — **RESOLVED**
- ~~Bug #4: `ChunkableTypes` conflation~~ — **RESOLVED**
- ~~Bug #13: `namespace` symbol kind rejected by schema CHECK constraint~~ — **RESOLVED**

**Medium impact:**
- Bug #8: Call expression receiver loss. Affects graph accuracy significantly.
- ~~Bug #5: `findDeclarationChild` whitelist maintenance burden~~ — **RESOLVED**
- ~~Bug #11: Size-gate contradiction~~ — **RESOLVED**

**Lower impact:**
- Bug #6: Signature comment stripping (works for common cases).
- Bug #7: Depth guard observability.
- Bug #9: Recursive call tracking.
- Bug #10: Generic type arguments.
- ~~Bug #12: Depth guard off-by-one~~ — **RESOLVED**
