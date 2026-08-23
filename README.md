# OPC DA Access Adapter

[![CI](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml)
[![Real DA validation](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A thin, DA-native HTTP access adapter for one local OPC DA server. It exposes
OPC DA to modern applications without renaming tags, normalizing values, or
storing process data.

This is a Windows-only, pre-1.0 project. It is intended for controlled
environments where the adapter and the DA server run on the same machine.

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

## Quick start

The listener defaults to `127.0.0.1:8080`; it never binds externally unless
configured explicitly.

```powershell
# Build on Windows (Go 1.26 or newer)
go build -trimpath -o adapter.exe ./cmd/adapter

$env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
.\adapter.exe
Invoke-RestMethod http://127.0.0.1:8080/v1/status
```

Use exactly one of `OPCDA_SOURCE_PROG_ID` or `OPCDA_SOURCE_CLSID`. Other
configuration, bounds, and explicit Write enablement are documented in the
[HTTP API guide](docs/http-api.md).

The adapter does not include an OPC DA server. A real server and its authorized
test configuration are required for interoperability testing.

## Development

The repository is developed and tested on Linux plus Windows CI runners. The
production COM path is Windows-only; Linux runs cover transport, validation,
and platform-independent logic.

```text
gofmt -w .
go test ./...
go vet ./...
GOOS=windows GOARCH=386 go build ./cmd/adapter
GOOS=windows GOARCH=amd64 go build ./cmd/adapter
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Changes
to scope, source semantics, limits, or runtime policy require design review and
an ADR.

## Documentation

- [Design baseline](docs/design.md)
- [Implementation status](docs/implementation-status.md)
- [Compatibility matrix and test procedure](docs/compatibility.md)
- [v0 HTTP API](docs/http-api.md)
- [Windows validation procedure](docs/validation/real-da-windows.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## Project status

The scoped v0 implementation and the official OPC Foundation DA 2.05a fixture
validation are complete. The compatibility result is limited to the exact
fixture and environments documented in [docs/compatibility.md](docs/compatibility.md);
it is not a certification of all OPC DA servers or a production-readiness
claim. Third-party compatibility results are added only after an authorized,
repeatable test.

## License

Licensed under the existing [Apache License 2.0](LICENSE).
