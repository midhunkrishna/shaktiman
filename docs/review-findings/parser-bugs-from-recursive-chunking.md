# Parser Bugs Uncovered During Recursive Chunking Implementation

**Date:** 2026-04-08
**Context:** Bugs found while implementing ADR-004 (recursive AST-driven chunking). Some are pre-existing and were hidden by the old whitelist-driven traversal; others are conceptual issues exposed by the new recursive approach. None are language-specific — they affect the parser architecture as a whole.

---

## Symbol Extraction Bugs

### 1. `extractName` returns type name instead of variable name for field-like declarations

**Location:** `internal/parser/chunker.go:403` (`extractName`)

**Symptom:** For a Java field `private final ExecutorService executor;`, `extractName` returns `"ExecutorService"` instead of `"executor"`.

**Root cause:** `extractName` walks named children looking for the first `identifier` / `type_identifier` / `constant` match. Type identifiers often appear before the variable name in child order, so the type wins.

**Affected patterns:**
- Java `field_declaration` (confirmed)
- Likely Rust `const_item` / `static_item` if the type comes before the name
- Likely TypeScript `public_field_definition` with type annotations

**Status:** Worked around in ADR-004 by removing `field_declaration` from Java's `SymbolKindMap`. The bug exists in the code but is no longer triggered. A proper fix requires preferring explicit `name` fields or excluding `type_identifier` children from the walk.

**Hidden before recursive chunking:** This code path was dead — `walkForSymbols` never reached `field_declaration` because `ClassBodyTypes` didn't include it.

### 2. Multi-declarator declarations lose all but the first name

**Location:** `internal/parser/symbols.go`, default branch of `walkForSymbols`

**Symptom:** Declarations like `int x = 1, y = 2;` (Java) only extract one symbol. The tree has a `field_declaration` / `local_variable_declaration` with multiple `variable_declarator` children, but `extractName` returns the first match and stops.

**Specialized handlers exist for:**
- TypeScript/JavaScript `lexical_declaration` / `variable_declaration` → `extractVariableSymbols`
- Go `var_declaration` / `const_declaration` → `extractGoVarSymbols`
- Go `type_declaration` → `extractGoTypeSymbols`

**Missing handlers for:**
- Java `field_declaration` and `local_variable_declaration`
- Rust grouped `const_item` / `static_item` (if they can be grouped)
- Any other language with grouped declarations

**Fix pattern:** Add a language-specific dispatch case in the `switch nodeType` block of `walkForSymbols` that iterates child declarators and extracts each.

### 3. Class fields were never extracted as symbols at all (pre-existing, hidden)

**Symptom:** Before ADR-004, class fields in Java, TypeScript, JavaScript, etc. were not in the symbol index at all.

**Root cause:** The old `walkForSymbols` gated class body recursion on `ClassBodyTypes`, which only contained method types (`method_declaration`, `constructor_declaration`, etc.) — not field types. So the entire class body walk never visited field nodes.

**Current state:** The new recursive walker can reach fields, but symbol extraction for them is disabled (see bug #1) until `extractName` is fixed.

---

## Chunker / Architecture Bugs

### 4. `ChunkableTypes` conflates two different concepts

**Location:** `internal/parser/languages.go` — `ChunkableTypes` map

**Problem:** A node type in `ChunkableTypes` can be either:
- A **container** (`class_declaration`, `interface_declaration`, `mod_item`, `impl_item`, `internal_module`) that holds nested definitions and should be recursed into
- A **leaf declaration** (`field_declaration`, `const_item`, `static_item`) that should be emitted as-is and not recursed into

**Current workaround:** `walkForSymbols` has a hardcoded whitelist of container node types:
```go
case "impl_item", "mod_item", "foreign_mod_item", "internal_module", "static_initializer":
    shouldRecurse = true
```

This list must be kept in sync with the configs, duplicating information that should live in one place.

**Proper fix:** Either split into `ContainerTypes` and `LeafTypes` maps, or change `ChunkableTypes` to `map[string]NodeMeta` where `NodeMeta` has `Kind string` and `IsContainer bool`.

### 5. `decorated_definition` wrapper kind resolution depends on `findDeclarationChild`

**Location:** `internal/parser/chunker.go` — `chunkNode` and `findDeclarationChild`

**Problem:** When a wrapper node (like Python's `decorated_definition` or TypeScript's `export_statement` / `ambient_declaration`) has `kind == ""` in `ChunkableTypes`, we resolve the kind by looking at the inner child via `findDeclarationChild`.

`findDeclarationChild` has its own hardcoded whitelist of declaration types. Adding a new chunkable type requires remembering to add it to this helper or the wrapper unwrapping silently fails.

**Proper fix:** Derive `findDeclarationChild`'s whitelist from the union of all languages' `ChunkableTypes`, or invert the logic — the wrapper node type advertises which child kinds it wraps.

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

### 11. Size-gate contradiction: `chunkNode` decomposes small containers eagerly

**Location:** `internal/parser/chunker.go:144-170` — `chunkNode`

**Symptom:** Small containers (e.g., a 20-line Ruby module containing a 15-line class) are decomposed into a signature chunk + child chunks even though the entire node fits in one chunk. The parent chunk holds only the signature summary, not the raw source.

**Root cause:** `chunkNode` calls `findChunkableChildren` unconditionally **before** checking `tokens <= maxChunkTokens`. The token check only runs in the no-extracted-children fallback branch at `chunker.go:173-183`. Any container with a chunkable descendant is decomposed regardless of size.

**Conflict with ADR-004:** ADR-004 §"Core Algorithm" pseudocode reads:

```
if tokens <= maxChunkTokens:
    emit node as chunk
    return
// Node is too big — try to decompose
```

and §"Key behaviors" states: *"Recursion is size-gated. A 20-line module with a 15-line class doesn't decompose — the whole thing fits in one chunk."*

The implemented behavior is the opposite: decomposition is triggered by the presence of chunkable children, not by size.

**Impact:**
- More chunks than necessary on small files.
- Signature chunks replace full content for tiny classes, so search hits return the terse `class X { ... }` summary instead of the actual code when the class is under 1024 tokens.
- Test suite currently locks in the eager behavior — `TestParse_RubyNestedModuleClass`, `TestParse_JavaInnerClass`, etc. assert 2+ class chunks on small fixtures. Fixing to match the ADR would break these tests.

**Resolution options:**
1. Add an early-return `if tokens <= maxChunkTokens { emit single chunk; return }` at the top of `chunkNode`, matching the ADR. Update the tests to use larger fixtures that genuinely exceed the token threshold.
2. Update ADR-004 to document eager decomposition as the intended behavior and delete the size-gate claim. Tests stay as-is.

Option 2 is simpler and preserves current test coverage. Option 1 is more faithful to the ADR intent (fewer chunks for small files, less signature noise).

### 12. Depth guard off-by-one — **RESOLVED**

**Location:** `internal/parser/chunker.go` — `chunkNode` depth guard.

**Status:** Fixed. The check now reads `if depth > maxChunkDepth` matching ADR-004 §6 pseudocode. With `maxChunkDepth = 10` the chunker handles 11 levels of nested chunkable containers before the fallback kicks in. Covered by `TestParse_DepthGuardBoundary` which uses 11 levels of nested Ruby modules and asserts the innermost method is extracted as a function chunk.

### 13. Schema CHECK constraint rejects `namespace` symbol kind

**Location:** `internal/storage/migrations/sqlite/001_base_schema.sql` and `internal/storage/migrations/postgres/001_base_schema.sql` — `symbols.kind` CHECK constraint; `internal/parser/languages.go` typescriptConfig `SymbolKindMap`.

**Symptom:** Inserting a TypeScript namespace symbol fails at the DB layer:

```
insert symbol Validators: CHECK constraint failed:
kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant')
```

The cold-index pipeline logs the error but continues, so the namespace symbol is silently dropped from the index even though the parser extracted it correctly. Observed during `TestEnsureParserVersion_PurgesOnMismatch` which indexes `testdata/typescript_project/namespace.ts`.

**Root cause:** ADR-004 added `"internal_module": "namespace"` to TypeScript's `SymbolKindMap` (languages.go:96) without updating the `symbols.kind` CHECK constraint in the schema migrations. The constraint pre-dates ADR-004 and does not include `"namespace"`. Postgres has the same constraint.

**Affected tests:** `TestEnsureParserVersion_PurgesOnMismatch`, `TestEnsureParserVersion_NoOpOnMatch`, any test or real-world run that indexes a file containing a TypeScript namespace. Parser-only tests (`TestParse_TypeScriptNamespace`, `TestParse_TypeScriptAmbientDeclarations`) pass because they assert on `result.Symbols` without going through the storage layer.

**Impact:** TypeScript namespace symbols are invisible to search, symbols-by-name, and the dependency graph for any namespace in a real indexed repo. This is a silent regression: the test fixture exercises the path but the error is only visible in log output.

**Fix options:**
1. Add `"namespace"` to the CHECK constraint via a new migration. Cleanest fix; matches the parser intent.
2. Change `"internal_module": "namespace"` to `"internal_module": "type"` in typescriptConfig's `SymbolKindMap`. Simpler but loses the namespace/type distinction in search filters.
3. Drop the CHECK constraint entirely in favor of an unconstrained TEXT column. More permissive but loses schema-level validation.

Option 1 is recommended. Requires a new goose migration in both `internal/storage/migrations/sqlite/` and `internal/storage/migrations/postgres/` that rewrites the `symbols` table (SQLite) or alters the CHECK constraint (Postgres supports `ALTER TABLE ... DROP CONSTRAINT ... ADD CONSTRAINT ...`).

**Priority:** High — silent data loss for namespace-heavy codebases.

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
- Bug #1 + #2: Field and multi-declarator symbol extraction. Without these, class fields are invisible to search and the dependency graph.
- Bug #4: `ChunkableTypes` conflation. The current workaround is fragile and will cause subtle bugs when adding new languages or new node types.
- Bug #13: `namespace` symbol kind rejected by schema CHECK constraint. TypeScript namespace symbols silently fail to persist. Needs a schema migration.

**Medium impact:**
- Bug #8: Call expression receiver loss. Affects graph accuracy significantly.
- Bug #5: `findDeclarationChild` whitelist maintenance burden.
- Bug #11: Size-gate contradiction. Pick option 1 or 2 before next ADR edit so the doc and code agree.

**Lower impact:**
- Bug #6: Signature comment stripping (works for common cases).
- Bug #7: Depth guard observability.
- Bug #9: Recursive call tracking.
- Bug #10: Generic type arguments.
- ~~Bug #12: Depth guard off-by-one~~ — **RESOLVED**
