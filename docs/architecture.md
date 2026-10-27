# Current architecture

The adapter runs beside one local OPC DA server on Windows. One explicitly
selected frontend provides client access while retaining source identifiers,
types, errors, and quality.

This page describes the current implementation. The numbered [design baseline](design/baseline.md)
records the original plan; [ADRs](design/adr/README.md) record later decisions.

## Components

```text
HTTP/JSON, gRPC, OPC UA, or MCP stdio
                 |
          selected frontend
                 |
          DA-native Runtime
                 |
      dedicated COM-owning thread
                 |
             local COM
                 |
          one OPC DA server
```

| Code | Responsibility |
|---|---|
| `cmd/adapter` | CLI, setup, foreground execution, Windows Service dispatch |
| `internal/app` | Configuration, frontend selection, process lifecycle |
| `internal/opcda` | COM ownership, DA operations, handles, reconnect, subscriptions |
| `internal/frontend/http` | HTTP validation and DA-native JSON responses |
| `internal/frontend/grpc` | Typed unary RPCs and server-streaming Subscribe |
| `internal/frontend/mcp` | Bounded stdio MCP session and DA tools |
| `internal/frontend/dajson` | Shared HTTP/MCP value and metadata encoding |
| `internal/opcua` | UA transport, sessions, address space, mappings, subscriptions |
| `api/opcda/v1` | Protobuf contract and generated bindings |
| `internal/validation`, `scripts` | Probes, independent client checks, release tooling |

The runtime does not depend on a frontend. Frontends translate requests and
encode results; they do not add routing, tag mapping, scaling, persistence,
or a common industrial model.

## Selection and execution

The source is one explicit ProgID or CLSID. Activation is local only. `detect`
lists bounded local DA 2.0 registrations without activating or selecting a server.

Each process selects HTTP, gRPC, OPC UA, or MCP. HTTP is the default. MCP is
inactive until selected; it then runs stdio without other frontend listeners.
Network frontends support Windows Service mode. An MCP client launches and
owns its stdio process.

Setup writes configuration version 4. Versions 1–3 remain readable within their
frontend scope. File execution does not merge environment settings. See [setup](operations/setup.md).

## COM ownership

A dedicated locked OS thread owns COM initialization, activation, interface
pointers, groups, item handles, and cleanup. Frontend goroutines submit work
through a bounded command queue.

DA 2.x Browse uses a mutable position, so each operation resets and traverses
its path on that thread. Read and Write use registered handles. The bounded
handle cache contains protocol metadata scoped to a connection generation,
not process-value history.

Frontend cancellation does not safely interrupt an in-flight COM call. A call
may complete after the client stops waiting. A permanently blocked call causes
degraded state and fail-fast behavior; recovery requires process restart.

## Data contract

Preserve exact ItemID, actual and canonical VARTYPE, raw 16-bit Quality,
source timestamp presence, HRESULT, and access rights. Batch results retain
request order and per-item failures.

No source timestamp is invented. Disconnect returns an explicit failure rather
than an old last-good value. Process values, including Write values, are not
logged by default.

HTTP and MCP encode 64-bit integers as decimal strings, non-finite floats by
explicit names, and arrays with types, dimensions, lower bounds, and lengths.
gRPC uses typed protobuf fields. UA documents its standard mappings and losses,
including vendor Quality bits and rejection of nonzero array lower bounds.
UA array Writes and ordinary-item array rank metadata remain limited; see the
[array contract](frontend/opcua/data-access.md#arrays).

Write is disabled by default, value-only, and strictly typed. The adapter does
not retry, replay, or promise rollback. A timeout does not prove that the source
did not apply the Write.

## Subscriptions

One DA subscription is one group advised through `IOPCDataCallback`. Callbacks
do not block. The core retains at most one pending value per active item,
coalescing newer values rather than creating a notification history.

| Frontend | Delivery and ownership |
|---|---|
| HTTP | No Subscribe endpoint |
| gRPC | One stream owns one group; transport flow control supplies backpressure |
| OPC UA | One Subscription maps to one group; Publish/retransmission follow the UA contract |
| MCP | A session creates a group, drains its coalesced set, and releases it explicitly or on session end |

Disconnect invalidates subscriptions and discards pending values. Reconnect
advances the generation and restores no subscription. Resubscribe is explicit;
a slow consumer does not terminate a DA group.

## Operational boundary

Network frontends bind to loopback by default. There is no production TLS,
authentication, or RBAC platform. UA serves anonymous `SecurityPolicy None`
only. MCP adds no network listener and uses its host account's COM permissions.

Requests, batches, paths, strings, arrays, connections, concurrency, queues,
and protocol state have explicit bounds. Setup does not change COM/DCOM or
firewall policy. Services depend on the vendor accepting LocalService.

Read [implementation status](implementation-status.md) for remaining work and
[compatibility evidence](validation/compatibility.md) before making deployment claims.

## Scope boundaries

This project is not a general industrial gateway. It does not provide:

- remote DCOM or remote server discovery;
- aggregation of multiple DA servers in one process;
- tag mapping, renaming, scaling, normalization, or a common asset model;
- process-value persistence or historical storage;
- automatic source selection, transparent resubscription, or last-good data;
- a plugin framework or non-DA source protocols;
- production authentication, authorization, TLS, or OPC UA security modes
  other than `None`.

These boundaries are part of the correctness model, not a feature backlog.
