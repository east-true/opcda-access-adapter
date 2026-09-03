# OPC DA to OPC UA mapping

This is the specification the future OPC UA frontend implements. Its normative
source is **OPC 10000-8 (Part 8: Data Access), Annex A — "OPC COM DA to UA
mapping"**, with the status code structure from **OPC 10000-4 Table 176 and
Table 177** and numeric status codes from the OPC Foundation's published
`StatusCode.csv` for the UA namespace.

Nothing here is derived from recollection. Where the specification has no row
for something the DA core can produce, that is stated as an adapter decision
rather than presented as standard behavior.

The UA frontend is implemented and runs against the OPC Foundation fixture on
both architectures; `docs/compatibility.md` carries the executed results. Only
`SecurityMode` `None` is served and ADR-0016 forbids describing it as production
ready.

## Where this adapter departs from Part 8

Every departure below is deliberate, and each says which clause it departs from
and why. They are collected here because a reader deciding whether this adapter
suits their source should not have to find them scattered through the document.

| Clause | What it asks | What this adapter does, and why |
|---|---|---|
| A.3.1.2 | the root branch's BrowseName "should" be the Server ProgId | named from `OPCDA_OPCUA_SOURCE_FOLDER`, default `Source` — a source may be configured by CLSID, where there is no ProgID, and an operator who wants the clause's behaviour can configure it |
| A.3.1.3 | `AnalogItemType` if the item has High and Low EU **or** an Analog EU Type | the EU Type alone does not promote, because 5.3.2.3 makes `EURange` mandatory and there would be no range to publish |
| A.3.1.3 | `MultiStateDiscreteType` for an enumerated EU Type | never claimed: its mandatory `EnumStrings` comes from EU Info, an array, and the DA layer carries no array VARIANTs |
| A.3.1.4 | an array-valued property is exposed with `ValueRank` `OneOrMoreDimensions` | not exposed at all — it could be browsed and never read, and a property that cannot answer is worse than one that is absent |
| Table A.3 | DA `LAST_KNOWN` → `Bad_OutOfService` | `Uncertain_NoCommunicationLastUsableValue`, because Table 61 says so and explains why: a Bad severity must return a Null value, which discards the last known value the quality exists to carry |
| 5.2 | the `SemanticsChanged` bit is set when a semantic property changes | set when the adapter **observes** a change, which is when a property is read; a change nobody reads is not detected, and detecting every one means polling the source |
| OPC 10000-5 Table 9 | `ServerType` makes `ServerDiagnostics` a mandatory component of the Server Object | not published. Its mandatory children are counters, session and subscription diagnostics arrays this server does not collect, and publishing them as zeros would report a diagnostic answer rather than the absence of one. The other eight mandatory components are carried |
| OPC 10000-4 5.14.1.1 | on lifetime expiry the server "shall issue a StatusChangeNotification notificationMessage with the status code Bad_Timeout" | the subscription is deleted and its DA group released, but no notification is sent: expiry happens precisely because no Publish request was available to carry one |
| OPC 10000-4 5.7.2.1 | subscriptions survive a session the server terminated, so they can be transferred | they are deleted with the session, because TransferSubscriptions is not implemented and a subscription nothing can reach would hold a DA group open indefinitely |
| OPC 10000-4 5.13.2.1 | "if the access rights change to read rights, the Server shall start sending data for the MonitoredItem" | access rights are learned once and never revised, so an item that becomes readable stays silent until it is created again. The half of the clause that matters more — the create succeeding, with the status delivered through Publish — is met |
| OPC 10000-4 Table 47 | a `maxAge` of max Int32 or greater "shall attempt to get a cached value" | every Read goes to the device. The DA group is created inactive, so the source keeps no cache for it, and design §16.2 makes device the v0 default "for correctness". A fresh value satisfies any staleness bound a client can ask for, so the cost is source load and never correctness. §16.2 permits exposing the DA cache source later, so this is a v0 choice rather than a closed question |
| 5.4 | a DataItem is never "defined by itself" | an item addressed without being browsed has no parent, because the adapter does not know where it sits in the source's hierarchy and inventing a place would point clients at the wrong one |

`scripts/spec-check/check.py` carries the Table A.3 row as a recorded deviation:
it is still checked, against the value written here rather than the table's, so
drifting away from the deviation fails as loudly as drifting away from the
specification.

## Value types

`internal/opcua.DataTypeFor` implements Part 8 Table A.2:

| DA VARTYPE | UA DataType |
|---|---|
| `VT_I1` | `SByte` |
| `VT_UI1` | `Byte` |
| `VT_I2` | `Int16` |
| `VT_UI2` | `UInt16` |
| `VT_I4` | `Int32` |
| `VT_UI4` | `UInt32` |
| `VT_I8` | `Int64` |
| `VT_UI8` | `UInt64` |
| `VT_R4` | `Float` |
| `VT_R8` | `Double` |
| `VT_BOOL` | `Boolean` |
| `VT_BSTR` | `String` |
| `VT_DATE` | `Double` |
| `VT_DECIMAL` | `Decimal` |

Two points are easy to get wrong and are called out deliberately:

- **`VT_DATE` maps to `Double`, not `DateTime`.** Table A.2 says `Double`.
- **`VT_ARRAY` has a Table A.2 row** (array of the mapped element type), but the
  DA core decodes no arrays, so the mapping reports arrays and by-reference
  variants as unmapped. Claiming the row would overstate what the adapter can
  carry.

`VT_EMPTY` and `VT_NULL` have no row. The adapter maps them to a DataValue with
no value; this is an adapter decision recorded in ADR-0016.

`VT_INT`, `VT_UINT`, `VT_ERROR`, and `VT_CY` have no row either. They are
reported as **unmapped** and fail explicitly. The DA core happens to decode
`VT_INT` and `VT_ERROR` as `int32`, but borrowing the `VT_I4` row for them would
be an invention, not a mapping.

### A node must not declare one type and deliver another

Two questions are asked about every value — what type its node declares, and
what type its `Variant` carries — and they used to be answered from different
inputs: the declared type from the raw VARTYPE through Table A.2, the `Variant`
from the Go value the DA core decoded. They can disagree, and they did.

`decodeVariant` reads **VT_INT and VT_ERROR out of the same storage as VT_I4**,
and **VT_UINT out of the same storage as VT_UI4**, so all three arrive as an
`int32` or a `uint32`. Table A.2 has no row for any of them. A VT_INT item was
therefore **delivered as an `Int32` by a node that declared itself the abstract
base type** — the server contradicting itself about one value. The write check
had the same split: it refused an `Int32` written to a VT_INT item as a type
mismatch the DA core would never have raised, since `validateWriteValue` groups
those VARTYPEs exactly as the decoder does.

The normalisation is now stated once, as `DAVarType.DecodesAs`, next to the
decoder it describes, and `DataTypeFor` composes with it. That is not coercion:
the adapter is not deciding VT_INT resembles VT_I4, it is reporting the type of
the value it actually produced. A VARTYPE with no Table A.2 row and no
normalisation, such as VT_CY, still has no answer.

An earlier test required VT_INT, VT_UINT and VT_ERROR to fail "rather than
borrow a similar type's mapping". That intent is kept where it applies — nothing
is widened or narrowed, and a mismatched width is still refused — but the three
VARTYPEs are not borrowing anything: the core already read them into the type
being reported.

**VT_DATE and VT_DECIMAL keep their Table A.2 rows and are unreachable.** The DA
core decodes neither, so a source reporting one gets `UNSUPPORTED_VARTYPE`
before the UA layer sees a value. The rows are a faithful transcription and stay;
the limitation is the decoder's. The interop suite deliberately does **not**
script a VT_DATE item, because a client check passing over a path no source can
reach is the one thing a conformance run must not report.

## Quality

A DA quality is 16 bits. Part 8 A.3.2.3 defines the lower byte as `QQSSSSLL`
(main quality, sub status, limit) and the upper byte as vendor specific.

`internal/opcua.StatusCodeForQuality` implements Part 8 Table A.3:

| DA quality (`QQSSSS`) | UA StatusCode |
|---|---|
| `GOOD` | `Good` |
| `LOCAL_OVERRIDE` | `Good_LocalOverride` |
| `UNCERTAIN` | `Uncertain` |
| `SUB_NORMAL` | `Uncertain_SubNormal` |
| `SENSOR_CAL` | `Uncertain_SensorNotAccurate` |
| `EGU_EXCEEDED` | `Uncertain_EngineeringUnitsExceeded` |
| `LAST_USABLE` | `Uncertain_LastUsableValue` |
| `BAD` | `Bad` |
| `CONFIG_ERROR` | `Bad_ConfigurationError` |
| `NOT_CONNECTED` | `Bad_NotConnected` |
| `COMM_FAILURE` | `Bad_NoCommunication` |
| `DEVICE_FAILURE` | `Bad_DeviceFailure` |
| `SENSOR_FAILURE` | `Bad_SensorFailure` |
| `LAST_KNOWN` | `Uncertain_NoCommunicationLastUsableValue` — see below |
| `OUT_OF_SERVICE` | `Bad_OutOfService` |
| `WAITING_FOR_INITIAL_DATA` | `Bad_WaitingForInitialData` |

### The one row that does not follow Table A.3

Table A.3 maps `LAST_KNOWN` to `Bad_OutOfService`, alongside `OUT_OF_SERVICE`.
This adapter maps it to `Uncertain_NoCommunicationLastUsableValue` instead,
because two clauses of Part 8 disagree and only one of them explains itself.

Table 61 says the fieldbus code `Bad_LastKnown` "**shall** be mapped to
`Uncertain_NoCommunicationLastUsable`", and gives the reason: "OPC UA requires
that the Server shall return a Null value when the Severity is Bad."

That reason holds exactly here. `LAST_KNOWN` exists to deliver **the last value
that had good quality**, and a Bad severity means the adapter must drop the
value — so following Table A.3 destroys the one thing the quality is for. A
client would receive a null value and a code saying the source is out of
service, which is not what the source said.

With `Uncertain`, the value survives and the client is told not to trust it as
current, which is what happened.

`OUT_OF_SERVICE` keeps the table's answer. It is a different condition: there is
no last known value to protect, the source is simply not operational. The two
qualities used to be indistinguishable after mapping; they are not any more.

`scripts/spec-check/check.py` records this as a **deliberate deviation** rather
than treating it as agreement. Every other row is still checked against the
table, and this row is still checked against the value written here — drifting
away from the deviation fails as loudly as drifting away from the table.

The DA limit field transfers directly into the UA limit bits: OPC DA and
OPC 10000-4 Table 177 use the same four values (none, low, high, constant). The
adapter sets `InfoType` to `DataValue` only when a limit is actually present, so
a value with no limit keeps the exact published status code.

A `QQSSSS` combination outside Table A.3 falls back to its main quality, so an
unlisted vendor sub status still arrives as good, uncertain, or bad rather than
as an unexpected error.

### Known loss: vendor quality bits

Part 8 A.3.2.3 states plainly that the vendor-specific upper byte **is
discarded**. The DA core preserves it and the HTTP and gRPC frontends expose the
raw 16-bit quality, so nothing is lost before the UA boundary; the loss happens
at the UA mapping and only there.

Per design §35.4 the adapter does **not** invent a custom UA property to carry
the vendor byte. `QualityVendorBits` exists so the discarded byte can be
observed and reported rather than vanishing silently.

## Errors

Per-item DA HRESULTs map to UA status codes by Part 8 Table A.4 (Read) and
Table A.5 (Write). A successful HRESULT is not an error: it maps to `Good`, and
the data condition is carried by the quality instead.

Every row of both tables is bound. A dash means the table has no row for that
direction.

| DA error | HRESULT | UA StatusCode (Read) | UA StatusCode (Write) |
|---|---|---|---|
| `OPC_E_BADRIGHTS` | `0xC0040006` | `Bad_NotReadable` | `Bad_NotWritable` |
| `OPC_E_UNKNOWNITEMID` | `0xC0040007` | `Bad_NodeIdUnknown` | `Bad_NodeIdUnknown` |
| `OPC_E_INVALIDHANDLE` | `0xC0040001` | `Bad_NodeIdUnknown` | `Bad_NodeIdUnknown` |
| `OPC_E_INVALIDITEMID` | `0xC0040008` | `Bad_NodeIdInvalid` | `Bad_NodeIdInvalid` |
| `OPC_E_INVALID_PID` | `0xC0040203` | `Bad_AttributeIdInvalid` | `Bad_NodeIdInvalid` |
| `OPC_E_BADTYPE` | `0xC0040004` | — | `Bad_TypeMismatch` |
| `OPC_E_RANGE` | `0xC004000B` | — | `Bad_OutOfRange` |
| `OPC_E_NOTSUPPORTED` | `0xC0040406` | — | `Bad_WriteNotSupported` |
| `OPC_S_CLAMP` | `0x0004000E` | — | `Good_Clamped` |
| `E_OUTOFMEMORY` | `0x8007000E` | `Bad_OutOfMemory` | `Bad_OutOfMemory` |
| `E_ACCESSDENIED` | `0x80070005` | `Bad_OutOfService` | — |
| `DISP_E_TYPEMISMATCH` | `0x80020005` | — | `Bad_TypeMismatch` |
| `DISP_E_OVERFLOW` | `0x8002000A` | — | `Bad_OutOfRange` |
| Others | | `Bad_UnexpectedError` | `Bad_UnexpectedError` |

Three things about this table are deliberate.

`OPC_E_INVALID_PID` is answered differently in each direction —
`Bad_AttributeIdInvalid` when reading, `Bad_NodeIdInvalid` when writing. The
asymmetry is the specification's, not a transcription slip: A.4 and A.5 give
that code different answers, and each direction follows its own table rather
than being reconciled into one.

`OPC_S_CLAMP` is a **success** code. It is answered before the general success
test, because the write did happen — the source simply stored a value other
than the one asked for, and reporting a plain `Good` would lose that.

The tables spell the same DA error two ways: `OPC_E_BADRIGHTS` in A.4,
`E_BADRIGHTS` in A.5, and neither spelling is reliably the one `opcerror.h`
uses. The names above are `opcerror.h`'s, which is where the values come from.

### Which rows this adapter can produce at all

Binding a row and being able to reach it are different questions, and the tables
answer only the first. Several rows cannot be produced by any client request,
because of decisions this project made on purpose:

| Row | Reachable through this adapter |
|---|---|
| `OPC_E_BADRIGHTS` | yes — a Write to an item the source does not permit |
| `OPC_E_UNKNOWNITEMID` | yes — an ItemID the source does not have |
| `OPC_E_INVALIDITEMID` | yes, if the source distinguishes malformed from absent |
| `OPC_E_RANGE`, `OPC_S_CLAMP` | yes, if the source enforces or clamps a range |
| `OPC_E_BADTYPE`, `DISP_E_TYPEMISMATCH`, `DISP_E_OVERFLOW` | **no** — ADR-0004 requires the requested VARTYPE to equal the canonical one and answers `TYPE_MISMATCH` itself, so no conversion is ever asked of the source |
| `OPC_E_INVALIDHANDLE` | **no** — item handles are the adapter's, and a client never supplies one |
| `OPC_E_INVALID_PID` | yes, if the source distinguishes an unknown property identifier — reachable since the adapter reads item properties |
| `OPC_E_NOTSUPPORTED` | **no** — a 2.05a Write carries a value only, never a quality or timestamp |
| `E_OUTOFMEMORY` | only under real memory exhaustion |
| `E_ACCESSDENIED` | activation-level on this source, not per item |

The unreachable rows stay bound. They are the specification's table, and each
becomes reachable the moment the decision behind it changes — relaxing strict
typing, or implementing Table A.1. A row that is bound and unreachable costs
nothing; a row that is reachable and unbound is what produced
`Bad_UnexpectedError` for a condition the table names precisely.

### What a real server has been made to produce

`internal/validation/daerrorprobe` runs in the real-DA validation against the
OPC Foundation fixture. For each row it either provokes the condition and
records the exact HRESULT the source returned, or records why the row cannot be
reached — and for a row it does observe, it feeds that real HRESULT to the real
mapping function and requires the table's answer. Feeding the mapping a constant
typed in by this project would prove only what it already assumed.

`docs/compatibility.md` carries the recorded result. `scripts/spec-check/check.py`
is the other half: it re-reads both tables from the OPC Foundation's published
Part 8 export and fails if any row drifts from them, and it checks each numeric
value against `opcerror.h` — for the four Windows codes, against
`golang.org/x/sys/windows`.

## Item properties: Table A.1

Part 8 Table A.1 maps the OPC COM DA item properties onto UA attributes and
properties. Nine rows are implemented in full. The tenth — "Other Properties" —
is implemented for scalar properties, with two of A.3.1.4's rules unapplied;
what that leaves out is set out below. They do not all become property nodes,
because the table does not ask them to.

| DA property | UA target | UA type |
|---|---|---|
| Access Rights (5) | `AccessLevel` attribute | Byte |
| Item Description (101) | `Description` attribute | LocalizedText |
| EU Units (100) | `EngineeringUnits` property | `EUInformation` |
| High EU (102) + Low EU (103) | `EURange` property | `Range` |
| High IR (104) + Low IR (105) | `InstrumentRange` property | `Range` |
| Close Label (106) | `TrueState` property | LocalizedText |
| Open Label (107) | `FalseState` property | LocalizedText |

**Access Rights is satisfied from a better source than the table names.**
`OPCITEMRESULT.dwAccessRights` from `AddItems` already carries it, so the value
is known without asking for the property, and nothing changed there.

**Two rows share one target twice.** High and Low EU both map to `EURange`, and
the two Instrument Range rows both map to `InstrumentRange`. A single property
cannot hold two Doubles as a scalar, so each pair becomes the UA `Range`
structure those BrowseNames are defined to carry — which is also what a client
reading `EURange` expects to decode. A Range is claimed only when the source
offers **both** ends: half a range is not a range, and supplying the other end
would be inventing a number the source never gave.

Offering a bound and then answering it with nothing is a different thing, and
clause 5.6.2 says what to do: "If a limit is not known a NaN shall be used." A
source that answers and gives nothing has told us the limit is unknown, so that
end becomes `NaN` and the end it did give survives. A source that **refuses** a
bound has not said the limit is unknown — it has failed to answer — and the
property carries that failure rather than reporting an unknown limit that was
never claimed.

**The UA types above are not Table A.1's third column.** A.1 says `String` for
EU Units, Close Label and Open Label. Those values land on properties the
standard `AnalogItemType` and `TwoStateDiscreteType` define as `EUInformation`
and `LocalizedText`, which is what A.3.1.3 assigns them to — so A.1's column is
the DA value's mapped type, not the UA property's. That reading is worked
through below, under the VariableType.

### Table A.1's "Other Properties" row

Everything a source offers that the nine named rows do not claim becomes a
Variable of `PropertyType`, following A.3.1.4:

- the DA property's **description** is the BrowseName and DisplayName; a
  property offered without one is named from its identifier, because a node
  needs a name and that is the only other thing that names it;
- its **PropertyID** is part of the NodeId — the identifier rather than the
  description, because a description is the server's prose and can change
  without the property changing;
- its **DataType** comes from its own VARTYPE through Table A.2;
- `ValueRank` is `Scalar`;
- `AccessLevel` is readable, and writable when the source gives the property its
  own ItemID.

This is what a client browsing a real source actually finds: Scan Rate, EU Type,
and whatever a vendor adds. Access Rights, Item Description and **Scan Rate**
are not among them: those map onto attributes, and exposing them as properties
as well would answer the same question twice. Nor are Value, Quality and
Timestamp, which belong to Read and Subscribe.

**A property the source also exposes as an item of its own is writable.**
A.3.1.4 says so, and `IOPCItemProperties::LookupItemIDs` is what answers it. The
adapter asks during discovery — a source that implements the interface without
that method answers `E_NOTIMPL`, which means no property has one, not that
anything failed.

The access level says writable **only because a Write to such a node really
reaches that item**. Reporting a node writable and then refusing the write would
be worse than reporting it readable, so the two are kept together: the write
goes to the property's own ItemID, not to the item it describes.

Such a property still cannot be **monitored**. A DA group notifies on item
values and this is an item, so it could be — but the refusal in the previous
section is by node kind, and lifting it for one kind of property without a way
for a client to learn the underlying ItemID would be a half-measure.

**One of A.3.1.4's rules remains unapplied, and it is a limit of this adapter,
not of the source.**

An **array-valued property is not exposed at all**. A.3.1.4 would have it
carried with `ValueRank` `OneOrMoreDimensions`. The DA layer does not carry
array VARIANTs at all, so such a node could be browsed and never read, and a
property that exists and cannot answer is worse than one that is absent. That is
the honest handling of the limitation, not a reading of the clause: **a source
whose items carry array properties gets less than A.3.1.4 describes.** EU Info
is the property this excludes on a real source, and closing it means array
support in the DA layer, not a change here.

**A property belongs to the type its item was given.** `EngineeringUnits` and
`InstrumentRange` exist on an analog item; `TrueState` and `FalseState` on a
two-state discrete one. An item that is neither has neither, however many DA
properties it happens to offer.

### Attributes A.3.1.3 assigns, beyond Table A.1

A.3.1.3's common mappings name three things Table A.1 does not:

| From the source | UA attribute |
|---|---|
| the item's access rights (from `AddItems`) | `AccessLevel`, and the same value for `UserAccessLevel` |
| the DA **Scan Rate** property | `MinimumSamplingInterval` |
| whether the value is an array | `ValueRank` |

`MinimumSamplingInterval` is read when the source's Scan Rate is, at discovery.
An item whose Scan Rate the source has not stated reports **no interval at
all** rather than zero: OPC 10000-3 reads zero as "the server samples as fast as
possible", which would be a claim about the source that nobody made.

Every node derived from the source reports `ValueRank` `Scalar`, because the DA
layer carries no arrays. That is the same limit recorded under the property row
above. The two nodes that do not are `ServerArray` and `NamespaceArray`, which
OPC 10000-5 defines as one-dimensional and which describe this server rather
than anything in the source.

### The SemanticsChanged bit

Clause 5.2: a server that implements Data Access **shall** set the StatusCode's
`SemanticsChanged` bit in notifications when certain property values change, so
a client re-reads the metadata before it trusts the value. OPC 10000-4 puts the
bit at **14:14**, which `scripts/spec-check/check.py` reads from Part 4 rather
than taking on trust.

Which properties count is stated per VariableType, and for the types this
adapter claims it is exactly four: `EURange` and `EngineeringUnits` from
BaseAnalogType, `TrueState` and `FalseState` from 5.3.3.2. **`InstrumentRange`
is not one of them** — it appears only in `ArrayItemType`'s list, which this
adapter never claims, and sweeping it in would report a semantic change the
specification does not.

The bit goes on **one** notification per monitored item, which is what the
clause asks. A monitored item created after a change is not told about it; a
change is not a change for an item that never saw the value before it.

The bit never appears on a Read. Clause 5.2 is explicit: it "has meaning only
for StatusCodes returned as part of a data change Notification or the
HistoryRead. StatusCodes used in other contexts shall always set this bit to
zero."

**What is detected, and what is not.** The adapter notices a change when it
reads a property, which is when a client asks for one. A source whose
`EURange` changes while nobody reads it is not detected, and no notification
carries the bit. Full compliance means polling the semantic properties of every
subscribed item, which is source load the adapter does not currently impose;
that is a decision with an operational cost rather than an oversight, and it is
recorded here rather than left to be discovered.

To notice a change at all the adapter remembers what each semantic property
last said. That remembered value is **never served** — a property is read from
the source every time a client asks — and exists only to compare.

### Nothing is cached, and nothing is asked for early

A property node's value is read from the source **every time a client asks**.
The address space stores which properties an item has — that is structure — and
never serves what they say. It does remember what the four semantic properties
last said, so that a change can be noticed for the `SemanticsChanged` bit; that
remembered value is compared and never handed to anybody. A property this adapter remembered would be one it could
still be serving after the source stopped reporting it.

The same applies to the `Description` attribute: the node records only that the
source offers Item Description, and the text is read when it is asked for. An
item whose source offers no description keeps answering `Bad_AttributeIdInvalid`,
which is the correct answer for an attribute a node does not have.

Which properties an item has is discovered when a client **browses** that item,
which is the moment it asks what the item has. An item nobody browses costs no
call to the source, and concurrent browses of the same item wait on one call
rather than queueing several identical ones on the DA thread.

A per-property HRESULT is a result rather than a failure, so it maps through
Table A.4 like every other read error: a source that refuses one property
answers `Bad_NotReadable` for that property alone.

### A property is read, never monitored or written

A Table A.1 property node carries the **ItemID of the item it describes**, which
is what lets it be addressed without browsing. That also meant every path which
turned a node into a DA item had to exclude it, and two did not:

- **Monitoring** one would have subscribed to the item's value and delivered a
  process value under the property's client handle — a live reading reported as
  an engineering range. It now answers `Bad_NotSupported`. OPC DA has no change
  notification for item properties: a group notifies on item values only, so
  monitoring one would mean the adapter inventing a sampling loop the source
  never agreed to. A client that wants a property reads it.
- **Writing** one answers `Bad_NotWritable`. A property node is created
  read-only, so the access-level check refused it already, but that was a side
  effect of how the node was built rather than a rule. The rule is now stated: a
  property describes an item and is not a place to put a value.

The root of both was that "is this a DA item?" was re-derived at each call site
as `Class == Variable && ItemID != ""` — which a property node satisfies. It is
now asked once, by `AddressSpace.ResolveNode`, which answers with what the node
actually is: a DA item, one of its properties, something else in the address
space, or nothing. Read, Write, Subscribe and Browse all go through it, so a
fifth caller inherits the distinction rather than having to remember it.

### A property set that does not fit is refused, not trimmed

The address space has a node budget, and a property set is checked against it
**as a whole, before anything changes** — the same rule a browsed branch
follows, and for the same reason. A client cannot tell a truncated property list
from a complete one, so attaching three of five and stopping would be reporting
an answer that looks finished.

A refusal is also not remembered. The discovery is not recorded, so the next
browse asks again rather than serving the truncation for the length of the
refresh interval.

There is no unlimited budget. A non-positive one creates nothing, so a caller
that forgets to pass one is refused rather than quietly allowed to grow the
address space without bound. Browse does not resolve at all: it asks what a node
**is**, which never creates, because Browse has no budget to pass and a client
browsing ItemIDs it invented would otherwise add a node for each.

### The DA-native frontends expose the properties themselves

Table A.1 is the OPC UA frontend's mapping. The gRPC and HTTP frontends are
DA-native and expose the two DA operations directly instead —
`AvailableItemProperties`/`ItemProperties` and `POST /v1/properties/available`
and `/v1/properties` — reporting the source's own property identifiers,
description text, VARTYPEs and HRESULTs without mapping them onto anything.

That is not duplication: a DA client asking what a source offers wants
`PropertyID 102`, and a UA client wants `EURange`. Both frontends publish
`capabilities.properties`, so a client told a source supports item properties
can now act on it whichever frontend it speaks.

### A source without the interface

`IOPCItemProperties` is optional. A source that does not implement it is working
correctly and simply has no properties to offer: `capabilities.properties`
reports `unsupported`, browsing its items succeeds with the references they do
have, and no property node is created. The answer is recorded once rather than
re-asked for every browse of every item.

### Branches, leaves, and the references between them

Annex A.3.1.2 is specific about the hierarchy a wrapper builds. A DA branch is
an Object of `FolderType`, and it references **child branches with `Organizes`
and DA leaves with `HasComponent`**.

The adapter used `Organizes` for both. A client that filters a Browse by
reference type — which is what a Part 8-aware client does — asked for
`HasComponent` and found **no items at all**. Both types are subtypes of
`HierarchicalReferences`, so a client that did not filter never saw the
difference, which is why it went unnoticed.

A.3.1.2 also says the root branch "should be represented by an Object where the
BrowseName is the Server ProgId". This adapter names it from
`OPCDA_OPCUA_SOURCE_FOLDER`, default `Source`. That is a deliberate deviation
from a *should*: the source may be configured by CLSID, in which case there is
no ProgID to use, and an operator who wants the clause's behaviour can configure
it. It is recorded here rather than left for a client to discover.

### The VariableType, from Annex A.3.1.3

A DA item's UA VariableType is chosen from the properties its source offers, as
A.3.1.3 prescribes. The adapter used to give every item `BaseDataVariableType`,
which appears nowhere in Annex A.

| The item has | and its DataType is | VariableType | Carrying |
|---|---|---|---|
| High EU **and** Low EU | a `Number` | `AnalogItemType` | `EURange`, plus `EngineeringUnits` and `InstrumentRange` when offered |
| Close Label **and** Open Label | `Boolean` | `TwoStateDiscreteType` | `TrueState`, `FalseState` |
| anything else | anything | `DataItemType` | — |

The DataType column is not decoration. Clause 5.3.2.3 gives `AnalogItemType` the
DataType `Number` and 5.3.3.2 gives `TwoStateDiscreteType` the DataType
`Boolean`. A DA source can perfectly well put High and Low EU on a string item
or state labels on an integer; claiming the type there would make the node
contradict its own type definition, which is the defect this adapter already
refuses to commit when a node's declared type and delivered value disagree.

An item whose DataType the source has not stated yet is not promoted either.
Promoting on a guess is the same contradiction arriving later.

The type is chosen when the properties become known, which is when a client
browses the item. Until then an item sits at `DataItemType`, the floor A.3.1.3
sets — including an item addressed without being browsed at all, which for a
source that does not implement Browse is the only type it ever has.

A claimed type and its mandatory property never disagree. Re-attaching
recomputes the type and replaces the property set together, under one lock, so
an item can change type between browses but is never briefly an
`AnalogItemType` without an `EURange`.

**Two departures from the clause, both because a type is a promise.**

A.3.1.3 says an item is `AnalogItemType` if it has High and Low EU **or** its EU
Type is Analog. Clause 5.3.2.3 makes `EURange` *mandatory* on that type. An item
whose EU Type is Analog but which offers neither bound has no range to publish,
so claiming the type would promise a property the adapter knows it cannot
supply. Such an item is given `DataItemType`.

`MultiStateDiscreteType` is never claimed. Its mandatory `EnumStrings` comes
from EU Info, whose DA value is an array of strings, and the DA layer does not
carry array VARIANTs — so the promise could never be kept.

### A standard property's name belongs to the standards body

OPC 10000-3 8.3: "if they want to provide a standard Property, its BrowseName
shall have the namespace of the standards body although the namespace of the
NodeId reflects something else, for example the local Server."

`EURange`, `InstrumentRange`, `EngineeringUnits`, `TrueState` and `FalseState`
are the OPC Foundation's own properties of `AnalogItemType` and
`TwoStateDiscreteType`, so their BrowseNames are in **namespace 0**. Only their
NodeIds are this server's, which is exactly the split 8.3 describes. A client
following the standard VariableType model looks for `0:EURange` and would not
find one named anywhere else.

A vendor property from Table A.1's "Other Properties" row keeps **this
adapter's** namespace, because its name is a description the source chose rather
than a name any standards body defined. That is also what keeps a vendor
property described "EURange" distinct from the standard one: 8.3 says the
namespace "is provided to make the BrowseName unique in some cases in the
context of a Node (e.g. Properties of a Node)".

Two vendor properties described identically are told apart by their property
identifier, which A.3.1.4 keys them by: `Loop Gain (5001)` and `Loop Gain
(5002)`. Clause 5.6.3 requires that "the BrowseName of a Property shall be
unique in the context of the Node containing the Properties", and nothing stops
a source describing two of an item's properties the same way. The description is
kept alongside the identifier rather than replaced by it, because it is still
what the source called the property.

### The property types are the standard types', not Table A.1's column

Table A.1 gives `String` as the "OPC UA DataType" for EU Units, Close Label and
Open Label. A.3.1.3 assigns those same values to `EngineeringUnits`, `TrueState`
and `FalseState` — properties the standard VariableTypes define as
`EUInformation` and `LocalizedText`.

Reading A.1's third column as **the DA value's mapped type** rather than the UA
property's reconciles the two, and A.3.1.3 is the clause that forces the
reading. This adapter followed A.1 literally at first; it now follows A.3.1.3.

`EngineeringUnits` carries the DA unit string as the `DisplayName` of an
`EUInformation` — which is what `DisplayName` is for: clause 5.6.4.3 calls it
"typically the abbreviation of the engineering unit", and an abbreviation is
what DA supplies.

The other three fields say, precisely, that nothing more is known.
`NamespaceUri` is empty because no organization defined this unit — a DA source
handed over a bare string. `UnitId` is **−1**, which 5.6.4.3 defines as the
value for "a unitId is not available"; zero would say *unit zero*, and zero is
a number a code set may legitimately use. `Description` is empty because it
holds the unit's full name and DA gave an abbreviation.

Deriving a UNECE code from a unit's name would be inventing an identity the
source never gave, which a client reading `UnitId` would then act on.

## Subscriptions: the one filter a DA group can apply

A.3.5 says a wrapper uses the SamplingInterval and the Deadband to set up the
DA callback, and that **only `PercentDeadbandType` is supported**. The DA core
has always carried a percent deadband — it is `AddGroup`'s `pPercentDeadband`,
and the gRPC frontend has always exposed it. Only the UA frontend refused it,
along with every other filter.

A `DataChangeFilter` asking for a percent deadband is now accepted and passed to
the group. Everything else is refused rather than accepted and quietly not
applied:

| The client asks for | Answer |
|---|---|
| no filter | accepted; the group gets no deadband |
| `DataChangeFilter`, deadband `None` or `Percent` in 0–100 | accepted |
| `DataChangeFilter`, deadband `Percent` outside 0–100 | `Bad_DeadbandFilterInvalid` |
| `DataChangeFilter`, deadband `Percent` on an item with no `EURange` | `Bad_DeadbandFilterInvalid` — Table 61 names this case too |
| `DataChangeFilter`, deadband `Absolute` | `Bad_MonitoredItemFilterUnsupported` — `AddGroup` takes a percentage and nothing else |
| trigger `StatusValueTimestamp` | `Bad_MonitoredItemFilterUnsupported` — DA notifies on a change of value or quality, never on a timestamp alone |
| any other filter | `Bad_MonitoredItemFilterUnsupported` |

The status changed with the behaviour. A filter this server cannot perform is
`Bad_MonitoredItemFilterUnsupported`; `Bad_FilterNotAllowed`, which the adapter
used to answer, means the filter cannot be used with the attribute — and a
`DataChangeFilter` on a Value attribute plainly can.

**A percent deadband needs an `EURange`.** Clause 7.2 defines the value as "the
percentage of the EURange. That is, it applies only to AnalogItems with an
EURange Property". An item that is not an `AnalogItemType` has no range to take
a percentage of, so the filter has no defined meaning there and is refused with
`Bad_DeadbandFilterInvalid` — which Table 61 defines as covering both "not
between 0.0 and 100.0" **and** "a PercentDeadband is not supported, since an
EURange is not configured". Passing it to the DA group anyway would apply a
percentage of nothing. A filter asking for **no** deadband is accepted
on any item, because there is no percentage in it.

**A deadband belongs to the subscription, because it belongs to the DA group.**
One UA subscription is backed by one DA group, and a group has exactly one
`pPercentDeadband`. A second monitored item asking for a different deadband
cannot be honoured, so it is refused; applying somebody else's deadband to it
would report changes the client did not ask to hear about, or hide ones it did.

### Disabled publishing holds the newest value, it does not bank them

Clause 5.14.1.1: disabling "causes the Subscription to cease sending
NotificationMessages to the Client", while the subscription "continues to execute
cyclically and continues to send keep-alive Messages". The monitored items keep
sampling throughout — `MonitoringMode` is a separate setting — so something has
to hold what they produce.

What holds it is each item's own slot, which is the queue of one it was promised.
A disabled subscription therefore accumulates nothing to send: when publishing
resumes, each item reports once, with the newest value it saw. Banking every
value instead would grow the send queue for as long as the client left publishing
off, and then hand it a history it had explicitly asked not to receive.

### Republish, because the queue is already there

Every Publish response carries its subscription's available sequence numbers,
and 5.14.1.1 says what a non-empty list of them means: that the server "supports
a retransmission queue and acknowledgement of NotificationMessages", and that
clients "are required to acknowledge NotificationMessages as they are received".
A server that answers those numbers and then refuses to act on them is
advertising a queue its clients cannot reach.

So Republish answers from that queue. 5.14.6.1: "this Service requests the
Subscription to republish a NotificationMessage from its retransmission queue.
If the Server does not have the requested Message in its retransmission queue, it
returns an error response." A message that is not there — never sent, already
acknowledged, or dropped when the queue overflowed — is `Bad_MessageNotAvailable`,
and a subscription that is not this session's is `Bad_SubscriptionIdInvalid`,
which are the two codes Table 93 defines.

Republishing does not consume the message. Only an acknowledgement removes one,
and a client that had to ask for a message again has not acknowledged it.

### The retransmission queue is bounded, and drops its oldest

Clause 5.14.1.1 gives a session a retransmission queue "of at least two times
the number of Publish requests per Session the Server supports", and says what
happens when it fills: "in the case of a retransmission queue overflow, the
oldest sent NotificationMessage gets deleted".

This server answers one Publish per session at a time, so two is the floor, and
the queue holds far more than that. It has to be bounded either way: the queue
only shrinks when a client acknowledges, and a client that publishes without
ever acknowledging would otherwise hold every DataValue the server ever sent it.

Order is tracked rather than inferred from the sequence number, because that
number does not reset — 5.14.1.1 has it run past four billion before it repeats,
so comparing two of them says nothing about which was sent first once it wraps.

A client that acknowledges a message the queue has already dropped is told
`Bad_SequenceNumberUnknown`, which is the answer for a sequence number the
server is not holding.

### A subscription's lifetime is enforced, not merely reported

Table 82: "when the publishing timer has expired this number of times without a
Publish request being available to send a NotificationMessage, then the
Subscription **shall be deleted** by the Server", and 5.14.1.1 adds that
"closing the Subscription causes its MonitoredItems to be deleted".

For this adapter that is not bookkeeping. A subscription holds a DA group open
on the source, so a client that stops publishing while keeping its session alive
would otherwise hold that group for as long as it liked. The revised lifetime
count is a promise about when the server will give up on a client, and a server
that reports one it does not act on has told the client something untrue.

The counter resets on "any Service call that uses the SubscriptionId or the
processing of a Publish response". Every such call goes through one lookup, and
the reset lives there rather than at each call site, so a service added later
cannot forget it. An outstanding Publish keeps every subscription in its session
alive, not only the one that cycle happens to pick: 5.14.1.1 counts cycles "in
which there have been no Publish requests available", and while a client is
waiting on one, none of its subscriptions is starving.

Two parts of the clause are departures.

5.14.1.1 also says the server "shall issue a StatusChangeNotification
notificationMessage with the status code Bad_Timeout". This adapter does not:
the condition for expiry is that no Publish request was available, so in the
ordinary case there is nowhere to send it.

Clause 5.7.2.1 says that when "a Server terminates a Session for any other reason,
Subscriptions associated with the Session are not deleted", so they can be
transferred to a new session before their lifetime runs out. This adapter
deletes them, because it does not implement TransferSubscriptions — a
subscription kept past its session would hold a DA group open with nothing able
to reach it, which is the leak the rule exists to avoid, arrived at from the
other side.

### An item nobody may read is still created

OPC 10000-4 5.13.2.1: "When a user adds a monitored item that the user is denied
read access to, the add operation for the item shall succeed and the bad status
Bad_NotReadable or Bad_UserAccessDenied shall be returned in the Publish
response." Table 65 agrees by omission — `Bad_NotReadable` is not among the
operation level result codes CreateMonitoredItems may return.

So a DA item whose access rights carry no read bit is created, and its status
arrives through Publish. The status is reported once: a monitored item reports
changes, and this one has nothing further to say. The item stays out of the DA
group, because there is nothing for the group to read and a source that refuses
`AddItems` for it would fail the whole rebuild, taking every readable item in the
same request down with it.

The rest of the clause is a departure. "If the access rights change to read
rights, the Server shall start sending data for the MonitoredItem" — this adapter
will not notice. It learns an item's access rights once, from the browse entry or
the first read that reports them, and does not revise them afterwards, so an item
that becomes readable stays silent until the client creates it again. Noticing
would mean re-reading rights on a schedule the source never agreed to.

### The sampling interval a monitored item is promised

A DA group has one update rate for every item in it, and OPC UA gives each
monitored item its own sampling interval. The two are reconciled by treating the
group's revised rate as a floor and pacing anything slower.

OPC 10000-4 7.21 requires that "the Server shall always return a
revisedSamplingInterval that is equal or higher than the requested
samplingInterval". An item asking for something faster than the group runs at
cannot be given it, so it is told the group's rate -- which is higher, as the
rule requires. An item asking for something slower keeps its own interval and is
paced to it: it is not handed everything the group delivers, because reporting
an interval and then sending five times as much would break the promise in the
other direction.

The two special values are read as 7.21 defines them. Zero "indicates that the
Server should use the fastest practical rate", which here is the group's. "Any
negative number is interpreted as -1", which asks for the subscription's
publishing interval.

Clause 5.13.1.2 puts a second floor under it: "if the Server specifies a value
for the MinimumSamplingInterval Attribute it shall always return a
revisedSamplingInterval that is equal or higher". That attribute carries the
source's DA Scan Rate, so it is the source's own statement about how often the
item can change, and promising anything faster would promise something the
source has already said it will not do.

### The queue holds one value, which is what the source offers

Every monitored item reports a `revisedQueueSize` of 1, which 7.21 permits: "0
or 1 -- the Server returns the default queue size which shall be 1 ... The queue
has a single entry, effectively disabling queuing."

That is not a limitation this layer imposes; it is the shape of the source. A DA
group's pending set holds one value per item, because between two update-rate
ticks a DA server reports only the latest cache value. There is no second value
to queue.

Clause 5.13.1.5 describes exactly this case: "if the queue size is one, the
queue becomes a buffer that always contains the newest Notification ... The
discard policy is ignored if the queue size is one." So `discardOldest` is
accepted and has no effect, and the Overflow bit is never set -- 5.13.1.5 sets it
only "if a Notification is discarded for a DataValue and the size of the queue
is larger than one".

A paced item holds its newest value rather than dropping values as they arrive,
for the same reason: a queue of one holds the newest, and dropping instead would
leave a client that asked for a slow rate stuck on a stale value whenever the
source went quiet. It also keeps its place while it waits: a DA subscription
drains "preserving first-seen order", which is the source's own account of what
changed first, and pacing carries that order through rather than reordering
around it.

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
`cache` source and says that exposing it later "scope 위반이 아니다" — what INV-6
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

## Wire encoding

`internal/opcua/binary.go` implements the UA Binary encoding of the built-in
types from **OPC 10000-6 clause 5.2**: little-endian integers and
floating-point values, `Int32` length prefixes where `-1` means null and `0`
means empty, `Guid` as `UInt32`/`UInt16`/`UInt16`/`Byte[8]`, one-dimensional
arrays preceded by an `Int32` element count, and `DateTime` as 100 nanosecond
intervals since 1601-01-01 UTC with the clause's saturation rules at both ends.

Those ends are where 5.2.2.5 puts them, not where the arithmetic happens to run
out. Anything "equal to or earlier than 1601-01-01 12:00AM UTC" encodes as 0 and
anything "equal to or greater than 9999-12-31 11:59:59PM UTC" encodes as the
`Int64` maximum — the boundary instants included, which a threshold set a tick
either side would quietly exclude. Go can represent instants well beyond the year
9999, so it is the clause's boundary that binds here rather than the platform's.

Both bounds round-trip exactly, which they have to: the clause closes by calling
them "invalid date/time values" that applications "should treat as such", and an
application cannot recognise a sentinel the encoder and decoder disagree about.
A wire value between the upper bound and the maximum can only arrive from
another implementation, never from this encoder, and decodes to the bound.

Booleans are written as `1` for true and any non-zero byte decodes as true, and
NaN is normalised to an IEEE quiet NaN, both as the clause requires.

### Picoseconds are dropped at a saturated timestamp

Clause 5.2.2 requires that "the Picoseconds shall be set to 0 when the DateTime
value is `DateTime.MinValue` or `DateTime.MaxValue`", and an encoded DataValue
here omits them there. Those two values are sentinels for "outside the
representable range" rather than instants, so a fraction of a tick past one says
nothing — and saying it would suggest the timestamp were exact when it is the
opposite.

Nothing this adapter reads from a DA source carries picoseconds, so the rule
bites only on a DataValue assembled with them. It is applied where it is stated
rather than where it happens to matter.

### An array shape the decoder was told to reject

Nothing here consumes an array Variant — the adapter carries no arrays — but the
decoder still has to walk one to find the end of it, and OPC 10000-6 Table 26
gives it two rules while it does: "all dimensions shall be specified and shall be
greater than zero", and "if ArrayDimensions are inconsistent with the ArrayLength
then the decoder shall stop and raise a `Bad_DecodingError`". Both are applied. A
decoder that accepts a shape it was told to reject is one that answers a message
it should have refused.

The `ArrayDimensions` flag on a scalar Variant is refused outright. Table 26
gives the field to arrays alone, and reading the flag without reading the field
would leave its bytes in the stream for the next field to take as its own —
turning one malformed Variant into a whole message decoded as something else.

### Null is not empty

`-1` and `0` are **different values**, and a field that is simply not present is
written as null rather than as a zero-length string. This is easy to get wrong
in Go, where the zero value of a `string` is `""` and both readings collapse to
the same thing, and easy to miss in testing, because this project's decoder
treats null and empty alike.

It is not cosmetic. OPC 10000-4 Table 192 says `issuedTokenType` "may only be
specified if TokenType is ISSUEDTOKEN", so writing an empty one on an
`ANONYMOUS` policy specifies a field the clause forbids. The OPC Foundation's
own .NET stack refused the endpoint outright with `Bad_IdentityTokenInvalid`
until this was fixed, while two other third-party clients tolerated it — a
reminder that "it works with the client I tried" is not the same as correct.

### Bounds

Design §35.5 requires the hand-written parser to bound what a peer can make it
do. `BinaryLimits` carries the message, String, ByteString, array, and nesting
bounds. The decoder validates every declared length against **both** its
configured bound and the bytes actually remaining before it allocates or
advances, so a hostile length prefix cannot drive an allocation from a small
message. Any negative length other than `-1` is malformed, including
`math.MinInt32`.

OPC 10000-6 5.1.8 requires decoders to support at least 100 nesting levels and
to report an error beyond what they support; the nesting bound is therefore
floored at 100 and cannot be configured below it.

Failures carry a UA status code a peer can be told: `Bad_DecodingError` for
malformed input, `Bad_EncodingLimitsExceeded` when a declared length or nesting
level exceeds a bound, and `Bad_EncodingError` for a caller's own encoding
mistake.

`FuzzDecodeUABinary` and `FuzzDecodeDateTime` run in CI with the same
deterministic execution budget as the existing fuzz targets.

## Connection protocol framing

`internal/opcua/uacp.go` implements the OPC UA Connection Protocol framing from
**OPC 10000-6 clause 7.1**: the 8 byte header of a three byte ASCII
`MessageType`, a reserved/`IsFinal` byte, and a `UInt32` `MessageSize` that
**includes the header itself**, plus the `Hello`, `Acknowledge`, and `Error`
bodies of Tables 74, 75, and 76.

Clause 7.1.2.2 requires the connection protocol layer to verify the message type
and that the size fits the negotiated receive buffer **before** anything reaches
the SecureChannel layer, so the decoder refuses an out-of-range size without
reading a byte of body. `HEL`, `ACK`, `ERR`, and `RHE` are owned by this layer;
`MSG`, `OPN`, and `CLO` are framed and passed through, which is why the header
layout is deliberately identical to the first eight bytes of the secure
conversation header.

### `MaxMessageSize` and `MaxChunkCount` mean opposite things in each direction

They appear in both the Hello and the Acknowledge with the same names and
different subjects, which is the trap.

**Table 74**, in the Hello, makes them the client's bound on **responses**:
`MaxMessageSize` is "the maximum size for any response Message", and the server
"shall return an Error Message with a `Bad_ResponseTooLarge` error if a response
Message exceeds this value" when no chunk has been sent. Responses are held to
that number.

**Table 75**, in the Acknowledge, makes them this server's bound on
**requests**: "the maximum size for any request Message" and "the maximum number
of chunks in any request Message". They are the server's own limits, announced,
and incoming requests are held to them.

Neither is a negotiation between the two. Tightening the Acknowledge by the
Hello would tell a client that asking for small responses had shrunk what it is
allowed to send. The buffer sizes *are* negotiated, and Table 75 says how: each
"shall not be larger than" its counterpart in the Hello.

Zero means the side that sent it imposes no bound, which both tables state. A
client that names no response limit gets whole responses, so zero has to keep
meaning "no limit" rather than "nothing may be sent".

### Negotiation

`NegotiateAcknowledge` implements Table 75: the server's receive buffer may not
exceed what the client says it will send, its send buffer may not exceed what
the client can receive, and neither may fall below the 8192 byte floor. The
1024 byte ECC floor is not offered because no ECC security policy is
implemented. A zero `MaxMessageSize` or `MaxChunkCount` means the sender
declared no limit, so the other side's limit is the one that applies.

The protocol version is 0; a Hello asking for a higher version is refused with
`Bad_ProtocolVersionUnsupported`. An `EndpointUrl` that is not under 4096 bytes
is refused with `Bad_TcpEndpointUrlInvalid`, on both the encode and decode
paths, because a peer can send one regardless of what this adapter would emit.

An `Error` message's `Reason` is capped at 4096 bytes. It is **truncated** on
encode rather than suppressed — failing to report an error would be worse than
reporting it with a shorter reason — and truncation never splits a rune. On
decode an over-long reason is dropped, which is what the clause tells a receiver
to do.

### Chunk accounting

A request may arrive in several chunks, which OPC 10000-6 6.7.3 sends
"sequentially" — so one accumulator per connection is enough, and chunks of two
messages never interleave. Each chunk carries its own security and sequence
header and is checked as it arrives, because the sequence number increments per
chunk and 6.7.3 has the receiver "check the security on the abort MessageChunk
before processing it".

`ChunkAccumulator` enforces the chunk count and message size the Acknowledge
announced while a multi-chunk message arrives, refusing a breach **before**
copying anything.

A server may announce no bound at all, which Table 75 allows with a zero
`MaxMessageSize`. That cannot mean an unbounded buffer: a peer would otherwise
decide how much memory this process spends, one intermediate chunk at a time and
never sending a final one. The binary message bound is the ceiling in that case,
and it caps an announced bound larger than itself too — a message past it could
not be decoded even if it were kept, so buffering that far only delays the same
refusal. An abort chunk discards the partial message and leaves the
channel open, which 6.7.3 requires in as many words: the receiver "shall ignore
the Message but shall not close the SecureChannel".

`FuzzDecodeUACP` drives the framing with arbitrary bytes in CI.

## Secure conversation framing

`internal/opcua/uasc.go` implements the secure conversation framing of **OPC
10000-6 clause 6.7**: the 12 byte header of Table 57 (the connection protocol's
first eight bytes plus a `UInt32` `SecureChannelId`), the asymmetric security
header of Table 58 for `OPN`, the `TokenId` header for `MSG` and `CLO`, and the
sequence header of Table 60.

Table 58's length rules are enforced exactly. A length of `0` or `-1` means the
field is not specified; **any other negative length is invalid** and, as the
clause requires, is reported as a security failure rather than tolerated. The
`SecurityPolicyUri` may not exceed 255 bytes, the `SenderCertificate` may not
exceed the size the chunk leaves for it, and a `ReceiverCertificateThumbprint`
is either absent or exactly 20 bytes. No declared length is honoured beyond the
bytes actually present.

`MaxSenderCertificateSize` implements the formula in 6.7.2.3 with the padding
and signature terms as parameters rather than assumptions, so a signed policy
can supply them without the formula being rewritten.

### The policy an OPN names

Clause 6.7.7 says: *"If the Message is the response sent to the Client, then the
SecurityPolicy shall be the same as the one specified in the request."* The
asymmetric security header is the only place an OPN chunk carries the policy, so
the reply **echoes the policy the request named**, and a conforming client that
receives an empty one refuses the channel rather than guess which policy secured
the reply.

The same clause requires the receiver to **verify that it supports the requested
policy**. Exactly one policy is served, so the request is compared against it: a
client asking for a signed and encrypted policy is told it is not served rather
than handed an unsecured channel it never asked for. A request naming *no*
policy is refused too — an unnamed policy cannot be verified as supported, and
the reply could not name what the request did not.

Both were wrong until a third-party client was pointed at the listener. This
project's decoder accepted the empty field its own encoder wrote, which is
precisely what a round trip against itself cannot catch.

### Sequence numbers

`SequenceValidator` enforces 6.7.2.4: the number is incremented by **exactly
one** per chunk, and a wrap is accepted only where the selected rule set allows
it — above `UInt32.MaxValue - 1024` and back below 1024 for the legacy rules, or
at `UInt32.MaxValue` and back to 0 for the zero-based rules.

Which rule set applies is a property of the SecurityPolicy. OPC 10000-7 governs
profiles and does not list the per-policy assignment: its clause 1 puts the
profiles in an online database. So the rule set is a parameter the caller
supplies rather than a value assumed for any policy.

### What is deliberately not bound yet

For the same reason, the `SecurityPolicy` URI strings are not hardcoded. The
framing layer treats `SecurityPolicyUri` as a length-validated opaque string,
which is all Table 58 requires of it. Binding the URI belongs with endpoint
description and `GetEndpoints`, where an operator supplies it from the profile
database — there is no pinned document to check it against.

Only `SecurityMode` `None` is implemented, and `RequireSupportedSecurityMode`
refuses `Sign` and `SignAndEncrypt` with `Bad_SecurityModeRejected` rather than
accepting a channel the adapter would then fail to protect. Per ADR-0016 the
`None` path is for local interoperability work and is never described as
production ready.

`FuzzDecodeUASC` drives this framing with arbitrary bytes in CI.

## SecureChannel token lifecycle

`internal/opcua/channel.go` implements the token lifecycle of **OPC 10000-6
6.7.4** and **OPC 10000-4 7.36**. The clause is explicit that these rules hold
even with `SecurityMode` `None`: the `SecureChannelId` and `TokenId` are still
assigned, the token shall still be renewed before its `RevisedLifetime` expires,
and **receivers shall still ignore invalid or expired TokenIds**. Choosing no
security does not turn the channel lifecycle off.

A renewal does not invalidate the previous token at once. The server keeps
accepting the old token until it expires or until a chunk secured with the new
token arrives, so a client still finishing a renewal is not cut off. Accepting
the new token retires the old one.

`MessageSecurityMode` carries the wire values of **OPC 10000-4 Table 139**:
`Invalid` is 0 precisely so an unset field can never be read as a deliberate
choice of no security, and Table 139 states it "will always be rejected".

### Bounds

`ChannelLimits` bounds the revised token lifetime and the number of concurrent
channels. A requested lifetime is clamped into the configured range and is
always greater than zero, as OPC 10000-4 requires of a server. `ExpireStale`
reclaims channels whose tokens have all expired, so a peer cannot hold slots by
going silent, and a channel is only stale once **every** token it holds has
expired.

Zero is not a valid channel or token identifier, so the counters skip it on
wrap-around. The identifier seeds are supplied by the caller because Table 57
advises that the first `SecureChannelId` after a restart should be unlikely to
collide with one a previously connected client still holds.

`SecurityTokenRequestType`'s wire values are bound: `Issue` 0 and `Renew` 1,
taken from the OPC Foundation UA NodeSet's DataType definition. This section
used to say they were not, on the grounds that the `OpenSecureChannel` body was
not decoded and that `NodeId` and `ExtensionObject` were unimplemented. All
three became untrue, and the paragraph immediately below this one has listed
`NodeId` and `ExtensionObject` as implemented the whole time it stayed.

## Structured types and service headers

`internal/opcua/nodeid.go` implements `NodeId`, `ExpandedNodeId`,
`QualifiedName`, `LocalizedText`, `ExtensionObject`, and `DiagnosticInfo` from
**OPC 10000-6 clauses 5.2.2.9 to 5.2.2.15**, plus the `RequestHeader` and
`ResponseHeader` of **OPC 10000-4 Tables 171 and 172**. Every service body needs
these, and the address space needs `NodeId`.

The encoder picks the most compact `NodeId` form the value fits, which is what
Table 17's two-byte and four-byte encodings exist for, and a plain `NodeId` that
arrives carrying the `ExpandedNodeId` flags is refused rather than having them
ignored — ignoring them would leave the rest of the stream misaligned. When an
`ExpandedNodeId` carries a `NamespaceUri` the `NamespaceIndex` is written as 0,
as 5.2.2.10 requires, because the index is then to be ignored.

The DA frontend will use the string `NodeId` form so an exact DA ItemID is
carried as-is, consistent with the identity rules above.

### A field order that is not the mask order

`DiagnosticInfo` is worth calling out. Table 22's stream order is `SymbolicId`,
`NamespaceUri`, **`Locale`**, **`LocalizedText`** — but the mask bits list
`LocalizedText` as `0x04` and `Locale` as `0x08`. The bits select presence; the
table rows fix the order, and the two disagree. Writing the fields in mask-bit
order would produce a stream that decodes into the wrong fields without any
length error to reveal it.

### Recursion

`DiagnosticInfo` is recursive. OPC 10000-6 5.2.2.12 sets a different bound from
the general nesting rule: decoders shall support at least 4 levels and are not
expected to support more than 10. The codec bounds it at 10 and refuses deeper
input on both the encode and decode paths.

`ExtensionObject` bodies are kept as raw bytes. The clause allows a decoder that
does not recognise the `TypeId` to treat the body as opaque, and this adapter
does not decode structures it has no schema for.

`FuzzDecodeStructuredTypes` drives all of these with arbitrary bytes in CI.

## SecureChannel services

`internal/opcua/service.go` implements the `OpenSecureChannel` and
`CloseSecureChannel` bodies of **OPC 10000-6 Table 64**, plus `ServiceFault`.

**OPC 10000-6 5.2.9**: a message is a structure prefixed by the `NodeId` of its
`DataTypeEncoding`, with **no length field** — unlike an `ExtensionObject`.
Enumerations are encoded as `Int32` (5.2.4). The encoding identifiers come from
the OPC Foundation NodeIds table: `ServiceFault` 397,
`OpenSecureChannelRequest` 446, `OpenSecureChannelResponse` 449,
`CloseSecureChannelRequest` 452, `CloseSecureChannelResponse` 455. A `TypeId`
that is not a standard numeric identifier in namespace 0 is refused with
`Bad_ServiceUnsupported`.

`SecurityTokenRequestType` is now bound — `Issue` 0, `Renew` 1 — from the OPC
Foundation UA NodeSet's `DataType` definition, which closes the gap ADR-0016
recorded. Both enumerations are validated on decode: a value outside the
enumeration is **refused**, not reduced to a neighbouring one, so a malformed
field can never look like a deliberate choice of no security.

`ChannelService` joins the decoded message to the token lifecycle. It checks the
protocol version against the Hello first, as 6.7.4 requires, and a renewal must
name an existing channel and must not change the security mode that channel was
opened with. With `SecurityMode` `None` the server nonce is written as **null**
rather than an empty byte string, so a client cannot read it as a zero-length
random value; 6.7.4 states the nonces are ignored and should be null.

`FuzzDecodeSecureChannelService` drives the bodies with arbitrary bytes in CI.

## The UA-TCP listener

`internal/opcua/listener.go` is the first part of Phase 8 that moves bytes. It
serves the connection sequence of **OPC 10000-6 7.1.3** — Hello, Acknowledge,
`OpenSecureChannel`, `CloseSecureChannel` — for the `None` security path only.
Per ADR-0016 it exists for local interoperability work and is never described as
production ready.

Everything a peer can consume before it has proved anything is bounded: the
concurrent connection count, the pre-negotiation header size, the wait for a
Hello (configurable, capped at the two minutes 7.1.3 allows), and per-message
read and write deadlines. A connection beyond the limit is closed immediately
rather than queued, so a peer cannot make the server hold sockets it will not
serve. A second Hello is an error that closes the connection, as 7.1.3 requires.

Failures that carry a UA status are reported as an `Error` message before the
socket closes; a plain transport failure carries nothing useful to the peer and
is not answered. A `MSG` is answered with `Bad_ServiceUnsupported` rather than
ignored, because no session service exists yet.

An `OpenSecureChannel` that presents a certificate or thumbprint is refused with
`Bad_SecurityPolicyRejected` rather than being silently treated as unsecured.

Sequence numbers are tracked **per direction**: each side of a channel assigns
its own series, so the received and sent numbers are separate counters.

### What writing it caught

The server was writing the `SecureChannelId` twice — once in the 12 byte header
of Table 57, where it belongs, and again at the start of the body. Every unit
test of the framing passed, because the encoder and decoder agreed with each
other. It only surfaced when a client parsed a real frame off a socket, which is
why this slice came before more service logic.

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

## Which services this server answers

Everything else is answered with a `ServiceFault` carrying
`Bad_ServiceUnsupported`, which leaves the channel open so a client can carry on.

| OPC 10000-4 service set | Answered | Not implemented |
| --- | --- | --- |
| 5.5 SecureChannel | `OpenSecureChannel`, `CloseSecureChannel` | — |
| 5.6 Discovery | `GetEndpoints` | `FindServers`, `FindServersOnNetwork`, `RegisterServer`, `RegisterServer2` |
| 5.7 Session | `CreateSession`, `ActivateSession`, `CloseSession` | `Cancel` |
| 5.8 NodeManagement | — | `AddNodes`, `AddReferences`, `DeleteNodes`, `DeleteReferences` |
| 5.9 View | `Browse`, `BrowseNext` | `TranslateBrowsePathsToNodeIds`, `RegisterNodes`, `UnregisterNodes` |
| 5.10 Query | — | `QueryFirst`, `QueryNext` |
| 5.11 Attribute | `Read`, `Write` | `HistoryRead`, `HistoryUpdate` |
| 5.12 Method | — | `Call` |
| 5.13 MonitoredItem | `CreateMonitoredItems`, `DeleteMonitoredItems` | `ModifyMonitoredItems`, `SetMonitoringMode`, `SetTriggering` |
| 5.14 Subscription | `CreateSubscription`, `SetPublishingMode`, `Publish`, `Republish`, `DeleteSubscriptions` | `ModifySubscription`, `TransferSubscriptions` |

The right-hand column is not a to-do list. A DA source has no history, no
methods, no query engine and no nodes a client may add, so 5.8, 5.10, 5.12 and
the two History services have nothing to map onto. `TransferSubscriptions` is a
recorded departure above. What the rest share is that a client can reach the same
end by other means — delete a monitored item and create it again rather than
modify it — at the cost of a round trip.

That last part is the test worth applying. `Republish` was implemented because
the server was already sending `availableSequenceNumbers`, which 5.14.1.1 says
tells a client the retransmission queue exists; refusing to act on those numbers
was the server disagreeing with itself. Nothing in the remaining column is
advertised that way.

## GetEndpoints

`internal/opcua/endpoints.go` implements `EndpointDescription`,
`ApplicationDescription`, `UserTokenPolicy` and the `GetEndpoints` service from
**OPC 10000-4 Tables 135, 109, 192 and 5**, with the `ApplicationType` and
`UserTokenType` values of Tables 111 and 193. The listener answers it on an open
channel: **OPC 10000-4 Table 5 states the authenticationToken is always null and
shall be ignored if provided**, so this service needs no session, which is why it
is the first one served.

Table 5 also fixes the filter's meaning: all endpoints are returned when the
profile list is empty, so a **non-empty** list that does not name this
endpoint's transport profile returns nothing rather than everything.

A service that is not implemented is answered with a `ServiceFault` carrying
`Bad_ServiceUnsupported`, which leaves the channel open — the channel is
healthy, only the request is unsupported. A `MSG` on a channel the server does
not know is refused at the transport instead, because there is no channel to
answer on.

The adapter publishes one endpoint, because it serves one source over one
listener. It carries no certificate, and its `securityLevel` is 0, which Table
135 defines as "not recommended" — an accurate description of an unsecured
endpoint.

### The security policy URI is configuration, not a constant

`EndpointConfig.SecurityPolicyURI` is **required and never defaulted**, and the
same applies to the transport profile URI.

Neither can be transcribed the way every other constant here is. OPC 10000-7 is
the specification that governs profiles, and it does not list them: its clause 1
says "the actual Profiles are maintained in an online database and accessible
via https://profiles.opcfoundation.org/". Nothing in Parts 3, 4, 5 or 6 carries
either URI either, so there is no pinned document to check a transcription
against — which is exactly the condition under which this project does not write
a constant from recollection.

A server that published a wrong policy URI would be unusable by a real client,
so the value is supplied by configuration and an operator takes it from the
profile database rather than from a specification PDF.

What the server does **not** do is verify it. The security mode is fixed to
`None` in code and cannot be misconfigured, but the policy URI is advertised
verbatim: an operator who typed the URI of a signing policy would publish an
endpoint naming it, alongside the `None` mode that contradicts it. Checking that
would mean hard-coding the one URI this adapter implements, from a source that
cannot be pinned, and the endpoint's mode is the field a client acts on.

## Concurrency

The listener serves each connection on its own goroutine, and the owning
application drives expiry and address-space invalidation from another. Every
service the listener holds is therefore reached from several goroutines at
once, and the package follows one rule because of it:

- **A service that holds mutable state synchronises itself.** It does not
  assume a caller serialises access, because no caller does.
- **A service that is immutable after construction needs nothing.** The
  endpoint description and the data-access service are exactly that.
- **A service never hands out a pointer to state it owns.** Callers get value
  snapshots — `ChannelInfo`, `SessionInfo` — so nothing outside can read a
  field while another connection writes it, or hold a stale object across two
  calls. Mutation happens only inside the owning type, under its own lock.

That third point is what makes the rule hold rather than merely be stated. A
registry that locks its map but returns `*Session` has only moved the race one
level down, because callers then read and write the session itself.

Six services already followed this. The channel and session registries did not:
they carried a comment asserting a single-goroutine owner that the listener has
never provided, and nothing checked. Two clients connecting at the same time
faulted the process with a Go runtime `concurrent map read and map write` —
which no `recover` can catch, so the server simply died. Every test until then
had used one connection at a time. `internal/opcua/concurrency_test.go` now
drives concurrent clients against a listener while expiry runs, so a service
that opts out of the rule fails there instead of in production.

## Sessions

`internal/opcua/session.go` implements `CreateSession`, `ActivateSession` and
`CloseSession` from **OPC 10000-4 Tables 15, 17 and 19**, and the listener
serves all three over a real socket.

### The client nonce, and the one deliberate deviation

**A nonce that is present is validated even with `SecurityMode` `None`.** OPC
10000-4 5.7.2 states the server shall check the length and return
`Bad_NonceInvalid` outside 32 to 128 bytes, and Table 16 repeats it. Neither
statement is conditioned on the security mode.

**An absent nonce is accepted when the mode is `None` *and* the endpoint
publishes only the anonymous user token policy.** This is the one place the
adapter knowingly departs from a literal reading of the clause — though not
from what real servers do. open62541 sends no nonce at all on an unsecured
channel, and neither reference server enforces the clause as written: the OPC
Foundation's own `StandardServer` skips the check entirely for an empty nonce
at every security mode, enforces no maximum, and then discards the nonce with
the comment *"ignore nonce if security policy set to none"*. Enforcing the text
literally made this adapter stricter than the specification's own reference
implementation, and unusable with open62541.

**Both conditions are needed, and the second is easy to miss.** 5.7.2 gives the
ClientNonce one job: the server proves possession of its
ApplicationInstanceCertificate in the response, and the same clause says the
server ignores certificates when the `securityPolicyUri` is `None`. That alone
looks like enough — but **Table 101's last row defines a `UserTokenSignature`
specifically for `SecurityMode None`**, over
`ServerNonce | HASH(ServerCertificate) | ClientNonce`. A client authenticating
with a certificate therefore signs the nonce even on an unsecured channel. An
unsecured channel on its own does *not* make the nonce inert.

What makes it inert here is that this endpoint publishes only the anonymous
policy and `ActivateSession` refuses every other token, so no
`UserTokenSignature` can ever be computed. The acceptance is conditioned on
that fact rather than on the security mode alone, so publishing any
non-anonymous policy restores the rule automatically instead of silently
inheriting a justification that no longer holds. A nonce that is present is
always checked against the full 32–128 range, and every other security mode
enforces the rule as written — both stricter than the Foundation's server. The
[interop validation doc](validation/ua-client-interop.md) records the deviation
so it can be reversed by decision rather than found by accident.

### Ending a session is one operation

OPC 10000-4 5.7.2 defines session termination with consequences beyond
forgetting the session: *"When a Session is terminated, all outstanding requests
on the Session are aborted and Bad_SessionClosed StatusCodes are returned to the
Client."* A session's subscriptions also hold DA groups open on the source, and
those must be released with it.

There are three routes to a session ending — an explicit `CloseSession`, the
timeout, and a lookup that finds one already expired — and each has to do all of
it. Implementing that at the call sites meant only one route did: the DA release
lived beside the `CloseSession` handler, so **a session that timed out left its
groups open on the source forever**, which is what an ordinary client crash or
lost network produces. The registry deleted a map entry and nothing else
happened.

Termination is now a single operation every route goes through. It removes the
session, closes a per-session channel that wakes anything still serving a
request for it, and invokes an end hook the listener registers once — which
releases the session's subscriptions. The hook runs after the registry lock is
released, because unsubscribing a DA group is a COM call on Windows and holding
a lock across it would stall every other connection's session work.

### A request in flight holds its session open

The clause terminates a session when the client *"fails to issue a Service
request"* within the timeout. A request the server has not answered yet is one
the client did issue, so the idle clock must not run against it.

This is not hypothetical. `Publish` is held until the subscription has something
to say, and the limits permit a subscription to be legitimately silent for a
publishing interval times a keep-alive count — up to 1h40m — against a session
timeout of at most 10 minutes. Before `Publish` was held, a client re-sent
constantly and kept its own session alive by accident; holding the request made
the gap reachable. A session with requests in flight is never stale, and the
idle clock restarts when the request is answered.

### Which channel may carry a session

A session is bound to a SecureChannel, and Table 15 has the authentication
token checked together with the `SecureChannelId` on every request. But
**"the channel that created the session" and "the channel that may use it now"
are two different facts**, and 5.7.3 says so:

> When the ActivateSession Service is called for the first time then the Server
> shall reject the request if the SecureChannel is not same as the one
> associated with the CreateSession request. **Subsequent calls to
> ActivateSession may be associated with different SecureChannels.**

The second sentence is the client's documented way back after a connection
failure — 5.7.2 has it open a new connection and *"call ActivateSession
again"*. Storing both facts in one field and checking every request against the
creating channel refused exactly that: a session survived its connection, as
the clause intends, and then **could never be used again**. It sat holding its
DA groups until it timed out, and the client had to build a new session for
every network blip.

The two facts are now separate. `CreatedOnChannel` never changes and decides one
thing only: the first activation must arrive on it. `BoundChannel` is what every
request is checked against, and ActivateSession may move it — which is also how
*"once the Server accepts the new SecureChannel it shall reject requests sent
via the old SecureChannel"* is satisfied, since the check is against the
updated binding.

A move is allowed subject to the clause's checks. The security ones are made:
the new channel must offer the SecurityMode and SecurityPolicy the session was
created with, and an anonymous identity may not move onto a signed channel. The
certificate and ClientUserId checks are not reproduced, because this endpoint
serves no certificates and accepts no identity but anonymous — there is nothing
to compare. The session records the security it was created under rather than
reading it back from the endpoint, so the comparison keeps working if a second
endpoint is ever published.

**A token that leaked to another channel is refused** with
`Bad_SecureChannelIdInvalid`. This matters more, not less, on an unsecured
endpoint.

The authentication token is 32 cryptographically random bytes, opaque rather
than numeric, so it cannot be derived from a session identifier a client has
already seen. A failure of the random source refuses the request rather than
falling back to something predictable.

### Identity

Table 17: "Null or empty user token shall always be interpreted as anonymous."
An explicit `AnonymousIdentityToken` is also accepted when its `policyId`
matches the one this endpoint publishes; Table 187 makes null and empty equal.
Every other token type is refused with `Bad_IdentityTokenInvalid`, because no
other policy is published — accepting one would mean claiming an
authentication check the adapter does not perform.

### Fault versus close

A **decoding** failure means the stream cannot be trusted and closes the
connection. A **service** failure — a bad nonce, an unknown session, a rejected
identity — is reported as a `ServiceFault` and leaves the channel open, because
the channel is healthy and only the request failed. Tests assert the client can
keep using the channel afterwards.

Sessions are bounded in count and reclaimed when they go quiet past their
revised timeout, which is clamped into a configured range and is always greater
than zero as Table 15 requires.

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
numerically similar one, and every variable is a scalar because the DA core
decodes no arrays.

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
OPC 10000-3 5.6.2 says each Variable "shall have exactly one type definition and
therefore be the SourceNode of exactly one HasTypeDefinition Reference", and
5.5.2 says the same of each Object. Browsing an item for its type definition
finds that one reference; browsing it for everything finds it alongside the
item's other references.

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

A read result goes through the Part 8 mapping already documented above: the DA
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
Variant may not contain a Variant. Array Variants are decoded and skipped —
their declared length is still bounded — because the DA core decodes no arrays
and this adapter produces none.

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

## Selecting the OPC UA frontend

The adapter can now be configured to serve OPC UA. It remains **one process, one
source, one frontend**: selecting OPC UA means HTTP and gRPC are not served,
exactly as choosing between HTTP and gRPC already worked.

Configuration file **version 3** adds the frontend. Versions 1 and 2 still load,
so an installed adapter keeps running after an upgrade, and a version below 3
that names the OPC UA frontend is refused rather than half-understood. Only the
selected frontend's listener is written, and a non-UA frontend may not carry OPC
UA settings.

### What the operator must supply

The endpoint settings have **no defaults**:

| Setting | Why it is not defaulted |
|---|---|
| `securityPolicyUri` | From the OPC Foundation profile database, which OPC 10000-7 clause 1 points to rather than listing. A wrong URI makes the server unusable by a real client, and the server does not verify it. |
| `transportProfileUri` | Same. |
| `endpointUrl`, `applicationUri`, `namespaceUri` | These identify a deployment, and the namespace URI must stay stable across restarts because design §35.2 forbids treating a namespace index as identity. |

### Configuration

The no-argument startup path reads these. They are the same settings guided
setup writes into a file, under their environment names:

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_OPCUA_LISTEN` | `127.0.0.1:4840` | OPC UA bind address |
| `OPCDA_OPCUA_ENDPOINT_URL` | *required* | endpoint URL published to clients |
| `OPCDA_OPCUA_APPLICATION_URI` | *required* | application identity, stable across restarts |
| `OPCDA_OPCUA_NAMESPACE_URI` | *required* | this adapter's namespace, stable across restarts |
| `OPCDA_OPCUA_SECURITY_POLICY_URI` | *required* | SecurityPolicy URI |
| `OPCDA_OPCUA_TRANSPORT_PROFILE_URI` | *required* | transport profile URI |
| `OPCDA_OPCUA_SOURCE_FOLDER` | `Source` | folder name for the DA source |
| `OPCDA_OPCUA_PRODUCT_URI` | empty | product identity in the endpoint description |
| `OPCDA_OPCUA_APPLICATION_NAME` | empty | display name in the endpoint description |

`OPCDA_FRONTEND=opcua` selects the frontend. The DA batch, Browse, ItemID,
BSTR, queue, reconnect and watchdog variables are shared with the other
frontends and are documented in the [HTTP reference](http-api.md#configuration);
the per-session and per-operation bounds a UA client meets are the ones
published in `ServerCapabilities` above, which are fixed rather than configured.

The five *required* settings have no default for the reason the table in the
previous section gives: a guessed value would make the server unusable by a
real client rather than merely misconfigured. `OPCDA_OPCUA_PRODUCT_URI` and
`OPCDA_OPCUA_APPLICATION_NAME` are the two that stay empty rather than being
invented — an empty display name is honest, and a wrong one is not. Neither has
a setup flag; both are environment-only.

Guided setup lists OPC UA as a third frontend and labels it plainly:
`SecurityPolicy None; local interoperability only, not production ready`. The
review screen repeats that the mode is None — no signing, no encryption,
anonymous users — before the operator confirms. ADR-0016 requires that language
and forbids describing this path as production ready.

### After a reconnect

The service watches the DA connection generation and invalidates the UA address
space when it changes. A new generation may expose a different address space,
and item registrations from the previous one are already invalid, so the cached
nodes must not be served as if they were current. The same tick expires stale
secure channels, sessions, and continuation points, which keeps that
housekeeping on one owned goroutine rather than a timer inside the listener.

## Subscriptions and MonitoredItems

`internal/opcua/subscription.go` implements `CreateSubscription`,
`CreateMonitoredItems`, `DeleteMonitoredItems`, `DeleteSubscriptions`,
`SetPublishingMode` and `Publish` from **OPC 10000-4 Tables 82, 63, 89, 164,
161, 140 and 148**. This is what makes the DA Subscribe core reachable over UA.

**One UA Subscription is one DA subscription, which is one DA group.** That
keeps the DA sampling model intact: the DA server decides what a client sees
between update-rate ticks, and this layer carries those notifications rather
than re-sampling them. The subscription's revised publishing interval becomes
the DA group's requested update rate.

Because a DA group's item set is fixed when the group is created, adding or
removing a MonitoredItem **replaces** the DA subscription with one covering the
new set, releasing the old one. The DA core never resubscribes on its own, so
doing it here is explicit rather than hidden.

### What the parameters actually mean here

- **`revisedSamplingInterval`** reports the update rate the **DA server settled
  on**, read back from the group once it exists, because that is the real
  sampling rate and the source never saw the client's number. A vendor may
  revise far from what was requested, and the client should be told what the
  source actually does. Before the group exists the subscription's publishing
  interval is the best answer available.
- **`revisedQueueSize` is 1.** The DA core coalesces per item, so the effective
  queue is one value per item; reporting a larger queue would overstate what the
  client will receive.
- **A monitoring filter is refused** with `Bad_FilterNotAllowed`. The DA group's
  percent deadband is the only filtering the source offers, and silently
  ignoring a filter a client asked for would misreport what it receives.
- **Two items may not share a client handle**, since the handle is what
  identifies an item in a notification.

### Publish

`Publish` answers from what the DA core has already delivered, and it **holds
the request** until the subscription has something to report or a keep-alive
comes due. OPC 10000-4 5.14.5.1 is explicit that Publish requests are queued in
the server, and the reason is practical: a client issues the next Publish as soon
as the last response arrives, so an empty response returned at once is answered
at once. Measured against a third-party client before this was fixed, that was
**3,874 exchanges in 40 seconds against one notification actually delivered**,
and the load starved the very sampling the subscription existed to deliver.

Holding a request cannot occupy the connection, because a client keeps a Publish
outstanding while it reads and browses on the same channel. So a Publish is
answered from its own goroutine while the read loop carries on, writes to the
socket are serialised — the send sequence number is assigned under the same lock,
so chunks cannot interleave or go out of order — and a held request is released
when its connection ends. The read deadline is extended while a connection has
Publish requests outstanding: such a connection is idle because **the server owes
it a response**, not because the client has gone away.

Table 89's guidance that a server should limit active Publish requests, while
accepting more than the number of subscriptions created, is implemented as a
per-connection bound answered with `Bad_TooManyPublishRequests`.

With nothing to report for `maxKeepAliveCount` publishing cycles it answers a
keep-alive, which carries no `NotificationData` at all and does not consume a
sequence number.

The first cycle is an exception to the count. Clause 5.14.1.1: "when a
Subscription is created, the first Message is sent at the end of the first
publishing cycle to inform the Client that the Subscription is operational ...
This is the only time a keep-alive Message is sent without waiting for the
maximum keep-alive count to be reached." With a large keep-alive count and a slow
publishing interval, that is the difference between a client learning its
subscription works in one interval and in a hundred.

A keep-alive does not repeat the last sequence number either. Clause 5.14.1.1: each keep-alive
"contains the sequence number of the **next** NotificationMessage that is to be
sent". That number is what tells a client holding a gap whether the message it
is missing is still coming or was never produced — repeating the last one would
have it wait for a message it already has. On a subscription that has sent
nothing, the number is 1, which 5.14.1.1 states outright: the first keep-alive
"contains a sequence number of 1, indicating that the first NotificationMessage
has not yet been sent".

A keep-alive is not held for retransmission. 5.14.1.1 reserves that queue for
responses that "actually contain one or more Notifications", which is the same
distinction the clause uses to define the word NotificationMessage.

Table 82's `maxNotificationsPerPublish` of zero means the client imposes no
limit; a smaller client value tightens the server's bound. What does not fit is
reported through `moreNotifications` rather than dropped.

A subscription belongs to the session that created it, and closing a session
releases its DA groups — a closed session must not leave groups open on the
source.

### When the source goes away

The DA core invalidates its subscription on a source disconnect. The UA
subscription survives, but every reporting item is given a `Bad_NotConnected`
status in the next notification, so a client **learns the source is gone rather
than seeing the stream fall silent**. The DA subscription is released rather
than left dangling; recovering means creating monitored items again, which
matches the DA core's rule that resubscribing is always explicit.

## Shutting the listener down

`Shutdown` takes a context, so it has to mean something. It used to close the
socket and return at once however long a caller was prepared to wait, because
the wait it needed did not exist: `Serve` joined its goroutines privately, and
nothing outside could observe when that had happened. The two other frontends
delegate to `net/http` and `grpc-go` and really do drain, so a caller could not
tell from the signature which behaviour it was getting.

The listener now tracks every goroutine it starts — connection loops *and* the
goroutines holding a `Publish`, which outlive the read loop that accepted them —
and raises a completion signal once `Serve` has joined them all. `Shutdown`
waits on that signal or on the caller's context, whichever comes first.

It also **ends its sessions**, which releases the DA groups their subscriptions
hold on the source. That was previously left to the DA runtime's own teardown,
which worked only because the application happens to stop the runtime
immediately afterwards — the listener's shutdown depended on its caller's
ordering rather than on itself.

## What has and has not been tested

The real-DA validation runs the UA frontend against the source-built OPC
Foundation DA 2.05a fixture on both architectures: the connection sequence, a
secure channel, `GetEndpoints`, a session, a Browse walk from Root down to a
variable, a Read of that variable, and a Subscription whose MonitoredItem
receives the server's initial snapshot and then change-driven notifications
induced through the UA Write service.

**Three third-party UA clients** now run against the frontend too, over a
scripted DA source, through `scripts/interop/run.sh`: asyncua (Python),
open62541 (C), and the OPC Foundation's own .NET stack. Between them they found
six defects this project's own probe could not — an `OpenSecureChannel` reply
naming no security policy, `Browse` ignoring `includeSubtypes`, `Publish`
answering immediately rather than holding the request, the standard `Server`
object being absent, unspecified endpoint strings written as zero-length rather
than null, and a session nonce rule that no unsecured open62541 client can
satisfy. Each is described above and covered by regression tests;
`docs/validation/ua-client-interop.md` records what the suite checks and why the
last of them is a deliberate deviation rather than a defect.

Two of the six came from the second and third clients **against a server the
first already passed**, which is why all three stay in the suite.

What that is evidence for: three third-party clients, one of them the
Foundation's own, interoperate with this server on connection, browse, read,
write, subscription, and the standard Server object. What it is not evidence
for: the DA side, which is scripted there and validated separately on Windows;
any security policy other than `None`; or conformance — three clients are not
the OPC Foundation's Compliance Test Tool. Per ADR-0016 **no "OPC UA Certified"
or "OPC UA Compliant" claim is made**, and the `SecurityPolicy None` path is for
local interoperability work only. UA Expert has not been tried: it is
distributed only to registered users.

## What is not decided here

Certificate handling and the signed and encrypted security policies, address
space construction, session and secure channel handling, subscription
and MonitoredItem behavior, certificates, and security policies are out of
scope for this document. See ADR-0016 for the phase order
and the conformance language rules.
