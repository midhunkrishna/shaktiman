# 001 — Fix Semantic Search Identifier Lookup

## Problem

Searching for an exact function name like `buildBuyerOptions` via `shaktiman search` returns
completely unrelated chunks (e.g. `require 'MailchimpTransactional'` from `mandrill_mailer.rb`)
with near-perfect scores (~0.608), while the correct chunks score too low to appear in the top 10.

## Root Cause

Two compounding causes were confirmed by direct database analysis and score simulation.

### Cause 1: Missing `nomic-embed-text` task prefixes

`nomic-embed-text` was trained with contrastive learning using task-specific prefixes:

- `search_document:` — for documents at index time
- `search_query:` — for queries at search time

These prefixes are not optional. They are how the model knows whether input is a query seeking
a match or a document being indexed. Without them, both are embedded in the same generic
subspace where the geometric relationship between query and document vectors breaks down.

**Confirmed missing** in two places:

```go
// embed_worker.go:336 — document indexing
texts[j] = job.Content   // ← raw source code, no prefix

// engine.go:embedQuery — query at search time
vec, err := e.embedder.Embed(ctx, input.Query)  // ← raw identifier, no prefix
```

### Cause 2: Short-string hubness in high-dimensional space

`chunk 3095` (`require 'MailchimpTransactional'`) has only **6 tokens**. The query
`"buildBuyerOptions"` tokenizes to ~3 wordpiece tokens. In a 768-dimension embedding space,
very short inputs produce vectors dominated by the model's default state rather than content.
All short, low-entropy strings cluster into the same narrow region — any two short strings
have artificially high cosine similarity regardless of meaning.

### Proof (from database)

FTS5 correctly finds `buildBuyerOptions` chunks:

```
chunk 8987:  BM25=-12.65 → normalized keyword score = 0.253
chunk 9241:  BM25=-11.25 → normalized keyword score = 0.225
chunk 10449: BM25=-12.05 → normalized keyword score = 0.241
```

Hybrid ranking weights (after session weight redistribution):

```
Semantic:   0.4706
Structural: 0.2353
Change:     0.1765
Session:    0.0000  (no access_log entries)
Keyword:    0.1176
```

Score simulation for chunk 8987 (no semantic signal):

```
0.1176 × 0.253  +  0.1765 × 0.7225  =  0.157
```

Score reconstruction for chunk 3095 (the wrong #1 result, score=0.608):

```
0.4706 × 1.0  +  0.2353 × 0.04  +  0.1765 × 0.7225  ≈  0.608
```

`chunk 3095` has semantic ≈ 1.0 for the query `"buildBuyerOptions"` — a near-perfect cosine
match between `"require 'MailchimpTransactional'"` and the identifier. This is the broken
embedding signal.

**Maximum possible score for chunk 8987 without semantic (perfect struct+change):**

```
0.1176×0.253 + 0.1765×0.7225 + 0.2353×1.0 = 0.393
```

`0.393 < 0.608` — the correct chunk **cannot beat the wrong chunk** under current conditions,
regardless of structural or change signals. Weight tuning alone does not fix this.

Adversarial simulation of Option A (Keyword=0.50):

```
chunk 8987 score = 0.209   chunk 3095 score = 0.294   → still loses
```

Keyword weight must reach ≥ 0.70 before chunk 8987 wins — an extreme and fragile threshold.

---

## Solution

Two independent fixes. Fix 1 corrects the data. Fix 2 is a reliable bypass that works
regardless of embedding quality.

---

## Fix 1: `nomic-embed-text` Task Prefixes

### 1.1 — Add prefix fields to `Config`

**File:** `internal/types/config.go`

Add to `Config` struct (after `EmbedTimeout`):

```go
// EmbedQueryPrefix is prepended to query text before embedding.
// For nomic-embed-text, use "search_query: ".
EmbedQueryPrefix string

// EmbedDocumentPrefix is prepended to chunk content before embedding.
// For nomic-embed-text, use "search_document: ".
EmbedDocumentPrefix string
```

Add to `DefaultConfig`:

```go
EmbedQueryPrefix:    "search_query: ",
EmbedDocumentPrefix: "search_document: ",
```

Add to `tomlEmbedding` struct:

```go
type tomlEmbedding struct {
    OllamaURL      *string `toml:"ollama_url"`
    Model          *string `toml:"model"`
    Dims           *int    `toml:"dims"`
    BatchSize      *int    `toml:"batch_size"`
    Timeout        *string `toml:"timeout"`
    QueryPrefix    *string `toml:"query_prefix"`     // new
    DocumentPrefix *string `toml:"document_prefix"`  // new
}
```

Add to `LoadConfigFromFile` below the existing `[embedding]` parsing block:

```go
if v := tc.Embedding.QueryPrefix; v != nil {
    cfg.EmbedQueryPrefix = *v
}
if v := tc.Embedding.DocumentPrefix; v != nil {
    cfg.EmbedDocumentPrefix = *v
}
```

Update `sampleConfig` const:

```toml
[embedding]
# ollama_url = "http://localhost:11434"
# model = "nomic-embed-text"
# dims = 768
# batch_size = 128
# timeout = "120s"
# query_prefix = "search_query: "       # Task prefix for query embedding (nomic-embed-text)
# document_prefix = "search_document: " # Task prefix for document embedding (nomic-embed-text)
```

---

### 1.2 — Apply document prefix at index time

**File:** `internal/vector/embed_worker.go`

Add `documentPrefix` field and input:

```go
type EmbedWorkerInput struct {
    Store          types.VectorStore
    Embedder       *OllamaClient
    BatchSize      int
    OnBatchDone    func(chunkIDs []int64)
    DocumentPrefix string  // new
}

type EmbedWorker struct {
    // ... existing fields ...
    documentPrefix string  // new
}
```

In `NewEmbedWorker`, assign: `documentPrefix: input.DocumentPrefix`

In `processBatch` (line ~160):

```go
texts := make([]string, len(batch))
for i, j := range batch {
    texts[i] = w.documentPrefix + j.Content  // was: j.Content
}
```

In `reconcileAndEmbed` (line ~332):

```go
for j, job := range needEmbed {
    texts[j] = w.documentPrefix + job.Content  // was: job.Content
    needIDs[j] = job.ChunkID
}
```

In `retryDeferred` (line ~476):

```go
vecs, err := w.embedder.EmbedBatch(ctx, []string{w.documentPrefix + dc.job.Content})
// was: []string{dc.job.Content}
```

---

### 1.3 — Apply query prefix at search time

**File:** `internal/core/engine.go`

Add `queryPrefix` field to `QueryEngine`:

```go
type QueryEngine struct {
    // ... existing fields ...
    queryPrefix string  // new
}
```

Add setter (consistent with `SetVectorStore`/`SetSessionStore` pattern):

```go
func (e *QueryEngine) SetQueryPrefix(prefix string) {
    e.queryPrefix = prefix
}
```

Update `embedQuery` to use prefixed string as both the input and cache key:

```go
func (e *QueryEngine) embedQuery(ctx context.Context, query string) ([]float32, error) {
    prefixed := e.queryPrefix + query
    if vec, ok := e.embedCache.Get(prefixed); ok {
        return vec, nil
    }
    vec, err := e.embedder.Embed(ctx, prefixed)
    if err != nil {
        return nil, err
    }
    e.embedCache.Put(prefixed, vec)
    return vec, nil
}
```

The cache key uses the prefixed string so a prefix change (e.g. switching models) does not
serve stale cached vectors.

---

### 1.4 — Wire prefix config into daemon

**File:** `internal/daemon/daemon.go`

Where `EmbedWorker` is constructed, pass the document prefix:

```go
embedWorker := vector.NewEmbedWorker(vector.EmbedWorkerInput{
    Store:          vectorStore,
    Embedder:       ollamaClient,
    BatchSize:      cfg.EmbedBatchSize,
    OnBatchDone:    ...,
    DocumentPrefix: cfg.EmbedDocumentPrefix,  // new
})
```

Where `QueryEngine` is constructed, set the query prefix:

```go
engine.SetQueryPrefix(cfg.EmbedQueryPrefix)
```

---

### 1.5 — Add `re-embed` CLI command

**File:** `cmd/shaktiman/main.go`

All existing embeddings in `embeddings.bin` were generated without prefixes and must be
discarded. Add a `re-embed` subcommand that deletes `embeddings.bin` and resets all
`embedded = 0` flags in the database so `shaktiman index --embed` regenerates everything.

```go
func reEmbedCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "re-embed <project-root>",
        Short: "Discard existing embeddings and regenerate with current config",
        Long: `Deletes embeddings.bin and resets all embedded flags in the database.
After running this, execute 'shaktiman index --embed <project-root>' to regenerate
all embeddings using the prefixes configured in shaktiman.toml.`,
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            projectRoot := args[0]
            cfg := types.DefaultConfig(projectRoot)
            cfg, err := types.LoadConfigFromFile(cfg)
            if err != nil {
                return fmt.Errorf("load config: %w", err)
            }
            ctx := context.Background()

            store, closer, err := openStore(cfg)
            if err != nil {
                return fmt.Errorf("open store: %w", err)
            }
            defer closer()

            // Delete embeddings.bin
            if err := os.Remove(cfg.EmbeddingsPath); err != nil && !os.IsNotExist(err) {
                return fmt.Errorf("delete embeddings: %w", err)
            }
            fmt.Printf("Deleted: %s\n", cfg.EmbeddingsPath)

            // Reset all embedded flags
            ws, ok := store.(types.WriterStore)
            if !ok {
                return fmt.Errorf("store does not implement WriterStore")
            }
            if err := ws.ResetAllEmbeddedFlags(ctx); err != nil {
                return fmt.Errorf("reset embedded flags: %w", err)
            }
            fmt.Println("Reset embedded flags.")
            fmt.Printf("Run: shaktiman index --embed %s\n", projectRoot)
            return nil
        },
    }
}
```

Register: `rootCmd.AddCommand(reEmbedCmd())`

**Migration path for existing deployments:**

```bash
shaktiman re-embed /path/to/project
shaktiman index --embed /path/to/project
```

---

## Fix 2: Symbol-Exact Pre-Search Bypass

Runs before hybrid search for identifier-style queries. Queries the `symbols` table directly
(which already correctly contains `buildBuyerOptions`). Returns score=1.0 for exact matches,
prepended before hybrid results. Independent of embedding quality.

### 2.1 — Add `GetSymbolByNameCI` to the store

**File:** `internal/storage/sqlite/metadata.go`

```go
// GetSymbolByNameCI returns symbols whose name matches case-insensitively.
func (s *Store) GetSymbolByNameCI(ctx context.Context, name string) ([]types.SymbolRecord, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT id, chunk_id, file_id, name, qualified_name, kind,
               line, signature, visibility, is_exported
        FROM symbols WHERE name = ? COLLATE NOCASE`, name)
    if err != nil {
        return nil, fmt.Errorf("get symbols named (ci) %s: %w", name, err)
    }
    defer rows.Close()
    return scanSymbols(rows)
}
```

**File:** `internal/types/interfaces.go`

Add to `MetadataStore` interface:

```go
// GetSymbolByNameCI returns symbols matching the given name case-insensitively.
GetSymbolByNameCI(ctx context.Context, name string) ([]types.SymbolRecord, error)
```

---

### 2.2 — Add `lookup.go` to the core package

**File:** `internal/core/lookup.go` (new file)

```go
package core

import (
    "context"
    "strings"
    "unicode"

    "github.com/shaktimanai/shaktiman/internal/types"
)

// isIdentifierQuery returns true when query looks like a single code identifier:
// no whitespace, contains only letters/digits/underscores, has at least one letter.
// Matches camelCase, PascalCase, snake_case, SCREAMING_SNAKE.
func isIdentifierQuery(query string) bool {
    if strings.ContainsAny(query, " \t\n\r") {
        return false
    }
    if len(query) < 2 {
        return false
    }
    hasLetter := false
    for _, r := range query {
        if unicode.IsLetter(r) {
            hasLetter = true
        } else if !unicode.IsDigit(r) && r != '_' {
            return false
        }
    }
    return hasLetter
}

// SymbolExactSearch looks up chunks by exact symbol name.
// Returns nil (not an error) when no symbols match, so callers fall through gracefully.
// Tries case-sensitive match first, then case-insensitive fallback.
// Score is always 1.0 — exact symbol matches are maximum relevance.
func SymbolExactSearch(ctx context.Context, store types.MetadataStore, query string, filter TestFilter) ([]types.ScoredResult, error) {
    // Case-sensitive first (cheaper, more precise)
    syms, err := store.GetSymbolByName(ctx, query)
    if err != nil {
        return nil, err
    }
    // Case-insensitive fallback
    if len(syms) == 0 {
        syms, err = store.GetSymbolByNameCI(ctx, query)
        if err != nil {
            return nil, err
        }
    }
    if len(syms) == 0 {
        return nil, nil
    }

    // Collect unique chunk IDs respecting test filter
    type symEntry struct {
        sym  types.SymbolRecord
        path string
    }
    seen := make(map[int64]bool, len(syms))
    var entries []symEntry

    for _, sym := range syms {
        if sym.ChunkID == 0 || seen[sym.ChunkID] {
            continue
        }
        isTest, err := store.GetFileIsTestByID(ctx, sym.FileID)
        if err != nil {
            continue
        }
        if filter.ExcludeTests && isTest {
            continue
        }
        if filter.TestOnly && !isTest {
            continue
        }
        path, err := store.GetFilePathByID(ctx, sym.FileID)
        if err != nil {
            continue
        }
        seen[sym.ChunkID] = true
        entries = append(entries, symEntry{sym: sym, path: path})
    }

    if len(entries) == 0 {
        return nil, nil
    }

    // Batch hydration path (1 query instead of N)
    chunkIDs := make([]int64, len(entries))
    for i, e := range entries {
        chunkIDs[i] = e.sym.ChunkID
    }

    if bs, ok := store.(types.BatchMetadataStore); ok {
        hydrated, err := bs.BatchHydrateChunks(ctx, chunkIDs)
        if err == nil {
            hydratedMap := make(map[int64]types.HydratedChunk, len(hydrated))
            for _, h := range hydrated {
                hydratedMap[h.ChunkID] = h
            }
            results := make([]types.ScoredResult, 0, len(entries))
            for _, e := range entries {
                h, ok := hydratedMap[e.sym.ChunkID]
                if !ok {
                    continue
                }
                results = append(results, types.ScoredResult{
                    ChunkID:    h.ChunkID,
                    Score:      1.0,
                    Path:       h.Path,
                    SymbolName: e.sym.Name,
                    Kind:       h.Kind,
                    StartLine:  h.StartLine,
                    EndLine:    h.EndLine,
                    Content:    h.Content,
                    TokenCount: h.TokenCount,
                })
            }
            return results, nil
        }
        // fall through to per-item on batch error
    }

    // Per-item fallback
    results := make([]types.ScoredResult, 0, len(entries))
    for _, e := range entries {
        chunk, err := store.GetChunkByID(ctx, e.sym.ChunkID)
        if err != nil || chunk == nil {
            continue
        }
        results = append(results, types.ScoredResult{
            ChunkID:    chunk.ID,
            Score:      1.0,
            Path:       e.path,
            SymbolName: e.sym.Name,
            Kind:       chunk.Kind,
            StartLine:  chunk.StartLine,
            EndLine:    chunk.EndLine,
            Content:    chunk.Content,
            TokenCount: chunk.TokenCount,
        })
    }
    return results, nil
}
```

---

### 2.3 — Integrate into `searchSemantic`

**File:** `internal/core/engine.go`

In `searchSemantic`, before the keyword search call, add the symbol exact lookup block.
After `HybridRank`, merge exact matches at the front:

```go
func (e *QueryEngine) searchSemantic(ctx context.Context, input SearchInput, level FallbackLevel) ([]types.ScoredResult, error) {
    filter := TestFilter{ExcludeTests: input.ExcludeTests, TestOnly: input.TestOnly}

    // Symbol exact lookup for identifier-style queries.
    var exactMatches []types.ScoredResult
    if isIdentifierQuery(input.Query) {
        var err error
        exactMatches, err = SymbolExactSearch(ctx, e.store, input.Query, filter)
        if err != nil {
            e.logger.Warn("symbol exact search failed, continuing", "err", err)
        }
    }

    // ... existing kwResults, queryVec, semResults, mergeResults, HybridRank unchanged ...

    // Prepend exact matches; append hybrid results deduplicating by chunk ID.
    if len(exactMatches) > 0 {
        seen := make(map[int64]bool, len(exactMatches))
        for _, r := range exactMatches {
            seen[r.ChunkID] = true
        }
        for _, r := range ranked {
            if !seen[r.ChunkID] {
                exactMatches = append(exactMatches, r)
            }
        }
        ranked = exactMatches
    }

    if len(ranked) > input.MaxResults {
        ranked = ranked[:input.MaxResults]
    }
    if input.MinScore > 0 {
        ranked = filterByScore(ranked, input.MinScore)
    }
    e.recordSession(ranked)
    return ranked, nil
}
```

---

### 2.4 — Integrate into `searchKeyword`

**File:** `internal/core/engine.go`

Same pattern in `searchKeyword`:

```go
func (e *QueryEngine) searchKeyword(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
    filter := TestFilter{ExcludeTests: input.ExcludeTests, TestOnly: input.TestOnly}

    var exactMatches []types.ScoredResult
    if isIdentifierQuery(input.Query) {
        var err error
        exactMatches, err = SymbolExactSearch(ctx, e.store, input.Query, filter)
        if err != nil {
            e.logger.Warn("symbol exact search failed, continuing", "err", err)
        }
    }

    results, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults, filter)
    if err != nil {
        return nil, fmt.Errorf("keyword search: %w", err)
    }

    if len(results) == 0 && len(exactMatches) == 0 {
        pkg, err := FilesystemFallback(ctx, e.projectRoot, input.Query, 4096)
        if err != nil {
            return nil, fmt.Errorf("filesystem fallback: %w", err)
        }
        return pkg.Chunks, nil
    }

    results = HybridRank(ctx, HybridRankInput{
        Candidates:    results,
        Store:         e.store,
        Weights:       DefaultRankWeights(),
        SemanticReady: false,
        SessionScorer: e.sessionScorer(),
    })

    // Prepend exact matches
    if len(exactMatches) > 0 {
        seen := make(map[int64]bool, len(exactMatches))
        for _, r := range exactMatches {
            seen[r.ChunkID] = true
        }
        for _, r := range results {
            if !seen[r.ChunkID] {
                exactMatches = append(exactMatches, r)
            }
        }
        results = exactMatches
    }

    if input.MinScore > 0 {
        results = filterByScore(results, input.MinScore)
    }
    e.recordSession(results)
    return results, nil
}
```

---

## Fix 3: FTS5 Prefix Search Support (optional, minor)

**File:** `internal/storage/sqlite/fts.go`

In `sanitizeFTSQuery`, allow trailing `*` to enable FTS5 prefix search so partial identifier
searches like `"buildBuyer*"` find `buildBuyerOptions`:

```go
// Current: terms = append(terms, `"`+clean+`"`)
// Replace with:
if strings.HasSuffix(clean, "*") {
    base := strings.TrimRight(clean, "*")
    if base != "" {
        terms = append(terms, `"`+base+`"*`)
    }
} else {
    terms = append(terms, `"`+clean+`"`)
}
```

---

## Execution Order

| Step | File(s) | Depends on |
|------|---------|------------|
| 1. Config prefix fields | `internal/types/config.go` | — |
| 2. Document prefix in EmbedWorker | `internal/vector/embed_worker.go` | Step 1 |
| 3. Query prefix in QueryEngine | `internal/core/engine.go` | Step 1 |
| 4. Wire config in daemon | `internal/daemon/daemon.go` | Steps 1–3 |
| 5. `re-embed` CLI command | `cmd/shaktiman/main.go` | Step 1 |
| 6. Run `re-embed` + `index --embed` | CLI | Steps 1–5 |
| 7. `GetSymbolByNameCI` on store | `internal/storage/sqlite/metadata.go`, `internal/types/interfaces.go` | — |
| 8. `lookup.go` — `isIdentifierQuery` + `SymbolExactSearch` | `internal/core/lookup.go` | Step 7 |
| 9. Integrate into `searchSemantic` + `searchKeyword` | `internal/core/engine.go` | Step 8 |
| 10. FTS prefix search (optional) | `internal/storage/sqlite/fts.go` | — |

Steps 7–9 are independent of steps 1–6 and can be implemented and verified before re-embedding.

---

## Verification

Fix 2 (symbol lookup) can be verified immediately without re-embedding:

```bash
cd /Users/bigbinary/BB/agentinbox
shaktiman search "buildBuyerOptions" --format json | jq '.[0:3] | .[].symbol_name'
# Expected: "buildBuyerOptions", "buildBuyerOptions", "buildBuyerOptions"
```

Fix 1 requires re-embedding first, then:

```bash
shaktiman search "buildBuyerOptions" --format json | jq '.[0:3] | .[] | {score, symbol_name, path}'
# Expected: score ≥ 0.85, symbol_name = "buildBuyerOptions" for top 3 results
```

Regression check — natural language queries should still work:

```bash
shaktiman search "how does routing work"
shaktiman search "authentication flow"
# Expected: semantically relevant results, not just symbol-name matches
```
