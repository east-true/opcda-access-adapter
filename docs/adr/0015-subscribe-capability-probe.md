# ADR-0015: probe the Subscribe capability instead of assuming it

- Status: Accepted
- Date: 2026-08-25

## Context

ADR-0013 added the DA Subscribe core and ADR-0014 exposed it over gRPC. Both
were validated against the OPC Foundation DA 2.05a fixture, which implements the
`IOPCDataCallback` connection point.

A third-party DA 2.0 server need not. The specification's asynchronous surface
is not universally implemented, and a synchronous-only server that supports
`IOPCItemMgt` and `IOPCSyncIO` is a perfectly legitimate source for Browse,
Read, and Write.

The runtime reported `Capabilities.Subscribe` as `true` for every connected
source, without ever probing for the connection point. Browse has always been
probed and reports `supported`, `unsupported`, or `unavailable`. Subscribe was
the only capability the adapter asserted without evidence, which contradicts the
project's own honesty invariants: a client reading status would be told the
source supports subscriptions, and would then discover otherwise only when a
subscription attempt failed late with a generic source error.

## Decision

Probe the group's `IOPCDataCallback` connection point during connect, exactly
where the Browse interface is probed, and reuse the same three-state result:

- `supported` — `IConnectionPointContainer` exists and offers the
  `IOPCDataCallback` sink;
- `unsupported` — the object has no `IConnectionPointContainer`
  (`E_NOINTERFACE`), or has one that offers no such sink
  (`CONNECT_E_NOCONNECTION`);
- `unavailable` — the probe failed for another reason.

`Capabilities.Subscribe` is true only for `supported`. A connection-loss HRESULT
raised by the probe is still treated as connection loss, so a disconnect during
connect reconnects rather than being recorded as a permanent capability answer.

The probe only inspects. It queries the interface, finds the connection point,
releases it, and never advises, so it cannot start a subscription as a side
effect of connecting.

`Subscribe` on a source whose capability is `unsupported` fails immediately with
`SUBSCRIBE_UNSUPPORTED` — mapped to gRPC `Unimplemented` — mirroring how Browse
returns `BROWSE_UNSUPPORTED`. Nothing is attempted against the source first.

`capabilities.subscribe` describes the **source**, not the frontend. The HTTP
frontend reports it and still exposes no Subscribe endpoint; that distinction is
documented rather than hidden by suppressing the field.

## Rejected alternatives

- **Keep assuming support and let Subscribe fail:** rejected because it makes
  status lie about the source and turns a knowable capability into a late,
  poorly diagnosed runtime error.
- **Probe lazily on the first Subscribe:** rejected because status must be
  answerable without mutating the source, and because the answer would then
  depend on whether anyone had subscribed yet.
- **Advise and immediately unadvise as the probe:** rejected because it starts a
  real subscription on a server merely because the adapter connected.
- **Widen `Capabilities.Subscribe` to a three-state string like Browse:**
  rejected for now because it changes both the HTTP and gRPC contracts for a
  distinction clients do not act on differently; `unsupported` and `unavailable`
  are already distinguishable through the error returned by `Subscribe`.
- **Treat any probe failure as `unsupported`:** rejected because it would hide a
  transport or source failure behind a capability answer.

## Consequences

- A synchronous-only vendor DA server is now reported honestly and refuses
  Subscribe with a specific code instead of a generic source failure.
- The fixture is unaffected: it exposes the connection point, so its capability
  remains true and its validated behavior is unchanged.
- This is a correctness fix for an unobserved vendor shape. It is not a
  compatibility claim: no third-party server has been tested, and
  `docs/compatibility.md` records what a vendor run must observe.
