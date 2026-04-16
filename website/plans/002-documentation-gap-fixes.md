# Documentation Gap & Accuracy Fixes

## Context

The Docusaurus site shipped on `chore/documentation-website` (merged as PR #58) was
authored from scratch and never had a formal correctness / effectiveness / conciseness
audit. Three parallel reviews (code-reviewer, adversarial-analyst, solution-fit-analyst)
identified a mix of:

- **Factual drift** — claims in reference pages that don't match the Go code.
- **Onboarding friction** — placeholders, missing prereqs, broken "see also" targets,
  happy-path-only flows.
- **Structural bloat** — multi-thousand-line ADRs imported wholesale, duplicated setup
  content, preamble-heavy overview pages.

This plan fixes each identified gap, grouped into three commit series. One commit per
step, per the project's convention of commit-per-plan-step on feature branches.

### Resolved open questions (from review)

1. **`design/architecture` audience → contributor/curious-mind, not end-user.** Keep it
   on the site but remove authoritative inbound links from user-facing pages; inline the
   numbers those pages need.
2. **`sqlite_fts5` build tag → required only for the sqlite backend, NOT always.**
   Verified: `go-sqlite3/sqlite3_opt_fts5.go:6` gates FTS5 on the tag, and sqlite
   migrations create an FTS5 virtual table. Postgres-only builds don't need it. The
   root `CLAUDE.md` is wrong; `website/docs/configuration/backends.md` is correct.
3. **`embedding.enabled` default → daemon: `true`, CLI: opt-in via `--embed`.** Docs
   need to state the daemon-vs-CLI distinction explicitly.

---

## Approach

Three commit series, small and surgical. Each step is one commit; each commit has a
message describing *why*, not just *what*.

- **Series A — Correctness pass.** Fix specific drifted claims, grounded in code.
- **Series B — Onboarding & integrity pass.** Fix dead ends, missing prereqs, vague
  instructions that leave the first-time user stuck.
- **Series C — Structural trim.** Reduce reader-attention waste; consolidate duplicates;
  fix cross-links that depend on the placeholder architecture page.

Series A lands first because those are the smallest, highest-confidence edits. Series B
and C can proceed in parallel if desired; B is the higher-priority of the two.

---

## Series A — Correctness pass

Each step cites a specific file + line and the code evidence that contradicts it.

### A.1 — `reference/cli.md:130` — move the 720h cap annotation
**Problem:** The `--limit` row's description reads "(capped at 720h internally)". The
720h cap belongs to `--since`, not `--limit`. `cmd/shaktiman/query.go:411` defines
`--limit` as a plain `int` default 50 with no internal cap.
**Fix:** Delete the parenthetical on the `--limit` row; ensure the `--since` row carries
it.

### A.2 — `reference/supported-languages.md` — fix broken file refs + misleading column
**Problems:**
- Lines 53 and 68 reference `internal/daemon/scan.go`; the actual file is
  `internal/daemon/scanner.go`.
- Line 17 column header "Import node types" conflates three distinct fields from
  `internal/parser/languages.go`: `ImportTypes`, `ExportType`, `AmbientType`.
**Fix:**
- Rename `scan.go` → `scanner.go` (two spots).
- Rename the column "Import / export / ambient wrappers" (or split into three columns if
  it won't blow up table width).

### A.3 — `guides/indexing.md:58` — drop the phantom TOML key
**Problem:** Says "debounces events by `watcher.debounce_ms` (default 200 ms)". There is
no `[watcher]` TOML section; the field `WatcherDebounceMs` in `internal/types/config.go`
has no `toml` tag and is not reachable from the config file.
**Fix:** Remove the `watcher.debounce_ms` reference. State the 200ms default and note
it is currently a Go-default only (not TOML-tunable).

### A.4 — `guides/multi-instance.md:43-53` — platform-correct the socket path
**Problem:** Docs say the socket lives at `/tmp/shaktiman-<hash>.sock`. Actual code
(`internal/lockfile/lockfile.go:101-103`) uses `os.TempDir()`, which on macOS is
`/var/folders/...` by default. The suggested `ls /tmp/shaktiman-*.sock` returns nothing
on macOS, masking real diagnostics.
**Fix:** Replace `/tmp` with `$TMPDIR` in prose and the `ls` example. Add a one-line
note: "on Linux this is usually `/tmp`; on macOS it's typically under `/var/folders/...`".

### A.5 — `reference/mcp-tools/enrichment-status.md:24-31` — add `n/a` circuit state
**Problem:** Lists 4 circuit states (`closed`/`open`/`half_open`/`disabled`). When
invoked via CLI (`cmd/shaktiman/query.go` → `EnrichmentStatusInput` with nil
`CircuitFn`), `core.GetEnrichmentStatus` (`internal/core/lookup.go:277`) returns
`"n/a"`. Five states, not four.
**Fix:** Add `n/a` to the list and note it is returned only on the CLI path when no
embed worker is attached.

### A.6 — Fix root `CLAUDE.md` `sqlite_fts5` claim
**Problem:** Root `/Users/minimac/p/shaktiman/CLAUDE.md` says "The `sqlite_fts5` build
tag is always required." Verified false against code: `cmd/shaktimand/imports_sqlite.go`
is gated on `sqlite` (not `sqlite_fts5`); the `sqlite_fts5` tag is only needed for the
`go-sqlite3` FTS5 extension used by the sqlite backend at runtime. Postgres-only builds
(`-tags "postgres pgvector"`) do not need `sqlite_fts5`.
**Fix:** Rewrite the sentence to "The `sqlite_fts5` build tag is required when the
`sqlite` backend is compiled in (it enables FTS5 support in the SQLite driver).
Postgres-only builds don't need it." The `website/docs/configuration/backends.md` build
matrix already reflects this — leave it alone.

---

## Series B — Onboarding & integrity pass

Fixes for first-time user friction and broken user journeys.

### B.1 — `getting-started/installation.md` — complete the prereqs
**Problem:** A fresh macOS / Linux / Windows user can't tell if Shaktiman will work.
Missing:
- Xcode CLT on macOS (`xcode-select --install`) for cgo — without it, `go build` fails
  at cgo with no hint.
- Ollama install pointer (docs reference `ollama pull` but never say to install
  `ollama` itself).
- `jq` (referenced by troubleshooting pages).
- Minimum disk / RAM guidance at common repo sizes.
- Explicit Windows stance (unsupported / WSL-only — TBD with maintainer; see B.1a
  below).

**B.1a decision needed:** confirm Windows stance with maintainer before writing prose.
Options: (a) "POSIX-only (macOS/Linux), use WSL on Windows"; (b) "Untested on Windows";
(c) full Windows support with instructions. Given the flock + Unix-socket + `/tmp`
assumptions throughout the code, (a) is most honest. **Default to (a) unless told
otherwise.**

**Fix:** Add a "Prerequisites" subsection with the items above; add a "Platform support"
callout at the top stating OS coverage.

### B.2 — `getting-started/claude-code-setup.mdx` — smoke test + embeddings reassurance
**Problems:**
- No pre-restart sanity check. If the `.mcp.json` `args` path is wrong, the user only
  learns after restarting Claude Code, with no clear diagnostic.
- Sample `summary` output shows "Embeddings: 100%" but quickstart treats `--embed` as
  opt-in. A first-time user who follows quickstart→setup will see `0%` and assume
  setup is broken.

**Fix:**
- Add a smoke-test step before the Claude Code restart:
  ```bash
  shaktimand /abs/path/to/project < /dev/null
  # should print startup logs and exit cleanly on stdin close
  ```
- Add a `:::note` on the sample output explaining that `Embeddings: 0%` is the expected
  first-run state when you haven't enabled embeddings. Reference the embeddings guide.

### B.3 — `getting-started/quickstart.mdx` — daemon/CLI default + anchors + link
**Problems:**
- Line 81 see-also points to `/troubleshooting/overview` when the actual relevant page
  is `/troubleshooting/empty-or-bad-results`.
- Line 66 "If numbers look right, move on." doesn't anchor what "right" means (users
  want: non-zero files, non-zero symbols, ~0 parse errors).
- Does not state that `--embed` is the CLI default-off, while daemon-launched indexing
  defaults to `embedding.enabled=true`. Combined with config-file.md:125, this confuses
  the user about which default applies to them.

**Fix:**
- Retarget the see-also link to `/troubleshooting/empty-or-bad-results`.
- Replace "If numbers look right, move on." with an anchored statement: "You want
  non-zero chunk + symbol counts and a parse-error count near zero."
- Add a one-line clarifier at step 3: "CLI indexing does not generate embeddings by
  default. The daemon-launched path (what Claude Code uses) does — see
  [Embeddings](/configuration/embeddings) for how the two paths differ."

### B.4 — `troubleshooting/embedding-failures.md:55` — make "restart shaktimand" concrete
**Problem:** The instruction "restart shaktimand" is ambiguous across integrations:
kill the PID? close Claude Code? re-run?
**Fix:** Replace with an integration-conditioned block:
- **Claude Code**: close the session and reopen (Claude Code owns the daemon's
  lifecycle).
- **Cursor / Zed / generic MCP**: close the client window/session.
- **CLI-only / manual**: `kill $(cat .shaktiman/daemon.pid)` then let the next client
  spawn a new leader.

### B.5 — `troubleshooting/daemon-and-leader.md:60` — remove py-spy, OS-tag diagnostics
**Problems:**
- `py-spy dump --pid <pid>` is Python-only; it does nothing useful on a Go binary.
- `lsof` on macOS sometimes needs `sudo`; not noted.
- Linux-only commands (`/proc/<pid>/fd/`) appear alongside macOS advice without tags.
**Fix:**
- Replace `py-spy dump` with Go-appropriate: `kill -QUIT <pid>` (dumps goroutine stack
  to stderr) or `dlv attach <pid>`.
- Split diagnostic blocks into "macOS" and "Linux" subsections.
- Note `sudo` where required.

### B.6 — Fix the broken "see also" targets (batch)
**Problem:** Five pages have see-also links that point at `/troubleshooting/overview`
(the TOC) instead of the targeted subpage. A user in pain reaches a TOC instead of an
answer.
**Files to fix:**
- `getting-started/quickstart.mdx:81` → `/troubleshooting/empty-or-bad-results` (also
  covered by B.3; land whichever happens first).
- `getting-started/claude-code-setup.mdx:90` → `/troubleshooting/daemon-and-leader`.
- `configuration/embeddings.md:99` → `/troubleshooting/embedding-failures`.
- `guides/multi-instance.md:112` → `/troubleshooting/daemon-and-leader`.
- `troubleshooting/embedding-failures.md:107` (if it points to overview) → the specific
  page.
**Fix:** One commit: update each link to the specific page.

### B.7 — `configuration/config-file.md` + `configuration/embeddings.md` — reconcile the default
**Problem:** `config-file.md:125` says `embedding.enabled` defaults to `true`;
`quickstart.mdx` treats `--embed` as opt-in; neither explains the daemon-vs-CLI
distinction.
**Fix:** Add a short subsection to `configuration/embeddings.md`:
> **Daemon vs CLI indexing.** The daemon-launched indexer (what MCP clients like Claude
> Code trigger) honors `embedding.enabled`, which defaults to `true`. The `shaktiman
> index` CLI is opt-in: pass `--embed` to generate embeddings. Both paths write to the
> same vector store; the distinction is only about whether embedding is automatic or
> explicit.

Cross-link this from `config-file.md:125` instead of restating.

### B.8 — `performance/scaling.md:50` — add the CREATE SCHEMA step
**Problem:** Scaling guide sets `[postgres].schema = "shaktiman_myproject"` but Postgres
returns a cryptic error if the schema doesn't exist; guide never says to create it.
**Fix:** Insert a `psql` snippet before the TOML block:
```sql
CREATE SCHEMA IF NOT EXISTS shaktiman_myproject;
```
with a one-line rationale.

### B.9 — `troubleshooting/backend-errors.md` + `guides/reindexing.md` — reindex prerequisites
**Problems:**
- `troubleshooting/backend-errors.md:88-94` tells the user to `shaktiman reindex
  --embed` without noting that `reindex` refuses if a daemon is running.
- Dimension-mismatch fix (line 121-123) doesn't remind about `--db` / `--vector` flags
  for non-default backends.
- `guides/reindexing.md:99-108` describes the Ctrl-C-between-phases state but doesn't
  tell the user how to *detect* they're in that state or that `shaktiman index` won't
  recover it (must be `reindex` re-run).
**Fix:** Three surgical edits; can land as one commit.

---

## Series C — Structural trim

Reader-attention reductions. Lower priority than B but improves long-term maintenance.

### C.1 — Delete `design/overview.mdx` (placeholder page)
**Problem:** The page is a `:::note[Placeholder]` + a link list the sidebar already
provides.
**Fix:** Delete the file; update `sidebars.ts` to remove the entry. Move the
"contributor/curious-mind audience, not end-user docs" disclaimer into a single
admonition at the top of `architecture.md`.

### C.2 — Trim ADRs 001-004 to 1-page summaries
**Problem:** `adr-001.md` (711 lines), `adr-002.md` (1521), `adr-003.md` (1527),
`adr-004.md` (765). Many sections are marked SUPERSEDED / NOT SHIPPED in their own
status notes. At contributor audience (Q1), the full record belongs in the repo; the
site should carry a summary.
**Fix per ADR:** Rewrite each to Status / Context / Decision / Key constraints / Link
to full ADR in the repo. Target 100-200 lines each. Keep original full versions under
`/docs/architecture/` in the repo as the canonical record.

### C.3 — Trim `architecture.md` — table + drop NFR traceability
**Problems:**
- Lines 1124-1150 ASCII "TOKEN EFFICIENCY" box restates a table in box-drawing chars.
- Lines 1180-1215 NFR traceability matrix is internal-only.
**Fix:** Replace the ASCII box with a markdown table; delete the NFR traceability
section from the site (keep in repo-local architecture doc if desired).

### C.4 — Fix inbound `design/architecture` authoritative links
**Problem:** `migrating/from-claude-default-tools.md:33,108` links to
`/design/architecture` as the source for headline token-reduction numbers. With Q1's
answer (architecture is contributor-facing), those marketing-critical numbers must live
inline on the user-facing migration pages, not behind a contributor link.
**Fix:** Inline the concrete numbers in the migration pages; keep
`/design/architecture` as a "further reading" link rather than a load-bearing citation.

### C.5 — `intro.mdx:21-26` — delete the "site being authored" admonition
**Problem:** A published landing page shouldn't advertise itself as in-flight. Leaks
build-meta to readers and signals "this site is not ready".
**Fix:** Delete the `:::note` block (lines 21-26).

### C.6 — Consolidate Claude Code setup content
**Problem:** `getting-started/claude-code-setup.mdx` and `integrations/claude-code.md`
duplicate `.mcp.json`, env vars, and the `/mcp` troubleshooting. Similar overlap with
`cursor.mdx` and `zed.mdx`.
**Fix:**
- Make `getting-started/claude-code-setup.mdx` the single canonical setup.
- Reduce `integrations/claude-code.md` to: CLAUDE.md template + subagent prompt +
  multi-window note + a link back to setup.
- `cursor.mdx` and `zed.mdx` keep only their config-file-path delta; link to setup for
  the rest.
- Add Docusaurus redirects for any URLs that move.

### C.7 — Trim `examples/overview.md` and `migrating/overview.md` preambles
**Problem:** Both pages have multi-section preambles ("conventions", "reading order",
"recurring theme", "what Shaktiman doesn't do") that restate sidebar navigation or
duplicate per-page content.
**Fix:** Keep the routing table; drop the preamble sections. If disclaimers are
important, collapse into a single 2-line admonition.

### C.8 — Remove sidebar-duplicating "See also" tails
**Problem:** Nearly every page ends with a "See also" block that links into the same
sidebar group the page is already in (e.g. `integrations/claude-code.md:115`,
`cursor.mdx:61`, `zed.mdx:57`, `performance/overview.mdx:76`,
`configuration/backends.md:93`, `multi-instance.md:108`).
**Fix:** Remove the tails where the links duplicate the sidebar group. Keep cross-group
"see also" entries (e.g. from a guide to a troubleshooting page).

---

## Commit & PR strategy

- **Branch naming:** `docs/a-correctness-pass`, `docs/b-onboarding-pass`,
  `docs/c-structural-trim`.
- **One commit per step.** Series A is 6 commits; Series B is 9 commits; Series C is 8
  commits. Commit messages follow the repo's `docs(website): ...` style.
- **Three PRs, independently mergeable.** Series A lands first; B and C can proceed in
  parallel.
- **Series C.6 needs Docusaurus redirects** for any moved URLs to avoid breaking
  inbound links from search engines and existing bookmarks.

## Verification

Each commit should pass:
```bash
cd website && pnpm build
```
Docusaurus build errors on broken internal links. That's the cheapest way to catch
typos in retargeted "see also" entries.

Optional: a link-check pass before merging each series:
```bash
cd website && pnpm start  # check anchor resolution in dev mode
```

## Open items before starting

- **B.1a:** Confirm Windows stance with maintainer (default to POSIX-only unless told
  otherwise).
- **C.2:** Confirm ADRs' canonical location is `/docs/architecture/` in the repo
  (assumed). If they live elsewhere, update the "Link to full ADR" URL accordingly.
- **C.6:** Confirm willingness to add Docusaurus redirects (low-cost but changes
  `docusaurus.config.ts`).
