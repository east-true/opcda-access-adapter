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

## Status

```text
GET /v1/status
```

Status reports runtime, source, connection generation, capabilities, Write
enablement, listener state, queue depth, reconnect count, and any degraded
reason. It never contains process values. In `degraded` state the reason tells
the operator to restart the process; the adapter does not terminate a hung COM
thread.

When a connection attempt or an established connection fails at the COM/OPC
method layer, status retains exactly one bounded diagnostic until the next
successful connection:

```json
{
  "state": "disconnected",
  "source": {
    "progId": "Vendor.Server.1",
    "connectionGeneration": 0,
    "lastError": {
      "operation": "CoCreateInstance(IOPCServer)",
      "hresult": {"value": -2147024891, "hex": "0x80070005"}
    }
  }
}
```

This record never contains an ItemID or process value. It is not an error
history and does not replace per-item Read/Write HRESULTs.

## Configuration

`opcda-access-adapter setup` can create a versioned, bounded configuration for
`run --config` or Windows Service execution. File-based execution does not
merge ambient environment variables. See [guided setup](setup.md). The
existing no-argument mode reads the environment variables below. New files use
configuration version 2; version 1 HTTP files remain readable.

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_FRONTEND` | `http` | select HTTP; `grpc` selects the separate typed frontend |
| `OPCDA_HTTP_LISTEN` | `127.0.0.1:8080` | HTTP bind address |
| `OPCDA_WRITE_ENABLED` | `false` | enable value Write explicitly |
| `OPCDA_MAX_HTTP_BODY_BYTES` | `1048576` | request body bound |
| `OPCDA_MAX_HTTP_CONNECTIONS` | `64` | accepted TCP connection bound |
| `OPCDA_MAX_CONCURRENT_REQUESTS` | `32` | HTTP concurrency bound |
| `OPCDA_MAX_HTTP_HEADER_BYTES` | `32768` | request header bound |
| `OPCDA_MAX_JSON_DEPTH` | `64` | JSON object/array nesting bound |
| `OPCDA_HTTP_READ_HEADER_TIMEOUT` | `5s` | incomplete-header timeout |
| `OPCDA_HTTP_READ_TIMEOUT` | `15s` | complete request read timeout |
| `OPCDA_HTTP_WRITE_TIMEOUT` | `15s` | response write timeout |
| `OPCDA_HTTP_IDLE_TIMEOUT` | `30s` | keep-alive idle timeout |
| `OPCDA_REQUEST_DEADLINE` | `10s` | frontend deadline |
| `OPCDA_RECONNECT_INITIAL` | `1s` | initial reconnect backoff |
| `OPCDA_RECONNECT_MAX` | `30s` | maximum reconnect backoff |
| `OPCDA_COM_CALL_WATCHDOG` | `30s` | threshold before fail-fast degraded state |
| `OPCDA_COMMAND_QUEUE` | `64` | serialized DA command queue bound |
| `OPCDA_MAX_READ_ITEMS` | `100` | Read batch bound |
| `OPCDA_MAX_WRITE_ITEMS` | `100` | Write batch bound |
| `OPCDA_MAX_BROWSE_ENTRIES` | `1000` | Browse result hard limit |
| `OPCDA_MAX_BROWSE_DEPTH` | `64` | Browse navigation depth bound |
| `OPCDA_MAX_REGISTERED_ITEMS` | `1024` | lazy item-registration cache bound |
| `OPCDA_MAX_ITEM_ID_BYTES` | `1024` | exact ItemID UTF-8 byte bound |
| `OPCDA_MAX_BSTR_CODE_UNITS` | `65536` | source/request BSTR UTF-16 bound |
| `OPCDA_MAX_ITEM_PROPERTIES` | `64` | DA item properties per item bound |

gRPC-specific listener and transport bounds are documented in the
[gRPC API reference](grpc-api.md). Only one frontend listener is selected per
adapter process; both frontends call the same DA runtime semantics.

The runtime also applies the hard per-batch, Browse, ItemID, BSTR, command
queue, and registration limits recorded in ADR-0001. No recent operation or
process-value history is retained in memory, so there is no unbounded
diagnostic ring. Logs go directly to the configured process output and never
include process values; deployment-level collection and rotation remain the
operator's responsibility.

"By default" would be the wrong hedge here: there is no setting that turns
value logging on. The guarantee is structural. The packages that handle values
-- `internal/opcda`, `internal/opcua` and the frontends -- do not log at all.
Every logging call in the adapter is in `cmd/adapter`, and none carries a value:
they log an address, a frontend name, a CLI argument, and errors. So the only
route a value could take into a log is inside an error message, and no error
message carries one -- they carry the VARTYPE, which is the part worth
reporting. `TestValueHandlingPackagesDoNotLog` fails the build if a
value-handling package acquires a logging import, which is what keeps that
reasoning true rather than merely currently accurate.

Individually valid settings must also fit the aggregate memory ceilings in
[ADR-0008](adr/0008-http-origin-and-aggregate-bounds.md). This prevents, for
example, a maximum body size multiplied by maximum concurrency from admitting
a multi-gigabyte in-flight workload. Unsafe combinations fail startup.

## Device Read

```text
POST /v1/read
Content-Type: application/json
```

```json
{
  "source": "device",
  "items": [
    {"itemId": "Random.Int2"},
    {"itemId": "Random.Real8"}
  ]
}
```

`source` defaults to `device`; v0 rejects every other value. Results remain in
request order. A method-level COM failure is a source-layer HTTP error. An
item-level HRESULT, including a partial failure, remains in that item result
and the HTTP response is `200`.

Successful item fields include the actual VARIANT `dataType`, the AddItems
`canonicalDataType`, value encoding, raw 16-bit Quality, source timestamp and
its presence, raw/decoded access rights, and decimal/hex HRESULT. A zero
`FILETIME` is represented as timestamp-absent; adapter time is never
substituted.

## Read value representation

| VARTYPE | v0 HTTP representation |
|---|---|
| `VT_EMPTY`, `VT_NULL` | JSON `null`, distinguished by `dataType` |
| `VT_I1/UI1/I2/UI2/I4/UI4/INT/UINT/ERROR` | JSON integer |
| `VT_I8/UI8` | decimal JSON string |
| finite `VT_R4/R8` | JSON number |
| NaN, positive/negative infinity | string with `valueEncoding: float-special` |
| `VT_BOOL` | JSON boolean |
| valid UTF-16 `VT_BSTR` within the configured bound | JSON string |

`VT_CY`, `VT_DATE`, `VT_DECIMAL`, `VT_VARIANT`, arrays, byref values, invalid
UTF-16 BSTRs, and unknown types are explicit item-level errors in the current
v0 implementation. Arrays are not flattened or silently converted. Returned
VARIANTs are still cleared even when their value type is unsupported.

## Errors

Error bodies distinguish `frontend`, `adapter`, and `source` layers. Source
method errors include the raw HRESULT. Item errors are not replaced by a
generic request error.

Transport hardening errors include `METHOD_NOT_ALLOWED`,
`UNSUPPORTED_MEDIA_TYPE`,
`UNSUPPORTED_CONTENT_ENCODING`, `DUPLICATE_JSON_FIELD`,
`JSON_DEPTH_LIMIT_EXCEEDED`, `BROWSER_ORIGIN_REJECTED`, and, for a loopback
listener, `UNTRUSTED_HOST`.
An internal Read/Write result count or ordered ItemID mismatch is
`INTERNAL_RESULT_MISMATCH` and fails closed with HTTP 500.

## Browse

```text
POST /v1/browse
Content-Type: application/json
```

```json
{
  "path": ["Channel1", "Device1"],
  "filter": "all"
}
```

`path: []` means root. The path is a navigation sequence, not an ItemID and
is never used to infer one. `filter` is `all`, `branch`, or `item` and defaults
to `all`.

The runtime resets a hierarchical server to root and walks each source browse
name serially on the DA thread. Flat namespaces accept only the root path.
Item entries use the exact ItemID returned by `GetItemID`; branch entries do
not invent an ItemID. Canonical type and access rights are omitted when this
Browse interface does not supply them.

`IOPCBrowseServerAddressSpace` is optional. A server returning
`E_NOINTERFACE` reports Browse as `unsupported`, while known-ItemID Read stays
available. Exceeding the hard entry limit fails with
`BROWSE_RESULT_LIMIT_EXCEEDED`; results are never silently truncated.

## Typed value Write

```text
POST /v1/write
Content-Type: application/json
```

```json
{
  "items": [
    {
      "itemId": "Random.Int2",
      "dataType": "VT_I2",
      "valueEncoding": "json",
      "value": 42
    }
  ]
}
```

Write is value-only and disabled by default. Set `OPCDA_WRITE_ENABLED=true`
only for an explicitly authorized source. While disabled, the endpoint returns
HTTP 403 with `WRITE_DISABLED` before admitting source work.

The supplied VARTYPE must exactly equal the canonical VARTYPE obtained during
item registration. The adapter performs no canonical-type conversion. The
v0 Write representations are:

| VARTYPE | Required representation |
|---|---|
| `VT_EMPTY`, `VT_NULL` | JSON `null` |
| `VT_I1/UI1/I2/UI2/I4/UI4/INT/UINT/ERROR` | in-range JSON integer |
| `VT_I8/UI8` | in-range decimal JSON string |
| finite `VT_R4/R8` | JSON number |
| non-finite `VT_R4/R8` | `"NaN"`, `"+Infinity"`, or `"-Infinity"` with `valueEncoding: float-special` |
| `VT_BOOL` | JSON boolean |
| `VT_BSTR` | bounded JSON string |

Other types are explicitly unsupported. The result order matches the request,
and per-item source HRESULTs remain in HTTP 200 responses. `TYPE_MISMATCH` is
an item-level adapter error. A method-level HRESULT is a source-layer error.
The adapter makes one source Write call per admitted batch and never retries,
replays, or promises rollback. If the frontend deadline expires after COM has
started, the source outcome is unknown.

The HTTP frontend exposes no Subscribe endpoint, and HTTP streaming, SSE, and
WebSocket are out of scope. `capabilities.subscribe` describes the **source**,
not this frontend: it is true when the configured DA server exposes an
`IOPCDataCallback` connection point. Subscribing to it requires the gRPC
frontend; see [`grpc-api.md`](grpc-api.md).
