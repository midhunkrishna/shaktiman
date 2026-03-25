# Architecture v2 — Critique Synthesis

> Merged findings from reviewer and adversarial-analyst parallel review.
> 35 total findings → deduplicated to 20 actionable items → prioritized below.

---

## Must Fix (blocks correctness or SLA)

### MF-1: Embedding Model Invalidation
**Severity: HIGH** — Every user who upgrades their embedding model gets silently corrupted ranking.

No `embedding_model_id` column exists. Old embeddings from model A are compared via cosine similarity with query embeddings from model B — mathematically meaningless. The architecture claims NFR-13 (swappable model) but has no invalidation mechanism.

**Fix:** Add `embedding_model_id` to the embeddings/config table. On model change, mark all embeddings as stale and re-queue. Reject mixed-model similarity searches.

---

### MF-2: Score Normalization Missing
**Severity: HIGH** — Hybrid ranker produces wrong rankings without it.

The 5-signal formula assumes all scores are in [0,1], but:
- Cosine similarity: [-1, 1]
- BFS distance: unbounded integer
- BM25/FTS5: unbounded positive float
- Diff magnitude: unbounded
- Session access count: unbounded

Without normalization, the weighted sum is meaningless. A high BM25 score can dominate or be negligible depending on corpus size. Example: a chunk with semantic_score=0.95 (perfect match) scores only 0.38 in the weighted sum, while a mediocre-everywhere chunk scores 0.39.

**Fix:** Define normalization per signal:
- semantic: (cosine + 1) / 2 → [0, 1]
- structural: 1 / (1 + bfs_distance) → [0, 1]
- keyword: min(bm25 / bm25_max, 1.0) or percentile normalization
- diff: recency_decay ∈ [0,1] × clamp(magnitude / threshold, 0, 1)
- session: clamp(access_count / max_access, 0, 1) with decay

---

### MF-3: SQLite Concurrency Model Unspecified
**Severity: HIGH** — Write contention during cold index breaks query SLA.

Architecture uses single SQLite DB with concurrent readers (queries) and writers (enrichment pipeline, embedding worker, session store, query-time triggers). WAL mode is never mentioned. Without WAL, readers block during writes. During cold index with "large transactions (1000 rows/txn)", the write lock is held for hundreds of milliseconds, blocking all queries.

**Fix:**
- Explicitly specify WAL mode
- Use a dedicated writer thread with a write queue (all writes go through single thread)
- Query-time synchronous writes (Triggers 2, 3) enqueue to the same writer
- Reads use separate connections (WAL allows concurrent reads during writes)

---

### MF-4: Query-Time Enrichment Cascades
**Severity: HIGH** — Can blow 200ms SLA.

Trigger 2 (unindexed file) does synchronous indexing. Dep Extractor discovers imports to other unindexed files. If those trigger further indexing, a query against a file importing 20 unindexed files → 21 sync index ops × 50ms = 1050ms.

No recursion depth limit, no total time budget, no "already enriching" guard.

**Fix:**
- Query-time enrichment is **single-file only** (no recursive follow)
- Total sync enrichment budget per query: 80ms max
- Per-file enrichment mutex (prevent concurrent enrichment of same file)
- If budget exhausted, serve what's available + queue rest as Priority 1 async

---

### MF-5: Concurrent Enrichment Race Condition
**Severity: HIGH** — Watcher + query-time trigger can both enrich the same file simultaneously.

Scenario: File changes at t=0 → watcher event queued → query arrives at t=5ms → Trigger 3 fires sync re-index → watcher event arrives at t=10ms → triggers another re-index. Two concurrent writes for the same file.

**Fix:**
- Per-file enrichment mutex (in-memory lock map)
- If file is already being enriched, query-time trigger waits on the existing enrichment (with timeout) rather than starting a duplicate

---

### MF-6: Tokenizer Unspecified
**Severity: MEDIUM** — Token budget guarantee (DC-7) is meaningless without a known tokenizer.

Pre-computed token counts use an unspecified tokenizer. Claude's tokenizer differs from GPT-4's. A chunk counted as 300 tokens by one tokenizer might be 350 by another. The "hard limit" of DC-7 can be violated.

**Fix:**
- Default tokenizer: cl100k_base (Claude-compatible estimate)
- Store tokenizer ID in config table
- Apply safety margin: use 95% of stated budget as effective limit
- Document that token counts are approximate; consumers should treat budget as soft cap

---

### MF-7: FR-14 Push Mode Undesigned
**Severity: MEDIUM** — Functional requirement with no architectural support.

FR-14 (proactive context push) maps to "Session Store → proactive pre-fetch" in traceability, but no push delivery mechanism exists. No trigger event, no delivery channel, no pre-fetch strategy.

**Fix:** Design options:
- **Option A (MCP resource):** Expose session context as an MCP resource that the agent reads at task start
- **Option B (MCP notification):** Push context via MCP sampling/notification when session state changes
- **Option C (Defer):** Move FR-14 to v2 scope, document as planned enhancement

---

### MF-8: NFR Traceability Gaps
**Severity: MEDIUM** — 8 of 15 non-functional requirements missing from traceability.

| NFR | Gap |
|---|---|
| NFR-7 (memory <100MB) | No memory budget breakdown |
| NFR-8 (disk <500MB) | No disk usage estimate |
| NFR-9 (CPU throttled) | No throttle mechanism specified |
| NFR-10 (reindex recovery) | No reindex behavior defined |
| NFR-12 (modular layers) | No interface boundaries defined |
| NFR-13 (swappable model) | Implicit only, needs MF-1 fix |
| NFR-14 (new language = grammar + queries) | Implied but not traced |
| NFR-15 (pluggable ranking) | Not addressed — ranker is hardcoded |

**Fix:** Add specifications for each. Key ones:
- Memory budget: SQLite page cache (10MB) + in-memory graph (~5MB) + session LRU (~2MB) + watcher (~1MB) + embedding worker buffer (~5MB) = ~23MB base. Well within 100MB.
- Disk: 10K chunks × 768 dims × 4 bytes = ~30MB vectors. Metadata ~20MB. Total ~50MB. Within 500MB.
- Ranking pluggability: Define a `RankingStrategy` interface with `score(chunk, context) → float`. Ship `HybridStrategy` as default.

---

## Should Fix (quality, robustness, maintainability)

### SF-1: Diff Engine Should Run After Parse, Not Parallel
The Diff Engine maps hunks to "affected symbols" using the *previous* symbol index. But symbols may have been renamed or split. Records pointing to old symbol IDs become ghost references.

**Fix:** Run Diff Engine after tree-sitter + Symbol Extractor. Map hunks to both old and new symbols. Added latency: ~5-15ms (acceptable).

---

### SF-2: Embedding Worker Needs Timeout + Circuit Breaker
If Ollama hangs (GPU OOM, model loading), the worker blocks indefinitely. Priority queue backs up. No timeout, no retry, no fallback.

**Fix:** 30s timeout per batch. 3 consecutive failures → circuit breaker opens → skip embedding for 5 min → retry. Log warning.

---

### SF-3: Tree-sitter Partial Parse Handling
Actively-edited files with syntax errors produce partial ASTs → phantom symbols, incorrect chunk boundaries, broken edges persist in index.

**Fix:** Detect error nodes in AST. Mark affected chunks as `parse_quality: partial`. Reduce their ranking weight by 50%. Re-index on next successful parse.

---

### SF-4: Context Assembler Expansion Unbounded
Highly-connected utility functions (called from 200+ places) cause the EXPAND step to pull hundreds of neighbors, dominating the budget.

**Fix:** Cap expansion: max 5 neighbors per chunk, max 30% of total budget for all expansions.

---

### SF-5: Session Store Eviction
`access_log` grows unboundedly. After weeks: millions of rows, degraded query performance.

**Fix:** 30-day TTL, max 100K rows. Prune on startup and periodically during idle.

---

### SF-6: Chunk Overlap Metric Undefined
"Overlap >50%" has no defined metric. Line-range overlap? Token overlap? Content hash?

**Fix:** Use line-range overlap (fast, O(1) comparison). Two chunks overlap if their line ranges intersect by >50% of the smaller chunk's range.

---

### SF-7: Token Efficiency Claims Optimistic
The 72% savings figure double-counts (savings 1 and 3 overlap) and assumes perfect session prediction. Realistic range: 45-65%.

**Fix:** Present ranges. Separate tool-call reduction (measurable, likely 70-90%) from token reduction (measurable, likely 40-60%).

---

### SF-8: Fallback Gap for Pure Semantic Queries Without Index
Level 3 (filesystem passthrough) requires file path hints. A query like "how does authentication work?" with no index available and no file hints → system returns nothing. Violates "never fails" claim.

**Fix:** Level 3 fallback for no-hint queries: scan project structure, return file/directory listing as orientation context. Not ideal, but never empty.

---

### SF-9: Session Score Filter Bubble
Persistent session bias deprioritizes undiscovered code. Agent gets stuck in familiar areas.

**Fix:** Add exploration decay: session_score for a chunk decays by 10% per query where the chunk was NOT in the result set. After 10 queries without appearing, session boost is near zero.

---

### SF-10: No Schema Migration Strategy
System updates that change the SQLite schema have no migration path. `shaktiman reindex` rebuilds from scratch, losing session history and diff history.

**Fix:** Add `schema_version` table. Ship migration scripts for each version bump. Only drop-and-rebuild for major version changes.

---

## Risk Summary

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Mixed-model embeddings corrupt ranking | HIGH | HIGH | MF-1 |
| SQLite write contention busts query SLA | HIGH | MEDIUM | MF-3 |
| Partial AST produces phantom index entries | HIGH | MEDIUM | SF-3 |
| Enrichment cascade breaks 200ms SLA | MEDIUM | HIGH | MF-4 |
| Concurrent enrichment of same file | MEDIUM | HIGH | MF-5 |
| Graph lost on crash, stale during reload | MEDIUM | MEDIUM | Document as known limitation |
| hnswlib sidecar loses transactional consistency | MEDIUM | MEDIUM | Prefer sqlite-vec only; drop hnswlib option |
| Embedding worker hangs indefinitely | MEDIUM | MEDIUM | SF-2 |
| Disk-full corruption with no recovery | LOW | HIGH | Add disk space check before large writes |

---

## Open Questions

1. **FR-14 push mode**: Hard requirement or aspirational? If hard, needs concrete design now.
2. **Max supported codebase size**: Is 1M+ lines in scope? If yes, the in-memory graph needs a lazy-loading or partitioned design.
3. **hnswlib sidecar**: Keep or drop? Supporting both sqlite-vec and hnswlib doubles the consistency surface area.
4. **Ranking weight tuning**: Ship defaults only, or make per-project configurable from day 1?
5. **Query-time enrichment recursion**: Single-file only (recommended) or follow imports?

---

## Status

Critique complete. Recommend incorporating MF-1 through MF-8 into the architecture before proceeding to Step 3. SF items can be tracked as design notes for implementation phase.
