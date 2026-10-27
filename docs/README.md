# Documentation

Choose a guide by task. Current behavior, original design decisions, and dated
execution evidence have separate entry points.

## Project overview

Read [architecture](architecture.md) for the current runtime and component
boundaries, then [implementation status](implementation-status.md) for completed
work, validation limits, and release blockers.

The adapter runs on Windows, serves one local OPC DA server, and selects one
frontend per process. HTTP is the default. Write is disabled by default; MCP
starts only when explicitly selected. UA supports `SecurityPolicy None` only
and is not production ready.

## Installation and operation

| Task | Guide |
|---|---|
| Build and read a known item | [Quick start](../README.md#quick-start) |
| Select a source/frontend or install a service | [Setup and Windows Service](setup.md) |
| Find local registrations and select the architecture | [Local detection](local-detection.md) |
| Diagnose activation and account permissions | [Windows COM security](security-windows.md) |
| Prepare and verify release archives | [Release procedure](releasing.md) |

## Client integration

| Frontend | Contract | Main boundary |
|---|---|---|
| HTTP/JSON | [HTTP API](http-api.md) | Request/response; no Subscribe |
| gRPC | [gRPC API](grpc-api.md), [protobuf schema](../api/opcda/v1/opcda_access.proto) | One stream owns one DA group |
| MCP stdio | [MCP API](mcp-api.md) | Client-launched process; explicit group creation, reads, and release |
| OPC UA | [UA overview](opcua-mapping.md) | Local interoperability with `SecurityPolicy None` |

HTTP, gRPC, and MCP preserve DA-native metadata. UA applies its documented
Part 8 mapping, including information losses and deliberate departures.

## Validation

| Question | Reference |
|---|---|
| What has run, on which server and architecture? | [Compatibility evidence](compatibility.md) |
| How is the source-built DA fixture tested? | [Windows real-DA procedure](validation/real-da-windows.md) |
| Which independent UA clients are tested? | [UA client interoperability](validation/ua-client-interop.md) |
| What do performance measurements establish? | [Benchmarks](validation/benchmarks.md) |
| What remains before a public release? | [Local destructive review](validation/local-vm-destructive.md) |

Fixture passes, one vendor installation, and client tests have different
scopes. None establishes broad vendor support or OPC certification.

## Design and history

The [design baseline](design.md) defines invariants and records the original
implementation plan. [ADRs](adr/README.md) record subsequent decisions. Use them
for design changes; use the current guides for operation and integration.

The [implementation history](history/implementation-log.md) preserves earlier
phase notes and validation runs. Historical headings are not a current backlog.

Follow [CONTRIBUTING.md](../CONTRIBUTING.md) when making changes. Keep contracts
in frontend references, evidence in `compatibility.md`, and current completion
and blockers in `implementation-status.md`.
