# OPC DA Access Adapter

[![CI](https://github.com/jiind/opcda-access-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/jiind/opcda-access-adapter/actions/workflows/ci.yml)
[![Real DA validation](https://github.com/jiind/opcda-access-adapter/actions/workflows/real-da-validation.yml/badge.svg)](https://github.com/jiind/opcda-access-adapter/actions/workflows/real-da-validation.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A Windows adapter that gives modern clients access to one local OPC DA server
through [HTTP/JSON](docs/frontend/http/README.md),
[typed gRPC](docs/frontend/grpc/README.md),
[OPC UA](docs/frontend/opcua/README.md), or [MCP stdio](docs/frontend/mcp/README.md).
COM stays on the DA host;
source identifiers, types, Quality, timestamps, and errors retain their meaning.

The adapter supports Browse, device Read, strict typed Write, item properties,
and DA-native subscriptions. Each process selects one source and one frontend.
HTTP is the default, MCP runs only when selected, and Write is disabled by default.

This is a pre-1.0, source-only project for controlled local Windows deployments.
There is no stable binary release or broad vendor compatibility claim. OPC UA
supports `SecurityPolicy None` only and is not production ready. Review the
[executed compatibility evidence](docs/validation/compatibility.md) before deployment.

## What it does

```text
HTTP / gRPC / OPC UA / MCP client
                |
       one selected frontend
                |
    dedicated COM-owning thread
                | local COM
                v
        one OPC DA server
```

The adapter runs on the DA host and preserves exact ItemIDs, VARTYPEs, raw
Quality, source timestamp presence, HRESULTs, and per-item errors. It does not
rename tags, scale values, store process values, or return last-good data after
a disconnect. See the [architecture](docs/architecture.md) for the runtime boundaries.

## Quick start

You need Windows, an installed local OPC DA server, Git, and the Go version
declared in [`go.mod`](go.mod). The adapter architecture must match the
server's COM registration.

![PowerShell walkthrough of guided setup selecting a local OPC DA server, starting the HTTP frontend, and confirming connected status](docs/assets/setup-demo.gif)

This 12-second demo is a sanitized replay of the executed `windows/386`
Graybox setup recorded in the [compatibility evidence](docs/validation/compatibility.md).
The demo uses a foreground loopback listener with Write disabled. The same
steps are available below. [Open the animation](docs/assets/setup-demo.gif).

### 1. Build

```powershell
git clone https://github.com/jiind/opcda-access-adapter.git
Set-Location opcda-access-adapter
go build -trimpath -o opcda-access-adapter.exe ./cmd/adapter
```

For a 32-bit-only registration, build the x86 executable instead:

```powershell
$env:GOARCH = "386"
go build -trimpath -o opcda-access-adapter.exe ./cmd/adapter
Remove-Item Env:GOARCH
```

A 64-bit executable cannot see a 32-bit-only COM registration. If the server
bitness is unknown, run `detect` with both builds.

### 2. Inspect local registrations

```powershell
.\opcda-access-adapter.exe detect
```

Detection returns a bounded JSON inventory of local OPC DA 2.0 registrations.
It does not activate a vendor server, select one automatically, change
configuration, or search remote machines. An empty list is a successful
result. See [local server detection](docs/operations/local-detection.md).

### 3. Configure and run

```powershell
.\opcda-access-adapter.exe setup
```

The guided flow requires four explicit decisions:

1. choose one detected source;
2. choose HTTP, gRPC, OPC UA, or MCP stdio;
3. choose foreground, Windows Service, or save-only execution;
4. review the exact configuration before it is written or started.

For the shortest first run, choose **HTTP/JSON** and **current terminal**.
HTTP listens on `127.0.0.1:8080` by default and Write remains disabled. Setup
never silently overwrites a file or service and never changes COM/DCOM or
firewall permissions.

### 4. Check status

```powershell
Invoke-RestMethod http://127.0.0.1:8080/v1/status
```

The response should name the selected source and eventually report
`connected`. A registration discovered by `detect` is not proof that the
vendor server can activate under the current Windows identity.

### 5. Read a known ItemID

```powershell
$body = @{
    source = "device"
    items  = @(@{ itemId = "Vendor.Example.Item" })
} | ConvertTo-Json -Depth 4

Invoke-RestMethod `
    -Method Post `
    -Uri http://127.0.0.1:8080/v1/read `
    -ContentType application/json `
    -Body $body
```

Use the exact ItemID accepted by the source. Browse is optional in OPC DA;
known-ItemID Read remains available when the source does not support Browse.

For service installation, saved configurations, OPC UA identity fields, and
the original environment-variable workflow, continue with the
[setup and Windows Service guide](docs/operations/setup.md).

## Compatibility

The recorded DA runs cover connection, Browse, Read, strict typed Write,
Subscribe, and reconnect. Their frontend coverage differs:

| DA source | Tested version | Windows / adapter | Frontends exercised |
|---|---|---|---|
| OPC Foundation OPC Classic Core Components TestServer | Source commit `efe0d1d1` | Server 2025 / `386`, `amd64` | HTTP, gRPC, OPC UA, MCP; automated fixture runs |
| Graybox Gray Simulator | File `1.7.9.701`, product `1.7.0.0` | Build `10.0.26200.9168` / `386` | HTTP, gRPC, read-only OPC UA; one manual installation |

The fixture does not establish vendor compatibility. The Graybox result applies
to one operator-supplied copy whose provenance is not independently established.
Three independent UA clients also run in CI; those tests use a scripted DA
source and do not establish OPC UA certification or real-server compatibility.

See the [compatibility evidence](docs/validation/compatibility.md) for exact
revisions, workflow runs, resource measurements, and untested conditions.

## Documentation

- [Documentation index](docs/README.md)
- [Architecture](docs/architecture.md)
- [Frontends: HTTP, gRPC, OPC UA, MCP](docs/frontend/README.md)
- [Implementation status](docs/implementation-status.md)

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow. Report
vulnerabilities privately through [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE). Dependency licenses are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
