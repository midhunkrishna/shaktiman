# Pending Edge Symbol Collision & Cross-Language Misresolution

> Plan for fixing two related issues in the edge resolution pipeline:
> (1) short-name collisions in `pending_edges`, and
> (2) cross-language misresolution where a Java import can resolve to a Python class.

---

## 0. Problem Statement

### 0.1 Short-Name Collision

`pending_edges.dst_symbol_name` stores only the short name of unresolved import
targets (e.g. `"List"` instead of `"java.util.List"`). When querying by short
name, all pending edges with that name match regardless of package origin.

| Scenario | What happens |
|---|---|
| Java imports `java.util.List` and guava's `ImmutableList` | Different short names, no collision |
| Java imports `java.util.List`, another lib also exports `List` | Both stored as `"List"`, queries return mixed results |
| Project defines its own `List` class | `ResolvePendingEdges` resolves `java.util.List` import to the project's `List` — wrong symbol |

### 0.2 Cross-Language Misresolution

`ResolvePendingEdges` and `lookupSymbolIDTx` do pure name-based lookup with no
language filter:

```go
// lookupSymbolIDTx — no language constraint
err := tx.QueryRowContext(ctx,
    "SELECT id FROM symbols WHERE name = ? LIMIT 1", name).Scan(&id)
```

If a Python file defines `class Config` and a Java file has
`import com.example.Config`, the Java import edge can resolve to the Python
class's symbol ID. The `LIMIT 1` makes this nondeterministic — whichever row
SQLite returns first wins.

This also affects `ResolvePendingEdges`: when a new symbol is inserted, ALL
pending edges with a matching `dst_symbol_name` are resolved to it, regardless
of source language.

### 0.3 Combined Impact

In a polyglot monorepo (the reported use case: 6k Java, 1k JS, 500 Python,
1k TypeScript files), common type names like `List`, `Config`, `Client`,
`Handler`, `Builder`, `Error`, `Result` are defined across multiple languages.
Both issues compound: a Java `Config` import resolves to whichever `Config`
symbol was indexed first, which might be from a Python, TypeScript, or Go file.

---

## 1. Current Data Flow

```
Parser (edges.go)
  javaImportEdges: "import java.util.List;" → addEdge(owner, "List", "imports")
                                                       short name only ──┘
                                                       no language tag ──┘
    │
    ▼
Writer (writer.go)
  InsertEdges:
    srcID = symbolIDs["MyService"]     ← resolved within same file
    dstID = lookupSymbolIDTx("List")   ← global lookup, no language filter
      found?  → edges(src_symbol_id, dst_symbol_id)  ← may be wrong symbol
      !found? → pending_edges(src_symbol_id, "List")  ← no qualified name
    │
    ▼
ResolvePendingEdges (on future file index):
  SELECT FROM pending_edges WHERE dst_symbol_name IN ('Config', 'List', ...)
    ← matches ALL pending edges with these names across ALL languages
  lookupSymbolIDTx("List", 0)
    ← LIMIT 1, no language filter — returns arbitrary match
```

---

## 2. Proposed Fix

Two changes that work together:

### 2.1 Add Qualified Name to EdgeRecord and pending_edges

Each language's import handler extracts the full import path alongside the short
name. This disambiguates `java.util.List` from `com.example.List` at the data
level.

**Schema (v2 → v3):**

```sql
ALTER TABLE pending_edges ADD COLUMN dst_qualified_name TEXT DEFAULT '';
```

**EdgeRecord type:**

```go
type EdgeRecord struct {
    // ... existing fields ...
    DstQualifiedName string // e.g. "java.util.List", "fmt", "std::collections::HashMap"
}
```

**Parser — new helper and per-language changes:**

```go
func (c *edgeContext) addEdgeQualified(src, dst, qualifiedDst, kind string) { ... }
```

| Language | Short name | Qualified name source |
|---|---|---|
| Java | `scoped_identifier.name` → `"List"` | `scoped_identifier` content → `"java.util.List"` |
| Go | import path last component → `"fmt"` | full path string → `"fmt"` or `"github.com/foo/bar"` |
| Python | `dotted_name` → `"os"` | same (Python imports are already module paths) |
| TypeScript | `import_specifier.name` → `"foo"` | module path + name → `"@scope/pkg/foo"` |
| Groovy | last dot component → `"List"` | `dotted_identifier` content → `"java.util.List"` |
| Rust | `scoped_identifier.name` → `"HashMap"` | `scoped_identifier` content → `"std::collections::HashMap"` |

**Storage — InsertEdges:**

```go
pendingStmt: INSERT INTO pending_edges
    (src_symbol_id, dst_symbol_name, dst_qualified_name, kind)
    VALUES (?, ?, ?, ?)
```

### 2.2 Add Language Filtering to Edge Resolution

Add language awareness to `pending_edges` resolution and symbol lookup so a Java
import never resolves to a Python symbol.

**Option A — Store language on pending_edges (preferred):**

```sql
ALTER TABLE pending_edges ADD COLUMN src_language TEXT DEFAULT '';
```

Populated from the source file's language during `InsertEdges`. Then
`ResolvePendingEdges` joins through `symbols → files` to ensure the destination
symbol's language matches the source:

```sql
SELECT pe.id, pe.src_symbol_id, pe.dst_symbol_name, pe.kind, pe.src_language
FROM pending_edges pe
WHERE pe.dst_symbol_name IN (...)

-- Resolution lookup becomes:
SELECT s.id FROM symbols s
JOIN files f ON s.file_id = f.id
WHERE s.name = ? AND f.language = ?
LIMIT 1
```

**Option B — Derive language from src_symbol_id at resolution time:**

No schema change. `ResolvePendingEdges` joins `pending_edges → symbols → files`
to get the source language, then filters destination candidates by the same
language. More complex query but avoids adding a column.

**Recommendation: Option A.** The extra column is cheap, makes queries simpler,
and avoids a 3-table join during resolution. Both columns (`dst_qualified_name`
and `src_language`) can be added in a single migration.

**Combined migration (v2 → v3):**

```sql
ALTER TABLE pending_edges ADD COLUMN dst_qualified_name TEXT DEFAULT '';
ALTER TABLE pending_edges ADD COLUMN src_language TEXT DEFAULT '';
```

### 2.3 Fix lookupSymbolIDTx

Add a `language` parameter:

```go
func lookupSymbolIDTx(ctx context.Context, tx *sql.Tx, name string, fileID int64, language string) (int64, error) {
    // Try same-file first (unchanged)
    // ...
    // Fallback: filter by language
    if language != "" {
        err := tx.QueryRowContext(ctx,
            `SELECT s.id FROM symbols s JOIN files f ON s.file_id = f.id
             WHERE s.name = ? AND f.language = ? LIMIT 1`,
            name, language).Scan(&id)
        if err == nil { return id, nil }
    }
    // Final fallback: any language (backward compat for non-import edges)
    err := tx.QueryRowContext(ctx,
        "SELECT id FROM symbols WHERE name = ? LIMIT 1", name).Scan(&id)
    // ...
}
```

Call sites pass the source file's language for import edges, empty string for
call/inheritance edges (which are always within the same language anyway).

---

## 3. Files to Modify

| File | Change |
|---|---|
| `internal/types/entities.go` | Add `DstQualifiedName` to `EdgeRecord` |
| `internal/storage/schema.go` | Bump to v3, add migration for both new columns |
| `internal/storage/graph.go` | Update `InsertEdges` to store qualified name + language; update `lookupSymbolIDTx` to accept language; update `ResolvePendingEdges` to filter by language |
| `internal/parser/edges.go` | Add `addEdgeQualified` helper; update 6 language import handlers to extract full import path |
| `internal/daemon/writer.go` | Pass file language to `InsertEdges` |
| `internal/format/types.go` | Add `QualifiedName` to `SymbolRef` (if merged after `fix-symbol-missing`) |
| `internal/mcp/tools.go` | Surface qualified name in responses (if merged after `fix-symbol-missing`) |

---

## 4. Migration Safety

- Two `ALTER TABLE ADD COLUMN` with `DEFAULT` — safe in SQLite, no table rewrite.
- Existing `pending_edges` rows get empty defaults for both columns.
- Old pending edges without language info fall back to language-unfiltered lookup
  (same behavior as today). Only newly indexed edges get the stricter resolution.
- Re-indexing a project fully populates both columns for all pending edges.

---

## 5. Testing Plan

| Test | Verifies |
|---|---|
| `TestEdges_JavaImportQualifiedName` | Java handler extracts `"List"` and `"java.util.List"` |
| `TestEdges_GoImportQualifiedName` | Go handler extracts short + full module path |
| `TestEdges_RustImportQualifiedName` | Rust handler extracts short + `"std::collections::HashMap"` |
| `TestInsertEdges_QualifiedNameAndLanguageStored` | `pending_edges` row has correct `dst_qualified_name` and `src_language` |
| `TestResolvePendingEdges_LanguageFilter` | Java pending edge does NOT resolve to a Python symbol with the same name |
| `TestResolvePendingEdges_SameLanguageResolves` | Java pending edge resolves to a Java symbol correctly |
| `TestLookupSymbolIDTx_LanguageFilter` | `lookupSymbolIDTx("Config", 0, "java")` returns Java symbol, not Python |
| `TestIntegration_CrossLanguageNoMisresolution` | Polyglot project: Python `Config` class + Java `import Config` — Java edge stays pending, not resolved to Python |
| `TestMigration_V2toV3` | Existing DB gains both columns, existing rows get empty defaults |

---

## 6. Scope

**In scope:**
- `dst_qualified_name` column and parser extraction for all 6 languages
- `src_language` column and language-filtered resolution
- `lookupSymbolIDTx` language parameter
- Schema migration v2 → v3
- Surfacing qualified name in query results

**Out of scope:**
- Adding qualified name to the resolved `edges` table (only `pending_edges`)
- Using qualified name for smarter resolution (e.g. matching `java.util.List`
  to a project class in `java.util` package) — qualified name is for display
  disambiguation in this iteration, not for resolution logic
- CLI `symbols` / `deps` commands (separate feature gap tracked elsewhere)

---

## 7. Code Coverage

Enforce a minimum of **90% line coverage** on all changed/new code. Measure with:

```bash
go test -tags sqlite_fts5 -coverprofile=cover.out ./internal/parser/ ./internal/storage/ ./internal/daemon/ ./internal/mcp/
go tool cover -func=cover.out | grep -E '(edges|graph|writer|schema|tools)\.go'
```

Coverage targets by file:

| File | Minimum | Rationale |
|---|---|---|
| `internal/parser/edges.go` | 90% | All 6 language import handlers must be exercised with qualified name extraction |
| `internal/storage/graph.go` | 90% | `InsertEdges`, `ResolvePendingEdges`, `lookupSymbolIDTx` — all paths including language-filtered and fallback |
| `internal/daemon/writer.go` | 80% | Only the `InsertEdges` call site changes; rest of `processEnrichmentJob` is existing code |
| `internal/storage/schema.go` | 90% | Migration path v2→v3 must be tested for both fresh and existing databases |

Gaps to watch:
- Each language handler must have at least one test that asserts `DstQualifiedName` is populated.
- `lookupSymbolIDTx` needs tests for: same-file match, same-language match, cross-language rejection, and fallback-to-any.
- `ResolvePendingEdges` needs tests for: same-language resolution, cross-language skip, and backward compat (empty `src_language` falls back to unfiltered).

---

## 8. Adversarial Analysis

Run `/adversarial-analyst` on the final changeset before merge. The analysis
must cover these specific attack surfaces:

**Edge resolution correctness:**
- Can a pending edge with `src_language=""` (pre-migration) resolve to a wrong-language symbol?
- What happens when `ResolvePendingEdges` encounters a name that exists in 3 languages — does `LIMIT 1` with the language filter always pick the right one, or can file ordering cause nondeterminism within the same language?
- If a file is re-indexed and its language field changes (e.g. `.tsx` reclassified), do stale pending edges with the old language get cleaned up?

**Qualified name extraction:**
- Are there tree-sitter AST shapes where the qualified name extraction returns partial paths? (e.g. Java star imports `import java.util.*`, Python `from os.path import *`, Rust `use std::*`)
- What happens with aliased imports? (Java: none. Go: `import alias "pkg"`. Python: `import X as Y`. Rust: `use Foo as Bar`. TS: `import { X as Y }`.) Does the qualified name reflect the original or the alias?
- What happens with re-exports? (TS: `export { X } from './other'`)

**Migration and data integrity:**
- Can the v2→v3 migration run concurrently with the daemon reading pending_edges?
- After migration, if the daemon crashes mid-index, are partially-written pending edges (with qualified name) and old pending edges (without) mixed in the table? Does this cause query issues?

**Performance at scale:**
- `ResolvePendingEdges` now joins `symbols → files` for language filtering. In a monorepo with 100k+ symbols, does this degrade? Is an index needed on `files(language)`?
- The qualified name column increases row size. For 120k pending_edges, what is the storage impact?

**Failure mode classification:**
- Which failures are loud (error returned) vs silent (wrong data stored)?
- What is the blast radius of a misresolved edge — does it cascade to other queries or is it contained?
