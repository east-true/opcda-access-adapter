# OPC DA Access Adapter

[![CI](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml)
[![Real DA validation](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

DA-native HTTP/JSON, typed gRPC, and OPC UA access to one local OPC DA
server—without making modern applications speak COM or changing the source
data model.

> [!IMPORTANT]
> This is a Windows-only, pre-1.0 project for controlled local-COM
> deployments. The scoped v0 is implemented and validated against a pinned
> OPC Foundation DA 2.05a test server, but it is not broad vendor
> certification or a production-readiness claim. There is no stable binary
> release yet; build from source and review the documented limits before use.

## Why this project?

OPC DA applications normally need Windows COM knowledge and direct access to
the server. This adapter keeps that legacy boundary on the DA machine and
offers an explicitly selected HTTP/JSON, typed gRPC, or OPC UA frontend for
Browse, device Read, and strictly typed value Write.

```text
HTTP, gRPC, or OPC UA client
    │
    │  DA-native transport contract
    ▼
explicitly selected bounded frontend
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
- bounded local OPC DA 2.0 registration detection without vendor activation
- guided source/frontend selection with reviewed configuration output
- optional SCM-managed Windows Service using the LocalService account
- `GET /v1/status`
- typed unary gRPC Status, Browse, Read, and Write
- gRPC server-streaming `Subscribe`, one stream to one DA group
- an OPC UA frontend over `SecurityPolicy` `None`, for local interoperability
  only and explicitly not production ready
- DA item properties, passed through with the source's own identifiers
- optional, source-backed DA 2.x Browse
- ordered batch device Read with per-item failures
- strict typed value Write, disabled by default and never retried
- dedicated COM-owning OS thread with bounded command serialization
- reconnect with connection-generation handle invalidation
- bounded bodies, batches, Browse results, connections, concurrency, and
  timeouts
- native `windows/386` and `windows/amd64` builds and tests

Remote DCOM, multiple DA servers in one adapter instance, tag mapping,
normalization, persistence, and plugin systems are deliberately out of the
current scope. So is any OPC UA `SecurityMode` other than `None`: what is
implemented is unsigned and unencrypted, and is not a conformance,
certification, or broad client-compatibility claim.

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

## Build

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

The adapter architecture must match the COM registration. A 64-bit build does
not see a 32-bit-only registration, so run the matching build—or both builds
when the server bitness is unknown.

## Quick start

The recommended first run is the guided setup:

```powershell
.\opcda-access-adapter.exe setup
```

It walks through four reviewed decisions:

1. choose one locally registered OPC DA 2.0 server;
2. choose the frontend (`HTTP/JSON`, typed DA-native gRPC, or OPC UA);
3. run in the current terminal, install a Windows Service, or save only; and
4. confirm the exact configuration before anything is written or started.

Even one detected candidate requires an explicit choice. HTTP defaults to
`127.0.0.1:8080`, gRPC to `127.0.0.1:50051`, OPC UA to `127.0.0.1:4840`, and
Write is disabled. The OPC UA frontend additionally requires an endpoint URL,
an application URI, a namespace URI, and the two profile URIs, none of which
are defaulted—see the [setup guide](docs/setup.md#selecting-opc-ua).
Setup never silently overwrites an existing file or service and never changes
COM/DCOM or firewall permissions.

For a Windows Service, use an elevated PowerShell terminal and put the
executable and configuration in stable paths readable by LocalService:

```powershell
$installDir = "C:\Program Files\OPCDAAccessAdapter"
$configDir = "C:\ProgramData\OPCDAAccessAdapter"

New-Item -ItemType Directory -Force -Path $installDir, $configDir | Out-Null
Copy-Item .\opcda-access-adapter.exe "$installDir\opcda-access-adapter.exe"

& "$installDir\opcda-access-adapter.exe" setup `
  --config "$configDir\adapter.json"
```

Select **Windows Service** when prompted. The service runs as
`NT AUTHORITY\LocalService`, starts automatically with Windows, and does not
store a password. A vendor that works for an interactive user can still deny
LocalService through its AppID/RunAs/DCOM policy; the adapter reports the
failure but does not weaken those permissions. See the
[setup and service guide](docs/setup.md).

Verify the running adapter:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/v1/status
```

For a gRPC selection, call
`opcda.access.v1.OPCDAAccess/Status` at `127.0.0.1:50051` with a client
generated from [`api/opcda/v1/opcda_access.proto`](api/opcda/v1/opcda_access.proto).

The status must name the selected source, show the listener, and eventually
report `connected`. Do not treat a detected registration alone as proof that
the vendor server can activate or that its permissions are sufficient.

### Manual foreground startup

For the original environment-variable workflow, configure exactly one source
identifier and start the adapter:

```powershell
$env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
# Alternatively: $env:OPCDA_SOURCE_CLSID = "{00000000-0000-0000-0000-000000000000}"

.\opcda-access-adapter.exe
```

Run `opcda-access-adapter-386.exe` instead when you built the x86 variant.

File-based foreground startup is also available after setup's **save only**
option:

```powershell
.\opcda-access-adapter.exe run --config .\opcda-access-adapter.json
```

File-based execution is strict and does not merge environment variables into
the reviewed configuration.

## Command reference

| Command | Purpose |
|---|---|
| `opcda-access-adapter setup` | Detect, explicitly select, review, save, and optionally start one adapter |
| `opcda-access-adapter detect` | List bounded local OPC DA 2.0 registrations without activating them |
| `opcda-access-adapter run --config FILE` | Run the reviewed configuration in the current terminal |
| `opcda-access-adapter service install --config FILE` | Install and start an SCM-managed LocalService instance |
| `opcda-access-adapter service uninstall` | Stop and remove the configured Windows Service |
| `opcda-access-adapter` | Run the original environment-variable workflow in the foreground |
| `opcda-access-adapter --version` | Print embedded version and source revision metadata |

Use `--help` on the command or subcommand for bounded detection, HTTP, gRPC
and OPC UA listener, configuration-path, Write, and service-name options.

### Local registration detection

To list locally registered OPC DA 2.0 candidates before choosing a source:

```powershell
.\opcda-access-adapter.exe detect
```

Detection returns bounded JSON containing the exact registered CLSID and the
ProgID when Windows can resolve it. It does not start a detected vendor server,
select a source automatically, alter configuration, or search remote hosts.
An empty list is a successful result. Run the matching 386 and amd64 builds
when registrations may exist in both Windows architecture views.
See [local server detection](docs/local-detection.md) for the output contract,
bounds, and limitations.

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
| `POST` | `/v1/properties/available` | DA item properties this source offers for an item |
| `POST` | `/v1/properties` | Those properties' values, with per-property HRESULTs |

See the [HTTP API reference](docs/http-api.md) for request/response contracts,
lossless value encodings, error layers, limits, and all configuration
variables.

## gRPC API

The typed service is `opcda.access.v1.OPCDAAccess`:

| RPC | Purpose |
|---|---|
| `Status` | Runtime, exact source, generation, capability, and listener status |
| `Browse` | Serialized DA Browse with branch/item distinction and exact ItemIDs |
| `Read` | Ordered batch device Read with raw DA metadata and partial failures |
| `Write` | Strict VARTYPE-matched value Write when explicitly enabled |
| `Subscribe` | Server-streaming change delivery; one stream is one DA group |
| `AvailableItemProperties` | `IOPCItemProperties::QueryAvailableProperties`, passed through |
| `ItemProperties` | `::GetItemProperties`, with the source's own HRESULTs |

The authoritative protobuf is
[`api/opcda/v1/opcda_access.proto`](api/opcda/v1/opcda_access.proto). See the
[gRPC API reference](docs/grpc-api.md) for scalar `oneof` mappings, typed error
details, limits, client generation, and plaintext-loopback security boundary.
HTTP exposes no Subscribe.

## OPC UA

The adapter can serve OPC UA instead of HTTP or gRPC, publishing the DA source
as an address space per OPC 10000-8 Annex A. Browse, Read, Write, and
Subscriptions with MonitoredItems are answered by the same DA runtime the other
frontends use.

Only `SecurityMode` `None` is implemented—no signing, no encryption, anonymous
users. It is for local interoperability, and it is **not production ready**.
Three third-party clients (asyncua, open62541, and the OPC Foundation .NET
stack) run against it in CI, which is not conformance and not a claim that any
particular client or deployment will work. See the
[OPC UA mapping](docs/opcua-mapping.md) for the address space, the type and
quality mappings, and every deliberate departure from Annex A.

## Safety defaults

- HTTP binds to loopback unless an external address is explicitly configured.
- gRPC is plaintext and binds to loopback unless an external address is
  explicitly configured; the project currently has no TLS/authentication
  platform.
- OPC UA runs unsigned and unencrypted under `SecurityPolicy` `None` with
  anonymous users, binds to loopback by default, and is not production ready.
- Loopback mode rejects non-loopback Host values; POST requires JSON and
  rejects direct browser Origin requests.
- Request paths, JSON field spelling, nesting, content encoding, and runtime
  result identity are validated strictly and fail closed.
- Write returns `403 WRITE_DISABLED` unless it is explicitly enabled with
  setup's `--enable-write` option or `OPCDA_WRITE_ENABLED=true` in the original
  environment-variable workflow.
- The adapter currently has no authentication, authorization, or TLS layer.
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

The same fixture also passes the complete guided setup and Windows Service
lifecycle on both architectures: explicit selection, exact-CLSID configuration,
LocalService startup, service-mode device Read, bounded Application Event Log
records, uninstall, and event-source cleanup.

The Phase 6 run additionally passes the selected gRPC frontend on both
architectures: Status, root/nested Browse, ordered partial Read, disabled and
strict typed Write, source-denied Write, LocalService execution, and loopback
listener checks. These are fixture-specific results, not a claim that every DA
server or external gRPC deployment is compatible.

Three third-party OPC UA clients—asyncua, open62541, and the OPC Foundation
.NET stack—run 425 checks against the UA frontend in CI. Three clients agreeing
is not conformance and not certification, and no claim is made that any other
client or deployment will work. What it establishes is narrower and worth
having: several defects were found only because a second and third client
disagreed with a server the first had already passed.

These results apply only to the exact recorded fixture and environment. See
the [compatibility matrix](docs/compatibility.md) for immutable workflow runs,
source pins, observed DA metadata, resource deltas, and untested conditions.

## Documentation

| Document | Contents |
|---|---|
| [Setup and Windows Service](docs/setup.md) | Guided selection, strict configuration, service lifecycle, and identity caveats |
| [Local server detection](docs/local-detection.md) | Registration inventory contract, bounds, architecture views, and limitations |
| [Design baseline](docs/design.md) | Product boundary, invariants, architecture, and v0 definition |
| [HTTP API](docs/http-api.md) | Endpoints, JSON contracts, configuration, limits, and errors |
| [gRPC API](docs/grpc-api.md) | Protobuf service, DA scalar mappings, typed errors, bounds, and client generation |
| [OPC UA mapping](docs/opcua-mapping.md) | Address space, Annex A mappings, departures, configuration, and UA service coverage |
| [Compatibility](docs/compatibility.md) | Executed server results and honest compatibility scope |
| [Windows validation](docs/validation/real-da-windows.md) | Reproducible real-DA VM procedure |
| [UA client interoperability](docs/validation/ua-client-interop.md) | What the three third-party UA clients check, and the recorded deviations |
| [Windows COM security](docs/security-windows.md) | Local activation, identity, ACL, and HRESULT guidance |
| [Local destructive review](docs/validation/local-vm-destructive.md) | Isolated VM attack/failure matrix and evidence gate |
| [Release procedure](docs/releasing.md) | Dry runs, publication gates, checksums, and attestations |
| [Implementation status](docs/implementation-status.md) | Completed phases, validation results, risks, and next work |
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
