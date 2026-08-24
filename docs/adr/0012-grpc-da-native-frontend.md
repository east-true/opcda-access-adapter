# ADR-0012: DA-native gRPC frontend

- Status: Accepted
- Date: 2026-08-24

## Context

The HTTP/JSON v0 and its local-COM runtime have completed their scoped x86 and
x64 validation. The design orders the next access frontend as gRPC before DA
Subscribe and OPC UA. gRPC must not introduce a common industrial model,
source routing, aggregation, persistence, or transport-specific source
semantics.

The existing guided configuration has one `frontend` object and version 1 is
HTTP-only. The first gRPC vertical slice does not need simultaneous frontend
listeners or a plugin framework.

## Decision

- Add `opcda.access.v1.OPCDAAccess` with unary `Status`, `Browse`, `Read`, and
  typed value `Write` RPCs. Subscribe remains absent until the DA callback core
  exists.
- Use OPC DA vocabulary first: DA ItemID, VARTYPE, raw Quality, source
  timestamp presence, HRESULT, access rights, Browse branch/item, and device
  Read. Do not add Asset, Device, Metric, Point, Signal, or telemetry models.
- Preserve the exact request order and per-item partial failures in successful
  Read and Write RPC responses. Method-level source HRESULTs remain distinct
  gRPC errors with a typed `DAOperationError` detail.
- Represent scalar values with a protobuf `oneof` whose field names retain the
  COM scalar width (`i2_value`, `i4_value`, `r4_value`, and so on). The raw
  VARTYPE remains the source of truth. Arrays, by-reference values, and scalar
  types unsupported by the DA core fail explicitly; they are never coerced.
- Encode source time as signed Unix seconds plus nanoseconds and keep a
  separate `timestamp_present` bit. No adapter timestamp is synthesized.
- Make gRPC an explicit alternative to HTTP in configuration and guided setup.
  One adapter process has one selected frontend, one runtime, and one local DA
  source. Simultaneous listeners are deferred until demonstrated demand and
  are not generalized into a registry.
- Generate configuration version 2 with a strict frontend-specific listen
  field. Continue reading version 1 HTTP configuration for safe upgrade, but
  never merge environment variables into file-based execution.
- Default gRPC to plaintext loopback `127.0.0.1:50051`. External bind is an
  explicit deployment decision; the project still supplies no TLS, auth, or
  RBAC platform.
- Bound connections, concurrent RPCs, HTTP/2 streams, request duration,
  metadata, and inbound and outbound message sizes. Reject unsafe aggregate
  receive, send, and metadata products. Do not enable server reflection or
  automatic retries. Bound handshake, idle, maximum-age, age-grace, and client
  keepalive behavior so a small connection set cannot be held forever.
- Pin the official `google.golang.org/grpc` and
  `google.golang.org/protobuf` modules. Record every shipped transitive module,
  license, and binary impact before merge. Generated Go bindings are committed;
  generation tool versions are pinned in the repository script.

## Rejected alternatives

- **OPC UA first:** rejected because the authoritative phase order puts the
  typed access frontend before the larger UA mapping/security/conformance
  surface.
- **Subscribe in this RPC slice:** rejected because DA callbacks must be
  implemented and validated in the runtime before any streaming frontend.
- **HTTP gateway or JSON tunneled through protobuf:** rejected because it
  would discard the typed DA-native contract and duplicate HTTP semantics.
- **Multiple simultaneous frontend registry:** rejected as premature generic
  infrastructure. Explicit source-tree frontend selection is sufficient.
- **Automatic selection or multi-server routing:** rejected because it breaks
  the explicit source choice and one-runtime/one-source invariant.

## Consequences

Clients gain a generated typed API without needing COM or HTTP JSON handling.
The binary and dependency graph grow materially and must be measured. A gRPC
deployment has a new HTTP/2 attack surface and remains loopback-only by
default. Existing version 1 HTTP configuration remains loadable, while new
configuration makes the frontend selection unambiguous.

OPC UA remains Phase 8. No OPC UA implementation or dependency is admitted by
this decision.

The pinned runtime build graph adds `google.golang.org/grpc v1.83.1`,
`google.golang.org/protobuf v1.36.12`, `golang.org/x/net v0.55.0`,
`golang.org/x/text v0.37.0`, and
`google.golang.org/genproto/googleapis/rpc` at
`3dc84a4a5aaa`. The release binary's embedded module metadata contains exactly
those modules plus the existing `golang.org/x/sys`; it does not contain an OPC
SDK. Licenses and the gRPC notice are reproduced in
`THIRD_PARTY_NOTICES.md`.

Compared with the prior stripped Windows binaries recorded by ADR-0011, the
gRPC build grows from 6,839,296 to 12,485,120 bytes on 386 (+5,645,824,
82.55%) and from 7,093,760 to 13,092,352 bytes on amd64 (+5,998,592, 84.56%).
This is accepted for the requested typed frontend but is a material deployment
cost, not described as a zero-cost abstraction.

Bindings were generated with official protoc v36.0, protoc-gen-go v1.36.12,
and protoc-gen-go-grpc v1.6.2. The downloaded official Linux x86-64 protoc ZIP
had SHA-256
`bc8211ce760bd43ee21ddc145d6d9dbaeeabae205267a79d9054a240e367d4b4`;
the tool is not shipped in the adapter or release archive.

## References

- gRPC Go documentation: https://grpc.io/docs/languages/go/
- gRPC-Go release v1.83.1:
  https://github.com/grpc/grpc-go/releases/tag/v1.83.1
- protobuf-Go release v1.36.12:
  https://github.com/protocolbuffers/protobuf-go/releases/tag/v1.36.12
- protoc release v36.0:
  https://github.com/protocolbuffers/protobuf/releases/tag/v36.0
