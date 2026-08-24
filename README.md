# OPC DA Access Adapter

[![CI](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml)
[![Real DA validation](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

DA-native HTTP/JSON access to one local OPC DA server—without making modern
applications speak COM or changing the source data model.

> [!IMPORTANT]
> This is a Windows-only, pre-1.0 project for controlled local-COM
> deployments. The scoped v0 is implemented and validated against a pinned
> OPC Foundation DA 2.05a test server, but it is not broad vendor
> certification or a production-readiness claim.

## Why this project?

OPC DA applications normally need Windows COM knowledge and direct access to
the server. This adapter keeps that legacy boundary on the DA machine and
offers a small HTTP API for Browse, device Read, and strictly typed value
Write.

```text
HTTP client
    │
    │  DA-native JSON
    ▼
bounded HTTP frontend
    │
    ▼
dedicated locked DA thread
    │
    │  local COM
    ▼
one OPC DA server
```

The adapter preserves exact ItemIDs, canonical and actual VARTYPEs, raw
Quality, source timestamp presence, HRESULTs, and access rights. It does not
rename tags, scale values, infer timestamps, return last-good data, or store
process values.

## Features

- OPC DA 2.05a baseline over local COM
- `GET /v1/status`
- optional, source-backed DA 2.x Browse
- ordered batch device Read with per-item failures
- strict typed value Write, disabled by default and never retried
- dedicated COM-owning OS thread with bounded command serialization
- reconnect with connection-generation handle invalidation
- bounded bodies, batches, Browse results, connections, concurrency, and
  timeouts
- native `windows/386` and `windows/amd64` builds and tests

OPC UA, gRPC, Subscribe, remote DCOM, multiple DA servers, tag mapping,
normalization, persistence, and plugin systems are deliberately out of scope.

## Requirements

- Windows with an installed local OPC DA server
- adapter architecture matching the server's COM registration (`386` or
  `amd64`)
- Go version declared in [`go.mod`](go.mod) when building from source

The repository does not bundle an OPC DA server or a prebuilt vendor runtime.
There is no stable binary release yet.

Release-shaped builds expose their exact source revision:

```powershell
.\opcda-access-adapter.exe --version
```

## Quick start

Clone and build on the Windows machine that hosts the DA server:

```powershell
git clone https://github.com/east-true/opcda-access-adapter.git
Set-Location opcda-access-adapter
go build -trimpath -o opcda-access-adapter.exe ./cmd/adapter
```

For a 32-bit-only OPC DA registration, build the x86 executable explicitly:

```powershell
$env:GOARCH = "386"
go build -trimpath -o opcda-access-adapter-386.exe ./cmd/adapter
Remove-Item Env:GOARCH
```

Configure exactly one source identifier and start the adapter:

```powershell
$env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
# Alternatively: $env:OPCDA_SOURCE_CLSID = "{00000000-0000-0000-0000-000000000000}"

.\opcda-access-adapter.exe
```

Run `opcda-access-adapter-386.exe` instead when you built the x86 variant.

The HTTP listener defaults to loopback at `127.0.0.1:8080`:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/v1/status
```

Read known ItemIDs directly from the device:

```powershell
$body = @{
    source = "device"
    items = @(
        @{ itemId = "Random.Int2" }
        @{ itemId = "Random.Real8" }
    )
} | ConvertTo-Json -Depth 4

Invoke-RestMethod `
    -Method Post `
    -Uri http://127.0.0.1:8080/v1/read `
    -ContentType application/json `
    -Body $body
```

ItemIDs must be the exact identifiers accepted by the DA server. Browse is an
optional source capability; known ItemID Read remains available when Browse is
unsupported.

## HTTP API

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/v1/status` | Runtime, source, generation, capability, and listener status |
| `POST` | `/v1/browse` | Serialized source Browse with exact ItemIDs |
| `POST` | `/v1/read` | Ordered batch device Read with per-item DA metadata |
| `POST` | `/v1/write` | Strict typed value Write when explicitly enabled |

See the [HTTP API reference](docs/http-api.md) for request/response contracts,
lossless value encodings, error layers, limits, and all configuration
variables.

## Safety defaults

- HTTP binds to loopback unless an external address is explicitly configured.
- Loopback mode rejects non-loopback Host values; POST requires JSON and
  rejects direct browser Origin requests.
- Write returns `403 WRITE_DISABLED` unless `OPCDA_WRITE_ENABLED=true`.
- The adapter has no authentication, authorization, or TLS layer in v0.
- An in-flight Write is never automatically retried or replayed.
- A disconnected source never returns a cached last-good value.
- Process values, including Write values, are not logged or persisted.

Treat an external bind as a deployment security decision and place the adapter
behind an appropriate network and authorization boundary. Report suspected
vulnerabilities through the private process in [SECURITY.md](SECURITY.md).

## Compatibility and validation

The pinned OPC Foundation DA 2.05a fixture passes local-COM Connect, root and
nested Browse, partial Read, typed and denied Write, server outage/reconnect,
and bounded stability tests on Windows Server 2025 for both x86/386 and
x64/amd64. The stability profile includes rapid, concurrent, malformed,
slow-header, overload/backpressure, and repeated source-failure scenarios.

These results apply only to the exact recorded fixture and environment. See
the [compatibility matrix](docs/compatibility.md) for immutable workflow runs,
source pins, observed DA metadata, resource deltas, and untested conditions.

## Documentation

| Document | Contents |
|---|---|
| [Design baseline](docs/design.md) | Product boundary, invariants, architecture, and v0 definition |
| [HTTP API](docs/http-api.md) | Endpoints, JSON contracts, configuration, limits, and errors |
| [Compatibility](docs/compatibility.md) | Executed server results and honest compatibility scope |
| [Windows validation](docs/validation/real-da-windows.md) | Reproducible real-DA VM procedure |
| [Windows COM security](docs/security-windows.md) | Local activation, identity, ACL, and HRESULT guidance |
| [Local destructive review](docs/validation/local-vm-destructive.md) | Isolated VM attack/failure matrix and evidence gate |
| [Release procedure](docs/releasing.md) | Dry runs, publication gates, checksums, and attestations |
| [Implementation status](docs/implementation-status.md) | Current SHA, completed phases, risks, and next work |
| [ADRs](docs/adr/) | Runtime, type, Write, reconnect, bounds, and fixture decisions |

## Getting help

Start with the API reference and existing issues. For a reproducible defect,
open a [bug report](https://github.com/east-true/opcda-access-adapter/issues/new?template=bug_report.yml);
for an in-scope behavior proposal, use the
[feature request](https://github.com/east-true/opcda-access-adapter/issues/new?template=feature_request.yml).
This project does not currently provide a dedicated support forum or paid
support channel.

Security vulnerabilities must not be reported publicly. Follow
[SECURITY.md](SECURITY.md) to open a private advisory.

## Contributing

Issues and pull requests are welcome when they stay within the OPC DA access
boundary. Start with [CONTRIBUTING.md](CONTRIBUTING.md) and the authoritative
[design baseline](docs/design.md). `main` is protected and changes require the
documented Linux and native Windows checks.

Please use public issues only for actionable bugs and in-scope proposals. Do
not include process values, credentials, or proprietary server data.

## License

Licensed under the [Apache License 2.0](LICENSE).
