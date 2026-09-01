# Compatibility Matrix and Validation Procedure

## Matrix

The matrix records only executed real local-COM results. Unit doubles,
cross-builds, and Windows ABI tests are not interoperability results.

| DA Server | Version | Windows | Server bitness | Adapter arch | Connect | Browse | Read | Write | Subscribe | Reconnect | Notes |
|---|---|---|---|---|---|---|---|---|---|---|---|
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x86 | 386 | PASS | PASS | PASS | PASS | PASS | PASS | DA 2.05a fixture; [HTTP/reconnect evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320), [gRPC evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269529), [Subscribe evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32803232555) |
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x64 | amd64 | PASS | PASS | PASS | PASS | PASS | PASS | DA 2.05a fixture; [HTTP/reconnect evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320), [gRPC evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269529), [Subscribe evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32803232555) |

## Recorded result: OPC Foundation DA 2.05a fixture

- Executed: 2026-08-23 on ephemeral GitHub-hosted Windows Server 2025 VMs.
- Adapter head: `5267aec6e05f98dff5da4721ded6315e5a2ba990`; tested pull-request
  merge tree: `a97fc2d58bdfb4e1470e2f869d3dfbbf6681b61d`.
- Server source: `OPCF-Members/OPC-Classic-CoreComponents` at pinned commit
  `efe0d1d1ea86a8a727bf26a501a261765e836766`.
- Security boundary: source and built outputs both passed Microsoft Defender
  custom scans. The source/build audit and the limits of this evidence are in
  ADR-0006; antivirus and static review do not prove arbitrary code harmless.
- Both architectures connected through local COM, detected Browse, returned
  the exact nested ItemIDs, preserved partial Read ordering, rejected Write by
  default, enforced a strict type mismatch, completed an explicitly enabled
  typed value Write, and preserved a source-denied Write result.
- The recorded Read metadata on both architectures was actual/canonical
  `VT_I4`, raw Quality `192` (`0x00C0`), source timestamp present, successful
  item HRESULT `0x00000000`, and invalid-ItemID HRESULT `0xC0040007`
  (`OPC_E_UNKNOWNITEMID`). The read/write item exposed canonical `VT_R4` and
  access rights raw `3`; the read-only item exposed canonical `VT_BSTR`, access
  rights raw `1`, and `OPC_E_BADRIGHTS` (`0xC0040006`) on Write.
- During a real server stop/unregistration the adapter returned no successful
  stale value, exposed disconnected/reconnecting state, advanced reconnect
  count and connection generation after re-registration, and lazy-registered
  the known ItemID again.
- Each architecture completed 200 bounded device Reads. Observed resource
  deltas were: 386 adapter handles `+8`, private bytes `+1,835,008`, server
  handles `0`, server private bytes `0`; amd64 adapter handles `+8`, private
  bytes `+1,978,368`, server handles `0`, server private bytes `0`.
- Fixture executable SHA-256 for run `32628886186`: x86
  `35B18E2542131907A256929FE1C5A54B204CAB8421AA9F90305E6C8B6583F681`;
  x64 `C715BFA24DE1414D6CC1E8A6B5F61FEE42530EB43A619BC6BD5185A7B0F6DDF7`.

### Phase 7 DA Subscribe core result

PR #31 workflow run
[`32803232555`](https://github.com/east-true/opcda-access-adapter/actions/runs/32803232555)
tested adapter head `a287b215960a251ececa5003480d027cff9f6210` on both native
x86/386 and x64/amd64. No frontend exposes Subscribe, so the probe drove the DA
runtime directly. Each architecture passed:

- one DA group per subscription with all three fixture items activated, and the
  server's requested rate of `250ms` revised to `300ms` and reported unchanged;
- a real `IOPCDataCallback::OnDataChange` snapshot delivered into the Go
  callback vtable for `Test/Int32`, `Test/Float`, and `Test/String`, each entry
  preserving its exact ItemID, VARTYPE, canonical type, access rights, raw
  Quality, timestamp presence, and HRESULT;
- three change-driven notifications: the fixture's `Test` items are static, so
  the probe wrote distinct `VT_R4` values to `Test/Float` through the ordinary
  typed Write path and the source reported every change;
- the coalescing bound, with no drained batch exceeding the subscription's
  active item count;
- 24 subscribe/unsubscribe cycles against a `MaxSubscriptions` limit of 16 with
  no leaked DA group or advise cookie, and no reused identifier;
- invalidation across an induced fixture termination, with pending values
  discarded rather than delivered, nothing restored implicitly by reconnect,
  the previous identifier unknown afterwards, and an explicit resubscribe
  receiving a new generation-scoped identity that delivered again
  (`sub-1-26` generation 1 to `sub-2-1` generation 2 on x64);
- no process value written to any probe output.

This is fixture evidence for the OPC Foundation DA 2.05a test server, not a
broad vendor-compatibility claim. Whether to test a second, third-party server
at all is an open decision recorded in
[ADR-0017](adr/0017-third-party-vendor-da-fixture.md): the licence of the
candidate examined permits it, but the vendor's own distribution no longer
exists and the surviving copy is behind an account, so obtaining one is a
supply-chain choice rather than a technical step. A vendor server that rejects connection
points, revises rates differently, or reports Quality or timestamps
differently has not been tested.

### Phase 8 OPC UA frontend result

PR #51 workflow run
[`32921155696`](https://github.com/east-true/opcda-access-adapter/actions/runs/32921155696)
tested adapter head `cc228a16f1b154b2c1e109f5a52161b7db32f7c6` on both native x86/386 and
x64/amd64. Each architecture passed, against the source-built OPC Foundation DA
2.05a fixture:

- the UA-TCP connection sequence, with 65536 byte buffers negotiated;
- an `OpenSecureChannel` issuing a channel and a token with a 60000 ms revised
  lifetime;
- `GetEndpoints` publishing exactly the configured endpoint URL and security
  policy URI, with security mode None, **no certificate**, and security level 0;
- `CreateSession` and `ActivateSession` with an anonymous identity;
- a Browse walk from Root through Objects to the source folder and down to the
  fixture's three DA items, with the address space filled from DA Browse on
  demand;
- a Read of one item returning `Good` with the source timestamp present;
- exactly one loopback TCP listener, Write disabled, and no process value in any
  log.

The security policy and transport profile URIs used in this run are
placeholders. The known URIs are published in the OPC Foundation profile
database, which OPC 10000-7 clause 1 points to rather than listing, so nothing
pinned here could check them and the run asserts only that the server publishes
exactly what it was configured with. **No third-party OPC UA client was involved**: the probe
uses this project's own codec, so this is evidence that the server is internally
consistent and reaches the DA source, not that a real UA client interoperates
with it. No conformance or interoperability claim is made.

### OPC UA subscription result

PR #53 workflow run
[`32928909002`](https://github.com/east-true/opcda-access-adapter/actions/runs/32928909002)
tested adapter head `e989553efb9d9ab8b7c3e537b54804b314c024d1` on both native x86/386
and x64/amd64. On top of the earlier UA result, each architecture passed:

- a Subscription with a requested 250 ms publishing interval revised to 250 ms
  and a keep-alive count of 3, whose revised lifetime count is at least three
  times the keep-alive count;
- a MonitoredItem with a revised queue size of **1**, matching the DA core's
  per-item coalescing;
- the server's initial snapshot delivered through Publish;
- **two change-driven notifications**, each induced by writing a distinct value
  through the UA Write service and required to arrive carrying the client handle
  the probe registered;
- the subscription deleted cleanly.

Write is enabled for the OPC UA scenario so those changes can be induced; the
fixture's `Test` items are static and would otherwise show only one snapshot.
The write-disabled default stays covered by the HTTP and gRPC scenarios.

### DA error code result

PR #71 workflow run
[`33069731246`](https://github.com/east-true/opcda-access-adapter/actions/runs/33069731246)
ran `internal/validation/daerrorprobe` against the fixture on both native
x86/386 and x64/amd64. **Both architectures produced identical results.**

All thirteen rows of OPC 10000-8 Tables A.4 and A.5 are bound. Three of them come
out of this server. For each observed row the probe fed the
HRESULT the source really returned to the real mapping function and required the
table's answer:

| Row | Result |
|---|---|
| `OPC_E_UNKNOWNITEMID` | **observed** — source answered `0xC0040007` on Read, mapped to `0x80340000` `Bad_NodeIdUnknown` |
| `OPC_E_BADRIGHTS` | **observed** — source answered `0xC0040006` on Write, mapped to `0x803B0000` `Bad_NotWritable` |
| `OPC_E_INVALID_PID` | **observed** — source answered `0xC0040203` for a property identifier the item does not have, mapped to `0x80350000` `Bad_AttributeIdInvalid` |

The third row became observable only when the adapter began reading item
properties. It had been recorded as unreachable *because* Table A.1 was not
implemented, and that reason outlived the limitation it described — the probe
went on asserting it for two changes after it stopped being true. Implementing a
feature can make a previously unreachable path reachable, and a recorded reason
is a claim that has to be revisited when the thing it depends on changes.

Three rows were provoked and this source does not produce them. That is a fact
about this server, not a gap in the adapter:

| Row | What this source did instead |
|---|---|
| `OPC_E_INVALIDITEMID` | answers `OPC_E_UNKNOWNITEMID` for malformed ItemIDs; it does not distinguish malformed from absent |
| `OPC_E_RANGE` | accepted an out-of-range value of the item's own canonical type |
| `OPC_S_CLAMP` | same — it stored the value rather than clamping it |

The remaining seven cannot be produced through this adapter at all. Five of those
are consequences of decisions made on purpose, not untested paths:

| Row | Why it cannot be reached |
|---|---|
| `OPC_E_BADTYPE`, `DISP_E_TYPEMISMATCH`, `DISP_E_OVERFLOW` | ADR-0004 requires the requested VARTYPE to equal the canonical one and answers `TYPE_MISMATCH` itself, so the source is never asked to convert anything. The probe **demonstrates** this: it attempts the mismatched Write and requires the adapter to refuse it with no source HRESULT attached. |
| `OPC_E_INVALIDHANDLE` | item handles are the adapter's; a client never supplies one |

| `OPC_E_NOTSUPPORTED` | a 2.05a Write carries a value only, never a quality or timestamp |
| `E_OUTOFMEMORY` | requires real memory exhaustion |
| `E_ACCESSDENIED` | activation-level on this source, not per item |

No value read from the source is printed by the probe. The extreme value written
to provoke `OPC_E_RANGE` is written back on the same path, and that restoring
Write is required to succeed so the fixture is not left altered.

Running this also exposed a defect in a different probe. `opcuaprobe` asserted
against the source the instant its session activated, while `grpcprobe` had
always waited for the source to connect first. The UA listener accepts as soon
as it is bound, which is earlier, so the UA probe had been racing since it was
written; the added work ahead of it made it lose, on 386, with Browse correctly
answering `Bad_NotConnected`. It now waits through that status for a bounded
30 s.

### Branch identifier result

The fixture names its branch, so the change that obtains a branch's ItemID from
`GetItemID` is exercised on every real-DA run:

```
GRPC_REAL_DA_PASS ... namedBranches=1
opcua address space annexA branches=1 named=1 leaves=3
```

That number is evidence rather than decoration. Three separate places asserted
that a branch has no ItemID — the gRPC frontend, the HTTP structural check, and
the gRPC probe — and each of them failed when branches started carrying one.
**Their failing is what proved `GetItemID` answers for a branch on a real
server**, which is what A.3.1.2 assumes.

How many branches a source names is reported, never required: naming one is the
source's decision. What is required is the adapter's part — an ItemID reported
as present is never empty.

### HTTP item property result

The real-DA run exercises the HTTP property endpoints alongside the gRPC ones,
reporting the same identifiers the source offered:

```
HTTP_ITEM_PROPERTIES_PASS offered=5,6,7,8 valuesLogged=false
```

The gRPC probe covers the DA path; this covers the JSON the HTTP frontend
produces, which that probe cannot see — that an available property always
carries a `dataType`, that a result never contradicts its own `valuePresent`,
and that a failed property carries no value.

### DA item property result

PR #82 exercised the DA item property path against the fixture on both native
x86/386 and x64/amd64. Both reported the same thing:

```
grpc item properties offered=5,6,7,8 read=5,6,7 empty=8 sourceRefused=none unrepresentable=none
```

This is the first time the COM code behind it ran against a real server at all.
`IOPCItemProperties::QueryAvailableProperties` and `::GetItemProperties`, the
`VARIANT` array the second returns, the per-property `HRESULT` array beside it,
and the release of both had never executed: the OPC UA path stops before them
because this fixture offers nothing Table A.1 maps, and the DA-native endpoints
were new.

| Property | Result |
|---|---|
| 5 Access Rights, 6 Scan Rate, 7 EU Type | answered with a value |
| 8 EU Info | **answered successfully with no value** |

The last row is the interesting one, and it found a defect. A source can offer a
property, be asked for it, succeed, and give nothing — an empty VARIANT is an
answer. Both frontends were collapsing that into the same shape as a refusal,
reporting `ok=false` with a successful HRESULT and no error code to explain it.
Value presence is now its own bit, for the same reason a Read timestamp's is.

The probe records the identifiers rather than counts, and separates the three
ways a property can fail to carry a value — the source refused it, the source
answered with nothing, or the adapter cannot represent what it said. An earlier
run printed `refused=8:0x00000000`, a refusal with a successful HRESULT, which
is what prompted the split.

### Table A.1 "Other Properties" result

With Table A.1's last row implemented, the fixture's items expose their DA
properties over OPC UA and the probe reads every one:

```
opcua item properties tableA1=none unnamed=9 described=0 valuesLogged=false
```

Nine property nodes across three items — Scan Rate and EU Type on each, plus
Access Rights where the source offers it as a property the nine named rows do
not claim. Every one is a `PropertyType` variable that answers a Read; a bad
status carries no value and a good one carries a value.

`tableA1=none` beside it is not a contradiction. It counts the **nine named**
rows, and this fixture offers none of the DA properties they are built from —
which is what the row below records. The two numbers together are the fixture's
whole answer about Table A.1: nothing for the named rows, everything for the
unnamed one.

### Table A.1 item property result

PR #76 ran `opcuaprobe`'s Table A.1 check against the fixture on both native
x86/386 and x64/amd64. Both reported the same thing:

```
opcua item properties tableA1=none described=0 valuesLogged=false
```

**The fixture offers no Table A.1 property for any of its items**, so the
mapping is implemented and unexercised end to end. That is a limitation of this
server, not of the adapter, and it was established rather than assumed:

- The pinned fixture configuration
  (`Source/Test/TestServer/OpcTestServer.config.xml` at the ADR-0006 commit)
  defines exactly one property on its items — `PropertyID="6"`, Scan Rate.
  Table A.1 maps none of properties 1 to 8 onto a UA property, so there is
  nothing for the adapter to expose.
- `capabilities.properties` reports `supported`, so the interface is present and
  was asked; the answer is that these items have no such properties.
- `TestBrowsingAnItemExposesItsTableA1Properties` covers the whole path —
  Browse asks the populator, the populator asks the source, the address space
  gains the nodes, and the same Browse reports them — so a broken path would
  fail in ordinary CI rather than look like this.

The larger sample configuration shipped in the same repository
(`Source/Shared/SampleServer205/OpcDa20Server.config.xml`) does define High EU
and Low EU on several items, which is what an `EURange` is built from. It is not
the configuration ADR-0006 pins for validation, and swapping it would mean
running against something other than the audited upstream artifact, so it has
not been swapped.

**Validating Table A.1 against a real source therefore needs a second server**,
which is what [ADR-0017](adr/0017-third-party-vendor-da-fixture.md) is about.
Until then the mapping rests on unit tests and on the specification.

### Third-party OPC UA client interoperability

The two results above were produced by this project's own codec talking to this
project's own server, which agree with each other by construction. **Three**
independent third-party clients now run against the UA frontend over a scripted
DA source — all as interop clients only, as design §5.2 permits, with nothing
in the adapter linking against any of them:

| Client | Version | Checks |
| --- | --- | --- |
| [asyncua](https://github.com/FreeOpcUa/opcua-asyncio) (Python) | 1.1.x | 142 |
| [open62541](https://github.com/open62541/open62541) (C) | 1.5.7 | 128 |
| [OPC Foundation .NET stack](https://github.com/OPCFoundation/UA-.NETStandard) | 1.5.378.156 | 131 |

See [docs/validation/ua-client-interop.md](validation/ua-client-interop.md) for
what they check and `scripts/interop/run.sh` to run them.

Together they found six defects the Go suite could not see. Four came from
asyncua: an `OpenSecureChannel` reply naming no security policy, so **no
third-party client could connect at all**; `Browse` ignoring `includeSubtypes`,
so a generic hierarchical walk found nothing; `Publish` answering immediately
rather than holding the request, which turned the client into a busy loop of
3,874 exchanges in 40 seconds; and the standard `Server` object being absent,
so the client's liveness probe failed.

Two more came from the other clients **against a server asyncua already
passed**, which is the argument for having more than one: the Foundation's own
stack refused the endpoint with `Bad_IdentityTokenInvalid`, because unspecified
strings were written as zero-length rather than null and Table 192 forbids
specifying `issuedTokenType` on an `ANONYMOUS` policy; and open62541 could not
connect at all, because it deliberately sends no session nonce on an unsecured
channel. The first is a plain defect and is fixed. The second is recorded as a
**deliberate deviation** — an absent nonce is accepted only under
`SecurityMode None`, where the clause's own rationale for the field does not
apply — and the validation doc explains it so it can be reversed by decision
rather than discovered by accident. All are covered by regression tests.

What this is evidence for: three third-party clients, one of them the OPC
Foundation's own, interoperate with this server on connection, browse, read,
write, subscription, and the standard Server object. What it is not evidence
for: the DA side, which is scripted here and validated separately on Windows;
any security policy other than `None`; or conformance. Three clients are not
the OPC Foundation's Compliance Test Tool, and **no "OPC UA Certified" or "OPC
UA Compliant" claim is made**. UA Expert is not tested: Unified Automation
distributes it only to registered users, so it could not be obtained without
creating an account.

### Phase 7 gRPC Subscribe streaming result

PR #34 workflow run
[`32809889799`](https://github.com/east-true/opcda-access-adapter/actions/runs/32809889799)
tested adapter head `a24c0eca5ab598028d413ef04276c1567257431e` on both native x86/386 and
x64/amd64. The write-enabled gRPC scenario drove the server-streaming
`Subscribe` RPC against the fixture. Each architecture passed:

- a `created` message reporting the subscription identity, connection
  generation, all three items active with canonical type and access rights, and
  the requested `250ms` rate revised by the server to `300ms`;
- the server's initial snapshot delivered over the stream with raw Quality,
  timestamp presence, VARTYPE, canonical type, and HRESULT preserved;
- two change-driven notifications induced through the ordinary typed Write
  path, since the fixture's `Test` items are otherwise static;
- the coalescing bound, with no update carrying more values than the
  subscription's item count;
- `subscription_count` returning to zero after the client closed the stream,
  showing the DA group is released by stream end alone with no explicit
  unsubscribe RPC;
- no process value in any probe output.

This is fixture evidence for the OPC Foundation DA 2.05a test server, not a
broad vendor-compatibility claim.

### Phase 6 gRPC frontend result

PR #29 workflow run
[`32752269529`](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269529)
tested adapter head `b83b7c2b159194cbac94ce66f52b325b9c22031f` on both native
x86/386 and x64/amd64 before merge SHA
`21345739af98de981d12de36c6805f64e5b502ff`. Each architecture passed:

- typed gRPC Status with the exact selected CLSID, connection generation,
  Browse/Read/Write capabilities, Write enablement, and listener state;
- root and nested DA Browse with exact ItemIDs;
- an ordered known/unknown ItemID device Read preserving `VT_I4`, raw Quality
  `192`, timestamp presence, successful HRESULT, and `OPC_E_UNKNOWNITEMID`;
- default-disabled Write before source access, canonical type mismatch, safe
  typed `VT_R4` value Write, and `OPC_E_BADRIGHTS` on the read-only `VT_BSTR`;
- explicit gRPC guided setup, strict version 2 exact-CLSID configuration,
  automatic LocalService execution, lifecycle Event Log metadata, cleanup,
  and exactly one IPv4 loopback listener; and
- no process values in the probe or adapter logs.

Both source and staged fixture trees passed Microsoft Defender custom scans.
The same jobs passed the established HTTP semantics, failure/reconnect/load,
and 200-Read soak regression. This result covers the pinned fixture only; it
does not validate third-party vendors, TLS/authentication, Subscribe, OPC UA,
or external network exposure.

### Long-running resource result

Workflow run
[`32630548279`](https://github.com/east-true/opcda-access-adapter/actions/runs/32630548279)
at adapter head `995c387cb977a37ab80ecd0fc5deb2f4a98e191d` repeated the complete
scenario and then passed 100,000 device Reads per architecture. The x86 job
took 3m04s overall and observed adapter handles `+18`, adapter private bytes
`+5,373,952`, server handles `0`, and server private bytes `-208,896`.
The x64 job took 3m40s overall and observed adapter handles `+18`, adapter
private bytes `+5,787,648`, server handles `+4`, and server private bytes
`+12,288`. All were below the explicit absolute and growth ceilings; no
operation failed and no process value was logged.

### Final main confirmation

Workflow run
[`32632091320`](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320)
repeated the complete 100,000-Read scenario at main SHA
`9e8928d729300a67197da35e7bfee6623a861495`. Both source and built-output
Defender scans found no threats. The x86 result observed adapter handles
`+16`, adapter private bytes `+5,287,936`, server handles `0`, and server
private bytes `-200,704`; the x64 result observed adapter handles `+18`,
adapter private bytes `+5,730,304`, server handles `+4`, and server private
bytes `+20,480`. VARTYPE, Quality, timestamp presence, HRESULT, Browse,
Write, outage, and reconnect observations matched the recorded result above.

### Windows HTTP and failure stability result

Workflow run
[`32634777223`](https://github.com/east-true/opcda-access-adapter/actions/runs/32634777223)
passed the bounded stability profile on Windows Server 2025 for adapter head
`ccc28487dfc33e1767e3f42c547a6d59a5ae4ca4` on both native x86/386 and
x64/amd64. Each architecture passed:

- normal status, exact nested Browse identity, and ordered partial device Read
  semantics against the real DA fixture;
- malformed/trailing JSON, unknown fields, invalid UTF-8, unpaired surrogate,
  embedded NUL, invalid source/filter/method/path, oversized body/batch/ItemID,
  and excessive Browse-depth rejection;
- 48 incomplete-header connections plus an oversized header. Windows rejected
  the oversized header by closing the connection, and the adapter immediately
  returned a healthy status; all incomplete headers were closed after the
  configured five-second header timeout;
- 5,000 no-delay sequential device Reads and 3,200 mixed requests from 16
  concurrent workers;
- deterministic overload with all 32 HTTP request slots occupied: the next 16
  requests returned bounded frontend `QUEUE_FULL`, and status plus ten device
  Reads succeeded after the blocking connections were closed;
- three consecutive real source unregister/re-register cycles. No successful
  stale value was returned, unavailable state was observed, generations
  advanced through 3, 4, and 5, and the known ItemID was lazy-registered after
  every recovery;
- the normal Browse/Read/Write scenario and a final 200-Read bounded soak.

The x86 HTTP profile completed in 10.873s and observed adapter deltas of `+8`
handles and `+5,640,192` private bytes; server deltas were `0` handles and
`+45,056` private bytes. The x64 profile completed in 10.153s and observed
adapter deltas of `+8` handles and `+6,463,488` private bytes; server deltas
were `+6` handles and `+172,032` private bytes. These samples include the
failure cycles, stability profile, and final soak and remained below the
explicit harness ceilings. Both source and built fixture scans reported no
Microsoft Defender threats. This antivirus result is a bounded observation,
not proof that arbitrary code is harmless. No process value was logged.

Final-main workflow run
[`32635274825`](https://github.com/east-true/opcda-access-adapter/actions/runs/32635274825)
repeated the entire profile at merge SHA
`f55b4bb8e8c092fa2a21f4f35089a14703c81a8d`. The x86 profile completed in
9.322s with adapter deltas of `+16` handles and `+6,725,632` private bytes;
server deltas were `0` handles and `+32,768` private bytes. The x64 profile
completed in 10.26s with adapter deltas of `+16` handles and `+5,763,072`
private bytes; server deltas were `+4` handles and `+143,360` private bytes.
Both architectures passed all semantic, abnormal-input, timeout, rapid,
concurrent, backpressure/recovery, source-failure, and soak assertions, and
both source/output Defender scans found no threats.

### Local registration detection result

PR #24 workflow run
[`32706109366`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109366)
executed at adapter head `387ba9c269848035980b544cb816dafdef92d2d1`
on ephemeral GitHub-hosted Windows Server 2025 VMs. On both x86/386 and
x64/amd64, the matching detector returned the pinned fixture's exact registered
ProgID and CLSID exactly once. The fixture process count was zero immediately
before and after the command, so registration detection did not activate that
vendor server. Native repeated COM lifecycle tests also passed on both
architectures in CI run
[`32706109315`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109315).

This observation confirms bounded local component-category enumeration for
the pinned fixture only. A detected registration is not evidence that the
class can activate, that its COM permissions are sufficient, or that its DA
behavior is compatible, so it does not add or upgrade a matrix PASS row.

### Guided setup and Windows Service result

PR #26 workflow run
[`32734190245`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734190245)
executed at adapter head `0fc3684128919ff28f94b9257c0dbc30e34ae328`
on ephemeral GitHub-hosted Windows Server 2025 VMs. Both native x86/386 and
x64/amd64 jobs passed the following assertions against the pinned fixture:

- guided setup displayed the registered fixture and required an explicit
  numeric source selection even though it was the intended candidate;
- HTTP/JSON and Windows Service were selected explicitly, followed by a final
  confirmation before mutation;
- the bounded version 1 configuration contained the fixture's exact CLSID,
  loopback HTTP listener, and `writeEnabled: false`;
- SCM reported automatic start, the `NT AUTHORITY\LocalService` account, and
  an image path using the internal `service run` entry point;
- the service reached connected state through local COM and completed a device
  Read of the known fixture ItemID without enabling Write;
- at least one bounded lifecycle record appeared under the service's Windows
  Application Event Log source; and
- uninstall stopped and deleted the service, removed the Event Log source, and
  the harness removed its temporary configuration directory.

The same jobs then passed the complete foreground semantic/failure regression,
including Browse, ordered partial Read, disabled/typed/denied Write, three real
source outage/reconnect cycles, 5,000 rapid Reads, 3,200 mixed concurrent
requests, bounded overload recovery, and a final 200-Read soak. This proves
that the pinned fixture accepted LocalService under the runner's local COM
permissions. It does not prove that another vendor's AppID launch/access or
RunAs policy will accept that identity, and setup did not modify DCOM security
or fall back to a more privileged account.

This result validates the scoped v0 path against this specific official test
fixture only. Third-party/vendor servers and non-Good Quality observations
remain untested; compatibility must not be inferred for them.

## Real-DA validation procedure

1. On the same Windows machine as an installed DA server, build the matching
   adapter architecture (`windows/386` for a 32-bit-only registration,
   `windows/amd64` where applicable).
2. Run `opcda-access-adapter detect` and record whether the intended local
   `OPC_DA_20` registration appears with its exact CLSID and ProgID. Confirm
   that detection alone does not start the vendor server process. Registration
   detection is not a compatibility PASS.
3. Set exactly one source identifier:

   ```powershell
   $env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
   # or
   $env:OPCDA_SOURCE_CLSID = "{00000000-0000-0000-0000-000000000000}"
   ```

4. Start the adapter with its default loopback listener. Check `GET
   /v1/status`, then Browse root and a nested branch if Browse is supported.
5. Read a known item, a batch with at least one invalid ItemID, and an item
   with non-Good Quality where available. Record exact VARTYPE, Quality,
   Timestamp presence, and HRESULT.
6. Confirm Write is rejected while disabled. Enable it only against a safe
   test item, verify type mismatch and access-denied behavior, then a typed
   value Write.
7. Restart the DA server, observe an unavailable result during disconnect,
   then verify `disconnected` during backoff/`reconnecting` during an attempt,
   an increased reconnect count, a strictly newer connection generation, and
   lazy re-registration rather than old-handle reuse. Do not accept any
   last-good value during the outage.
8. Check `capabilities.subscribe` in status. The adapter probes the group's
   `IOPCDataCallback` connection point at connect time and never advertises
   Subscribe without it. If it is false, record **UNSUPPORTED** in the Subscribe
   column rather than FAIL: a synchronous-only DA 2.0 server is a legitimate
   source for Browse, Read, and Write. Confirm that a Subscribe attempt is
   refused immediately as `SUBSCRIBE_UNSUPPORTED` instead of failing late.
9. If Subscribe is supported, open a gRPC Subscribe stream against a safe item
   set. Record the requested rate, the server's revised rate, per-item activation
   status, and whether the source delivers an initial snapshot, change-driven
   notifications, or both. Then close the stream and confirm
   `subscription_count` returns to zero.
10. Repeat the required scenarios for each applicable adapter architecture and
   record actual PASS/FAIL/BLOCKED results in the table above.
11. For soak validation, repeatedly exercise a bounded safe Read batch while
   monitoring process private bytes, handle count, goroutine count, and
   request errors. Exercise Browse and an authorized safe Write at controlled
   intervals. Do not persist response values; record only duration, counts,
   resource deltas, and failure metadata. A VM without an installed real DA
   server cannot satisfy this step.

### Vendor variations to record

The fixture results above cover one server. These are the behaviors most likely
to differ on a third-party server, and each should be recorded as an exact
observation rather than used to change source semantics.

| Variation | What to record | Adapter behavior today |
|---|---|---|
| No `IOPCDataCallback` connection point | `capabilities.subscribe` false | Probed at connect; Subscribe refused as `SUBSCRIBE_UNSUPPORTED`. Over OPC UA this is `Bad_ServiceUnsupported`. |
| No `IOPCItemProperties` | `capabilities.properties` `unsupported` | Probed at connect. Property reads are refused as `PROPERTIES_UNSUPPORTED`; the source is working correctly and simply has no properties to offer. OPC 10000-8 Table A.1 cannot be mapped for such a source, and nothing about the item's value, quality, timestamp or access rights is affected — those come from Read, Subscribe and `AddItems`. |
| No `IOPCBrowseServerAddressSpace` | `capabilities.browse` `unsupported` | Browse refused as `BROWSE_UNSUPPORTED`, and over OPC UA as `Bad_ServiceUnsupported`. The UA address space stays empty, but a client that knows its ItemIDs can still read, write and monitor them: a UA node identifier carries the exact ItemID. |
| An item's canonical type or access rights never reported | the node's `DataType` and `AccessLevel` before any Read | DA reports both in `AddItems` rather than Browse, so a browsed node knows neither. The source decides both until a Read or notification teaches the adapter. |
| Different revised update rate, or a rate the server refuses | requested and revised values | The server's revised rate is reported unchanged, including as a UA MonitoredItem's `revisedSamplingInterval` |
| Non-Good Quality | the exact raw 16-bit Quality | Passed through as data, never a transport failure |
| Absent source timestamp | `timestampPresent` false with a zero timestamp | Presence is an independent bit; no timestamp is synthesized |
| Scalar VARTYPE outside the supported set | the exact raw VARTYPE | Explicit `UNSUPPORTED_VARTYPE`; never coerced or narrowed |
| Vendor-specific disconnect HRESULT | the exact operation and HRESULT from `lastError` | **Not recognized as a disconnect.** Per ADR-0005 the adapter does not guess: an unrecognized HRESULT stays a source error, so the runtime keeps reporting `connected` and does not reconnect until the process restarts. Recording the exact HRESULT is what justifies adding it. |
| A per-item HRESULT other than `OPC_E_BADRIGHTS` or `OPC_E_UNKNOWNITEMID` | the exact HRESULT, the operation, and the UA status the client saw | All thirteen rows of Part 8 Tables A.4 and A.5 are bound, but only those two have been observed from a real server. The other eleven are transcribed from the specification with values checked against `opcerror.h`; seeing one confirms a row rather than adding it. |
| `AppID`/`RunAs` that refuses `NT AUTHORITY\LocalService` | the exact activation HRESULT and the DCOM event | Setup never edits COM/DCOM or firewall permissions; run the adapter in the foreground under an account the vendor policy permits |

Adding a row to the matrix requires an executed result on that server. Do not
infer vendor-wide compatibility from one installation, and do not place process
values in this document.

## Automated isolated fixture

The manual `Real OPC DA validation` GitHub workflow builds the OPC Foundation
DA 2.05a test server from a pinned, reviewed source commit and registers it by
local COM on an ephemeral Windows VM. It covers both native x86/386 and
x64/amd64 paths. See the [execution procedure](validation/real-da-windows.md)
and [fixture/supply-chain decision](adr/0006-real-da-validation-fixture.md).

The workflow file existing in the repository is not a test result. Add a row
to the matrix only after an actual run passes, and include its immutable run
URL and commit SHA. Never copy response values into the matrix or workflow
logs.
