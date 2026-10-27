# UA address space, Browse, Read, and Write

[UA overview](../opcua-mapping.md) · [Documentation](../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../compatibility.md).

## A refusal names the parameter it refuses

Table 48 gives Read two result codes of its own — `Bad_MaxAgeInvalid` and
`Bad_TimestampsToReturnInvalid` — and Table 64 repeats the second for
CreateMonitoredItems. `Bad_InvalidArgument` is true of either case but says only
that something in the request was wrong. Naming the parameter is the difference
between a client that can fix its request and one that can only guess, so each
refusal carries the code the table names.

`timestampsToReturn` is checked against Table 180, which defines four values
that name timestamps plus `INVALID`, "no value specified". A request carrying
`INVALID` has not asked for anything, so it is refused alongside the values the
table does not define at all. `NEITHER` is a real answer and is accepted: Table
180 forbids it only for HistoryRead.

### Every attribute OPC 10000-3 makes mandatory

The attribute matrix in OPC 10000-3 clause 5.9 says which attributes each node
class shall have. This adapter exposes two of the eight classes, so its whole
obligation is those two columns.

A **Variable** shall answer `NodeId`, `NodeClass`, `BrowseName`, `DisplayName`,
`Value`, `DataType`, `ValueRank`, `AccessLevel`, `UserAccessLevel` and
`Historizing`. An **Object** shall answer `NodeId`, `NodeClass`, `BrowseName`,
`DisplayName` and `EventNotifier`.

`EventNotifier` reads as a zero Byte. Table 43 gives it three meaningful bits,
and all three are false here: bit 0 clear says the node "cannot be used to
subscribe to Events", and bits 2 and 3 that its event history is neither
readable nor writeable. This adapter serves no events and keeps no history — the
design forbids a historian — so zero is the honest answer, and answering nothing
would have a client read a mandatory attribute as missing.

The optional attributes are answered where the source supplies them:
`Description` from the DA Item Description property, and
`MinimumSamplingInterval` from the DA Scan Rate. `ArrayDimensions` is not
answered, which the matrix permits and the scalar-only mapping makes moot.

### AccessLevel and UserAccessLevel answer alike

A.3.1.3 maps `OPC_READABLE` and `OPC_WRITABLE` onto the AccessLevel attribute
and adds: "note that the same values are also set for the UserAccessLevel in the
COM UA Wrapper". This adapter does the same.

The two can only differ where a server restricts a node per user, and there is
no user model here to restrict it by. OPC DA reports one set of access rights per
item, and every session sees the item the source described; answering a narrower
UserAccessLevel would be inventing a restriction the source never stated.

That is also why no operation answers `Bad_UserAccessDenied`. A refusal here is
always about the item's own rights — `Bad_NotReadable` or `Bad_NotWritable` —
never about who is asking.

### An undefined enumeration value is answered, not disconnected

An out-of-range value reaches the service rather than being refused while
decoding. A decoding failure drops the connection, and such a message decoded
perfectly — one enumeration value is out of range. The spec has a result code
for each of these, and answering with it lets a client correct itself instead of
losing every session on the channel.

Three enumerations are handled this way, and where the spec makes the code an
**operation level** result, one bad operation fails on its own rather than
taking its neighbours down with it:

| Enumeration | Table | Result code | Level |
| --- | --- | --- | --- |
| `TimestampsToReturn` | 180 | `Bad_TimestampsToReturnInvalid` | service (Tables 48, 64) |
| `BrowseDirection` | 112 | `Bad_BrowseDirectionInvalid` | operation (Table 36) |
| `MonitoringMode` | 148 | `Bad_MonitoringModeInvalid` | operation (Table 65) |

Each table defines the values that mean something plus, in two cases, an
`INVALID` meaning "no value specified". A request carrying `INVALID` has not
asked for anything, so it is refused alongside the values the table does not
define at all — the client is equally unable to say what it wanted either way.

What stays a decoding failure is an encoding the message could not have carried:
a built-in type identifier outside OPC 10000-6's range, a NodeId carrying
ExpandedNodeId flags, a Variant nested in a Variant. Those are not a client
naming something the standard does not define; they are bytes that are not a
message.

## `maxAge` on a Read

OPC 10000-4 Table 47 gives `maxAge` three rules, and the adapter meets two of
them exactly.

**Negative values are invalid**, and a negative `maxAge` is refused with
`Bad_InvalidArgument` rather than treated as zero.

**A `maxAge` of 0 "shall attempt to read a new value from the data source"**,
which is what every Read here does.

**A `maxAge` of max Int32 or greater "shall attempt to get a cached value"**, and
this adapter reads from the device anyway. It has no cache to offer: its DA group
is created inactive, so the source maintains nothing for it, and design §16.2
makes `device` the v0 default "for correctness". Serving that rule would mean
activating the group, which makes the source push updates for every item ever
read — load an operator did not ask for.

Nothing is misreported by this. `maxAge` bounds how **stale** a value may be, and
a value read now is within any bound. The client receives something fresher than
it asked for, and the source does more work than it needed to.

This is a v0 choice and not a closed question. §16.2 permits representing the DA
`cache` source and says that exposing it later "within scope" — what INV-6
forbids is a persistent value store, which is a different thing from the source's
own cache. A later version that exposed `cache` could meet this rule as written.

## Timestamps

Part 8 A.3.2.4: the DA timestamp becomes the UA **SourceTimestamp**. The UA
**ServerTimestamp** is the adapter's own time for the operation.

The DA core already distinguishes an absent source timestamp from a zero one.
An absent DA timestamp must leave SourceTimestamp unset; the adapter never
substitutes its own time for the source's, and ServerTimestamp is never
presented as a source timestamp.

## Identity

Part 8 Annex A discusses three wrapper strategies for deriving names and
NodeIds, one of which is a configurable ItemID path delimiter.

**This adapter does not use that strategy.** Design §35.2 forbids guessing a DA
server's delimiter or reconstructing an ItemID from a browse path. The rules
are:

- the exact DA ItemID is the item identity, used directly as the NodeId where
  possible;
- BrowseName and DisplayName come from what DA Browse returned, never from
  splitting an ItemID;
- the adapter keeps its namespace URI stable and never treats a namespace index
  such as `ns=2` as persistent identity;
- names are never tidied — no case changes, no replacing `.` or spaces with `_`.

## The namespace table's reserved entries

OPC 10000-5 8.3.2 fixes the first two entries: "index 0 is reserved for the OPC
UA namespace, and index 1 is reserved for the local Server", and "the
ApplicationUri of an OPC UA Server shall be identical to the URI set in index 0
of the ServerArray and index 1 of the NamespaceArray".

So index 1 is the **ApplicationUri**, and this adapter's own namespace follows
at index 2. The two URIs are configured separately on purpose: an ApplicationUri
commonly names a host, while design §35.2 requires a namespace URI that stays
stable across restarts, and the two do differ in practice — this project's own
interop harness sets them to different values. Putting the stable one at index 1
would have made it the server's identity as well.

The ApplicationUri is therefore required to build an address space at all. It is
this server's identity in two tables, and an absent one is a hole in both rather
than a missing label.

## The limits a client can read instead of discovering

Part 4 7.9 says a server "specif[ies] a maximum number of ContinuationPoints per
Session in the ServerCapabilities Object defined in OPC 10000-5". This server
enforces that limit and several others, and until now published none of them: a
client learned each one by being refused.

`ServerCapabilities` carries the children OPC 10000-5 Table 10 makes mandatory,
and the values are taken from the same configuration the services are built
from, so the two cannot drift. `MaxBrowseContinuationPoints` is the browse
service's own per-session bound; `MinSupportedSampleRate` is the fastest
publishing interval a subscription can be revised to; `OperationLimits` carries
`MaxNodesPerRead`, `MaxNodesPerWrite` and `MaxNodesPerBrowse`.

Three of the mandatory values say that something is absent rather than bounded.
`MaxQueryContinuationPoints` and `MaxHistoryContinuationPoints` are zero because
neither service exists here. `SoftwareCertificates` is empty, which is the same
answer the endpoint gives.

`ServerProfileArray` is **empty**, and deliberately. ADR-0016 forbids claiming a
profile this project has not been certified against; an empty array says no
profile is claimed, which is exactly true, while naming one would be the claim
the ADR forbids.

`ModellingRules` and `AggregateFunctions` are mandatory folders and both are
empty: this server defines no modelling rules of its own and computes no
aggregates.

`VendorServerInfo` and `ServerRedundancy` are the other two mandatory
components, and both are answered without collecting anything.
`VendorServerInfoType` defines no children at all, so the Object is empty: this
adapter defines no vendor extension. `ServerRedundancyType` defines one
property, and `RedundancySupport` is `None` — one process in front of one DA
source has no second one to fail over to, and any other value would describe a
deployment that does not exist.

`ServerDiagnostics` is the one mandatory component still missing, and it is in
the departures table. Its own mandatory children are counters and diagnostic
arrays this server does not collect; publishing them as zeros would report a
diagnostic answer rather than the absence of one.

## Root's three standard entry points

OPC 10000-5 8.2 gives Root three browse entry points — `Objects`, `Types` and
`Views` — and the OPC Foundation's own NodeSet has Root organize all three. This
server publishes all three, and two of them are empty.

`Types` is empty because no type definition node is materialised, which
OPC 10000-3 4.6 permits: a server may "use well-known NodeIds without
representing the corresponding TypeDefinitionNodes in their AddressSpace".

`Views` is empty because this server publishes no views at all — a Browse
naming any view is refused with `Bad_ViewIdUnknown`. Clause 8.2.3 makes the
folder the entry point for View nodes and says it "shall not reference any other
NodeClasses", which an empty folder satisfies exactly.

Both are present rather than omitted because an empty folder says "nothing
here", while a missing one says nothing at all and leaves a client to guess
whether the server has no views or simply did not build that part of the tree.
Including one and omitting the other, which is what this server used to do, is
the answer that tells a client least.

## The address space

`internal/opcua/addressspace.go` maps the DA source onto UA nodes. The standard
nodes — Root, Objects, Types, the folder that holds the source, and the `Server`
object — use the identifiers from the OPC Foundation NodeIds table, and the
attribute and `AccessLevel` values come from the AttributeIds table and OPC
10000-3.

### The standard Server object

OPC 10000-5 8.3.2 places a `Server` object in every server's address space, and
a generic UA client depends on it in two ways that are not optional in practice:

- It reads the **`NamespaceArray` before anything else**, because a namespace
  index means nothing on its own — only the URI it stands for is stable, which
  is the same reason design §35.2 keeps the URI rather than the index as this
  adapter's durable name.
- It reads **`ServerStatus` on a timer** to decide whether the server is still
  alive. Without it a client concludes the server is dead however healthy it is,
  and tears the connection down.

`internal/opcua/serverstatus.go` publishes `ServerArray`, `NamespaceArray`, the
`ServerStatus` subtree (`StartTime`, `CurrentTime`, `State`, `BuildInfo` and its
six fields), `ServiceLevel`, and `Auditing`. `ServerStatus` and `BuildInfo` are
carried as `ExtensionObject` structures naming their `DefaultBinary` encodings,
with the field order the NodeSet gives: `StartTime`, `CurrentTime`, `State`,
`BuildInfo`, `SecondsTillShutdown`, `ShutdownReason`, and within `BuildInfo`
`ProductUri`, `ManufacturerName`, `ProductName`, `SoftwareVersion`,
`BuildNumber`, `BuildDate` — an order the names alone do not suggest, and one a
foreign decoder is the only thing that can confirm.

These variables are answered from the address space and **never reach the DA
source**: the server is reporting on itself. `CurrentTime` is answered as of the
read rather than fixed at construction, which is what makes it useful as a
liveness signal. `State` is always `Running`: a DA source that is disconnected
is reported per item, never by claiming the UA server itself has failed.

The node budget counts **only source-derived nodes**. It exists to stop a source
with a very large or hostile address space from exhausting memory, and counting
the server's own fixed nodes would mean that adding a node the specification
requires silently reduced how many DA items an operator's configured limit
allows.

`NodeClass` is a **bit mask** (1, 2, 4, 8, …), not an ordinal. That is why
Browse filters node classes with a mask, and treating it as an ordinal would
make every such filter wrong.

### Identity follows the design, not convenience

Design §35.2 governs this, and the mapping implements it literally:

- **An item's node carries the exact DA ItemID**, with no trimming, case
  conversion, or delimiter rewriting. Tests pin awkward identifiers — embedded
  spaces, mixed case, a trailing space, an embedded tab — through the round
  trip.
- **A branch has no ItemID.** The design forbids reconstructing one from a
  browse path, so a branch's identity is the navigation path itself, marked so
  it can never collide with an item's identifier. A branch never resolves to an
  ItemID.
- **BrowseName and DisplayName are what DA Browse returned**, unmodified.
- The namespace **URI** is the durable name; the index is not treated as
  identity. OPC 10000-5 8.3.2 reserves the first two entries of the namespace
  table — "index 0 is reserved for the OPC UA namespace, and index 1 is reserved
  for the local Server" — so this adapter's own nodes are at **index 2**. A
  client resolves that index by looking its URI up in the table, which is the
  same rule read from the other side.

A nested branch keeps its full path, so two branches with the same name under
different parents stay distinct nodes.

### Types and access

A variable's DataType comes from the Part 8 mapping. A VARTYPE with no Table A.2
row falls back to the **abstract base type** rather than borrowing a
numerically similar one. Ordinary DA item nodes still report scalar `ValueRank`
even when Read returns an array; see the [array limitations](data-access.md#arrays).

### A browsed item knows neither its type nor its rights

**OPC DA reports an item's canonical type and access rights in the `AddItems`
result, not in Browse.** A browsed node therefore starts out knowing neither,
and the address space records which of the two the source has actually told it.

Where something is unknown, **the source is the authority**:

- **Access rights.** The node is reported readable and writable and the adapter
  does not gate the operation, because it imposes no restriction of its own. The
  source answers `OPC_E_BADRIGHTS` for what it does not permit, which Tables A.4
  and A.5 map to `Bad_NotReadable` and `Bad_NotWritable`.
- **Canonical type.** The client's own `Variant` decides the VARTYPE — the same
  width, never widened or narrowed — and the DA core still writes strictly. A
  server whose canonical type differs answers with a type-mismatch HRESULT that
  Table A.5 maps to `Bad_TypeMismatch`.

Refusing locally in either case would be the adapter enforcing a restriction it
invented and cannot verify, and would make **every browsed item permanently
unreadable and unwritable**.

### A source need not implement Browse

`IOPCBrowseServerAddressSpace` is **optional in DA 2.05a**. Against a source
that does not implement it the address space can never be populated, which would
leave the UA server with nothing but its standard nodes.

A node identifier is self-describing: it carries the exact DA ItemID verbatim.
So an item can be **read, written, and monitored without having been browsed** —
a client of such a source knows its ItemIDs from elsewhere, and the adapter has
everything it needs to ask the source about them.

The source stays the authority on whether the item exists. Its
`OPC_E_UNKNOWNITEMID` maps through Table A.4 to exactly the `Bad_NodeIdUnknown`
a client expects for a node that is not there, so accepting the identifier costs
no correctness.

A node created this way is **not linked into the hierarchy** — it was never
browsed, so Browse must not report it as a child of anything — and it draws on
the same node budget browsing does, so addressing items directly cannot grow the
space without limit. An identifier that names no DA item at all is still
`Bad_NodeIdUnknown`.

**This is a deviation from clause 5.4**, and it is one this adapter chooses.
5.4 says DataItems "are always defined as data components of other Nodes in the
AddressSpace. They are never defined by themselves." A node with no parent is
defined by itself.

The alternative is worse. To give such a node a parent the adapter would have to
pick one, and it does not know where the item sits in the source's hierarchy —
that is what browsing would have told it. Hanging it off the source folder would
claim the item is top-level, which for a nested item is simply false, and a
client walking the hierarchy would be led to the wrong place rather than to
none.

So the node is reachable by identifier and invisible to Browse, which is
accurate: the adapter knows the item exists and does not know where it belongs.
Satisfying 5.4 for such an item needs the source's hierarchy, and a source that
does not implement Browse cannot supply it — the case this whole section is
about.

A source that does not implement Browse, or that has no `IOPCDataCallback`
connection point, reports `Bad_ServiceUnsupported`: both interfaces are optional,
so a source lacking one is stating a capability rather than failing.

### The address space learns

A Read and a subscription notification both come back through `AddItems`, so
each one is the source reporting the item's canonical type and access rights.
The node records them the first time it sees them, which makes its attributes
accurate for every client that follows. Once known, they are enforced locally
without asking the source again.

Re-browsing a branch replaces its forward references, so the space reflects the
source instead of accumulating nodes the source no longer has, while the
inverse references that walk back up survive.

## Browse

`internal/opcua/browse.go` implements `Browse` and `BrowseNext` from **OPC
10000-4 Tables 34, 37, 113, 168, 112 and 194**, served by the listener to an
activated session.

Three rules in Table 34 are easy to get subtly wrong, and each is pinned by a
test:

- **`nodeClassMask` is a mask, not an equality test**, and zero means *all*
  classes rather than none.
- **`resultMask` is a request for specific fields.** Anything not asked for is
  omitted rather than sent anyway.
- **`requestedMaxReferencesPerNode` of zero means the client imposes no limit**,
  so the server's own bound applies. A client can tighten that bound but cannot
  raise it.

- **`includeSubtypes` is honoured.** A reference matches when the requested type
  is one of its supertypes, using the relation the OPC Foundation NodeSet gives:
  `Organizes` is a `HierarchicalReferences`, `HasProperty` and `HasComponent`
  aggregate and are hierarchical, `HasTypeDefinition` is non-hierarchical, and
  everything is a `References`. This matters more than it looks: browsing for
  `HierarchicalReferences` with subtypes included is **how a generic client
  walks an address space**, and ignoring the flag made such a client see nothing
  at all. This project's own probe browsed with an unspecified reference type
  and never noticed.

Table 168 adds one more: a type definition exists only for `Object` and
`Variable`, so any other node class carries a null NodeId there.

**Every instance is also the source of its own `HasTypeDefinition` reference.**
OPC 10000-3 5.6.2 and 5.5.1 require each Variable and Object to have exactly
one type definition and a HasTypeDefinition reference to it. Browsing an item's
type definition returns that reference; browsing all references returns it
alongside the item's other references.

The type nodes themselves are not materialised, which 4.6 allows: a server may
"use well-known NodeIds without representing the corresponding
TypeDefinitionNodes in their AddressSpace". Their browse names and node classes
come from the OPC Foundation's `NodeIds.csv` and the spec check compares them
with it, because a name carried in code rather than read from a node is a
transcription like any other.

The reference is built when a node is browsed rather than stored on it, because
a node's type definition can change after the node exists: A.3.1.3 promotes an
item from `DataItemType` once its properties say it is an `AnalogItemType`, and
a stored reference would still name the type the item had when it was created.

The results array matches `nodesToBrowse` in size and order, so a node that
fails occupies its slot with a per-node status rather than shortening the list —
the service call itself still succeeds. A `referenceTypeId` that names no
reference type is `Bad_ReferenceTypeIdInvalid`, not a filter that silently
matches nothing.

### Continuation points

A continuation point is **consumed by use**: the client receives a new one if
more remains, so a stale point cannot be replayed. Points expire if a client
abandons a browse, and `BrowseNext` with `releaseContinuationPoints` returns
empty arrays and frees them, as Table 37 requires.

**A point belongs to one session.** Clause 5.9.3.1: "the BrowseNext shall be
submitted on the same Session that was used to submit the Browse or BrowseNext
that is being continued." Another session offering the same opaque value gets
`Bad_ContinuationPointInvalid` and does not consume it, and a release from the
wrong session leaves it alone. Clause 7.9 adds the other end of a point's life:
they "remain active until the Client retrieves the remaining results, the Client
releases the ContinuationPoint or the Session is closed", so closing a session
frees everything it held.

**The allowance is per session**, as 7.9 has it — "Servers specify a maximum
number of ContinuationPoints per Session". One session filling its allowance
neither blocks another nor is spent by it.

**A new request frees this session's oldest point rather than being refused.**
7.9: "a Server shall automatically free ContinuationPoints from prior requests
from a Session if they are needed to process a new request from this Session."
The newest request is the one the client is waiting on; the abandoned one is
what it has stopped asking about. A client that then offers the freed point gets
`Bad_ContinuationPointInvalid`, which is what 7.9 specifies for a point "that has
been released".

*Prior* requests is the whole of it. A point handed out earlier in the same
response is not spare capacity, because freeing it would revoke a point in the
very response that carries it. 7.9 says what happens instead: "a Server shall
process the operations until it uses the maximum number of continuation points
in this response. Once that happens the Server shall return a
Bad_NoContinuationPoints error for any remaining operations."

**The original Browse's limit governs every continuation of it.** `BrowseNext`
has no parameter to restate `requestedMaxReferencesPerNode`, and 5.9.3.1 defines
"too large" as exceeding "the maximum number of results to return that was
specified by the Client in the original Browse request" — so a client that asked
for five references per node gets five from each `BrowseNext` too, not the
server's own far larger bound. The limit is carried in the point.

When a point cannot be issued the operation reports the failure rather than
silently truncating the result: a client holding part of an answer and no
continuation point has no way of knowing the rest existed. `BrowseNext` never
answers `Bad_NoContinuationPoints`, because 7.9 says a server "shall never return
Bad_NoContinuationPoints error when continuing a previously halted operation".

### Session enforcement

`Browse` and `BrowseNext` require an **activated** session. A session that was
created but never activated is refused with `Bad_SessionNotActivated`, so a
client cannot skip `ActivateSession` and still read the address space.

## Read and Write

`internal/opcua/variant.go` adds `Variant` and `DataValue` (**OPC 10000-6 Tables
26, 27 and 1**), and `internal/opcua/readwrite.go` implements `Read` and `Write`
(**OPC 10000-4 Tables 47, 53, 167, 180 and 131**). **This is where the UA
frontend actually reaches the DA runtime.**

### The mapping is applied, not re-decided

A read result follows the [Part 8 mapping](../opcua-mapping.md): the DA
quality becomes the UA `StatusCode`, a per-item HRESULT maps through Table A.4,
and the DA timestamp becomes the `SourceTimestamp` while the adapter's own time
becomes the `ServerTimestamp`. **An absent DA timestamp stays absent** rather
than being filled in with the adapter's clock.

Scalar widths survive: the Go type the DA core produced decides the built-in
type, so nothing is widened or narrowed on the way out. A bad status carries no
value, as Table 131 requires.

### Write is strictly typed

The node's canonical DataType decides the VARTYPE, and the `Variant` must
already carry exactly that type. A `Double` written to an `Int32` node is
`Bad_TypeMismatch`, and so is a narrower `Int16` — nothing is converted. This
matches the DA core's own strict typed write.

Table 53 says a server returns `Bad_WriteNotSupported` when it cannot write
timestamps or an `indexRange`. The DA core writes **values only**, so a write
carrying a source timestamp, a server timestamp, a status code, or an index
range is refused before it reaches the source.

### Encoding details worth stating

`DataValue`'s mask means a field is written only when it carries information: a
`Good` status and an absent timestamp are **omitted**, not written as zeros.
Picoseconds of 10000 or more decode as 9999, as Table 27 requires.

A `Variant` whose Go value does not match its declared built-in type is refused
rather than coerced, since coercion would produce a stream the client cannot
decode. Unassigned built-in ids (26–31) are **accepted on decode** as
ByteStrings and **never produced on encode**, exactly as Table 26 states. A
Variant may not contain a Variant. Incoming array elements are skipped after
bounded length and shape validation, and array Writes are refused. Outgoing
Reads can encode zero-based DA arrays, including multidimensional values.

### No source attached

A listener built without a DA runtime answers `Read` and `Write` with
`Bad_NotConnected` rather than returning empty values, so a client is told the
source is unavailable instead of being handed something that looks like data.

## Filling the address space

`internal/opcua/population.go` fills the address space from DA Browse **on
demand**: a branch is browsed the first time a client browses its node, not when
the server starts.

That choice follows from what Annex A says about wrapper strategies and from how
DA servers behave. A DA address space can be large and can change while the
server runs, so browsing it all at startup would delay startup, hold a snapshot
that drifts, and do work for branches no client ever visits. Browsing on demand
also keeps every DA call on the request path, where its failure can be reported
to the client that caused it.

### Bounds and freshness

`PopulationLimits` caps the total node count and the depth a client can drive
the adapter into the source, so neither a very large hierarchy nor a persistent
client can exhaust memory. **The node budget is checked before entries are
added**, so a large branch cannot push the space past its bound and then be
trimmed.

A branch is reused for a configured refresh interval and then browsed again,
because a DA address space can change while the server runs. A **failed browse
is not recorded as done**, so the next caller retries rather than treating an
empty branch as authoritative.

Concurrent clients asking for the same branch **share one DA call**. A DA Browse
is serialized on the runtime's owning thread, so several identical calls would
queue behind each other for no benefit.

A population failure is reported for **that node alone**; other nodes in the
same Browse request are unaffected, and a standard node needs no population at
all.

### After a reconnect

`Listener.InvalidateAddressSpace` sends the next browse of every branch back to
the source. The application calls it after a reconnect, because a new connection
generation may expose a different address space.

