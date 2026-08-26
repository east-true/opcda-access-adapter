# Third-party OPC UA client interoperability

Every OPC UA test in this project before this one exercised its own encoder
against its own decoder. Those agree with each other by construction, so a
round trip against itself cannot catch a field both sides get wrong the same
way. The duplicated `SecureChannelId` that survived a full unit suite and was
caught only by a real socket was the standing reminder; this suite is the
answer to it.

The client is [asyncua](https://github.com/FreeOpcUa/opcua-asyncio). Design
§5.2 names it among the projects that may be a reference or interop target but
are not adopted as an implementation base, and nothing in the adapter links
against it. It is installed into a throwaway virtual environment by the runner
and is never a build or runtime dependency of the adapter.

## Running it

```bash
scripts/interop/run.sh
```

The runner builds `internal/validation/uainterop`, starts it in three
configurations, and runs the client against each. It exits non-zero if any
check fails.

`INTEROP_WORKDIR` reuses a prepared directory; `INTEROP_PYTHON` uses an
interpreter that already has `asyncua`, so a repeated run needs no network.

## What it runs against

`internal/validation/uainterop` serves the adapter's real UA frontend over a
**scripted DA source**. The source is a stand-in and never touches COM, which
is what lets the suite run on any platform. What it does reproduce faithfully
is the shape of what the DA core hands the UA layer: exact ItemIDs, raw
qualities, canonical VARTYPEs, timestamp presence, per-item HRESULTs, and
access rights reported by `AddItems` rather than by Browse.

That division is deliberate. The DA side is validated against a real OPC DA
2.05a server on Windows, which is the only thing that can judge it; this suite
judges the UA side, where the thing missing was a foreign client's opinion.

Three configurations run:

| Configuration | What it covers |
| --- | --- |
| default | a source implementing every optional DA interface |
| `-browse=false` | a source that does not implement `IOPCBrowseServerAddressSpace`, which DA 2.05a leaves optional |
| `-write-enabled` | the Write path, which is disabled by default everywhere else |

## What the client checks

The client's own decoder makes every assertion.

- **Connection**: Hello/Acknowledge, `OpenSecureChannel`, `GetEndpoints`,
  `CreateSession`, `ActivateSession`, the namespace array, and the anonymous
  user token policy.
- **Address space**: the source folder and the standard `Server` object under
  `Objects`; branches and items; browsed identifiers carrying the exact
  ItemID; a hierarchical browse with `includeSubtypes`, which is how a generic
  client walks an address space.
- **Types**: every VARTYPE the adapter maps, each required to decode as the UA
  built-in type OPC 10000-8 Table A.2 names. `VT_DATE` is the row worth
  watching: the table maps it to `Double`, not `DateTime`.
- **Quality**: raw DA qualities mapped through Table A.3, including
  `LAST_KNOWN` and `OUT_OF_SERVICE` sharing the `Bad_OutOfService` row and
  `LOCAL_OVERRIDE` staying `Good`.
- **Timestamps**: a DA timestamp becoming the `SourceTimestamp`, and an absent
  one staying absent rather than being filled in with the adapter's clock.
- **Access rights**: a write-only item refusing a read with the source's own
  answer, and a read-only item reading.
- **Identity**: ItemIDs with spaces, slashes, mixed case, and non-ASCII
  characters surviving the round trip through a UA `NodeId` unchanged, and an
  identifier naming no item answering `Bad_NodeIdUnknown`.
- **Attributes**: `NodeClass`, `DataType` after a Read has taught it, and
  `ValueRank`.
- **Write**: refused when the adapter disables it; reaching the source and
  reading back when enabled.
- **Subscription**: a subscription and monitored items created, changes
  arriving for both items, and changes still arriving seconds later — which is
  what fails when Publish does not hold its request.
- **Standard Server object**: `ServerStatus` decoding as a structure with its
  `BuildInfo` fields in the NodeSet's order, `State` reading `Running`,
  `CurrentTime` answered as of the read, `ServiceLevel`, and `Auditing`.

## What it found

Four defects that the Go suite could not see, each of which made the adapter
unusable with a conforming client:

1. **The `OpenSecureChannel` reply named no security policy.** OPC 10000-6
   6.7.7 requires the response to name the policy the request named, and the
   asymmetric security header is the only place an OPN chunk carries it. A
   conforming client refuses the channel rather than guess, so **no
   third-party client could connect at all**. This project's decoder accepted
   the empty field its own encoder wrote, which is exactly the blind spot a
   self-round-trip has. The same clause also requires the receiver to verify
   that it supports the requested policy, which was not being checked either.
2. **`Browse` decoded `includeSubtypes` and then ignored it.** A client that
   browses for `HierarchicalReferences` with subtypes included — the normal
   way to walk an address space — saw nothing. This project's own probe
   browsed with an unspecified reference type and never noticed.
3. **`Publish` answered immediately instead of holding the request.** OPC
   10000-4 5.14.5.1 has Publish requests queued in the server. A client issues
   the next Publish as soon as the last response arrives, so an empty response
   returned at once was answered at once: **3,874 exchanges in 40 seconds**,
   measured on the wire, against one notification actually delivered. The load
   starved the sampling the subscription existed to deliver.
4. **The standard `Server` object was missing.** A generic client reads the
   `NamespaceArray` before anything else, and reads `ServerStatus` on a timer
   to decide whether the server is alive. Without those nodes the client
   concluded the server was dead and tore the connection down after the first
   notification.

## What it does not cover

- **The DA side.** The source is scripted. Nothing here is evidence about COM,
  DCOM, a real DA server, or any vendor.
- **Security.** Only `SecurityPolicy None` is served. ADR-0016 forbids
  describing that as production ready, and this suite does not change that.
- **Conformance.** One client is not the OPC Foundation's Compliance Test
  Tool. Passing here means one third-party client interoperates with this
  server on the services exercised above. **No "OPC UA Certified" or "OPC UA
  Compliant" claim is made**, and ADR-0016 forbids one.
- **Other clients.** UA Expert, open62541, and the OPC Foundation .NET stack
  have not been tried.
