---
title: Supported Languages
sidebar_position: 2
---

# Supported Languages

Shaktiman indexes source files using [tree-sitter](https://tree-sitter.github.io/)
grammars. The following languages are wired today, with their file extensions, imports
recognized, and chunkable node types. The authoritative list lives in
`internal/parser/languages.go` (`GetLanguageConfig`).

## Languages

| Language | Extensions | Import / export / ambient wrappers | Chunkable kinds |
|---|---|---|---|
| **TypeScript** | `.ts`, `.tsx` | import: `import_statement`; export: `export_statement`; ambient: `ambient_declaration` | function, class, interface, type, block, method, namespace |
| **JavaScript** | `.js`, `.jsx`, `.mjs`, `.cjs` | import: `import_statement`; export: `export_statement` | function, class, type, block, method |
| **Python** | `.py` | import: `import_statement`, `import_from_statement` | function, class, type, block |
| **Go** | `.go` | import: `import_declaration` | function, method, type, block |
| **Rust** | `.rs` | import: `use_declaration` | function, type, interface, block (modules / impl / traits / macros) |
| **Java** | `.java` | import: `import_declaration` | class, interface, type, method, block |
| **Ruby** | `.rb`, `.rake`, `.gemspec` | — (require / require_relative are method calls, not distinct import nodes) | function, class |
| **ERB** | `.erb` | — | block (directive / output_directive / template) |
| **Bash** | `.sh`, `.bash` | — | function |

## What "chunkable" means

Every language config maps tree-sitter node types to a `NodeMeta{Kind, IsContainer}`
entry:

- **`IsContainer: true`** — the parser recurses into the node's body (classes, traits,
  modules, impl blocks, export wrappers). Nested methods, trait methods, and so on
  each become their own chunks.
- **`IsContainer: false`** — the parser stops at the declaration (functions, types,
  const/var).
- **Wrappers** (e.g. `export_statement`, `decorated_definition`) have `Kind: ""` and
  are resolved via their inner child.

The recursive-AST design is captured in
[ADR-004: Recursive AST-Driven Chunking](/design/adr-004-recursive-chunking).

## Adding a language

Per the [Contributing guide](/contributing), adding a language requires:

1. Add a `LanguageConfig` entry in `internal/parser/languages.go` with the
   tree-sitter grammar, import node types, and chunkable kinds.
2. Import the tree-sitter grammar (`tree-sitter-<lang>`).
3. Register file extensions in `internal/daemon/scanner.go` and
   `internal/core/fallback.go`.
4. Add test fixtures under `testdata/`.
5. Optionally add test-file patterns in `langTestPatterns`
   (`internal/types/config.go`).

## Known gaps

Tree-sitter grammar coverage is not uniform — a language shown above may still have
specific constructs that don't produce an expected chunk. See
[Known Limitations](/reference/limitations) and
`docs/review-findings/parser-bugs-from-recursive-chunking.md` for the current list.

## Source of truth

- `internal/parser/languages.go` — `GetLanguageConfig`, per-language `NodeMeta`
  mappings, grammar imports.
- `internal/types/config.go` — `langTestPatterns` (test-file glob defaults).
- `internal/daemon/scanner.go` — file extension → language dispatch.
