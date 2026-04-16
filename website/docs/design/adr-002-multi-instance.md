---
title: ADR-002 — Multi-Instance Concurrency
sidebar_position: 3
---

# ADR-002: Multi-Instance Concurrency

:::note Placeholder

The canonical ADR is imported from
[`docs/design/adr-002-multi-instance-concurrency.md`](https://github.com/midhunkrishna/shaktiman/blob/master/docs/design/adr-002-multi-instance-concurrency.md)
in Step 7 of the rollout.

**Status (Today):** shipped as single-daemon + socket-proxy. `flock` on
`.shaktiman/daemon.pid`, Unix socket at `/tmp/shaktiman-<hash>.sock`, re-exec on
leader exit.

:::
