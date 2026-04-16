---
title: Indexing stuck / stale results
sidebar_position: 3
---

# Indexing stuck / stale results

Covers "my edits don't show up in search results" and watcher issues.

## Symptom: recent edits don't appear in search results

### Likely causes (ranked)

1. **You're not running the daemon.** The CLI (`shaktiman search`) reads a static
   index. If nothing's re-indexing after your edits, nothing can show up. Check
   with `ls /tmp/shaktiman-*.sock` or look for `shaktimand` in `ps`.
2. **The watcher is disabled in config.** `watcher.enabled = false` in
   `.shaktiman/shaktiman.toml` (not the default) turns off auto-reindex.
3. **The file extension isn't recognised.** Shaktiman indexes only the languages
   wired in `internal/daemon/scan.go`; arbitrary `.txt` / `.md` / config files
   aren't indexed. See [Supported Languages](/reference/supported-languages).
4. **The file matches an ignore rule** — `.gitignore`, `.shaktimanignore`, or a
   pattern in `[test].patterns` if you searched with `scope:"impl"` (default).

### Diagnostic

```bash
# Is the daemon watching?
tail -f /path/to/project/.shaktiman/shaktimand.log | jq 'select(.component == "watcher")'

# Save a file and watch for the event
touch /path/to/project/src/<your-file>
# Expect to see: {"msg": "file change detected", ...} within ~200ms

# Check the scope you searched in
shaktiman search "your-query" --root /path/to/project --scope all
#                                                      ^^^^^^^^^^^^ no filter
```

### Fix

- Start the daemon (open Claude Code, or run `shaktimand /path/to/project` in
  another terminal).
- If the file extension isn't wired: see
  [Supported Languages → Adding a language](/reference/supported-languages#adding-a-language).
- If `scope` is hiding it, re-query with `scope: "all"`.
- If `.shaktimanignore` matches when it shouldn't, edit or remove the pattern.

## Symptom: `enrichment_status` pending count keeps growing

Chunks are being added faster than the embedding worker drains them.

### Likely causes

1. A cold index is in progress — this is normal and will catch up.
2. Ollama is responding slowly (weak GPU, CPU-only mode, or a very large batch).
3. `embedding.batch_size` is too small for your hardware, so each HTTP round-trip
   dominates.

### Diagnostic

```bash
# Watch pending count over time
watch -n 5 'shaktiman enrichment-status --root .'

# How fast is each batch?
grep "embed_batch" /path/to/project/.shaktiman/shaktimand.log | tail -n 20
```

### Fix

- Raise `[embedding].batch_size` if your Ollama can handle it.
- Move Ollama to a faster machine if CPU-bound.
- Accept that it takes a while on a huge repo; keyword search works in the
  meantime.

## Symptom: the watcher is dropping events

The log shows `"dropped file change event"` warnings.

### Likely cause

A burst of file changes exceeded the watcher's buffered channel (capacity 100).
Common causes: `npm install`, `git reset --hard`, extracting a big tarball into
the project, or a build tool writing generated files in-tree.

### Fix

- **Add noise to `.shaktimanignore`** — node_modules, build/, dist/, generated
  directories. This prevents them from entering the watcher in the first place.
- Ignore transient noise: the watcher intentionally drops silently under load
  rather than blocking indexing; a periodic full scan would pick up genuine
  missed changes, but that scan isn't implemented today. For now, if you suspect
  a file was missed, `touch` it to force a re-index.

## See also

- [Guides → How indexing works](/guides/indexing) — the pipeline and watcher
  design.
- [Guides → Re-indexing](/guides/reindexing) — the nuclear option when you're
  really stuck.
