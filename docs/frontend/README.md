# Frontends

Only one frontend runs in an adapter process.

| Frontend | Best fit | Surface | Security boundary |
|---|---|---|---|
| [HTTP/JSON](http/README.md) | Scripts and simple integrations | Status, Browse, Read, Write, item properties | Loopback by default; no built-in TLS or authentication |
| [Typed gRPC](grpc/README.md) | Generated clients and DA-native streaming | HTTP surface plus server-streaming Subscribe | Plaintext loopback by default; no built-in TLS or authentication |
| [OPC UA](opcua/README.md) | Local UA interoperability work | Browse, Read, Write, Subscriptions, DA-to-UA properties | `SecurityPolicy None` only; anonymous, unsigned, unencrypted, not production ready |
| [MCP stdio](mcp/README.md) | Local AI client access | DA operations and explicit group create/read/release | Inactive until selected; no network listener; Write disabled by default |

MCP is inactive by default. Select `frontend: {"type":"mcp"}` in a version 4
configuration, then let the MCP host launch `opcda-access-adapter.exe run --config <file>`
with stdio. HTTP remains the default; frontends do not run simultaneously.

The authoritative gRPC contract is
[`api/opcda/v1/opcda_access.proto`](../../api/opcda/v1/opcda_access.proto). HTTP does
not expose Subscribe. OPC UA is a deliberately bounded frontend, not a general
UA server or a conformance claim.
