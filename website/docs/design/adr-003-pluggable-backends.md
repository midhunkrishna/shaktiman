---
title: ADR-003 — Pluggable Storage Backends
sidebar_position: 4
---

# ADR-003: Pluggable Storage Backends

:::note Placeholder

The canonical ADR is imported from
[`docs/design/adr-003-pluggable-storage-backends.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-003-pluggable-storage-backends.md)
in Step 7 of the rollout.

**Status (Today):** shipped. Metadata: `sqlite` (default), `postgres`. Vector:
`brute_force` (default), `hnsw`, `qdrant`, `pgvector`. Constraint A12 enforced in
`ValidateBackendConfig`.

:::
