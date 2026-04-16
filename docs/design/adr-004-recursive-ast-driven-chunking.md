# ADR-004: Recursive AST-Driven Chunking

**Status:** PROPOSED
**Date:** 2026-04-07
**Deciders:** Shaktiman maintainers

> **Status (Today, 2026-04-16):** **SHIPPED** (status field above predates merge).
> Recursive AST-driven chunking is implemented in `internal/parser/` with container/
> leaf node handling per-language. Language configs in `internal/parser/languages.go`
> declare `NodeMeta{Kind, IsContainer}` for each chunkable node type. See also
> `impl-004-recursive-ast-driven-chunking.md` and
> `docs/review-findings/parser-bugs-from-recursive-chunking.md` for follow-up work.

---

## Context

Shaktiman's parser splits source files into semantic chunks using tree-sitter ASTs. The current design uses a **whitelist-driven, two-level chunking model**: a top-level loop (`chunkFile`) dispatches class-like nodes to `chunkClass`, which extracts one level of methods from the class body via `ClassBodyTypes`. Everything else becomes either a flat top-level chunk or part of the file header.

This model relies on three per-language config maps to control traversal:

| Config field | Purpose |
|---|---|
| `ClassTypes` | Which node types are "class-like" and should enter `chunkClass` |
| `ClassBodyType` | The node type for the class body container (e.g., `body_statement`, `class_body`, `block`) |
| `ClassBodyTypes` | Which body children to extract as method chunks |

### Problems Found

Systematic analysis of all 9 supported languages against real-world code patterns revealed that the two-level model silently drops code in every language with nesting deeper than `file > class > method`:

#### Critical (idiomatic patterns broken)

| Language | Pattern | Node dropped | Root cause |
|---|---|---|---|
| **Ruby** | `module > class > methods` | Nested `class` inside `module` body | `class` not in Ruby's `ClassBodyTypes` |
| **Python** | `@staticmethod`/`@property`/`@classmethod` methods inside classes | `decorated_definition` wrapping `function_definition` | `decorated_definition` not in Python's `ClassBodyTypes`; `chunkDecoratedDef` only called from top-level loop |

#### High (common patterns broken)

| Language | Pattern | Node dropped | Root cause |
|---|---|---|---|
| **Rust** | `trait` with method signatures and default impls | Methods inside `trait_item` | `trait_item` not in Rust's `ClassTypes` |
| **Rust** | `mod` with nested fns/structs/impls | Contents of `mod_item` | `mod_item` not in Rust's `ClassTypes` |
| **Java** | Inner classes | `class_declaration` inside outer `class_body` | `class_declaration` not in Java's `ClassBodyTypes` |
| **Java** | Interface default/static methods | Methods inside `interface_declaration` | `interface_declaration` not in Java's `ClassTypes` |
| **TypeScript** | `namespace`/`module` declarations | Entire namespace contents | `internal_module` not in TS's `ChunkableTypes` at all |

#### Medium (less common but valid)

| Language | Pattern | Node dropped |
|---|---|---|
| **Java** | Enum with methods | `enum_declaration` not in `ClassTypes` |
| **TypeScript** | Large interfaces | `interface_declaration` not in `ClassTypes` |
| **Python** | Nested classes | `class_definition` not in Python's `ClassBodyTypes` |

#### Downstream impact

The same gaps exist in `walkForSymbols` (symbols.go) which uses identical `ClassBodyTypes`/`ClassTypes` checks. Dropped chunks mean dropped symbols, which means missing edges in the dependency graph. The `dependencies` MCP tool returns incomplete results for any symbol inside a dropped container.

### Root Causes

1. **`ClassBodyTypes` conflates "what to extract" with "what to recurse into."** A nested class is not a method but should be recursed into. These are separate concerns forced into one map.

2. **`chunkClass` is the only body-aware function, but not all containers are classes.** Rust traits, Rust modules, Java interfaces, Java enums, and TypeScript namespaces all have bodies with extractable children but are not in `ClassTypes`.

3. **Adding entries to these maps is unsustainable.** Each new language pattern requires discovering the gap, adding the right node type to the right map, and potentially adding special-case code in `chunkClass`. The config surface is large and the failure mode is silent data loss.

### Current Architecture Snapshot

| Component | File | Role |
|---|---|---|
| Language configs | `internal/parser/languages.go` | Per-language `LanguageConfig` with 7 maps/fields controlling traversal |
| Chunker | `internal/parser/chunker.go` | `chunkFile` (top-level), `chunkClass` (one-level body walk), `splitLargeChunks`, `mergeSmallChunks` |
| Symbol extractor | `internal/parser/symbols.go` | `walkForSymbols` uses same `ClassTypes`/`ClassBodyTypes` for recursion |
| Edge extractor | `internal/parser/edges.go` | `walkForEdges` does full recursive walk (not affected by this bug) |
| Signature builder | `internal/parser/chunker.go:503-551` | `buildClassSignatureWithConfig` generates compact parent summaries |

---

## Decision

**Replace the whitelist-driven, two-level chunking model with a recursive, size-driven AST traversal that uses `ChunkableTypes` as the sole traversal guide.**

### Comment Exclusion

Comments are **completely excluded from chunking**. They are not emitted as chunks, not included in signatures, and not collected into the file header. The chunker only indexes code structure.

**Rationale:**
- Comments (especially large doc blocks) distort search ranking — a 1500-line comment block about authentication would outrank the 20-line method that implements it.
- Tree-sitter does not reliably distinguish doc comments from regular comments across languages. All produce a generic `comment` node type.
- Comments cannot be meaningfully associated with their related code at the chunk level without adding schema complexity (`RelatedChunkIndex`) or query-time lookups that slow retrieval.
- Comments are available in the source file via `Read` when needed. The chunker's job is to index code structure, not prose.

The `comment` node type is skipped at every level: top-level iteration, recursive body traversal, and signature generation.

### Core Algorithm

```
func chunkNode(node, depth):
    tokens = countTokens(node)

    if tokens <= maxChunkTokens:
        emit node as chunk (excluding comment children from content)
        return

    // Node is too big — try to decompose
    extractedChildren = []
    for each named child of node:
        skip if child.Kind() == "comment"
        if child.Kind() is in ChunkableTypes:
            extractedChildren += chunkNode(child, depth+1)
        else if countTokens(child) > maxChunkTokens:
            extractedChildren += chunkNode(child, depth+1)  // recurse into large non-chunkable nodes too

    if extractedChildren is not empty:
        emit signature chunk for node (code-only, no comments)
        emit extractedChildren
    else:
        // No chunkable children found — split by lines as fallback
        splitByLines(node)
```

### What Changes

| Before | After |
|---|---|
| `ClassTypes` map controls which nodes enter `chunkClass` | Removed. Any node too big to be a single chunk gets recursed into. |
| `ClassBodyTypes` map controls which body children are extracted | Removed. Any child in `ChunkableTypes` is extracted. |
| `ClassBodyType` string identifies the body container node | Removed. The chunker recurses into all named children. |
| `chunkClass` handles exactly one level of nesting | Replaced by recursive `chunkNode` that handles arbitrary depth. |
| `chunkDecoratedDef` called only from top-level loop | No longer needed as a separate path. Decorated nodes are just nodes — if they contain chunkable children, the chunker recurses. |
| `chunkExportStatement` unwraps TS export wrappers | Retained as `ExportType` unwrapping, but simplified. |
| Signature builder uses `ClassBodyType`/`ClassBodyTypes` to find methods | Signature builder uses `ChunkableTypes` — any extracted child becomes a line in the signature. |

### What Stays the Same

| Component | Why |
|---|---|
| `ChunkableTypes` map | Still needed: maps AST node types to internal kinds (`"function"`, `"class"`, etc.). This is semantic, not traversal. |
| `SymbolKindMap` map | Still needed for symbol extraction. |
| `ImportTypes` map | Still needed for header collection and edge extraction. |
| `ExportType` string | Still needed for TypeScript/JavaScript export unwrapping. |
| `maxChunkTokens` / `minChunkTokens` | Still the size thresholds. |
| `mergeSmallChunks` / `splitLargeChunks` | Still the post-processing passes. |
| `extractEdges` / `walkForEdges` | Already does full recursive walk — unaffected. |
| Token counting, signature extraction | Retained. |

### Simplified LanguageConfig

```go
type LanguageConfig struct {
    Name           string
    Grammar        *tree_sitter.Language
    ChunkableTypes map[string]string // node_type → chunk kind (traversal + labeling)
    SymbolKindMap  map[string]string // node_type → symbol kind
    ImportTypes    map[string]bool   // node types treated as imports
    ExportType     string            // export wrapper type (empty if N/A)
    AmbientType    string            // ambient declaration wrapper type (empty if N/A)
}
```

Three fields removed: `ClassTypes`, `ClassBodyTypes`, `ClassBodyType`. One field added: `AmbientType` (used only by TypeScript for `declare` wrapper unwrapping).

### Ruby Class-Level Calls

Ruby uses method calls at class scope as structural declarations (`has_many :posts`, `validates :email`, `attr_accessor :name`, `include Comparable`). These are syntactically identical to any other method call — tree-sitter produces the same `call` node for `has_many :posts` as for `puts "hello"`. No other supported language has this pattern — Python uses decorators, Java uses annotations, Rust uses macros, all of which produce distinct node types.

These calls are **not** given special treatment by the chunker. They are part of the class body and handled naturally by the recursive algorithm:

- **Small class:** The entire class fits in one chunk. DSL calls are included alongside methods.
- **Large class:** The recursive chunker extracts methods (which are in `ChunkableTypes`). The DSL calls are non-chunkable body content that remains in the class's **signature chunk** — the compact summary that captures the class declaration and lists extracted members. This is correct: `has_many :posts` is metadata about the class, not an independent code unit.

Extracting DSL calls as separate chunks (via a method-name whitelist) was considered and rejected:
- **No syntactic distinction.** `call` nodes in a class body can be DSL declarations (`has_many :posts`), side-effect calls (`puts "loading"`), or inherited class method invocations (`establish_connection`). Only the method name distinguishes them, and that's a semantic judgment.
- **Unbounded list.** Every gem adds its own DSL methods (`acts_as_paranoid`, `devise`, `pundit`, `carrierwave`, etc.). A whitelist would be perpetually incomplete and framework-coupled.
- **False positives.** `include` could be a mixin or a method on a string. `delegate` could be a Rails DSL or a custom method.

### Signature Generation

When a node has extracted children, the parent chunk stores a compact code-only signature instead of the full source. The signature is built from AST information:

1. **Declaration line(s)**: the node's declaration (e.g., `class Railtie < Rails::Railtie`), extracted from the source text between the node start and the body start. Comments are stripped.
2. **Member listing**: for each extracted child, emit `name(signature) { ... }` using `extractName` and `extractSignature` (both already exist and work from AST node fields, not language config).
3. **Footer**: language-appropriate closing (e.g., `end` for Ruby, `}` for Java/TS).

No `ClassBodyType` needed — the "body" is implicit: it's whatever contains the extracted children. No comments are included — signatures are pure code structure.

### Symbol Extraction Changes

`walkForSymbols` gets the same recursive fix: instead of checking `ClassBodyTypes` to decide what to recurse into within a class body, it recurses into any named child that is in `ChunkableTypes` or `SymbolKindMap`. This ensures symbols inside nested containers are always extracted.

---

## Alternatives Considered

### Alternative A: Patch the Current Model (Add Missing Entries)

Add the missing node types to each language's `ClassTypes`/`ClassBodyTypes` and make `chunkClass` recurse when it finds a nested class.

**Steelman:** Smallest possible change. ~30 lines across two files. No architectural change. Low risk of regression. Every test keeps passing. If the only problem were Ruby nested modules, this would be the right call.

**Devil's advocate:** This is whack-a-mole. We found 10+ gaps across 7 languages by manual analysis. There will be more — every tree-sitter grammar update can introduce new node types, and the failure mode is silent data loss. The config surface (`ClassTypes` × `ClassBodyTypes` × `ClassBodyType` per language) is a maintenance trap. The Python `decorated_definition` bug shows that even well-tested languages have gaps hiding in plain sight.

**Why not chosen:** Solves the symptoms, not the cause. The root cause is that traversal is controlled by a whitelist that must enumerate every container pattern in every language. The recursive approach eliminates the whitelist entirely.

### Alternative B: Per-Language Chunker Implementations

Define a `Chunker` interface and implement it per language. Each language gets full control over its chunking strategy.

**Steelman:** Maximum flexibility. Ruby can handle `module > class` nesting, Python can handle `decorated_definition`, Rust can handle `trait` and `mod` — all with language-specific logic tuned to idioms. No compromises to fit a generic model.

**Devil's advocate:** 95% of the chunking logic is identical across languages: header collection, token counting, merge/split, signature extraction. Per-language chunkers would duplicate all of that. 9 languages × ~200 lines = ~1800 lines of near-identical code. When you fix a bug in the shared logic (e.g., the split boundary calculation), you fix it 9 times. When you add a language, you write the whole chunker from scratch. The variance is only in how bodies are traversed — which is exactly what the recursive approach fixes generically.

**Inversion (what if we tried the opposite?):** What if instead of more language-specific code, we had *less*? The recursive approach moves toward zero language-specific traversal logic. The only per-language knowledge is "which node types are meaningful" — a flat dictionary that maps directly to tree-sitter's `node-types.json`.

**Why not chosen:** Disproportionate complexity for the actual variance. The problem isn't that languages chunk differently — it's that the current generic chunker doesn't recurse.

### Alternative C: Use an External Chunking Library (e.g., CocoIndex)

Delegate chunking to a third-party library that already handles tree-sitter parsing and semantic splitting.

**Steelman:** Someone else maintains the language-specific logic. Battle-tested against real codebases. Focus shaktiman on what's unique (symbol extraction, dependency graph, MCP integration).

**Devil's advocate:** CocoIndex (the leading option) is Python-only with a Rust core — no Go bindings. It requires PostgreSQL as a runtime dependency. It only does chunking (no symbol extraction, no edge extraction, no dependency graph). Integrating it would mean running a Python sidecar process, adding IPC overhead, and still maintaining all the Go code for symbols/edges. The chunking itself is the simpler part of the problem — symbol and edge extraction are where the real complexity lies, and no external library provides those.

**Why not chosen:** No Go-native library exists that provides both chunking and code intelligence. The integration cost exceeds the benefit.

### Alternative D: Recursive AST-Driven Chunking (Chosen)

Replace the whitelist-driven traversal with a recursive size-driven traversal guided only by `ChunkableTypes`.

**Steelman for alternatives:** Alternative A is genuinely lower risk for a targeted fix. If we only cared about Ruby `module > class`, patching `ClassBodyTypes` and adding one recursion branch would be the pragmatic choice. The recursive approach touches more code and requires more careful testing.

**Why chosen despite the risk:**

1. **Eliminates an entire class of bugs.** Every nesting gap we found — and the ones we haven't found yet — disappears because the chunker doesn't need to be told where to recurse.

2. **Reduces config surface.** Three fields removed from `LanguageConfig`. Each new language needs only `ChunkableTypes`, `SymbolKindMap`, `ImportTypes`, and optionally `ExportType`. No body-type archaeology required.

3. **Tree-sitter grammar updates become safe.** If a grammar adds a new container node type, the chunker handles it automatically as long as it's in `ChunkableTypes`. No silent data loss from missing config entries.

4. **The algorithm is simpler to reason about.** "Recurse into anything too big, extract anything chunkable" vs "check if it's a ClassType, find its ClassBodyType, check if children match ClassBodyTypes." The mental model is one rule instead of three interacting maps.

5. **Fixes symbol extraction and chunking in one pass.** `walkForSymbols` gets the same simplification — recurse into any child in `ChunkableTypes`, not just `ClassBodyTypes`.

---

## Detailed Design

### 1. New Chunker Core: `chunkNode`

Replaces `chunkFile` + `chunkClass` + `chunkDecoratedDef` with a single recursive function.

```go
// chunkNode recursively decomposes a node into semantic chunks.
// It extracts chunkable children, emits a signature for the parent,
// and falls back to line-splitting for oversized leaf nodes.
func (p *Parser) chunkNode(node *tree_sitter.Node, source []byte, cfg *LanguageConfig, depth int) []types.ChunkRecord {
    content := node.Utf8Text(source)
    tokens := p.tokens.Count(content)
    nodeType := node.Kind()
    kind := cfg.ChunkableTypes[nodeType]  // "" if not chunkable

    // Small enough to be a single chunk
    if tokens <= maxChunkTokens {
        return []types.ChunkRecord{{
            SymbolName: extractName(node, source),
            Kind:       kindOrDefault(kind, "block"),
            StartLine:  line(node.StartPosition()),
            EndLine:    line(node.EndPosition()),
            Content:    content,
            TokenCount: tokens,
            Signature:  extractSignature(node, source),
        }}
    }

    // Too big — find chunkable children
    var extracted []types.ChunkRecord
    body := findBodyNode(node)  // heuristic: largest named child, or node itself
    target := body
    if target == nil {
        target = node
    }

    for i := 0; i < int(target.NamedChildCount()); i++ {
        child := target.NamedChild(uint(i))
        childType := child.Kind()

        // Unwrap export wrappers (TypeScript)
        if cfg.ExportType != "" && childType == cfg.ExportType {
            child = unwrapExport(child)
            if child == nil { continue }
            childType = child.Kind()
        }

        if _, chunkable := cfg.ChunkableTypes[childType]; chunkable {
            extracted = append(extracted, p.chunkNode(child, source, cfg, depth+1)...)
        }
    }

    if len(extracted) > 0 {
        // Parent becomes a signature chunk
        sig := buildSignatureFromExtracted(node, source, extracted, cfg)
        parent := types.ChunkRecord{
            SymbolName: extractName(node, source),
            Kind:       kindOrDefault(kind, "block"),
            StartLine:  line(node.StartPosition()),
            EndLine:    line(node.EndPosition()),
            Content:    sig,
            TokenCount: p.tokens.Count(sig),
            Signature:  extractSignature(node, source),
        }
        // Set parent index on extracted children
        result := []types.ChunkRecord{parent}
        parentIdx := 0  // will be reindexed later
        for i := range extracted {
            extracted[i].ParentIndex = &parentIdx
        }
        return append(result, extracted...)
    }

    // No chunkable children — fall back to line splitting
    return p.splitByLines(node, source, kind)
}
```

### 2. Top-Level Entry Point: `chunkFile`

Simplified to: skip comments, collect header (imports, package), then recurse.

```go
func (p *Parser) chunkFile(root *tree_sitter.Node, source []byte, cfg *LanguageConfig) []types.ChunkRecord {
    var chunks []types.ChunkRecord
    var headerParts []headerFragment

    for i := 0; i < int(root.NamedChildCount()); i++ {
        child := root.NamedChild(uint(i))
        nodeType := child.Kind()

        // Skip comments entirely — not indexed
        if nodeType == "comment" {
            continue
        }

        // Header material
        if cfg.ImportTypes[nodeType] ||
           nodeType == "package_clause" || nodeType == "package_declaration" {
            headerParts = append(headerParts, makeHeaderFragment(child, source))
            continue
        }

        // Chunkable node — recurse
        if _, ok := cfg.ChunkableTypes[nodeType]; ok {
            // Unwrap export wrappers
            if cfg.ExportType != "" && nodeType == cfg.ExportType {
                inner := findDeclarationChild(child)
                if inner != nil {
                    chunks = append(chunks, p.chunkNode(inner, source, cfg, 0)...)
                    continue
                }
            }
            chunks = append(chunks, p.chunkNode(child, source, cfg, 0)...)
            continue
        }

        // Non-chunkable, non-header → include in header
        headerParts = append(headerParts, makeHeaderFragment(child, source))
    }

    // Prepend header, post-process
    if len(headerParts) > 0 {
        header := buildHeaderChunk(headerParts, p.tokens)
        header.ChunkIndex = 0
        for i := range chunks { chunks[i].ChunkIndex = i + 1 }
        chunks = append([]types.ChunkRecord{header}, chunks...)
    }

    chunks = p.mergeSmallChunks(chunks)
    chunks = p.splitLargeChunks(chunks)  // catches any remaining oversized chunks

    for i := range chunks {
        chunks[i].ChunkIndex = i
        chunks[i].ParseQuality = "full"
    }
    return chunks
}
```

### 3. Signature Builder: `buildSignatureFromExtracted`

Replaces `buildClassSignatureWithConfig`. No longer needs `ClassBodyType` or `ClassBodyTypes`. Produces code-only signatures with no comments.

```go
func buildSignatureFromExtracted(node *tree_sitter.Node, source []byte, children []types.ChunkRecord, cfg *LanguageConfig) string {
    var sb strings.Builder

    // Declaration: extract the node's declaration line(s), stripping comments.
    // Walk non-comment children before the first chunkable child to find
    // the declaration text (e.g., "class Railtie < Rails::Railtie").
    decl := extractDeclarationLine(node, source)  // strips comments, returns first non-comment line(s)
    sb.WriteString(decl)
    sb.WriteString("\n")

    // Member listing
    for _, child := range children {
        if child.ParentIndex == nil { continue }  // only direct children
        sb.WriteString("  ")
        sb.WriteString(child.SymbolName)
        if child.Signature != "" {
            sb.WriteString(child.Signature)
        }
        sb.WriteString(" { ... }\n")
    }

    // Closing delimiter (heuristic: last line of node)
    nodeEnd := node.EndPosition().Row
    lastLine := getLine(source, int(nodeEnd))
    trimmed := strings.TrimSpace(lastLine)
    if trimmed == "end" || trimmed == "}" {
        sb.WriteString(trimmed)
        sb.WriteString("\n")
    }

    return sb.String()
}
```

### 4. Symbol Extraction: `walkForSymbols` Simplification

Replace the `ClassTypes`/`ClassBodyTypes` recursion with `ChunkableTypes`-guided recursion:

```go
func (p *Parser) walkForSymbols(node *tree_sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
    nodeType := node.Kind()

    // Export context tracking (TypeScript) — unchanged
    if cfg.ExportType != "" && nodeType == cfg.ExportType { ... }

    // Decorated definition unwrapping (Python) — unchanged
    if nodeType == "decorated_definition" { ... }

    // Symbol extraction — unchanged
    kind, isSymbol := cfg.SymbolKindMap[nodeType]
    if isSymbol { /* extract symbol, same as today */ }

    // Recursion: into chunkable children (replaces ClassTypes/ClassBodyTypes checks)
    if _, chunkable := cfg.ChunkableTypes[nodeType]; chunkable || !isSymbol {
        for i := 0; i < int(node.NamedChildCount()); i++ {
            p.walkForSymbols(node.NamedChild(uint(i)), source, exported, chunks, symbols, cfg)
        }
    }
}
```

This is simpler and handles all nesting automatically.

### 5. Language Config Changes

Each language config removes `ClassTypes`, `ClassBodyTypes`, `ClassBodyType`. Additionally, exhaustive analysis of every tree-sitter grammar (via `node-types.json` and `grammar.js`) identified missing `ChunkableTypes` and `SymbolKindMap` entries per language.

#### Ruby — 1 addition

| Addition | Kind | Why |
|---|---|---|
| `singleton_class` | `"class"` | `class << self ... end` — extremely common for defining class-level methods. Currently invisible to the chunker. |

Ruby DSL calls (`attr_accessor`, `include`, `has_many`, `scope`, `validates`, etc.) are generic `call` nodes — syntactically indistinguishable from any other method call. They are not extracted as separate chunks. Instead, they naturally remain in the class signature chunk when methods are extracted by the recursive chunker. See "Ruby Class-Level Calls" section above.

#### Python — 1 addition

| Addition | Kind | Why |
|---|---|---|
| `type_alias_statement` | `"type"` | Python 3.12 `type X = ...` syntax. Currently falls into header. Also add to `SymbolKindMap` as `"type"`. |

Note: `async def` is NOT a separate node type — the `async` keyword is an unnamed child of `function_definition`. No config change needed.

#### Rust — 3 additions

| Addition | Kind | Why |
|---|---|---|
| `function_signature_item` | `"function"` | Trait method signatures and `extern "C"` function declarations. Currently dropped inside trait bodies. Also add to `SymbolKindMap` as `"function"`. |
| `foreign_mod_item` | `"block"` | `extern "C" { ... }` blocks — containers with fn signatures and statics inside. |
| `union_item` | `"type"` | `union Foo { ... }` — rare but valid. Currently falls into header. Also add to `SymbolKindMap` as `"type"`. |

Note: `associated_type` (e.g., `type Item = T;` inside impl/trait) is small enough to be covered by `mergeSmallChunks` in practice.

#### Java — 2 additions

| Addition | Kind | Why |
|---|---|---|
| `compact_constructor_declaration` | `"method"` | Record compact constructors (no parameter list). Also add to `SymbolKindMap` as `"method"`. |
| `static_initializer` | `"block"` | `static { ... }` blocks in classes. Can be large. |

Note: `enum_constant` with body (anonymous subclass) is an edge case — the enum itself is already chunkable, and enum constants with bodies are rare enough that the size-driven split handles them.

#### TypeScript — 4 additions

| Addition | Kind | Why |
|---|---|---|
| `internal_module` | `"block"` | `namespace Foo { }` — very common in TS codebases. Container with nested definitions. Also add to `SymbolKindMap` as `"namespace"`. |
| `ambient_declaration` | `""` | `declare ...` wrapper — analogous to `export_statement`. Needs unwrapping to find the actual declaration inside. Add to `AmbientType` config field (new, see below). |
| `function_signature` | `"function"` | Overload signatures, ambient function declarations. Also add to `SymbolKindMap` as `"function"`. |
| `generator_function_declaration` | `"function"` | `function* foo() { }` — uncommon but valid. Also add to `SymbolKindMap` as `"function"`. |

Note: `ambient_declaration` requires unwrapping logic similar to `export_statement`. Add a new `AmbientType` field to `LanguageConfig` (TypeScript-only, empty for all other languages) to handle the `declare` wrapper. This adds one field but is simpler than special-casing the node type in the chunker.

#### JavaScript — no changes

Current config is complete for practical purposes. `using_declaration` (TC39 Stage 3) is too new to warrant inclusion yet.

#### Go — no changes

Current config is complete. All definition-level node types are covered.

#### Bash — no changes

Only `function_definition` is worth chunking. `declaration_command` (`declare`/`local`/`export`) could be added but is noisy in practice.

#### ERB — no changes

Template directives are already covered.

### Updated LanguageConfig

The `AmbientType` field is added for TypeScript's `declare` wrapper:

```go
type LanguageConfig struct {
    Name           string
    Grammar        *tree_sitter.Language
    ChunkableTypes map[string]string // node_type → chunk kind (traversal + labeling)
    SymbolKindMap  map[string]string // node_type → symbol kind
    ImportTypes    map[string]bool   // node types treated as imports
    ExportType     string            // export wrapper type (empty if N/A)
    AmbientType    string            // ambient declaration wrapper type (empty if N/A)
}
```

Both `ExportType` and `AmbientType` are wrapper node types that need unwrapping — the chunker strips the wrapper and processes the inner declaration. Only TypeScript uses `AmbientType` (`"ambient_declaration"`).

### Complete ChunkableTypes Reference

Derived from exhaustive analysis of each tree-sitter grammar's `node-types.json`.

**Ruby:**
```go
"method":           "function",
"singleton_method": "function",
"class":            "class",
"module":           "class",
"singleton_class":  "class",    // NEW
"lambda":           "function",
```

**Python:**
```go
"function_definition":   "function",
"class_definition":      "class",
"decorated_definition":  "",          // resolved from child
"type_alias_statement":  "type",      // NEW
"import_statement":      "",
"import_from_statement": "",
```

**Rust:**
```go
"function_item":           "function",
"function_signature_item": "function",  // NEW
"struct_item":             "type",
"enum_item":               "type",
"union_item":              "type",      // NEW
"trait_item":              "interface",
"impl_item":               "block",
"type_item":               "type",
"mod_item":                "block",
"foreign_mod_item":        "block",     // NEW
"use_declaration":         "",
"const_item":              "block",
"static_item":             "block",
"macro_definition":        "function",
```

**Java:**
```go
"class_declaration":               "class",
"interface_declaration":           "interface",
"enum_declaration":                "type",
"record_declaration":              "class",
"annotation_type_declaration":     "type",
"method_declaration":              "method",
"constructor_declaration":         "method",
"compact_constructor_declaration": "method",  // NEW
"static_initializer":              "block",   // NEW
"import_declaration":              "",
"package_declaration":             "",
"field_declaration":               "block",
```

**TypeScript:**
```go
"function_declaration":           "function",
"generator_function_declaration": "function",  // NEW
"function_signature":             "function",  // NEW
"class_declaration":              "class",
"abstract_class_declaration":     "class",
"interface_declaration":          "interface",
"type_alias_declaration":         "type",
"enum_declaration":               "type",
"internal_module":                "block",     // NEW (namespace)
"export_statement":               "",
"ambient_declaration":            "",          // NEW (declare wrapper)
"lexical_declaration":            "block",
"variable_declaration":           "block",
```

**JavaScript:**
```go
"function_declaration":           "function",
"class_declaration":              "class",
"generator_function_declaration": "function",
"export_statement":               "",
"lexical_declaration":            "block",
"variable_declaration":           "block",
```

**Go:**
```go
"function_declaration": "function",
"method_declaration":   "method",
"type_declaration":     "type",
"import_declaration":   "",
"var_declaration":      "block",
"const_declaration":    "block",
```

**Bash:**
```go
"function_definition": "function",
```

**ERB:**
```go
"directive":        "block",
"output_directive": "block",
"template":         "block",
```

### 6. Depth Guard

To prevent unbounded recursion on pathological ASTs:

```go
const maxChunkDepth = 10

func (p *Parser) chunkNode(node *tree_sitter.Node, source []byte, cfg *LanguageConfig, depth int) []types.ChunkRecord {
    if depth > maxChunkDepth {
        return p.splitByLines(node, source, "block")
    }
    // ...
}
```

### 7. Edge Extraction

`walkForEdges` already does a full recursive walk of the AST (lines 143-146 of edges.go). It does not use `ClassTypes` or `ClassBodyTypes`. **No changes needed.**

---

## Risks and Mitigations

### Risk 1: Regression in existing chunk boundaries

**Impact:** Changed chunk boundaries invalidate embeddings and alter search results.

**Mitigation:** This is expected and unavoidable for any chunking improvement. The daemon already handles reindexing: chunk deletes cascade to symbols and edges, and `embedding_status` resets to `pending`. First deploy after the change triggers a full reindex. Add a schema version bump or config flag to force reindex on upgrade.

**Mitigation (testing):** Write golden-file tests that capture chunk output for each language's test fixtures. Compare before/after to verify that the new chunker produces strictly more chunks (new chunks for previously-dropped code) and the same or better chunks for code that was already handled.

### Risk 2: Signature quality varies by language

**Impact:** The generic signature builder might produce awkward summaries for some languages (e.g., Ruby `end` vs Java `}`).

**Mitigation:** The signature builder uses heuristics (node start-to-first-child for header, last line for closing). If specific languages need better signatures, add a `SignatureStyle` enum to `LanguageConfig` (e.g., `BraceDelimited`, `EndDelimited`, `IndentDelimited`) — a minimal config addition, not a traversal control.

### Risk 3: Performance regression from deeper recursion

**Impact:** Deeply nested code (e.g., Rust module trees) could cause more function calls.

**Mitigation:** Tree-sitter ASTs are already fully materialized in memory. The recursion depth is bounded by `maxChunkDepth = 10`. Token counting is the expensive operation, not tree walking. Benchmark before/after on large real-world files (Rails, Django, tokio).

### Risk 4: Over-decomposition

**Impact:** Aggressively recursing could create too many tiny chunks for files with many small functions.

**Mitigation:** `mergeSmallChunks` (unchanged) catches this — chunks below `minChunkTokens` (20) are merged with the previous chunk. The size-driven recursion only triggers when a node exceeds `maxChunkTokens` (1024), so small functions are never decomposed.

### Risk 5: Large comment blocks distort search ranking

**Impact:** Files with 100+ line doc comments between methods could inflate chunk sizes and distort embedding similarity scores if comments are included in chunks.

**Decision:** Comments are excluded entirely from chunking. The `comment` node type is skipped at every level — top-level, recursive traversal, and signature generation. Comments are available via `Read` when needed. This eliminates the ranking distortion problem without adding schema complexity (e.g., `RelatedChunkIndex` fields) or query-time overhead (adjacency lookups).

**Tradeoff accepted:** Comments are not searchable via the shaktiman index. Users searching for concepts described only in comments must use `Grep`. This is acceptable because shaktiman indexes code structure for semantic retrieval, not prose.

---

## Testing Strategy

### Phase 1: Golden-file regression tests

For each language, create test cases with:
- Current test fixtures (verify same-or-better output)
- New fixtures exercising the specific gaps found:
  - Ruby: `module > class > methods`
  - Python: class with `@staticmethod`, `@property`, `@classmethod`
  - Rust: `trait` with methods, `mod` with nested items
  - Java: inner class, interface with default methods, enum with methods
  - TypeScript: `namespace` with exported functions

### Phase 2: Real-world corpus validation

Parse representative files from:
- Ruby: `rails/rails` (railties/lib/rails/railtie.rb)
- Python: `django/django` (django/db/models/base.py)
- Rust: `tokio-rs/tokio` (tokio/src/runtime/mod.rs)
- Java: `spring-projects/spring-framework` (spring-core)
- TypeScript: `angular/angular` (packages/core/src)

Verify: no chunk exceeds `maxChunkTokens` (except line-split fragments), all expected symbols are extracted, dependency graph is complete.

### Phase 3: Benchmark

Compare before/after on:
- Token count per chunk (distribution)
- Number of chunks per file
- Symbol extraction completeness
- Parsing time (wall clock)
- Memory usage

---

## Migration

### Backward Compatibility

This is a **breaking change to chunk content and boundaries**, not to APIs or storage schema. The storage schema is unchanged. The MCP tool interfaces are unchanged. The only visible effect is that reindexing produces different (more complete) chunks.

### Rollout

1. Implement behind a feature flag (`parser.recursive_chunking = true` in config).
2. Run both chunkers in parallel on the test corpus. Compare output.
3. When confident, make recursive the default. Remove the old chunker.
4. First run after upgrade triggers full reindex (detected via config version or chunk schema hash).

---

## Decision Outcome

The recursive AST-driven chunker eliminates an entire class of silent data loss bugs, reduces per-language config from 7 fields to 4, and handles arbitrary nesting depth without per-language traversal logic. The tradeoff is a larger initial change with a mandatory reindex, mitigated by golden-file tests and a feature flag rollout.
