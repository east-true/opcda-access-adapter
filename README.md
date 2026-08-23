# OPC DA Access Adapter

A thin, DA-native HTTP access adapter for one local OPC DA server. It is being
built to expose OPC DA without renaming tags, normalizing values, or storing
process data.

## Current status

The project is in v0 implementation. The bootstrap and dedicated Windows COM
foundation are implemented, including local-only activation and owning-thread
cleanup. DA group, Read, Browse, and Write operations are added in subsequent
phases. It is not production-ready and has not yet been validated against a
real OPC DA server.

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
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## License

Licensed under the existing [Apache License 2.0](LICENSE).
