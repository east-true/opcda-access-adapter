# Third-party OPC UA client interoperability

Every OPC UA test in this project before this suite exercised its own encoder
against its own decoder. Those agree with each other by construction, so a
round trip against itself cannot catch a field both sides get wrong the same
way. The duplicated `SecureChannelId` that survived a full unit suite and was
caught only by a real socket was the standing reminder; this suite is the
answer to it.

Three independent clients judge the server, because they disagree with it in
different ways when it is wrong:

| Client | What it is | Why it is here |
| --- | --- | --- |
| [asyncua](https://github.com/FreeOpcUa/opcua-asyncio) | Python, hand-rolled codec | A widely used client with an implementation that shares nothing with ours |
| [open62541](https://github.com/open62541/open62541) | C, a reference implementation | Named in design §5.2 as an interop target |
| [OPC Foundation .NET stack](https://github.com/OPCFoundation/UA-.NETStandard) | The Foundation's own | Where it and this adapter disagree, look at the adapter first |

Each found something the others did not. Design §5.2 names asyncua and
open62541 among the projects that may be a reference or interop target but are
not adopted as an implementation base; **nothing in the adapter links against
any of them**, and none is a build or runtime dependency. The runner installs
them into throwaway directories.

asyncua is always run. open62541 and the .NET stack run when their toolchains
are present and are skipped with a notice when they are not, so the suite stays
runnable on a machine with only Python.

## Running it

```bash
scripts/interop/run.sh
```

The runner builds `internal/validation/uainterop`, starts it in three
configurations, and runs the client against each. It also runs the Windows
real-DA probe (`internal/validation/opcuaprobe`) against the same frontend, so
a change to the UA wire format that would break the Windows validation run is
caught here rather than in CI. It exits non-zero if any check fails.

Environment overrides, all optional, so a repeated run needs no network:

| Variable | Purpose |
| --- | --- |
| `INTEROP_WORKDIR` | reuse a prepared directory |
| `INTEROP_PYTHON` | an interpreter that already has `asyncua` |
| `INTEROP_O62_ROOT` | a built open62541 checkout, from which the client is compiled |
| `INTEROP_O62_CLIENT` | an already-built open62541 client binary |
| `INTEROP_DOTNET` | a `dotnet` binary |
| `INTEROP_NETSTACK_PROJECT` | a prepared .NET project directory |

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

Six defects that the Go suite could not see, each of which made the adapter
unusable with some conforming client. The first four came from asyncua; the
last two from the second and third clients, against a server the first already
passed — which is the argument for having more than one.

1. **The `OpenSecureChannel` reply named no security policy.** OPC 10000-6
   6.7.7 requires the response to name the policy the request named, and the
   asymmetric security header is the only place an OPN chunk carries it. A
   conforming client refuses the channel rather than guess, so **no
   third-party client could connect at all**. This project's decoder accepted
   the empty field its own encoder wrote, which is exactly the blind spot a
   self-round-trip has. The same clause also requires the receiver to verify
   that it supports the requested policy, which was not being checked either.

   This project's own real-DA probe had the matching defect: it sent an empty
   policy too. Adding the server-side check the clause requires failed the
   probe immediately, which is the right outcome — the probe now names its
   policy and asserts that the reply echoes it, so the Windows validation run
   covers both halves of the clause against a real DA source.

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
5. **Unspecified endpoint strings were written as empty rather than null.**
   Found by the OPC Foundation's own .NET stack, which refused the endpoint
   with `Bad_IdentityTokenInvalid`. A null String and a zero-length String are
   distinct values in the UA binary encoding, and Table 192 says
   `issuedTokenType` "may only be specified if TokenType is ISSUEDTOKEN" — so
   writing an empty one on an `ANONYMOUS` policy specifies a field the clause
   forbids. asyncua and open62541 both tolerated it; the Foundation's stack did
   not, and it is right. This project's own decoder reads null and empty alike,
   which is why nothing here noticed.
6. **`CreateSession` refused a client that sends no nonce.** Found by
   open62541, which could not connect at all. See the note below — this one is
   a deliberate deviation rather than a plain defect.

## One deliberate deviation: an absent session nonce

OPC 10000-4 5.7.2 says the `clientNonce` "shall have a length between 32 and
128 bytes inclusive. The Server shall check the length", and Table 16 repeats
it as the condition for `Bad_NonceInvalid`. Neither statement is conditioned on
the security mode.

open62541 sends **no nonce at all** when the channel is unsecured — deliberately,
by a comment in its own source. So a literal reading of the clause makes this
server unusable with a reference implementation. The Foundation's .NET stack,
by contrast, sends a full nonce even under `None`.

The adapter accepts an absent nonce **only when the SecurityMode is None**. The
clause's own stated purpose for the field is to "prove possession of its
ApplicationInstanceCertificate in the response", and the same clause says a
server shall ignore that certificate when the `securityPolicyUri` is `None`.
Under `None` nothing is signed, so there is nothing for the nonce to take part
in and accepting its absence costs no security. A nonce that *is* present is
still checked, and under any other security mode the rule is enforced exactly
as written — which is where the nonce does real work.

This is the one place the adapter knowingly departs from a literal reading, and
it is recorded here so it can be reversed by a decision rather than discovered
by accident.

## UA Expert

**Not tested.** Unified Automation distributes UA Expert only to registered
users — its download page states "You need to log in to download" — so it could
not be obtained without creating an account. It remains untested, and no claim
is made about it either way.

## What it does not cover

- **The DA side.** The source is scripted. Nothing here is evidence about COM,
  DCOM, a real DA server, or any vendor.
- **Security.** Only `SecurityPolicy None` is served. ADR-0016 forbids
  describing that as production ready, and this suite does not change that.
- **Conformance.** Three clients are not the OPC Foundation's Compliance Test
  Tool. Passing here means three third-party clients interoperate with this
  server on the services exercised above — including the Foundation's own
  stack, which is the strongest of the three signals but still not a
  certification. **No "OPC UA Certified" or "OPC UA Compliant" claim is made**,
  and ADR-0016 forbids one.
- **Other clients.** UA Expert has not been tried, for the reason above.
