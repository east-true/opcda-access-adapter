# ADR-0020: Explicitly selected DA-native MCP stdio frontend

- Status: Accepted
- Date: 2026-10-27
- Issue: [#226](https://github.com/east-true/opcda-access-adapter/issues/226)

## Context

Add MCP in this repository to expose existing functionality to AI clients.
Design §22 applies: one local DA server, without another bridge process,
multi-source aggregation, asset models, new sources, persistent values, or
automated control.

## Decision

1. `mcp` is mutually exclusive with HTTP, gRPC, and UA. The default remains
   `http`; no MCP server, session, or transport is created until selected.
   MCP calls the same in-process DA runtime directly and opens no listener.
2. Use client-launched stdio. stdout carries one JSON-RPC message per line;
   diagnostics use stderr. Retain the SDK's default discard logger and no wire
   logging. Reject Windows Service installation; setup offers save-only execution.
3. Configuration version 4 uses `frontend: {"type":"mcp"}`. Continue reading
   versions 1–3, but allow MCP only in version 4. Reject other frontend addresses
   and UA settings in an MCP file.
4. Expose Status, Browse, device Read, strict value-only Write, available item
   properties, item properties, and group creation/read/release. Validate fixed
   input schemas and preserve exact ItemID, order, partial failures, VARTYPE,
   raw Quality, timestamp presence, HRESULT, and access rights.
5. Share the DA JSON value codec with HTTP: decimal strings for 64-bit integers,
   explicit non-finite float names, and SAFEARRAY element type, dimension bounds,
   lengths, and order. Do not route requests through HTTP or gRPC handlers.
6. MCP tools do not form a DA event stream. Create one session-owned DA group;
   next drains the core's coalesced set directly. Add no queue, resampling, drop
   counter, automatic resubscribe, or replay. Slow consumers retain their group.
   Empty wait expiry returns no results; invalidation returns
   `SUBSCRIPTION_INVALIDATED`. Removed or unowned handles return
   `SUBSCRIPTION_NOT_FOUND`. Reject parallel next calls on one handle.
7. Explicit release, EOF, cancellation, and transport failure clean up groups,
   then shut down the runtime. A new connection generation restores no group.
   Cancelling a Write does not establish that the source did not execute it.
8. Retain default-disabled, value-only, strictly typed Write with no automatic
   retry. Tool annotations are hints, not authorization. Add no approval UI
   or automated judgment policy to the adapter.
9. Bound input lines to 1 MiB, output to 4 MiB, outstanding requests to 16, JSON
   depth to 64, and string request IDs to 128 bytes. Check output after text
   escaping. Batches, duplicate keys/IDs, invalid UTF-8/surrogates, and oversized
   frames are session errors. Tool errors use `isError`, layer, code, and original
   HRESULT without input or process values. Use the existing request deadline
   (default 10 seconds); it takes precedence over next's maximum 30-second wait.
10. Pin official `github.com/modelcontextprotocol/go-sdk v1.8.0` and
    `github.com/google/jsonschema-go v0.4.3`. The SDK handles negotiation,
    cancellation, and tools; the adapter bounds requests and frames before the
    SDK's asynchronous queue. Its default discard logger was reviewed. Do not
    expose HTTP, OAuth, resources, prompts, or sampling capabilities.

## Dependencies and distribution

The additional modules are the two above, SDK JSON implementations
`github.com/segmentio/encoding v0.5.4` and `github.com/segmentio/asm v1.1.3`,
URI templates `github.com/yosida95/uritemplate/v3 v3.0.2`, and
`golang.org/x/oauth2 v0.36.0`, `golang.org/x/sync v0.22.0`, and `golang.org/x/time v0.15.0`.
They add no OPC SDK or industrial source. Record versions and license texts in
`THIRD_PARTY_NOTICES.md` and reasons in the dependency allowlist. Preserve the
SDK LICENSE's Apache-2.0 transition and remaining MIT contributions verbatim
rather than describing them as one license.

All frontends ship in one executable, so code and dependencies grow even when
MCP is not selected. No MCP session or network listener starts before selection.
With local Go 1.26.0 and default `go build`, unstripped sizes changed from
19,270,144 to 22,624,768 bytes on 386 and 20,131,328 to 23,719,936 bytes on amd64.
These are local executable sizes, not release ZIP sizes or memory measurements.

## Validation and alternatives

Go SDK client tests cover tools, value representation, partial failures, Write
policy, group ownership, coalescing, invalidation, and EOF cleanup. An independent
Python client tests the executable's stdio in Linux CI and the existing Windows
386/amd64 fixture procedure. Workflow configuration alone is not a real-DA
result; record executed evidence separately.

A separate gRPC bridge would add deployment and API-version contracts without
serving this initial requirement. Streamable HTTP adds an unrequested network
boundary. Use the official SDK rather than writing JSON-RPC, while retaining
responsibility for transport bounds. Do not present MCP resource-change
notifications as DA callbacks; expose explicit group tools.

## References

- [MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
- [MCP transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [Pinned official SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)
