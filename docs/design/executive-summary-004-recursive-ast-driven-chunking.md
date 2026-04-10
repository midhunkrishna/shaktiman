# Executive Summary: Recursive AST-Driven Chunking

**ADR:** [ADR-004](adr-004-recursive-ast-driven-chunking.md)
**Implementation Plan:** [impl-004](impl-004-recursive-ast-driven-chunking.md)
**Date:** 2026-04-07

---

## Problem

Shaktiman's chunker uses a whitelist-driven, two-level model to split source files into semantic chunks. It handles `file > class > method` but silently drops code at any deeper nesting. Systematic analysis found **10+ gaps across 7 of 9 supported languages** — Ruby nested modules, Python decorated methods, Rust traits, Java inner classes, TypeScript namespaces, and more. The same gaps propagate to symbol extraction and the dependency graph, making MCP tools return incomplete results.

The root cause is architectural: three per-language config maps (`ClassTypes`, `ClassBodyTypes`, `ClassBodyType`) control traversal, and every missing entry means silent data loss. The failure mode is invisible — no errors, just missing chunks.

---

## Decisions

### 1. Recursive AST-driven chunking

Replace the two-level `chunkFile` + `chunkClass` with a single recursive `chunkNode` that decomposes any node exceeding the token limit by extracting its chunkable children. Traversal is guided by `ChunkableTypes` (a flat map of AST node types to chunk kinds) and bounded by a depth guard.

**Why:** Eliminates the entire class of nesting bugs. The chunker no longer needs to be told where to recurse — it just recurses into anything too big. Three config fields removed. One rule replaces three interacting maps.

### 2. Comments excluded entirely

`comment` nodes are skipped at every level — not chunked, not in signatures, not in the file header. The chunker indexes code structure only.

**Why:** Large comment blocks distort search ranking. Tree-sitter cannot distinguish doc comments from regular comments. Associating comments with adjacent code would require schema complexity or query-time overhead that isn't justified. Comments remain accessible via `Read` and `Grep`.

### 3. ChunkableTypes expanded from exhaustive grammar research

Analysis of every tree-sitter grammar's `node-types.json` identified **11 missing entries across 5 languages:**

| Language | Missing node types |
|---|---|
| Ruby | `singleton_class` (`class << self`) |
| Python | `type_alias_statement` (3.12 `type X = ...`) |
| Rust | `function_signature_item`, `foreign_mod_item`, `union_item` |
| Java | `compact_constructor_declaration`, `static_initializer` |
| TypeScript | `internal_module` (namespace), `ambient_declaration` (declare), `function_signature`, `generator_function_declaration` |

Go, JavaScript, Bash, and ERB configs are already complete.

**Why:** Ensures we're not discovering gaps reactively. Every definition-level construct in every supported grammar is now accounted for.

### 4. TypeScript `ambient_declaration` unwrapping

New `AmbientType` field on `LanguageConfig` handles `declare` wrappers — same pattern as the existing `ExportType` for `export` wrappers. TypeScript-only; empty for all other languages.

**Why:** `declare class Foo {}` wraps `class_declaration` inside an `ambient_declaration` node. Without unwrapping, the inner declaration is invisible to the chunker.

### 5. Ruby DSL calls are NOT special-cased

`has_many`, `validates`, `attr_accessor`, `include`, and other class-level method calls remain as ordinary class body content. No method-name whitelist.

**Why:** These are generic `call` nodes — syntactically identical to `puts "hello"`. A whitelist would be incomplete (every gem adds DSL methods), framework-coupled (assumes Rails), and prone to false positives. The recursive chunker handles them naturally: when a large class is decomposed, methods are extracted as children, and DSL calls remain in the class's signature chunk.

---

## Scope

| Changed | Unchanged |
|---|---|
| `internal/parser/languages.go` — config struct, 9 language configs | `internal/parser/edges.go` — already does full recursive walk |
| `internal/parser/chunker.go` — recursive `chunkNode` replaces 4 functions | `internal/parser/parser.go` — entry point unchanged |
| `internal/parser/symbols.go` — recursive symbol extraction | `internal/parser/token.go` — token counting unchanged |
| `internal/parser/parser_test.go` — 18+ new tests | MCP tool interfaces — unchanged |
| 13 new test fixture files | Storage schema — unchanged |

**LanguageConfig:** 3 fields removed, 1 field added. Net reduction.

---

## Risks

| Risk | Mitigation |
|---|---|
| Changed chunk boundaries invalidate embeddings | Expected. Daemon cascades reindex. Chunk version bump forces full reindex on upgrade. |
| Signature quality varies by language | Heuristic builder with optional `SignatureStyle` enum if needed later. |
| Performance regression from deeper recursion | Depth guard (max 10). Tree-sitter ASTs are in memory. Benchmark before/after. |
| Over-decomposition into tiny chunks | `mergeSmallChunks` (unchanged) catches this. Recursion only triggers above 1024 tokens. |

---

## Rollout

1. Golden-file tests capture current behavior as baseline.
2. Failing tests document all known gaps before code changes.
3. Implement recursive chunker + config changes.
4. Verify: all existing tests pass or produce strictly better output; all gap tests pass.
5. Chunk version bump triggers automatic reindex on first run after upgrade.
