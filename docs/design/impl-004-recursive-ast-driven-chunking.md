# Implementation Plan: Recursive AST-Driven Chunking

**ADR:** [ADR-004](adr-004-recursive-ast-driven-chunking.md)
**Date:** 2026-04-07

---

## Overview

Replace the whitelist-driven two-level chunking model with a recursive, size-driven AST traversal. Remove `ClassTypes`, `ClassBodyTypes`, and `ClassBodyType` from `LanguageConfig`. Fix the same nesting gaps in `walkForSymbols`.

**Files changed:** 4 (languages.go, chunker.go, symbols.go, parser_test.go)
**Files unchanged:** edges.go (already does full recursive walk), parser.go, token.go
**Estimated scope:** ~300 lines changed, ~200 lines removed

---

## Phases

### Phase 0: Preparation (Before Any Code Changes)

**Goal:** Establish a baseline so we can detect regressions.

#### 0.1 — Capture golden-file snapshots for all existing test fixtures

For each language's test fixtures in `testdata/`, capture the current chunker output (chunk boundaries, kinds, symbol names, parent indices) as golden files.

**Files:**
- Create `testdata/golden/` directory
- Add `TestGoldenChunks` in `internal/parser/parser_test.go` that serializes chunk output to JSON and compares against golden files
- Generate initial golden files from current behavior

**Why:** Golden files let us diff old vs new output for every language. We can verify the new chunker produces strictly-more-complete output, not different-and-worse output.

#### 0.2 — Add test fixtures for the known gaps

Add new test source files that exercise every gap we found. These cover both the nesting bugs AND the newly added ChunkableTypes:

| File | Language | Pattern tested |
|---|---|---|
| `testdata/ruby_project/nested.rb` | Ruby | `module > class > methods` (nesting bug) |
| `testdata/ruby_project/singleton_class.rb` | Ruby | `class << self` with methods (new ChunkableType) |
| `testdata/python_project/decorated_class.py` | Python | Class with `@staticmethod`, `@property`, `@classmethod` methods (nesting bug) |
| `testdata/python_project/nested_class.py` | Python | Class inside class (nesting bug) |
| `testdata/python_project/type_alias.py` | Python | `type X = ...` Python 3.12 syntax (new ChunkableType) |
| `testdata/rust_project/trait_and_mod.rs` | Rust | `trait` with methods, `mod` with nested fns (nesting bug) |
| `testdata/rust_project/extern_and_union.rs` | Rust | `extern "C" { }` block, `union`, fn signatures (new ChunkableTypes) |
| `testdata/java_project/com/example/InnerClass.java` | Java | Outer class with inner class (nesting bug) |
| `testdata/java_project/com/example/InterfaceWithDefaults.java` | Java | Interface with default methods (nesting bug) |
| `testdata/java_project/com/example/RecordWithCompact.java` | Java | Record with compact constructor, static initializer (new ChunkableTypes) |
| `testdata/typescript_project/namespace.ts` | TypeScript | `namespace` with exported functions (new ChunkableType) |
| `testdata/typescript_project/ambient.d.ts` | TypeScript | `declare` statements — function, class, namespace, module (new ChunkableType) |
| `testdata/typescript_project/overloads.ts` | TypeScript | Function overload signatures (new ChunkableType) |

Write failing tests that assert the expected chunks. These tests fail under the current chunker (documenting the bugs) and will pass after Phase 2.

---

### Phase 1: Simplify LanguageConfig and Add Missing ChunkableTypes

**Goal:** Remove `ClassTypes`, `ClassBodyTypes`, `ClassBodyType` from the config struct. Add `AmbientType` field. Add all missing `ChunkableTypes` and `SymbolKindMap` entries identified by exhaustive grammar analysis.

#### 1.1 — Update `LanguageConfig` struct

**File:** `internal/parser/languages.go`

```go
// Before:
type LanguageConfig struct {
    Name           string
    Grammar        *tree_sitter.Language
    ChunkableTypes map[string]string
    SymbolKindMap  map[string]string
    ClassBodyTypes map[string]bool   // REMOVE
    ImportTypes    map[string]bool
    ExportType     string
    ClassBodyType  string            // REMOVE
    ClassTypes     map[string]bool   // REMOVE
}

// After:
type LanguageConfig struct {
    Name           string
    Grammar        *tree_sitter.Language
    ChunkableTypes map[string]string // node_type -> chunk kind
    SymbolKindMap  map[string]string // node_type -> symbol kind
    ImportTypes    map[string]bool   // node types treated as imports
    ExportType     string            // export wrapper type (empty if N/A)
    AmbientType    string            // ambient declaration wrapper, e.g. "ambient_declaration" (empty if N/A)
}
```

#### 1.2 — Update each language config function

Remove the three fields from all 9 config functions. Add missing entries per the exhaustive grammar audit:

**Ruby** — add to `ChunkableTypes`:
- `"singleton_class": "class"` — `class << self ... end`

Add to `SymbolKindMap`:
- `"singleton_class": "class"`

Note: Ruby DSL calls (`attr_accessor`, `has_many`, `validates`, etc.) are generic `call` nodes — not given special treatment. They remain in the class signature chunk when methods are extracted.

**Python** — add to `ChunkableTypes`:
- `"type_alias_statement": "type"` — Python 3.12 `type X = ...`

Add to `SymbolKindMap`:
- `"type_alias_statement": "type"`

**Rust** — add to `ChunkableTypes`:
- `"function_signature_item": "function"` — trait method signatures, extern fn declarations
- `"foreign_mod_item": "block"` — `extern "C" { ... }` blocks
- `"union_item": "type"` — `union Foo { ... }`

Add to `SymbolKindMap`:
- `"function_signature_item": "function"`
- `"union_item": "type"`
- `"mod_item": "module"` (currently missing from SymbolKindMap, already in ChunkableTypes)

**Java** — add to `ChunkableTypes`:
- `"compact_constructor_declaration": "method"` — record compact constructors
- `"static_initializer": "block"` — `static { ... }` blocks

Add to `SymbolKindMap`:
- `"compact_constructor_declaration": "method"`

**TypeScript** — add to `ChunkableTypes`:
- `"internal_module": "block"` — `namespace Foo { }`
- `"ambient_declaration": ""` — `declare ...` wrapper (resolved from child)
- `"function_signature": "function"` — overload signatures
- `"generator_function_declaration": "function"` — `function* foo() { }`

Add to `SymbolKindMap`:
- `"internal_module": "namespace"`
- `"function_signature": "function"`
- `"generator_function_declaration": "function"`

Set `AmbientType`:
- `AmbientType: "ambient_declaration"`

**JavaScript, Go, Bash, ERB** — no additions needed.

#### 1.3 — Verify compilation fails

The removed fields are referenced in `chunker.go` and `symbols.go`. Compilation will fail, confirming all usage sites. This is intentional — Phase 2 fixes them.

---

### Phase 2: Replace Chunker Core

**Goal:** Replace `chunkFile` + `chunkClass` + `chunkDecoratedDef` + `chunkExportStatement` with `chunkFile` + `chunkNode`.

**File:** `internal/parser/chunker.go`

#### 2.1 — Implement `chunkNode`

New recursive function. Core logic:

```
chunkNode(node, source, cfg, depth) -> []ChunkRecord:
    guard: if depth > maxChunkDepth, fall back to splitByLines

    content = node text (with comment children stripped)
    tokens = count(content)
    kind = ChunkableTypes[node.Kind()] or "block"

    if tokens <= maxChunkTokens:
        return single chunk

    // Decompose: find chunkable children
    extracted = []
    for each named child:
        skip if child.Kind() == "comment"
        unwrap export wrapper if applicable (ExportType)
        unwrap ambient wrapper if applicable (AmbientType)
        if child.Kind() in ChunkableTypes:
            extracted += chunkNode(child, ...)

    if len(extracted) > 0:
        sigChunk = buildSignature(node, extracted)  // code-only, no comments
        set ParentIndex on extracted children -> sigChunk
        return [sigChunk] + extracted
    else:
        // No chunkable children, node is just big -> split by lines
        return splitByLines(node, source, kind)
```

Key behaviors:
- **Comments are skipped.** `comment` nodes are never extracted, never included in signatures, never collected into the header.
- **Recursion is size-gated.** A 20-line module with a 15-line class doesn't decompose — the whole thing fits in one chunk.
- **Recursion is ChunkableTypes-gated.** Random large nodes (e.g., a giant string literal) don't get recursed into meaninglessly — only nodes whose children include chunkable types are decomposed.
- **Depth guard.** `maxChunkDepth = 10` prevents pathological cases.

#### 2.2 — Rewrite `chunkFile`

Simplified top-level loop:

```
chunkFile(root, source, cfg) -> []ChunkRecord:
    chunks = []
    headerParts = []

    for each root named child:
        if comment -> skip entirely (comments are not indexed)
        if import or package_clause -> headerParts
        else if in ChunkableTypes:
            if export wrapper (ExportType) -> unwrap, then chunkNode
            if ambient wrapper (AmbientType) -> unwrap, then chunkNode
            else -> chunkNode
        else:
            -> headerParts (non-chunkable top-level)

    prepend header
    mergeSmallChunks
    splitLargeChunks  (catches any edge cases)
    reindex
```

#### 2.3 — Replace `buildClassSignatureWithConfig`

New function `buildSignatureFromExtracted(node, source, extractedChildren)`:

1. Extract the node's declaration line(s) — the code between node start and body start, **with comments stripped**. For `class Railtie < Rails::Railtie` preceded by 100 lines of doc comments, only the `class Railtie < Rails::Railtie` line is included. Implement via `extractDeclarationLine(node, source)` which walks the node's non-comment children to find the declaration text.
2. For each extracted direct child (where `ParentIndex` points to this node), emit a one-line summary: `  name(signature) { ... }\n`
3. Append the closing line of the node (e.g., `end`, `}`) by reading the last line of the node's source range.

No comment text is included in signatures. Signatures are pure code structure.

#### 2.4 — Remove dead code

Delete:
- `chunkClass` function
- `chunkDecoratedDef` function
- `chunkExportStatement` function (if fully subsumed by the unwrap-in-chunkFile approach; otherwise simplify)
- `buildClassSignatureWithConfig` function
- `findChildByType` helper (only used by removed code — check if used elsewhere first)

#### 2.5 — Handle special cases in `chunkNode`

Four patterns need attention:

**Python `decorated_definition`:**
Tree-sitter wraps `@decorator\ndef func()` as a `decorated_definition` containing a `function_definition`. The `decorated_definition` is in `ChunkableTypes`. When `chunkNode` processes it:
- If it's small enough → emit as-is (includes decorator text). Done.
- If it's too big → recurse into children. The inner `function_definition` is in `ChunkableTypes` → extracted. The decorator lines become part of the parent signature.

This works naturally — no special case needed.

**TypeScript `export_statement` (ExportType):**
Wraps declarations. `ExportType` handling in `chunkFile` unwraps before calling `chunkNode`. Inside `chunkNode`, if we encounter an export wrapper (nested exports), unwrap and recurse. The existing `findDeclarationChild` helper is reused.

**TypeScript `ambient_declaration` (AmbientType):**
Wraps declarations with `declare` prefix. Analogous to `export_statement` — the chunker unwraps the ambient wrapper and processes the inner declaration. `chunkFile` and `chunkNode` both check `cfg.AmbientType` and unwrap using the same `findDeclarationChild` helper (which already handles the common declaration child types). Add `"ambient_declaration"` to `findDeclarationChild`'s known types if not already present.

Forms to handle:
- `declare function foo(): void;` → inner `function_signature`
- `declare class Foo {}` → inner `class_declaration`
- `declare namespace Foo {}` → inner `internal_module`
- `declare const x: number;` → inner `lexical_declaration`
- `declare module "foo" {}` → inner `module`
- `declare global { ... }` → special: `global` keyword + `statement_block` (treat as block chunk)

**Go `type_declaration` with `type_spec`:**
Go's `type_declaration` can contain multiple `type_spec` children (grouped type declarations). The current code has special handling (`extractGoTypeName`). In the new model, `type_declaration` is in `ChunkableTypes`. If the grouped declaration is small (most are), it becomes a single chunk. If it's large (rare), `chunkNode` tries to find chunkable children — but `type_spec` is not in `ChunkableTypes`. This falls through to line-splitting, which is acceptable for the rare large grouped type declaration.

**Ruby `singleton_class`:**
`class << self ... end` has a `body_statement` containing methods. Since `singleton_class` is now in `ChunkableTypes`, and `method`/`singleton_method` are also in `ChunkableTypes`, the recursive chunker handles this naturally: if the singleton class is too big, it recurses and extracts the methods. No special case needed.

**Ruby class-level DSL calls:**
`has_many`, `validates`, `attr_accessor`, etc. are generic `call` nodes — not in `ChunkableTypes`. When the recursive chunker decomposes a large class, these calls are not extracted as children. They remain as non-chunkable body content and become part of the class's signature chunk (the compact parent summary after methods are extracted). No special handling needed.

---

### Phase 3: Fix Symbol Extraction

**Goal:** Make `walkForSymbols` recurse into nested containers.

**File:** `internal/parser/symbols.go`

#### 3.1 — Replace ClassTypes/ClassBodyTypes recursion

**Before (lines 80-100):**
```go
// Recurse into class bodies for methods
if cfg.ClassBodyType != "" && nodeType == cfg.ClassBodyType {
    for i := 0; i < int(node.NamedChildCount()); i++ {
        child := node.NamedChild(uint(i))
        if cfg.ClassBodyTypes[child.Kind()] {
            p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
        }
    }
    return
}

// For class declarations, recurse into their body
if cfg.ClassTypes[nodeType] {
    if cfg.ClassBodyType != "" {
        body := findChildByType(node, cfg.ClassBodyType)
        if body != nil {
            p.walkForSymbols(body, source, exported, chunks, symbols, cfg)
        }
    }
    return
}
```

**After:**
```go
// For chunkable container nodes, recurse into all named children
// to find nested symbols. This replaces the ClassTypes/ClassBodyTypes
// checks and handles arbitrary nesting depth.
if _, chunkable := cfg.ChunkableTypes[nodeType]; chunkable || !isSymbol {
    for i := 0; i < int(node.NamedChildCount()); i++ {
        p.walkForSymbols(node.NamedChild(uint(i)), source, exported, chunks, symbols, cfg)
    }
}
```

This is simpler and more general. It recurses into:
- Chunkable nodes (classes, modules, traits, etc.) to find nested symbols
- Non-symbol nodes (body_statement, block, declaration_list, etc.) to traverse structural containers

The `decorated_definition` handler (lines 32-40) and export wrapper handler (lines 24-29) remain unchanged — they handle their specific unwrapping before recursion.

#### 3.2 — Verify no double-extraction

With the new recursion, ensure symbols are extracted exactly once. The current code extracts a symbol when it matches `SymbolKindMap` (line 42-78) and then decides whether to recurse. The `isSymbol` flag prevents double-counting: if a node is a symbol AND chunkable, we extract the symbol and then recurse into it for nested symbols.

---

### Phase 4: Testing

#### 4.1 — Update existing tests

All existing tests in `parser_test.go` should continue to pass or produce strictly more chunks. For each test:
- Same chunks as before for already-working patterns
- Additional chunks for previously-dropped patterns

Update expected values where the new chunker produces better output (e.g., a class that previously became a blob now has method chunks).

#### 4.2 — New tests pass

The gap tests from Phase 0.2 should now pass:

**Nesting bug fixes:**
- `TestParse_RubyNestedModuleClass` — module chunk + class chunk + method chunks
- `TestParse_PythonDecoratedMethodsInClass` — class chunk + decorated method chunks
- `TestParse_PythonNestedClass` — outer class chunk + inner class chunk + method chunks
- `TestParse_RustTraitWithMethods` — trait chunk + method signature chunks + default impl chunks
- `TestParse_RustModuleWithItems` — mod chunk + function/struct chunks
- `TestParse_JavaInnerClass` — outer class chunk + inner class chunk + method chunks
- `TestParse_JavaInterfaceWithDefaults` — interface chunk + method chunks

**New ChunkableTypes:**
- `TestParse_RubySingletonClass` — `class << self` chunk + method chunks inside
- `TestParse_PythonTypeAlias` — `type X = ...` extracted as type chunk
- `TestParse_RustExternBlock` — `extern "C"` chunk + fn signature chunks inside
- `TestParse_RustUnion` — `union` extracted as type chunk
- `TestParse_RustFunctionSignature` — trait fn signatures extracted as function chunks
- `TestParse_JavaRecordCompactConstructor` — compact constructor extracted as method chunk
- `TestParse_JavaStaticInitializer` — `static { }` extracted as block chunk
- `TestParse_TypeScriptNamespace` — namespace chunk + function chunks inside
- `TestParse_TypeScriptAmbientDeclarations` — `declare` unwrapped, inner declarations extracted
- `TestParse_TypeScriptOverloadSignatures` — function signatures extracted as function chunks
- `TestParse_TypeScriptGeneratorFunction` — `function*` extracted as function chunk

#### 4.3 — Verify symbol extraction completeness

For each gap test, verify that symbols inside nested containers are extracted:
```go
// Example: Ruby module > class > method
symbols := result.Symbols
assertSymbolExists(t, symbols, "Rails", "class")      // module
assertSymbolExists(t, symbols, "Railtie", "class")     // nested class
assertSymbolExists(t, symbols, "configure", "method")   // method inside nested class
```

#### 4.4 — Verify edge extraction unaffected

Run existing edge tests. They should pass unchanged since `walkForEdges` already does full recursive traversal.

#### 4.5 — Benchmark

```go
func BenchmarkChunkFile_Ruby(b *testing.B) { ... }
func BenchmarkChunkFile_Python(b *testing.B) { ... }
func BenchmarkChunkFile_Rust(b *testing.B) { ... }
// etc.
```

Compare against baseline captured in Phase 0.

---

### Phase 5: Cleanup

#### 5.1 — Remove dead code

- `chunkClass`, `chunkDecoratedDef`, `chunkExportStatement` — replaced by `chunkNode`
- `buildClassSignatureWithConfig` — replaced by `buildSignatureFromExtracted`
- `findChildByType` — verify if used elsewhere; remove if not
- `ClassTypes`, `ClassBodyTypes`, `ClassBodyType` references — removed in Phase 1

#### 5.2 — Update CLAUDE.md

If any tool usage patterns change (they shouldn't — MCP interface is unchanged), update the documentation.

#### 5.3 — Trigger reindex on upgrade

Add a mechanism to detect that the chunking algorithm has changed and force a full reindex:
- Option A: Bump a `chunkVersion` constant in the parser. Store it in the DB. On mismatch, trigger reindex.
- Option B: Add a config flag `parser.force_reindex = true` that the release notes document.

Option A is preferred — it's automatic and doesn't require user action.

---

## File-by-File Change Summary

| File | Change type | Description |
|---|---|---|
| `internal/parser/languages.go` | **Modify** | Remove `ClassTypes`, `ClassBodyTypes`, `ClassBodyType` from struct. Add `AmbientType` field. Add missing ChunkableTypes/SymbolKindMap entries: Ruby (`singleton_class`), Python (`type_alias_statement`), Rust (`function_signature_item`, `foreign_mod_item`, `union_item`), Java (`compact_constructor_declaration`, `static_initializer`), TypeScript (`internal_module`, `ambient_declaration`, `function_signature`, `generator_function_declaration`). |
| `internal/parser/chunker.go` | **Modify** | Replace `chunkClass`, `chunkDecoratedDef`, `chunkExportStatement` with `chunkNode`. Simplify `chunkFile` (skip comments, handle `AmbientType` unwrapping). Replace `buildClassSignatureWithConfig` with `buildSignatureFromExtracted`. Keep `mergeSmallChunks`, `splitLargeChunks`, `extractName`, `extractSignature` unchanged. |
| `internal/parser/symbols.go` | **Modify** | Replace `ClassTypes`/`ClassBodyTypes` recursion in `walkForSymbols` with `ChunkableTypes`-guided recursion. Add `AmbientType` unwrapping. ~20 lines changed. |
| `internal/parser/parser_test.go` | **Modify** | Update existing test expected values. Add 18+ new tests for nesting bugs and new ChunkableTypes. Add golden-file test infrastructure. |
| `internal/parser/edges.go` | **No change** | Already does full recursive walk. |
| `internal/parser/parser.go` | **No change** | Entry point unchanged. |
| `internal/parser/token.go` | **No change** | Token counting unchanged. |
| `testdata/ruby_project/nested.rb` | **New** | Ruby `module > class > methods`. |
| `testdata/ruby_project/singleton_class.rb` | **New** | Ruby `class << self` with methods. |
| `testdata/python_project/decorated_class.py` | **New** | Python class with decorated methods. |
| `testdata/python_project/nested_class.py` | **New** | Python class inside class. |
| `testdata/python_project/type_alias.py` | **New** | Python 3.12 `type X = ...`. |
| `testdata/rust_project/trait_and_mod.rs` | **New** | Rust trait with methods, mod with nested items. |
| `testdata/rust_project/extern_and_union.rs` | **New** | Rust `extern "C"`, union, fn signatures. |
| `testdata/java_project/.../InnerClass.java` | **New** | Java inner classes. |
| `testdata/java_project/.../InterfaceWithDefaults.java` | **New** | Java interface with default methods. |
| `testdata/java_project/.../RecordWithCompact.java` | **New** | Java record + compact constructor + static initializer. |
| `testdata/typescript_project/namespace.ts` | **New** | TypeScript namespace with functions. |
| `testdata/typescript_project/ambient.d.ts` | **New** | TypeScript `declare` statements. |
| `testdata/typescript_project/overloads.ts` | **New** | TypeScript function overload signatures. |

---

## Execution Order and Dependencies

```
Phase 0.1 (golden files)
    ↓
Phase 0.2 (gap test fixtures + failing tests)
    ↓
Phase 1 (simplify LanguageConfig)  ← compilation breaks here intentionally
    ↓
Phase 2 (replace chunker core)    ← compilation restored
    ↓
Phase 3 (fix symbol extraction)
    ↓
Phase 4 (all tests pass, new tests pass, benchmarks)
    ↓
Phase 5 (cleanup, reindex mechanism)
```

Phases 2 and 3 can be done in a single commit since they fix the same compilation errors from Phase 1. Phase 0 should be a separate commit (or commits) to establish the baseline.

---

## Rollback Plan

If issues are found after merge:

1. **Git revert.** The change is contained to the parser package. Reverting restores the old chunker. The daemon will detect chunk version mismatch and reindex back to the old format.

2. **Feature flag.** If we want to ship both chunkers temporarily, gate on a config flag:
   ```toml
   [parser]
   recursive_chunking = true  # default: true after rollout
   ```
   This adds complexity but allows instant rollback without a code change.

Preference: skip the feature flag, rely on git revert. The change is well-tested and the blast radius is contained to chunk content. No API, schema, or config changes.

---

## Resolved Questions

1. **~~Signature truncation~~** — Resolved: comments are excluded entirely from chunking. Signatures contain only code structure (declaration line + member listing + closing delimiter). The 100-line Railtie doc comment is simply not indexed. No truncation logic needed.

2. **~~Large comment blocks between methods~~** — Resolved: comments are skipped at every level. A 1500-line comment block between methods is invisible to the chunker. This eliminates ranking distortion, avoids schema complexity (`RelatedChunkIndex`), and avoids query-time adjacency lookups. Users search comments via `Grep`.

## Open Questions

1. **ParentIndex depth:** Currently `ParentIndex` is a single pointer (child → parent). With recursive chunking, we get multi-level nesting (method → class → module). Should `ParentIndex` point to the immediate parent only, or should we add a chain? Proposal: keep single `ParentIndex` pointing to immediate parent — the chunk hierarchy is implicit in the nesting, and the dependency graph uses symbols, not chunk hierarchy.

2. **Chunk version storage:** Where to store the chunk algorithm version for reindex detection? Options: (a) new column in `files` table, (b) metadata table, (c) SQLite `PRAGMA user_version`. Proposal: metadata table — it's backend-agnostic and already exists for other purposes.

3. **Comment stripping from node content:** When `chunkNode` emits a small-enough node as a single chunk, should the `Content` field contain the raw node text (which includes inline comments) or should comments be stripped? Proposal: use raw node text for leaf chunks. Comment nodes are skipped during *traversal* (they don't become their own chunks), but if a function has a `// handle edge case` comment inline, that's part of the function's source and should be in the chunk. Only top-level and inter-method comment blocks are excluded.
