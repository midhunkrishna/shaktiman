---
title: ADR-004 — Recursive AST-Driven Chunking
sidebar_position: 5
---

# ADR-004: Recursive AST-Driven Chunking

**Status:** PROPOSED at merge, **shipped** since (the status field in the full ADR predates the merge commit).

:::info[This is a summary]

The full ADR — context, alternatives, detailed algorithm, 400+ lines of per-language
implementation notes, risk analysis, testing strategy, and migration plan — lives
in the repo:
[`docs/design/adr-004-recursive-ast-driven-chunking.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-004-recursive-ast-driven-chunking.md).
Follow-up work is tracked in
[`impl-004-recursive-ast-driven-chunking.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/impl-004-recursive-ast-driven-chunking.md)
and
[`review-findings/parser-bugs-from-recursive-chunking.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/review-findings/parser-bugs-from-recursive-chunking.md).

:::

## Status today

**Shipped.** Recursive AST-driven chunking is implemented in `internal/parser/`
with container/leaf node handling per language. Language configs in
`internal/parser/languages.go` declare `NodeMeta{Kind, IsContainer}` for each
chunkable node type. See [Supported languages](/reference/supported-languages)
for what's wired up today.

## Context

The original parser used a whitelist-driven, two-level chunking model: top-level
`chunkFile` dispatched class-like nodes to `chunkClass`, which extracted exactly
one level of methods from the class body. It required three config maps
(`ClassTypes`, `ClassBodyTypes`, `ClassBodyType`) per language to control
traversal — brittle, hard to extend, and unable to handle nested structures
(e.g., methods inside nested classes, Rust modules inside modules, Ruby
singleton classes).

## Decision

**Replace the two-level model with a recursive, size-driven AST traversal that
uses `ChunkableTypes` as the sole traversal guide.**

Core algorithm (paraphrased from the full ADR):

```
chunkNode(node):
  if tokens(node) <= maxChunkTokens:
    emit node as chunk (excluding comment children)
    return

  // Node is too big — decompose.
  for each named child of node:
    skip if child is a comment
    if child is chunkable OR child is too big:
      recurse into child

  if recursion produced children:
    emit a signature chunk for the parent + the extracted children
  else:
    fall back to splitByLines
```

**Comments are excluded entirely** — not emitted as chunks, not folded into
signatures, not collected into the file header. Rationale: doc blocks distort
search ranking; tree-sitter can't reliably distinguish doc comments from
regular comments across languages; the source is available via `Read` when
needed.

## Key constraints

- **`ChunkableTypes` is the single source of truth** for traversal. Adding a
  language means declaring its node types in `NodeMeta` — no need to pick which
  are "class-like".
- **Handles arbitrary nesting.** Nested classes, Rust modules, trait impls,
  Ruby singleton classes all just work because any chunkable child can itself
  be chunkable.
- **Size-driven recursion.** The chunker only recurses when a node exceeds
  `maxChunkTokens`; small nodes are emitted as-is even if they contain
  chunkable children.
- **Fallback path (`splitByLines`).** When a huge node has no chunkable
  children (e.g., enormous JSON-in-JS or a massive method body), the chunker
  splits by lines. Known to produce oversized chunks for pathological files —
  see the parser-bugs follow-up link above.

## When to revisit

- Adding a language with unusual grammar shapes (check the existing parser-bugs
  findings first).
- If `splitByLines` continues to produce oversized chunks that break embedding
  or ranking in practice.
- If the comment-exclusion rule becomes an issue (e.g., someone wants
  docstring-aware search).
