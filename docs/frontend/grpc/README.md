# DA-native gRPC API

The gRPC frontend is a typed access transport for one explicitly
configured local OPC DA server. Its authoritative schema is
[`api/opcda/v1/opcda_access.proto`](../../../api/opcda/v1/opcda_access.proto).
The schema and generated Go bindings are included in source and the `.proto`
file is included in release-shaped archives.

## Reference

- [Subscribe](subscribe.md): DA group ownership, sampling, and stream lifetime.
- [Values](values.md): scalar and array representation.
- [Errors](errors.md): ordered partial results and gRPC status mapping.
- [Item properties](item-properties.md): discovery and property queries.
- [Configuration](configuration.md): setup, listener, and transport bounds.

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

The repository helper [`scripts/generate-proto.sh`](../../../scripts/generate-proto.sh)
fails unless those exact generator versions are present. Generated bindings do
not change the source contract; the `.proto` file remains authoritative.
