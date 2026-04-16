---
title: Migrating to Shaktiman
sidebar_position: 1
---

# Migrating to Shaktiman

Coming from another tool? These pages describe the mental-model shift, show a
feature-parity table, and are honest about what you'd be giving up.

## Picking the right comparison

| You currently use | Read |
|---|---|
| `grep` / `ripgrep` for code search | [From grep / ripgrep](./from-grep-ripgrep) |
| `ctags` / LSP for symbol navigation | [From ctags / LSP](./from-ctags-lsp) |
| Sourcegraph (Cody) for multi-repo code intelligence | [From Sourcegraph / Cody](./from-sourcegraph-cody) |
| Claude Code's built-in `Grep` / `Glob` / `Read` loops | [From Claude default tools](./from-claude-default-tools) |

## The recurring theme

Shaktiman **ranks** — it returns the top-N most relevant hits. Exact-match
tools **enumerate** — they return every match. Both are valuable; they solve
different problems. Every migration page ends with "when to keep the old tool"
because Shaktiman is meant to complement, not replace, the literal-search tools
in your belt.

## Universal: what Shaktiman doesn't do

- **Cross-repo search.** Shaktiman indexes one project at a time (one
  `.shaktiman/` directory per project root).
- **Static analysis / linting / type-checking.** That's LSP / language-specific
  tooling.
- **Refactoring commands.** `dependencies` helps you decide what to change;
  you still do the edit with your editor.
- **History search older than the daemon's lifetime.** `diff` reads Shaktiman's
  own change log, which only has what was captured while the daemon (or the
  CLI) was indexing. `git log` remains the source of truth for commit history.

If these are must-haves, keep the tool you're migrating from — and use
Shaktiman alongside it.
