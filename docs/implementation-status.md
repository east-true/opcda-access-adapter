# Implementation status

The scoped implementation is complete, including HTTP, gRPC, OPC UA, and MCP.
There is no public release yet. The release blocker is the unexecuted local
Windows destructive review; broad vendor compatibility is also unproven.

This page records current completion and next work. Earlier phase notes and
dated CI runs are preserved in [implementation history](history/implementation-log.md).

## Completed scope

| Area | Current state | Reference |
|---|---|---|
| DA runtime, phases 0–5 | Local COM, Browse, device Read, typed Write, reconnect, limits, and cleanup | [Architecture](architecture.md) |
| HTTP/JSON v0 | Status, Browse, Read, Write, and item properties | [HTTP API](frontend/http/README.md) |
| gRPC, phases 6–7 | Typed unary operations and server-streaming Subscribe | [gRPC API](frontend/grpc/README.md) |
| DA Subscribe core, phase 7 | One DA group per subscription, non-blocking callbacks, coalescing, explicit invalidation | [ADR-0013](design/adr/0013-da-native-subscribe-core.md) |
| OPC UA, phase 8 | Browse, Read, Write, properties, Subscriptions, and MonitoredItems; `SecurityPolicy None` | [UA overview](frontend/opcua/README.md) |
| MCP stdio, PR #227 | Nine DA tools, explicit selection, bounded stdio, group ownership, and EOF cleanup | [MCP API](frontend/mcp/README.md) |
| Setup | Registration-only detection, explicit configuration, foreground execution, LocalService for network frontends | [Setup](operations/setup.md) |
| Item properties | DA queries and UA Table A.1 mapping | [ADR-0018](design/adr/0018-da-item-properties.md) |
| SAFEARRAY | Shape-preserving Read/Write and frontend encodings; no real-source validation | [ADR-0019](design/adr/0019-safearray-representation.md) |
| Distribution tooling | Windows archives, checksums, version metadata, attestations; no published release | [Release procedure](operations/releasing.md) |

HTTP remains the default. MCP is inactive until selected explicitly. Write is
disabled by default. Each process serves one local DA source through one frontend.

## Validation achieved

| Evidence | What it establishes | Limit |
|---|---|---|
| OPC Foundation DA 2.05a fixture, 386 and amd64 | Scoped COM operations, failures, reconnect, subscriptions, frontends, and soak | One source-built fixture |
| Graybox Gray Simulator 1.7.9.701, one x86 installation | Manual DA core, HTTP, gRPC unary/streaming, and read-only UA observations | One operator-supplied copy; provenance and other versions unproven |
| asyncua, open62541, OPC Foundation .NET | UA client behavior over a scripted DA source; all three gated in CI | No DA-side or conformance proof |
| MCP stdio, PR #227 | Independent Python client against the real fixture on both architectures | Recorded scenarios, not every host or vendor |
| Windows unit and ABI tests | Both architectures, memory layouts, VARIANT/BSTR/SAFEARRAY handling | Unit coverage is not source interoperability |

[Compatibility evidence](validation/compatibility.md) records exact versions, commits,
run links, resource deltas, and scenario limits. It remains the authority for
executed compatibility claims.

## Release blocker and next work

1. Recreate the isolated local Windows environment when capacity is available.
   The prior VM was removed on 2026-08-24 before the scenario matrix ran.
2. Complete the [destructive review](validation/local-vm-destructive.md): COM
   launch/access/RunAs failures, load, resource bounds, reboot, both architectures,
   and cleanup. GitHub-hosted results do not satisfy this gate.
3. Record evidence for the revision proposed for release, then complete the
   [release checklist](operations/releasing.md#publishing).

Further compatibility work includes real DA SAFEARRAY values, more authorized
vendor installations, sources without optional DA interfaces, and vendor-specific
disconnect HRESULTs. UA Expert remains untested. Signed/encrypted UA policies,
new source protocols, storage, and a plugin framework are outside the current
scope, not unfinished implementation phases.

## Operational limits

- A permanently blocked COM call cannot be recovered by killing its owning
  thread. The runtime reports degraded state and requires process restart.
- A vendor-specific HRESULT is not guessed to mean disconnect. Add one only
  after observing it and updating the reconnect decision.
- Service access depends on the source accepting LocalService and scoped COM
  permissions. Setup does not grant permissions or run the service as LocalSystem.
- UA is for local interoperability only. Its standard departures and information
  losses are listed in the [mapping reference](frontend/opcua/data-access.md).

## Development checks

The PR gate has ten jobs: `quality`, `spec-check`, `interop`, `release-package`,
plus `windows-build`, `windows-test`, and `OPC Foundation DA 2.05a` on each of
386 and amd64. Check a candidate with `gh pr checks`; older counts in the history
apply to their recorded revisions.

The [decision index](design/adr/README.md) lists the accepted design decisions.
Documentation uses frontend, operations, validation, and design directories.
Each frontend has an overview and can add operation-specific pages.
The documentation cleanup is tracked in [#228](https://github.com/jiind/opcda-access-adapter/issues/228).
