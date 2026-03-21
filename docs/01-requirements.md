# Shaktiman: Requirements Document

> Local-first, high-performance code context system for coding agents.

---

## 1. Functional Requirements

### 1.1 Codebase Indexing
- **FR-1**: Index source files across a project — symbols (functions, classes, methods, types, variables), file metadata, and structure.
- **FR-2**: Build a dependency/import graph across files and modules.
- **FR-3**: Support incremental indexing — only re-index files that changed since last index (via mtime or content hash).
- **FR-4**: Support multi-language indexing (TypeScript, Python, Go, Rust at minimum; extensible to others via tree-sitter grammars).

### 1.2 Semantic Retrieval
- **FR-5**: Given a natural language query or code snippet, retrieve the most relevant code chunks ranked by semantic similarity.
- **FR-6**: Support hybrid retrieval: combine embedding similarity with structural signals (call graph proximity, import distance, recency of edit).
- **FR-7**: Return results at the right granularity — function/class/block level, not whole files.

### 1.3 Context Assembly
- **FR-8**: Given a task description + active file(s), assemble a context package: ranked list of relevant code chunks that fits within a configurable token budget.
- **FR-9**: Include structural context automatically — e.g., if a function is selected, include its type signatures, callers, and callees within budget.
- **FR-10**: Deduplicate and merge overlapping chunks to avoid redundancy.
- **FR-11**: Attach lightweight metadata to each chunk (file path, symbol name, last modified, relevance score) so the agent can navigate efficiently.

### 1.4 Agent Integration
- **FR-12**: Expose capabilities via MCP (Model Context Protocol) server so Claude Code and other MCP-compatible agents can consume context natively.
- **FR-13**: Provide a CLI interface for manual queries, index management, and debugging.
- **FR-14**: Support a "push" mode — proactively supply context when a file is opened or a task begins, without the agent needing to search.

### 1.5 Session & History
- **FR-15**: Persist index and embeddings to disk; survive process restarts without full re-index.
- **FR-16**: Track which files/symbols the agent accessed in the current session to bias future retrievals toward the working set.
- **FR-17**: Maintain a lightweight edit history — recently modified files/symbols get retrieval priority.

### 1.6 Configuration
- **FR-18**: Respect `.gitignore` and a `.shaktimanignore` file for exclusion patterns.
- **FR-19**: Allow per-project config: languages, token budget, embedding model, chunk size, retrieval strategy.

---

## 2. Non-Functional Requirements

### 2.1 Performance
- **NFR-1**: Cold index of a 100K-line codebase must complete in < 60 seconds.
- **NFR-2**: Incremental re-index after a single file change must complete in < 500ms.
- **NFR-3**: Semantic query → ranked results must return in < 200ms for codebases up to 500K lines.

### 2.2 Token Efficiency
- **NFR-4**: Context packages must not exceed the configured token budget (default: 8K tokens). No over-fetching.
- **NFR-5**: Metadata overhead per chunk must be < 5% of the chunk's token count.
- **NFR-6**: The system must reduce agent tool-call round-trips by at least 60% compared to unassisted exploration (grep/glob/read cycles).

### 2.3 Resource Usage
- **NFR-7**: Idle memory usage < 100MB for a 100K-line codebase index.
- **NFR-8**: Embedding storage on disk < 500MB for a 100K-line codebase.
- **NFR-9**: Background indexing must not degrade foreground developer experience (CPU-throttled, low-priority I/O).

### 2.4 Reliability
- **NFR-10**: Index corruption must be recoverable via a single `shaktiman reindex` command.
- **NFR-11**: Graceful degradation — if embeddings are unavailable, fall back to structural/keyword retrieval.

### 2.5 Maintainability
- **NFR-12**: Modular architecture — retrieval, indexing, embedding, and integration layers must be independently replaceable.
- **NFR-13**: Embedding model must be swappable without re-architecting (local models like `nomic-embed-text`, or API-based).

### 2.6 Extensibility
- **NFR-14**: Adding a new language requires only providing a tree-sitter grammar + a symbol extraction query file.
- **NFR-15**: Retrieval ranking strategy must be pluggable (pure semantic, hybrid, structural-only).

---

## 3. Pain Points This System Solves

| # | Pain Point | How Shaktiman Addresses It |
|---|---|---|
| P1 | **Too many tool calls** — agents make 5-15 grep/glob/read calls to locate relevant code, wasting time and tokens. | Pre-indexed semantic search returns relevant chunks in one call. |
| P2 | **Whole-file reads** — agents read entire files when they need one function, consuming thousands of tokens. | Chunk-level retrieval returns only the relevant symbol/block. |
| P3 | **No structural awareness** — agents don't know the call graph, dependency tree, or module boundaries. | Dependency graph + structural context automatically surfaces related code. |
| P4 | **Lost context between sessions** — every new conversation starts from zero knowledge. | Persisted index + session history preserves accumulated context. |
| P5 | **Irrelevant context** — agents stuff context with marginally related code, diluting signal. | Token-budgeted ranking ensures only high-relevance chunks are included. |
| P6 | **Slow on large codebases** — exploration is linear; agents hit timeouts or context limits. | Pre-computed index makes retrieval O(1) relative to codebase size. |
| P7 | **No recency bias** — agents treat old library code the same as freshly edited business logic. | Edit history + recency scoring biases toward the active working set. |
| P8 | **Blind navigation** — agents guess file names and grep patterns, missing unconventional naming. | Semantic search finds code by meaning, not naming convention. |

---

## 4. Design Constraints

### 4.1 Local-First
- **DC-1**: All indexing, storage, and retrieval must run locally. No required cloud services.
- **DC-2**: Embedding generation must support fully local models (e.g., via Ollama). Cloud embedding APIs are optional and opt-in.
- **DC-3**: Data never leaves the developer's machine unless explicitly configured.

### 4.2 Embedding Strategy
- **DC-4**: Embeddings are generated asynchronously in the background — never block the developer or agent on embedding computation.
- **DC-5**: The system must function (with degraded ranking quality) before embeddings are fully computed, using keyword/structural fallback.
- **DC-6**: Chunk embedding granularity: function/method/class/block level, not file level.

### 4.3 Token Budget
- **DC-7**: The system is the gatekeeper of token budgets. It must never return more tokens than the configured limit.
- **DC-8**: Budget allocation must be priority-weighted — highest relevance chunks get full representation; lower-ranked chunks may be summarized or trimmed.

### 4.4 Integration
- **DC-9**: Primary integration surface is MCP. The system runs as an MCP server that coding agents connect to.
- **DC-10**: Must work with Claude Code's existing tool-calling model — no modifications to the agent required.

### 4.5 Technology
- **DC-11**: Prefer SQLite (or similar embedded DB) for index/metadata storage. No external database dependencies.
- **DC-12**: Use tree-sitter for parsing. No language-specific parser dependencies.
- **DC-13**: Embedding vector storage must be local — embedded vector store (e.g., SQLite with vector extensions, or a purpose-built solution like hnswlib).

---

## 5. Success Criteria

### 5.1 Quantitative
| Metric | Target | How to Measure |
|---|---|---|
| **Tool call reduction** | >= 60% fewer grep/glob/read calls per task | Compare agent transcripts with/without Shaktiman on identical tasks. |
| **Token savings** | >= 40% fewer input tokens per task | Measure total input tokens in agent transcripts. |
| **Retrieval relevance** | >= 80% of returned chunks are judged relevant by developer | Manual relevance rating on sample queries. |
| **Cold index time** | < 60s for 100K lines | Benchmark on representative repos. |
| **Query latency** | < 200ms p95 | Benchmark under load. |
| **Context assembly** | Fits within budget 100% of the time | Automated token counting on output. |

### 5.2 Qualitative
- Developer reports that the agent "already knows" where relevant code is.
- Agent produces correct changes on the first attempt more often.
- Developer rarely needs to manually point the agent to files.
- System feels invisible — low friction to set up, no babysitting.

### 5.3 Anti-Goals (Explicitly Out of Scope)
- **Not an IDE** — no editor UI, syntax highlighting, or code completion.
- **Not a code review tool** — no diff analysis or PR workflow.
- **Not a documentation generator** — summaries exist only to save tokens for the agent.
- **Not a cloud service** — no hosted version, no SaaS, no telemetry.

---

## Status

**Step 1: Requirements** — Complete. Awaiting confirmation before proceeding to Step 2 (Architecture Design).
