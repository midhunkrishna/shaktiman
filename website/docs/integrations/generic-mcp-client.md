---
title: Generic MCP client
sidebar_position: 4
---

# Generic MCP client

Shaktiman ships as an MCP stdio server. Any client that speaks the
[Model Context Protocol](https://modelcontextprotocol.io/) over stdio can drive
it — Claude Code, Cursor, Zed, or anything you build yourself.

## On the wire

- **Transport:** stdio (stdin / stdout line-delimited JSON-RPC).
- **Server launch:** `shaktimand <project-root>` as a child process. The client
  owns the process lifetime; when the client exits, the child should too.
- **Protocol version:** as implemented by the
  [`mcp-go`](https://github.com/mark3labs/mcp-go) library that Shaktiman embeds.
- **Capabilities:** `tools`. The seven
  [registered tools](/reference/mcp-tools/overview) — `summary`, `search`,
  `context`, `symbols`, `dependencies`, `diff`, `enrichment_status` — appear in
  the `tools/list` response.
- **No resources, prompts, or notifications today** — see
  [Known Limitations](/reference/limitations#no-mcp-resources).

## Wiring your own client

Minimum viable setup from any language that can spawn a subprocess and speak
line-delimited JSON-RPC over stdio:

1. `spawn("/absolute/path/to/shaktimand", ["/absolute/path/to/project"])`.
2. Send the MCP `initialize` handshake.
3. Send `tools/list` — you should get the seven tools back.
4. Send `tools/call` with a tool name and arguments matching the schemas at
   [Reference → MCP Tools](/reference/mcp-tools/overview).
5. On shutdown, close stdin to let the server exit cleanly.

A reference JSON-RPC call for `summary`:

```jsonc
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "summary",
    "arguments": {}
  }
}
```

The response has `content[0].text` as a JSON string — parse it for the
structured result.

## Behaviour to expect

- **Multi-instance:** Launching a second `shaktimand` against the same project
  automatically enters proxy mode. Your client doesn't need to be aware; it's
  transparent. See [Multi-instance concurrency](/guides/multi-instance).
- **Logging:** stdout is reserved for MCP traffic. Structured logs go to
  `.shaktiman/shaktimand.log` (JSON per line). Stderr is for fatal startup
  errors only.
- **Read-only surface:** Every tool advertises
  `readOnlyHint: true`, `destructiveHint: false`, `idempotentHint: true`. Your
  client is safe to retry any call.
- **Error shape:** Bad input returns an MCP error `CallToolResult` with
  `isError: true`. Internal errors are truncated to 200 characters (defense in
  depth) and returned the same way.

## When this is the right path

- You're embedding Shaktiman in a custom agent framework.
- You're building automated pipelines that don't use an IDE-backed client.
- You're writing a test harness against the MCP surface.

For interactive use, reach for a ready-made client
([Claude Code](/integrations/claude-code), [Cursor](/integrations/cursor),
[Zed](/integrations/zed)) — they handle the handshake, tool registration, and
lifecycle for you.

## See also

- [MCP Tools Overview](/reference/mcp-tools/overview) — tool schemas.
- [Custom agents](/integrations/custom-agents) — scripting Shaktiman without
  MCP at all (via the CLI).
