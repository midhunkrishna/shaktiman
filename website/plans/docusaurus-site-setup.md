# Docusaurus Documentation Site for Shaktiman

## Context

Shaktiman has ~30 markdown files under `docs/` — mostly internal architecture notes, ADRs, and
planning artifacts. The `README.md` is the only user-facing surface today. As Shaktiman grows
a user base (MCP clients, CLI users, backend operators), we need a proper documentation site
that's:

- **Discoverable**: searchable, linkable, indexable by search engines
- **Versioned & navigable**: sidebars, breadcrumbs, cross-links
- **Cheap to host**: static site, no servers to operate

**Outcome**: a Docusaurus v3 site under `website/` that covers getting-started, CLI reference,
MCP tool reference, configuration, and backend setup — plus a curated "Design & ADRs" section
that surfaces the architectural decision record for contributors. Deployed to Cloudflare Pages
via Git integration (push-to-deploy, free PR previews).

---

## Approach

### 1. Audit architecture doc & existing docs for drift (do this FIRST)

Before any Docusaurus work, make sure the source material we're about to lift from is
actually accurate. Docs are seed material, not the source of truth — the codebase is.

**1a. Architecture audit.** Read the latest architecture doc
(`docs/architecture/03-architecture-v3.md` + `03-architecture-v3-addendum.md`) and compare
against current code:

| Claim in the architecture doc | Verify against |
|---|---|
| Leader/proxy multi-instance model | `internal/daemon/` (flock, socket, promotion paths) |
| MCP tool set (names, contracts) | `internal/mcp/tools.go` |
| Storage backends & registration | `internal/storage/` factories, build tags |
| Vector backends | `internal/vector/` factories, build tags |
| Chunking strategy (AST-driven, recursive) | `internal/chunk/` (or equivalent) |
| Watcher / enrichment pipeline | `internal/watcher/`, `internal/enrichment/` |
| Language coverage | wired tree-sitter grammars in code, not README list |
| Config surface | `internal/types/config.go` |

Produce a short drift log (worktree-local scratch file, not committed) listing every
discrepancy. For each drift, decide: *update the doc* (code is canonical and intended) or
*file a follow-up* (doc describes intent, code hasn't caught up — do not silently rewrite
the doc).

**1b. Update `docs/architecture/03-architecture-v3.md`** (and/or addendum) to match the
code where the code is canonical. Keep diffs minimal and surgical — this is not a rewrite.

**1c. Apply the same drift check** to each ADR we plan to import
(`adr-001-code-review-capabilities.md`, `adr-002-multi-instance-concurrency.md`,
`adr-003-pluggable-storage-backends.md`, `adr-004-recursive-ast-driven-chunking.md`). For
an ADR, "drift" is handled by appending a short *Status / Today* note at the top if the
implementation has diverged — ADRs are historical records; don't rewrite their bodies.

**1d. README drift check.** Cross-reference `README.md` against code for: install commands,
build tags, supported languages, CLI command list, MCP tool list, config example. Patch
the README as part of this step so the final site and the repo's front door agree.

**Deliverable of step 1:** architecture doc and README are in sync with code; ADRs carry
status notes where relevant; we have a clean foundation to author the site from.

### 2. Scaffold Docusaurus v3 in `website/`

```bash
cd /Users/minimac/p/shaktiman
npx create-docusaurus@latest website classic --typescript
```

This produces `website/` with `docusaurus.config.ts`, `sidebars.ts`, `docs/`, `src/`, `static/`,
`package.json`, and `tsconfig.json`.

Delete the scaffolded placeholder content (`website/blog/`, `website/docs/intro.md`,
`website/src/pages/*`) — we'll replace it.

### 2b. Preserve this plan in-tree

Create `website/plans/` and copy this plan into it so the rollout plan lives alongside the
site it describes (and future site plans have a natural home).

- `mkdir -p website/plans`
- Copy `/Users/minimac/.claude/plans/resilient-sprouting-tiger.md` →
  `website/plans/docusaurus-site-setup.md`
- Commit `website/plans/docusaurus-site-setup.md` along with the scaffolded site files.
- `website/plans/` is **not** surfaced in the Docusaurus site (no sidebar entry, no docs
  route). It's intentionally in-tree-only — a record for contributors, not a user-facing
  page.

### 3. Configure `website/docusaurus.config.ts`

Key settings:
- `title: 'Shaktiman'`
- `tagline: 'Local-first code context engine for coding agents'`
- `url: 'https://shaktiman.dev'` (placeholder — user can swap for actual domain later)
- `baseUrl: '/'`
- `organizationName: 'shaktimanai'`, `projectName: 'shaktiman'`
- `onBrokenLinks: 'throw'`, `onBrokenMarkdownLinks: 'warn'`
- `presets.classic`:
  - `docs.sidebarPath: './sidebars.ts'`
  - `docs.routeBasePath: '/'` (docs-only site, no separate landing page needed initially)
  - `docs.editUrl: 'https://github.com/shaktimanai/shaktiman/tree/master/website/'`
  - `blog: false`
  - `theme.customCss: './src/css/custom.css'`
- `themeConfig`:
  - `navbar`: logo, title, links to GitHub, sections (Docs, Reference, Design)
  - `footer`: minimal, MIT license link
  - `prism`: add `bash`, `go`, `toml`, `yaml`, `json` languages
  - `colorMode`: respect user preference, default light
- No `algolia` block initially — add once the site is live and DocSearch is applied for.

### 4. Information Architecture

```
website/docs/
├── intro.md                            # what Shaktiman is, when to use it, what it isn't
├── getting-started/
│   ├── installation.md                 # build tags, prerequisites, binary/source
│   ├── quickstart.md                   # init → index → first query
│   └── claude-code-setup.md            # MCP client config (.mcp.json)
├── guides/
│   ├── indexing.md                     # how indexing works, watcher behavior
│   ├── searching.md                    # search vs context vs symbols vs deps — when to use each
│   ├── multi-instance.md               # leader/proxy pattern (from ADR-002 exec summary)
│   └── reindexing.md                   # reindex command, when to use
├── examples/                           # real-world implementation examples
│   ├── overview.md                     # when to read which example
│   ├── refactor-impact-analysis.md     # using `dependencies` + `search` to scope a refactor
│   ├── onboarding-to-a-new-repo.md     # summary → search → context workflow
│   ├── bug-triage-with-diff.md         # using `diff` to understand recent regressions
│   ├── cross-file-feature-tracing.md   # using `context` with a budget
│   └── api-change-blast-radius.md      # using `dependencies direction:"callers"` for impact
├── integrations/                       # integration patterns with popular tools
│   ├── claude-code.md                  # primary path: MCP via .mcp.json
│   ├── cursor.md                       # MCP config for Cursor
│   ├── zed.md                          # MCP config for Zed
│   ├── generic-mcp-client.md           # any stdio MCP client
│   ├── ci-pipelines.md                 # using shaktiman in CI for PR analysis / doc generation
│   └── custom-agents.md                # Agent SDK / custom scripts hitting the daemon socket
├── configuration/
│   ├── config-file.md                  # .shaktiman/config.toml full reference
│   ├── backends.md                     # sqlite vs postgres, build tags matrix
│   ├── vector-stores.md                # bruteforce, hnsw, pgvector, qdrant tradeoffs
│   ├── embeddings.md                   # ollama setup, model selection, prefixes
│   └── performance-tuning.md           # tuning knobs + their implications (see §3b)
├── performance/                        # performance implications & trade-offs (see §3b)
│   ├── overview.md                     # throughput/latency/memory axes, how to measure
│   ├── backend-selection.md            # sqlite vs postgres; bruteforce vs hnsw vs pgvector vs qdrant
│   ├── indexing-performance.md         # watcher debounce, enrichment workers, batch size
│   ├── query-performance.md            # search modes, min_score, token budget effects
│   └── scaling.md                      # when to move from single-host to postgres+qdrant
├── migrating/                          # migration guides from competing tools (see §3c)
│   ├── overview.md                     # when Shaktiman fits vs. when it doesn't
│   ├── from-grep-ripgrep.md            # mental-model shift: ranked vs literal
│   ├── from-ctags-lsp.md               # definition lookup parity + gaps
│   ├── from-sourcegraph-cody.md        # cloud → local-first, feature parity table
│   └── from-claude-default-tools.md    # replacing Grep/Glob/Read loops with shaktiman MCP
├── reference/
│   ├── cli.md                          # all `shaktiman` / `shaktimand` commands
│   ├── mcp-tools/
│   │   ├── overview.md
│   │   ├── summary.md
│   │   ├── search.md
│   │   ├── context.md
│   │   ├── symbols.md
│   │   ├── dependencies.md
│   │   ├── diff.md
│   │   └── enrichment-status.md
│   ├── supported-languages.md          # tree-sitter grammars supported
│   └── limitations.md                  # known limitations & workarounds (see §3d)
├── troubleshooting/                    # structured troubleshooting flows (see §3e)
│   ├── overview.md                     # decision tree entry point
│   ├── daemon-and-leader.md            # socket errors, leader election, flock failures
│   ├── indexing-stuck.md               # watcher not picking up changes; enrichment stalled
│   ├── empty-or-bad-results.md         # no search results, low scores, missing symbols
│   ├── embedding-failures.md           # ollama connection, dimension mismatch, timeouts
│   ├── backend-errors.md               # pg connection, qdrant collection, hnsw file corruption
│   └── performance-problems.md         # slow queries, high memory, disk usage
├── design/                             # curated from repo docs/
│   ├── overview.md                     # short orientation for contributors
│   ├── architecture.md                 # copy of docs/architecture/03-architecture-v3.md
│   ├── adr-001-code-review.md
│   ├── adr-002-multi-instance.md
│   ├── adr-003-pluggable-backends.md
│   └── adr-004-recursive-chunking.md
├── contributing.md                     # from docs/reference/contributing_guide.md
└── changelog.md                        # mirror of CHANGELOG.md
```

Sidebar defined in `website/sidebars.ts` as auto-generated from the directory structure with
manual ordering: Getting Started → Guides → Examples → Integrations → Configuration →
Performance → Migrating → Reference → Troubleshooting → Design → Contributing → Changelog.

### 4b. Performance implications & trade-offs — content approach

Each page under `performance/` is structured consistently:

- **What the knob does** (1–2 sentences linked to the config field)
- **Axes it affects**: latency (p50/p95), throughput, memory footprint, disk footprint, index-build time
- **Measurement recipe**: how to observe the effect (`shaktiman enrichment-status`, logs, timing a
  representative query)
- **Trade-off table**: setting → pros → cons → recommended range
- **Worked example** with numbers from a small/medium/large repo (sourced from dev testing; if
  no data is available, the page is seeded with *representative* ranges and clearly labeled as
  such, with a link to open an issue to contribute measurements)

Concrete trade-offs to cover (derived from `internal/types/config.go` and ADR-003):

| Choice | Primary trade-off |
|---|---|
| `bruteforce` vs `hnsw` | exact recall vs. sub-linear query time; HNSW uses more disk |
| `sqlite` vs `postgres` metadata | zero-setup vs. multi-host / multi-project isolation |
| `pgvector` vs `qdrant` | single-system simplicity vs. purpose-built vector performance |
| `EnrichmentWorkers` | CPU/ollama saturation vs. tail latency on foreground queries |
| `ContextBudgetTokens` | result completeness vs. LLM prompt size/cost |
| `SearchMinScore` | recall vs. precision; noise reduction |
| `WatcherDebounceMs` | freshness vs. churn on rapid edits |
| `EmbedBatchSize` | throughput vs. ollama memory; failure blast radius on batch errors |

### 4c. Migration guides — content approach

Each migration page follows a consistent template:

1. **Mental model shift** — one paragraph on the fundamental difference
2. **Feature parity table** — columns: capability / other tool / shaktiman equivalent / notes
3. **Side-by-side command/workflow comparison** — concrete before/after
4. **Gaps** — what the other tool does that shaktiman *doesn't* (honest: ctags finds more tag
   kinds than our symbol extractor for some languages; Sourcegraph has cross-repo; etc.)
5. **When to keep the old tool** — shaktiman complements, not replaces, exact-string search

### 4d. Known limitations & workarounds

`reference/limitations.md` is a single curated page (not hidden in release notes). Content
seeded from:

- `README.md` caveats
- `docs/review-findings/parser-bugs-from-recursive-chunking.md`
- `docs/planning/09-symbol-collision.md` and `10-parser-bug-fixes-plan.md`
- Current constraints: only Ollama for embeddings; postgres backend requires pgvector/qdrant
  (from CLAUDE.md A12); tree-sitter grammar coverage gaps

Each entry is structured:
- **Limitation** (what it is, what you observe)
- **Why** (root cause or design constraint, linked to ADR if relevant)
- **Workaround** (concrete steps, or "accept and file an issue if it blocks you")
- **Status** (tracked? planned? wontfix?)

### 4e. Troubleshooting flows

`troubleshooting/overview.md` is the entry point — a decision tree that routes the reader to
the right sub-page. Example flow:

```
Symptom                          → Page
─────────────────────────────────────────────────────────
"shaktimand won't start"         → daemon-and-leader.md
"search returns nothing"         → empty-or-bad-results.md
"results are stale"              → indexing-stuck.md
"embedding never finishes"       → embedding-failures.md
"queries are slow"               → performance-problems.md
"postgres/qdrant connect error"  → backend-errors.md
```

Each sub-page uses the same structure: **Symptom → Likely causes (ranked) → Diagnostic commands
→ Fix** — and cross-links to the relevant config or design page for the "why".

### 5. Author user-facing content — code-first, docs as seed

**Sourcing rule:** for every page, code is authoritative. README and `docs/` are seed
material used to kickstart drafting; every claim is verified against the current code
before the page is considered done. If code and seed disagree, the code wins and we fix
the seed (see step 1).

| Target page(s) | Primary source (code, authoritative) | Seed / secondary |
|---|---|---|
| `reference/mcp-tools/*` parameter schemas | `internal/mcp/tools.go` | — |
| `reference/mcp-tools/overview.md` | `internal/mcp/tools.go` (tool registrations) | `CLAUDE.md` MCP Tools table |
| `reference/cli.md` | `cmd/shaktiman/*.go`, `cmd/shaktimand/*.go` (cobra commands, flags) | README CLI section |
| `reference/supported-languages.md` | wired tree-sitter grammars in code (e.g. `internal/parser/`) | README language list |
| `reference/limitations.md` | current code constraints (e.g. `internal/vector/ollama.go` is the only embedding client; postgres+pgvector/qdrant constraint from A12) | `docs/review-findings/parser-bugs-from-recursive-chunking.md`, `docs/planning/09-symbol-collision.md`, `docs/planning/10-parser-bug-fixes-plan.md` |
| `configuration/config-file.md` | `internal/types/config.go` (every field, tag, default, validation) | — |
| `configuration/backends.md`, `performance/backend-selection.md` | backend factories in `internal/storage/`, `internal/vector/`; build tags in source files | `CLAUDE.md` build-tags section, `docs/design/adr-003-pluggable-storage-backends.md` |
| `configuration/embeddings.md` | `internal/vector/ollama.go` (HTTP contract, batch size, prefix handling) | README embedding section |
| `configuration/performance-tuning.md` | config field ranges + where each is read in code | — |
| `guides/indexing.md`, `guides/reindexing.md` | `internal/indexer/`, `cmd/shaktiman/index.go`, reindex command | README |
| `guides/searching.md` | `internal/core/engine.go`, `internal/core/lookup.go` | CLAUDE.md tool table |
| `guides/multi-instance.md` | `internal/daemon/` (flock, socket, promotion) | `docs/design/executive-summary-002-multi-instance-concurrency.md` |
| `performance/scaling.md` | actual scaling knobs in config + code | `docs/planning/08-scalability-enhancement*.md` |
| `intro.md` | code capabilities (what shaktiman can actually do today) | README intro |
| `getting-started/*` | verified commands (see step 6) | README quickstart |
| `integrations/claude-code.md` | actual `.mcp.json` shape accepted by Claude Code + shaktimand's stdio MCP surface | README Claude Code section |
| `integrations/{cursor,zed,generic-mcp-client}.md` | shaktimand's MCP stdio contract; each client's own docs for config format | — |
| `integrations/ci-pipelines.md` | what the CLI commands emit today (exit codes, output formats) | — |
| `examples/*` | runnable against current code — each example tested end-to-end before publishing | — |
| `migrating/*` | authored new; parity tables fact-checked against actual capabilities in code | — |
| `troubleshooting/*` | error paths in `internal/daemon/`, `internal/mcp/`, `internal/vector/`, `internal/storage/` (grep for error returns and wrap points) | CLAUDE.md caveats |
| `contributing.md` | current `Makefile` / build commands / test invocations | `docs/reference/contributing_guide.md` |
| `changelog.md` | `CHANGELOG.md` | — |

**Verification during authoring:** for each reference/configuration/integration page, the
author (Claude in implementation phase) loads the code file named above and confirms the
page matches. No "copy from README and hope" — the README is checked too.

### 6. Getting-started must be a crisp linear recipe

One happy path. No branching mid-flow. Alternatives (backend swaps, build-tag combos,
binary vs source, non-Claude-Code clients) each live on a *linked* page, not inside the
recipe.

**The happy path** (to be verified end-to-end against current code before publishing):

`getting-started/installation.md` — binary install only (fastest path). Single section:
what to download/build, one command to produce the binary, one command to verify
(`shaktiman --version`). Source build and postgres-only builds link out to
`configuration/backends.md`.

`getting-started/quickstart.md` — ordered, copy-pasteable steps:

1. `cd` into your project
2. `shaktiman init` — confirm `.shaktiman/` is created
3. `shaktimand &` (or equivalent) — confirm the daemon is running
4. Wait for initial index — `shaktiman enrichment-status` until done
5. Run a sanity query — `shaktiman search "<something in your repo>"`
6. Expected output shape shown inline

`getting-started/claude-code-setup.md` — ordered steps:

1. Exact `.mcp.json` snippet for Claude Code (path to `shaktimand`, args, env)
2. Restart Claude Code
3. Verify the `mcp__shaktiman__summary` tool is listed
4. Run `summary` — expected output shape

If the user follows these three pages top-to-bottom, they end with a working
shaktiman-via-MCP-in-Claude-Code setup. Every command is verified against the current code
(actual flag names, actual init behavior, actual enrichment-status output, actual tool
names) before the page ships.

Everything configurable — backend choice, embedding model, vector store, performance
tuning — is out-of-flow and linked from a single "Next steps" block at the bottom of
`quickstart.md`.

### 7. Import curated design docs

Copy (don't symlink — MDX processing needs local files):
- `docs/architecture/03-architecture-v3.md` → `website/docs/design/architecture.md`
- `docs/design/adr-001-code-review-capabilities.md` → `website/docs/design/adr-001-code-review.md`
- `docs/design/adr-002-multi-instance-concurrency.md` → `website/docs/design/adr-002-multi-instance.md`
- `docs/design/adr-003-pluggable-storage-backends.md` → `website/docs/design/adr-003-pluggable-backends.md`
- `docs/design/adr-004-recursive-ast-driven-chunking.md` → `website/docs/design/adr-004-recursive-chunking.md`

Each imported file gets a front-matter block (`title`, `sidebar_position`) and a note at the
top: *"This is an architectural design record. See [Getting Started](/getting-started/installation) for installation."*

Original files in `docs/` stay where they are — they're still useful for in-repo review.

### 8. Build & dev ergonomics

- Add to root `.gitignore`:
  ```
  website/node_modules/
  website/build/
  website/.docusaurus/
  ```
- Add `website/README.md` with three commands: `npm install`, `npm run start` (local dev on
  `:3000`), `npm run build` (produces `website/build/`).
- Pin Node version: `website/.nvmrc` with `20`.

### 9. Cloudflare Pages deployment (Git integration)

No code/CI changes. Done entirely via the Cloudflare dashboard:

1. **Cloudflare dashboard → Workers & Pages → Create → Pages → Connect to Git**
2. Select the shaktiman GitHub repo, authorize Cloudflare Pages GitHub app.
3. Framework preset: **Docusaurus**.
4. Build settings:
   - Build command: `npm run build`
   - Build output directory: `website/build`
   - Root directory (advanced): `website`
5. Environment variables:
   - `NODE_VERSION=20`
6. Production branch: `master`. PRs automatically get preview deployments at
   `<pr-number>.<project>.pages.dev`.
7. (Optional, after first successful deploy) Add custom domain via **Custom domains** tab.

**Gotchas to honor** (from research):
- Docusaurus emits `build/404.html`; CF Pages uses this natively. **Do not** add a
  `/* /index.html 200` rule in `_redirects` — CF treats it as an infinite loop.
- Keep `baseUrl: '/'` unless a custom path is decided later.
- No `wrangler.toml` needed — Git integration handles everything.

---

## Files to be created

| Path | Purpose |
|---|---|
| `website/package.json` | scaffolded by `create-docusaurus` |
| `website/docusaurus.config.ts` | site config (section 2) |
| `website/sidebars.ts` | sidebar structure (section 3) |
| `website/tsconfig.json` | scaffolded |
| `website/.nvmrc` | Node 20 pin |
| `website/README.md` | local dev instructions |
| `website/src/css/custom.css` | scaffolded, minimal edits |
| `website/docs/**/*.md(x)` | content per IA in section 3 |
| `website/static/img/logo.svg` | placeholder logo (reuse existing or generate) |
| `website/plans/docusaurus-site-setup.md` | copy of this rollout plan, in-tree record (not surfaced on the site) |
| `.gitignore` | add `website/node_modules`, `website/build`, `website/.docusaurus` |

## Files to read

**Code (authoritative — fact-check all docs against these):**
- `internal/mcp/tools.go` — MCP tool schemas, parameters, handler signatures
- `internal/mcp/` (rest) — daemon stdio surface, error wrapping
- `cmd/shaktiman/*.go`, `cmd/shaktimand/*.go` — cobra commands, flags, entry points
- `internal/types/config.go` — every config field, tag, default, validation
- `internal/daemon/` — leader/proxy, flock, socket, promotion
- `internal/storage/` — backend factories + registered build tags
- `internal/vector/` — vector-store backends, `ollama.go` for embedding client
- `internal/parser/` (or equivalent) — wired tree-sitter grammars (true language support)
- `internal/indexer/`, `internal/watcher/`, `internal/enrichment/` — pipeline behavior

**Existing docs (seed material, verified against code):**
- `README.md` — quickstart seed; patched in step 1d if drifted
- `CHANGELOG.md` — changelog page source
- `docs/architecture/03-architecture-v3.md` + addendum — design overview; audited and
  updated in step 1b
- `docs/design/adr-00{1,2,3,4}-*.md` — ADRs to import; annotated per step 1c
- `docs/design/executive-summary-002-multi-instance-concurrency.md` — multi-instance guide seed
- `docs/planning/08-scalability-enhancement*.md`, `09-symbol-collision.md`,
  `10-parser-bug-fixes-plan.md` — limitations page seeds
- `docs/review-findings/parser-bugs-from-recursive-chunking.md` — limitations page seed
- `docs/reference/contributing_guide.md` — contributing page seed
- `CLAUDE.md` — MCP tool table, build-tag cheatsheet, constraints (A12, etc.)

## Verification

1. **Architecture drift closed (step 1)** — re-read the updated
   `docs/architecture/03-architecture-v3.md` and README against the code audit checklist
   in step 1a. Zero open drift items, or every open item is documented as a known-gap in
   `reference/limitations.md`.

2. **Getting-started recipe works end-to-end** — on a clean checkout of a sample repo,
   follow `installation.md` → `quickstart.md` → `claude-code-setup.md` literally,
   copy-paste only. Expected outcome: shaktiman indexes the sample repo and an
   `mcp__shaktiman__summary` call from Claude Code returns data. Any deviation = fix the
   page, don't fix the steps.

3. **Content accuracy spot-check** — cross-reference against authoritative code sources:
   - 3 MCP tool pages vs. `internal/mcp/tools.go` (parameter names, types, defaults)
   - 3 CLI command pages vs. `cmd/shaktiman/*.go` (flags, arguments)
   - `configuration/config-file.md` vs. `internal/types/config.go` (every field present,
     defaults correct)

4. **Local build**
   ```bash
   cd website && npm install && npm run build
   ```
   Expect: `website/build/index.html`, `website/build/404.html`, all doc pages rendered,
   zero broken-link errors (`onBrokenLinks: 'throw'` is enforced).

5. **Local dev server**
   ```bash
   cd website && npm run start
   ```
   Visit `http://localhost:3000/`, click through every sidebar entry, confirm no 404s and
   all code blocks render with syntax highlighting.

6. **Cloudflare preview deploy**
   - Push a branch, open a PR.
   - Confirm CF Pages comments on the PR with a preview URL within ~2 minutes.
   - Load the preview, verify 404 page works (visit `/does-not-exist`), verify sidebar nav.

7. **Production deploy**
   - Merge PR → master.
   - Confirm `*.pages.dev` production URL serves the site.
   - (If custom domain configured) confirm DNS/HTTPS green on the Cloudflare side.

## Out of scope (explicitly deferred)

- Algolia DocSearch integration — apply after site has a stable public URL.
- Versioned docs (Docusaurus supports `docs/versioned_docs/`) — add once v1.0 releases.
- i18n/translations.
- Auto-generating CLI/MCP reference from code (e.g. via a Go program emitting MDX) — nice to
  have, but hand-authored reference is sufficient for v1 and easier to review.
- Logo/brand asset design — use a placeholder.
- Custom domain & DNS setup — handled once the target domain is chosen.
- **Benchmarked performance numbers in `performance/*` pages** — pages ship with
  *representative* ranges and qualitative trade-offs; real measurements from a reproducible
  harness (small/medium/large repo) are a separate follow-up once a benchmark corpus is
  chosen. Each perf page carries a banner noting this so readers don't mistake ranges for
  measured results.
