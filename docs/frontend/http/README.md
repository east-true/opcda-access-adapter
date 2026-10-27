# v0 HTTP API

The v0 listener defaults to `127.0.0.1:8080`. All request bodies and batch
sizes are bounded. ItemIDs are passed to OPC DA exactly as decoded; the
adapter does not trim, rename, normalize, or infer them.

All POST requests require `Content-Type: application/json`; parameters such as
`charset=utf-8` are accepted. Direct browser requests carrying an `Origin`
header are rejected because v0 has no browser frontend or CORS/authentication
contract. On a loopback-configured listener, a non-loopback `Host` is also
rejected to limit DNS-rebinding attacks. JSON responses are marked `no-store`,
`nosniff`, and `Cross-Origin-Resource-Policy: same-origin`.

Request targets use the exact paths below without query parameters or
percent-encoded aliases. Status is bodyless. JSON object keys are unique and
use the documented spelling; nesting is bounded. Compressed request bodies are
not accepted. Known endpoints return 405 and an `Allow` header for a wrong
method.

## Reference

| Operation | Contract |
|---|---|
| Status | [GET /v1/status](status.md) |
| Browse | [POST /v1/browse](browse.md) |
| Device Read | [POST /v1/read](read.md) |
| Typed Write | [POST /v1/write](write.md) |
| Item properties | [Property queries](item-properties.md) |

See [configuration](configuration.md) for environment settings and shared DA
limits, and [errors](errors.md) for request-level failures. HTTP has no Subscribe.

`capabilities.subscribe` reports source support, not an HTTP endpoint. Use
[gRPC](../grpc/subscribe.md), [OPC UA](../opcua/subscriptions.md), or
[MCP group tools](../mcp/README.md#subscription-lifecycle) for subscriptions.
