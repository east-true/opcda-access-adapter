# MCP stdio API

MCP exposes the adapter's existing DA functions to an AI client. It is inactive
until `mcp` is explicitly selected; HTTP remains the default. One process serves
one local DA source through one frontend. Selecting MCP starts no HTTP, gRPC,
or UA listener.

## Configuration and launch

Use `detect` on Windows and explicitly choose the source's exact CLSID. Replace
the placeholder below with that registration:

```json
{
  "version": 4,
  "source": {"clsid": "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"},
  "frontend": {"type": "mcp"},
  "writeEnabled": false
}
```

Configure the host to launch the executable with stdio. Adapt the surrounding
configuration to the MCP client's format:

```json
{
  "mcpServers": {
    "opcda": {
      "command": "C:\\opcda\\opcda-access-adapter.exe",
      "args": ["run", "--config", "C:\\opcda\\mcp.json"]
    }
  }
}
```

The executable's 386 or amd64 architecture must match COM registration. The
host owns stdin/stdout; stdout carries JSON-RPC messages only. Windows Service
installation is unsupported. Choosing MCP as the fourth setup frontend saves
configuration only.

Environment-based execution uses `OPCDA_FRONTEND=mcp` and the existing single
source settings. File-based execution does not merge environment variables.

## Tools

| Name | Input | Operation |
|---|---|---|
| `da_status` | `{}` | Source, state, capabilities, Write policy, connection generation |
| `da_browse` | Optional `path`, `filter` | One Browse path; default filter `all` |
| `da_read` | `items: [{itemId}]`, optional `source: "device"` | Ordered device Read |
| `da_write` | `items: [{itemId, dataType, valueEncoding, value}]` | One strict typed value-only Write |
| `da_available_item_properties` | `itemId` | Source property identifiers, descriptions, and types |
| `da_item_properties` | `itemId`, `propertyIds` | Ordered property results, partial errors, value presence |
| `da_subscribe` | `items`, `requestedUpdateRateMs`, optional `percentDeadband` | Create a session-owned DA group |
| `da_subscription_next` | `subscriptionId`, optional `waitMs` | Drain the current coalesced set |
| `da_unsubscribe` | `subscriptionId` | Release the group |

Fixed JSON Schemas require exact field spelling. No tool performs registration
detection, source selection, configuration changes, or arbitrary code/command
execution. Properties 2, 3, and 4 are refused under the existing contract; use
Read or Subscribe for value, Quality, and timestamp together.

## Values and errors

Each result has `structuredContent` and equivalent JSON in text `content`.
Read fields and value encodings follow the [HTTP contract](../http/README.md): exact
ItemID, actual/canonical VARTYPE, raw Quality, source timestamp and its presence
bit, access rights, and HRESULT. Non-Good Quality must not be treated as a healthy
measurement. Absent source time is `timestampPresent: false` and `timestamp: null`;
no replacement timestamp is generated.

64-bit integers use decimal strings. NaN and infinity use `float-special`.
SAFEARRAY uses `array` with element type, per-dimension lower bounds/lengths,
and elements. Unsupported types fail explicitly.

```json
{
  "name": "da_read",
  "arguments": {"items": [{"itemId": "Vendor.Example.Item"}]}
}
```

Partial failures remain in result entries. Request-level or source-method
failure returns `isError: true`, `error.layer`, `error.code`, and the source
operation/HRESULT when applicable. Input and process values are not included
in error messages. An unknown tool is an SDK MCP protocol error.

Write is disabled by default. Only explicit `writeEnabled: true` permits a
source Write. Never retry automatically: cancellation or timeout does not prove
the source did not apply it. Tool annotations are hints, not authorization.

## Subscription lifecycle

`requestedUpdateRateMs` is an integer from 1 to 3,600,000; `percentDeadband` is
0–100. Creation returns `subscriptionId`, `connectionGeneration`, the source's
`revisedUpdateRateMs`, per-item AddItems results, and `activeItemCount`.

Next drains the core's current per-item coalesced set once. `waitMs` defaults
to 0 and allows at most 30,000. An empty wait returns `results: []`. Concurrent
next calls on one handle receive `QUEUE_FULL`. The existing request deadline
(default 10 seconds) takes precedence over `waitMs`.

The frontend adds no queue, history, or sampling. Slow clients read the core's
latest pending value per item and do not lose their group for being slow.
Disconnect discards pending values and returns `SUBSCRIPTION_INVALIDATED`.
Reconnect restores nothing: create a new subscription explicitly. Removed or
unowned handles return `SUBSCRIPTION_NOT_FOUND`.

Explicit unsubscribe, stdin EOF, cancellation, and transport failure clean up
owned groups. Session shutdown also shuts down the runtime. No replay or
automatic resubscribe occurs.

## Limits and validation

| Resource | Limit |
|---|---|
| Input JSON-RPC line | 1 MiB |
| Output JSON-RPC line | 4 MiB |
| Outstanding requests | 16 |
| JSON nesting depth | 64 |
| String JSON-RPC request ID | 128 bytes |
| Operation time | Existing request deadline; default 10 seconds |
| DA items, paths, arrays, and groups | Existing runtime bounds |

Reduce the batch when output is too large. Duplicate keys, invalid UTF-8 or
surrogates, oversized frames, batches, and duplicate outstanding IDs end the
session. Streamable HTTP, another authentication service, and simultaneous
frontends are unsupported. COM calls use the executing account's permissions.

Go SDK client tests run under `go test ./...`. For an independent executable
check, run `python3 scripts/mcp/check.py --adapter <executable>`. On Linux this
checks the unsupported-platform boundary. `--real-da --clsid <fixture CLSID>` is
for an authorized OPC Foundation fixture on Windows and includes enabled Write.
Only executed results in [compatibility evidence](../../validation/compatibility.md) justify a
real-DA validation claim. [ADR-0020](../../design/adr/0020-mcp-stdio-frontend.md) records the decision.
