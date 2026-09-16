# Documentation

This directory contains the operating guides, protocol references, design
record, and validation evidence for OPC DA Access Adapter. Pick the path that
matches what you are trying to do; the root README intentionally keeps only
the shortest route into the project.

## Start here

| Goal | Read |
|---|---|
| Build and run the adapter for the first time | [Project quick start](../README.md#quick-start) |
| Select a source, frontend, or Windows Service mode | [Guided setup and Windows Service](setup.md) |
| Understand why a server appears only to one architecture | [Local OPC DA server detection](local-detection.md) |
| Diagnose local COM activation or identity failures | [Windows COM security and permissions](security-windows.md) |
| Check whether an environment has actually been tested | [Compatibility and validation evidence](compatibility.md) |

## Integrate a client

| Interface | Reference | Important boundary |
|---|---|---|
| HTTP/JSON | [HTTP API](http-api.md) | Request/response only; no Subscribe |
| Typed gRPC | [gRPC API](grpc-api.md) and [protobuf contract](../api/opcda/v1/opcda_access.proto) | Server-streaming Subscribe maps one stream to one DA group |
| OPC UA | [OPC DA to OPC UA mapping](opcua-mapping.md) | `SecurityPolicy None` only; local interoperability, not production readiness |

All frontends use the same DA runtime. HTTP and gRPC preserve DA-native ItemID,
VARTYPE, Quality, timestamp-presence, HRESULT, and access-right fields. OPC UA
applies the documented Part 8 mapping and reports its deliberate losses and
refusals explicitly; it must not become an undocumented interpretation layer.

## Operate and secure

| Document | Use it for |
|---|---|
| [Guided setup and Windows Service](setup.md) | Configuration versions, safe defaults, service lifecycle, and LocalService identity |
| [Windows COM security and permissions](security-windows.md) | Activation scope, AppID/RunAs layers, architecture views, and HRESULT-led diagnosis |
| [Security policy](../SECURITY.md) | Supported versions, deployment posture, and private vulnerability reporting |
| [Release procedure](releasing.md) | Dry runs, checksums, attestations, tags, and publication gates |

The repository does not provide a production authentication, authorization,
or TLS platform. Loopback is the default boundary; exposing a listener is an
operator decision that requires an appropriate external security layer.

## Understand the design

| Document | Authority |
|---|---|
| [Design baseline](design.md) | Product definition, invariants, architecture, failure model, and explicit non-goals |
| [Architecture decision records](adr/) | Reversible engineering decisions and the evidence behind them |
| [Implementation status](implementation-status.md) | Current completion state, executed validation, known risks, and next work |

When documents disagree, the design baseline wins over implementation ideas;
an accepted ADR records an intentional change or refinement. The
implementation status is a continuation record, not an alternative design.

## Reproduce or review evidence

| Document | Evidence |
|---|---|
| [Compatibility](compatibility.md) | Exact server/client versions, architectures, workflow runs, observations, and untested conditions |
| [Real OPC DA validation](validation/real-da-windows.md) | Reproducible Windows fixture procedure and covered observations |
| [Third-party UA client interoperability](validation/ua-client-interop.md) | asyncua, open62541, and OPC Foundation .NET client coverage |
| [Benchmarks](validation/benchmarks.md) | How to run measurements and what they do—and do not—establish |
| [Local destructive review](validation/local-vm-destructive.md) | Isolated-VM failure/attack matrix; currently not an executed PASS |

Compatibility claims must come from an authorized, executed result recorded in
`compatibility.md`. A source-built fixture, one vendor installation, or three
UA clients must never be generalized into certification or broad vendor
support.

## Contribute to the documentation

Documentation-only changes are welcome. Keep the short path in the root README
and put protocol detail, operational caveats, and validation evidence here.
Use relative links so they work in clones and on branches. Behavior changes
must update the relevant reference, the implementation status, and an ADR when
they change a decision, default, resource bound, or compatibility claim.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for checks and pull-request workflow.
