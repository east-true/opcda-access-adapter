# DA-native gRPC API

The Phase 6 gRPC frontend is a typed access transport for one explicitly
configured local OPC DA server. Its authoritative schema is
[`api/opcda/v1/opcda_access.proto`](../api/opcda/v1/opcda_access.proto).
The schema and generated Go bindings are included in source and the `.proto`
file is included in release-shaped archives.

## Service

```text
package: opcda.access.v1
service: OPCDAAccess

Status
Browse
Read
Write
```

All four methods are unary. Subscribe and server streaming are intentionally
absent until the DA runtime has a validated callback/subscription core.

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

| Layer | Typical gRPC code | Preserved detail |
|---|---|---|
| frontend/validation | `InvalidArgument`, `ResourceExhausted` | adapter error code and bounded message |
| adapter/runtime | `PermissionDenied`, `Unavailable`, `DeadlineExceeded`, `Unimplemented` | adapter error code |
| DA source method | `Unavailable` | operation and raw HRESULT |

The adapter does not configure automatic retries. In particular, a Write whose
client deadline expires has an unknown source outcome and must not be replayed
automatically.

## Configuration

Guided setup lists HTTP/JSON and gRPC and requires an explicit choice:

```powershell
.\opcda-access-adapter.exe setup --grpc-listen 127.0.0.1:50051
```

Select `gRPC`, then foreground, Windows Service, or save only. New setup files
use strict configuration version 2:

```json
{
  "version": 2,
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

Version 1 HTTP files remain readable. A gRPC configuration contains no HTTP
listener and a single adapter process still owns one runtime and one source.

The original environment workflow can select gRPC explicitly:

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_FRONTEND` | `http` | select `http` or `grpc` |
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
