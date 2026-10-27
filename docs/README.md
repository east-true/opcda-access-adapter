# Documentation

Start with the [quick start](../README.md#quick-start) for a first connection. Read
[architecture](architecture.md) for the runtime boundaries or
[implementation status](implementation-status.md) for completion and release blockers.

| Area | Guides |
|---|---|
| Frontends | [Choose a frontend](frontend/README.md), [HTTP](frontend/http/README.md), [gRPC](frontend/grpc/README.md), [OPC UA](frontend/opcua/README.md), [MCP](frontend/mcp/README.md) |
| Operations | [Setup and Windows Service](operations/setup.md), [CLI](operations/cli.md), [local detection](operations/local-detection.md), [Windows COM security](operations/windows-security.md), [releasing](operations/releasing.md) |
| Validation | [Compatibility evidence](validation/compatibility.md), [real DA](validation/real-da-windows.md), [UA clients](validation/ua-client-interop.md), [benchmarks](validation/benchmarks.md), [local destructive review](validation/local-vm-destructive.md) |
| Design | [Baseline](design/baseline.md), [architecture decisions](design/adr/README.md) |
| History | [Implementation log](history/implementation-log.md) |

Each frontend directory starts with a `README.md` and keeps its operation or
protocol details beside it. Keep commands in operating guides, contracts in
frontend references, and executed results in validation records.

The baseline and accepted decisions define design constraints. The implementation
log preserves dated work; its historical headings are not a current backlog.
