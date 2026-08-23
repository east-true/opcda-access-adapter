# OPC DA Access Adapter

A thin, DA-native HTTP access adapter for one local OPC DA server. It is being
built to expose OPC DA without renaming tags, normalizing values, or storing
process data.

## Current status

The v0 implementation and its scoped validation are complete. The dedicated
Windows COM foundation,
internal DA group, lazy AddItems cache, device `IOPCSyncIO::Read`, and HTTP
`POST /v1/read` are implemented. Optional DA 2.x Browse and
`POST /v1/browse` are also implemented without an inferred hierarchy. Strict
typed value `IOPCSyncIO::Write` and `POST /v1/write` are implemented with Write
disabled by default and no retry/replay. Bounded reconnect, generation-based
handle invalidation, disconnected fail-fast behavior, and a non-destructive
COM-call watchdog are implemented. An isolated Windows run passed against the
source-built OPC Foundation DA 2.05a test server on both x86/386 and
x64/amd64. This is one honest interoperability result, not broad vendor
certification or a production-readiness claim. See the compatibility matrix
for the exact server commit, adapter commit, observations, and run evidence.

## Scope

- Windows only (`windows/386` and `windows/amd64` builds)
- Local COM only; no remote DCOM or tunneling
- OPC DA 2.05a baseline
- HTTP/JSON first: status, Browse, Read, and typed value Write
- Write disabled by default

This repository does not implement OPC UA, gRPC, Subscribe, persistence, tag
mapping, scaling, normalization, an asset model, or multi-server aggregation.

## Run

The listener defaults to `127.0.0.1:8080`; it never binds externally unless
configured explicitly.

```powershell
$env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
go run ./cmd/adapter
Invoke-RestMethod http://127.0.0.1:8080/v1/status
```

Use exactly one of `OPCDA_SOURCE_PROG_ID` or `OPCDA_SOURCE_CLSID`. Other
configuration is documented in the implementation status and ADR records.

## Documentation

- [Design baseline](docs/design.md)
- [Implementation status](docs/implementation-status.md)
- [Compatibility matrix and test procedure](docs/compatibility.md)
- [v0 HTTP API](docs/http-api.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## License

Licensed under the existing [Apache License 2.0](LICENSE).
