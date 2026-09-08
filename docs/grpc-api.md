# DA-native gRPC API

The gRPC frontend is a typed access transport for one explicitly
configured local OPC DA server. Its authoritative schema is
[`api/opcda/v1/opcda_access.proto`](../api/opcda/v1/opcda_access.proto).
The schema and generated Go bindings are included in source and the `.proto`
file is included in release-shaped archives.

## Service

```text
package: opcda.access.v1
service: OPCDAAccess

Status      unary
Browse      unary
Read        unary
Write       unary
Subscribe   server streaming

AvailableItemProperties   unary
ItemProperties            unary
```

`Subscribe` is the only stream, and it is server-streaming only: a client never
pushes into an open subscription. Changing what is subscribed means opening a
new stream.

The messages use OPC DA vocabulary and preserve:

- exact ItemID and request/result ordering;
- raw 16-bit VARTYPE plus its symbolic name and flags;
- raw 16-bit Quality;
- source timestamp and its independent presence bit;
- signed, raw unsigned, and hexadecimal HRESULT representations;
- source access rights; and
- method-level versus per-item failures.

There is no Asset, Device entity, Metric, Point, Signal, normalization, tag
mapping, routing, persistence, or multi-server contract.

## Subscribe

One `Subscribe` stream is exactly one OPC DA group. The request carries the
items, `requested_update_rate_ms` (the group's `dwRequestedUpdateRate`), and an
optional `percent_deadband`.

The first stream message is always `created`. It reports the subscription
identifier, the connection generation, the **server's** `revised_update_rate_ms`
rather than the requested rate, and one status per item. An item the source
refused keeps its exact HRESULT in that status instead of failing the whole
subscription, so a subscription can legitimately come back with
`active_item_count` lower than the number of requested items.

Every later message is an `update` carrying one coalesced notification batch.
Its values are `DAReadResult` messages, the same type a device Read returns, so
a subscribed value and a read value cannot drift apart.

### Delivery follows DA sampling, not an event log

OPC DA is a sampling model. Between two update-rate ticks a server reports only
the latest cache value for an item, so the adapter coalesces per item in the
same way. The consequences are deliberate:

- **A slow client observes a slower update rate.** Backpressure is the HTTP/2
  flow-control window and nothing else; the adapter holds no queue of its own.
  While the client is behind, the DA core keeps replacing each item's pending
  value, exactly as a server does between ticks.
- **There is no overflow.** The pending state holds at most one entry per
  active item, so there is nothing to overflow and no drop counter to report.
- **A slow client is never disconnected for being slow.** No DA server does
  that, so the adapter does not either.

### Ending a stream

| Cause | What the client sees |
|---|---|
| client cancels or closes | stream ends; the adapter releases the DA group |
| source disconnect, degraded runtime, or shutdown | `Aborted` with a `DAOperationError` whose code is `SUBSCRIPTION_INVALIDATED` |
| too many concurrent streams | `ResourceExhausted` with code `SUBSCRIPTION_LIMIT_EXCEEDED` |

Ending a stream for any reason releases the subscription. Values still pending
when a subscription is invalidated are **discarded, never delivered**, because
they belong to a connection generation that has ended. The adapter never
resubscribes on its own and never replays a value: after an invalidated stream
the client must call `Subscribe` again, and it receives a new
generation-scoped identifier.

`DARuntimeStatus.subscription_count` reports how many DA groups the runtime
currently holds, and returns to zero once a stream has been released.

### Connection age bounds a stream

`MaxConnectionAge` (30 minutes by default) plus its grace period applies to
subscription streams as well as unary calls, so a long-lived stream is closed
periodically by design. This keeps connection lifetime bounded; the client
reconnects and subscribes again explicitly. Raise
`OPCDA_GRPC_MAX_CONNECTION_AGE` if a deployment needs longer streams, and
understand that doing so extends how long one connection can be held.

## Scalar value representation

`DAScalarValue` is a protobuf `oneof`. Its field must match the explicit raw
VARTYPE on Write and the source VARTYPE on Read.

| OPC DA VARTYPE | protobuf field |
|---|---|
| `VT_EMPTY`, `VT_NULL` | `empty_or_null` |
| `VT_I1` | `i1_value` |
| `VT_UI1` | `ui1_value` |
| `VT_I2` | `i2_value` |
| `VT_UI2` | `ui2_value` |
| `VT_I4` | `i4_value` |
| `VT_UI4` | `ui4_value` |
| `VT_I8` | `i8_value` |
| `VT_UI8` | `ui8_value` |
| `VT_R4` | `r4_value` |
| `VT_R8` | `r8_value` |
| `VT_BOOL` | `bool_value` |
| `VT_BSTR` | `bstr_value` |
| `VT_ERROR` | `error_value` |
| `VT_INT` | `int_value` |
| `VT_UINT` | `uint_value` |

The VARTYPE, not the protobuf numeric container, is the source of truth for
the original COM width. Protobuf float and double preserve NaN and infinities
without a JSON-specific encoding. Unsupported scalar types, SAFEARRAY, and
BYREF values fail explicitly and are never coerced.

Source time uses signed Unix seconds and nanoseconds only when
`timestamp_present` is true. Absence remains absence; the adapter does not
insert its clock.

## Batch and error semantics

Read and Write are batch-first and preserve request order. A source result such
as success/failure/success returns a successful gRPC response with three
ordered result messages and each item's HRESULT/error. Bad Quality is data,
not a transport failure.

Request-level failures use canonical gRPC codes and attach a typed
`DAOperationError` detail:

Every adapter error code the gRPC frontend can return, and the canonical status
it arrives as. The codes themselves are described in the
[HTTP reference](http-api.md#errors); this is the mapping, not a second
vocabulary.

| gRPC status | Adapter error codes |
|---|---|
| `InvalidArgument` | `INVALID_REQUEST`, `INVALID_VALUE`, `ITEM_ID_TOO_LONG`, `BSTR_TOO_LONG`, `TYPE_MISMATCH` |
| `ResourceExhausted` | `REQUEST_LIMIT_EXCEEDED`, `BROWSE_RESULT_LIMIT_EXCEEDED`, `REGISTERED_ITEM_LIMIT_EXCEEDED`, `QUEUE_FULL`, `SUBSCRIPTION_LIMIT_EXCEEDED` |
| `Unimplemented` | `BROWSE_UNSUPPORTED`, `PROPERTIES_UNSUPPORTED`, `SUBSCRIBE_UNSUPPORTED`, `UNSUPPORTED_VARTYPE` |
| `PermissionDenied` | `WRITE_DISABLED` |
| `DeadlineExceeded` | `RUNTIME_DEADLINE_EXCEEDED` |
| `NotFound` | `SUBSCRIPTION_NOT_FOUND` |
| `Aborted` | `SUBSCRIPTION_INVALIDATED` |
| `Internal` | `INTERNAL_RESULT_MISMATCH` |
| `Unavailable` | `RUNTIME_UNAVAILABLE`, `DA_METHOD_FAILED`, and anything not named above |

`Unavailable` is the default, so an unmapped code arrives as `Unavailable`
rather than as something more specific. A failure that is neither a source
error nor an adapter error is the one case with no adapter code behind it: it
arrives as `Internal` carrying `INTERNAL_ERROR`. `Aborted` is deliberate for
`SUBSCRIPTION_INVALIDATED`: the client must resubscribe explicitly, and the
call must not look transparently retryable. A cancelled call arrives as
`Canceled` carrying `RUNTIME_DEADLINE_EXCEEDED`.

A `DA_METHOD_FAILED` detail carries the operation name and the raw HRESULT; the
frontend and adapter layers carry the code and a bounded message.

| Layer | Preserved detail |
|---|---|
| frontend/validation | adapter error code and bounded message |
| adapter/runtime | adapter error code |
| DA source method | operation and raw HRESULT |

The adapter does not configure automatic retries. In particular, a Write whose
client deadline expires has an unknown source outcome and must not be replayed
automatically.

## Item properties

`AvailableItemProperties` and `ItemProperties` are
`IOPCItemProperties::QueryAvailableProperties` and `::GetItemProperties`, passed
through. Two calls because they are two questions: what does this source offer
for this item, and what are those properties' values.

The frontend is DA-native — the source's own property identifiers, description
text, VARTYPEs and HRESULTs are reported and mapped onto nothing. OPC 10000-8
Table A.1 is applied by the OPC UA frontend, not here.

A per-property HRESULT is a result rather than a failure: the request succeeds
and a refused property carries its exact HRESULT with no substituted value.

The item's value, quality and timestamp are properties 2, 3 and 4, and naming
any of them is **not** a refusal of that kind. It fails the whole call with
`InvalidArgument` and `INVALID_REQUEST`, even alongside valid identifiers and
wherever in the list it appears: a source declining a property is a result, but
asking this method for a value is a mistake in the request. Read and Subscribe
deliver a value with its timestamp and raw quality together, and answering the
same question a second way without them could produce a different answer.

A source that does not implement `IOPCItemProperties` reports
`capabilities.properties` as `unsupported` and answers `Unimplemented` with
`PROPERTIES_UNSUPPORTED`, the same way a source without Browse does.

## Configuration

Guided setup lists HTTP/JSON, gRPC and OPC UA, and requires an explicit
choice:

```powershell
.\opcda-access-adapter.exe setup --grpc-listen 127.0.0.1:50051
```

Select `gRPC`, then foreground, Windows Service, or save only. New setup files
use strict configuration version 3:

```json
{
  "version": 3,
  "source": {
    "clsid": "{00000000-0000-0000-0000-000000000000}"
  },
  "frontend": {
    "type": "grpc",
    "grpcListen": "127.0.0.1:50051"
  },
  "writeEnabled": false
}
```

Versions 1 and 2 remain readable. A gRPC configuration contains no HTTP or
OPC UA listener and a single adapter process still owns one runtime and one
source.

The original environment workflow can select gRPC explicitly:

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_FRONTEND` | `http` | select `http`, `grpc`, or `opcua` |
| `OPCDA_GRPC_LISTEN` | `127.0.0.1:50051` | gRPC bind address |
| `OPCDA_MAX_GRPC_RECEIVE_BYTES` | `1048576` | inbound message bound |
| `OPCDA_MAX_GRPC_SEND_BYTES` | `4194304` | outbound message bound |
| `OPCDA_MAX_GRPC_CONNECTIONS` | `16` | accepted TCP connection bound |
| `OPCDA_MAX_CONCURRENT_GRPC_RPCS` | `32` | process-wide admitted unary RPC bound |
| `OPCDA_MAX_GRPC_STREAMS` | `16` | HTTP/2 concurrent streams per connection |
| `OPCDA_MAX_GRPC_METADATA_BYTES` | `32768` | request metadata/header-list bound |
| `OPCDA_GRPC_CONNECTION_TIMEOUT` | `5s` | HTTP/2 connection handshake timeout |
| `OPCDA_GRPC_MAX_CONNECTION_IDLE` | `2m` | idle connection lifetime |
| `OPCDA_GRPC_MAX_CONNECTION_AGE` | `30m` | maximum connection lifetime |
| `OPCDA_GRPC_MAX_CONNECTION_AGE_GRACE` | `30s` | bounded grace after maximum age |
| `OPCDA_GRPC_KEEPALIVE_MIN_TIME` | `30s` | minimum accepted client ping interval |
| `OPCDA_REQUEST_DEADLINE` | `10s` | server-side operation deadline |
| `OPCDA_MAX_SUBSCRIPTIONS` | `16` | concurrent `Subscribe` stream bound |
| `OPCDA_MAX_SUBSCRIPTION_ITEMS` | `100` | items in one `Subscribe` request |

DA batch, Browse, ItemID, queue, reconnect, BSTR, and COM watchdog variables
are shared with HTTP and documented in the HTTP reference.

## Security boundary

gRPC defaults to plaintext IPv4 loopback. The project does not yet provide a
TLS, authentication, authorization, or RBAC platform. Binding to a non-loopback
address is an explicit deployment security decision and must use an appropriate
external network and identity boundary.

The server bounds connections, streams, metadata, message sizes, concurrent
RPCs, and operation duration. Aggregate receive, send, and metadata budgets
also reject individually valid settings whose product would admit unsafe
in-flight memory. Reflection is not enabled. Process values and Write values
are not logged.

## Client generation

Use a protobuf toolchain compatible with the committed schema. This repository
generates its Go bindings with protoc v36.0, protoc-gen-go v1.36.12, and
protoc-gen-go-grpc v1.6.2:

```powershell
protoc `
  --go_out=. --go_opt=paths=source_relative `
  --go-grpc_out=. --go-grpc_opt=paths=source_relative `
  api/opcda/v1/opcda_access.proto
```

The repository helper [`scripts/generate-proto.sh`](../scripts/generate-proto.sh)
fails unless those exact generator versions are present. Generated bindings do
not change the source contract; the `.proto` file remains authoritative.
