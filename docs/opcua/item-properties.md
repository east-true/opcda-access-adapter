# DA item properties in the UA address space

[UA overview](../opcua-mapping.md) · [Documentation](../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../compatibility.md).

## Item properties: Table A.1

Part 8 Table A.1 maps the OPC COM DA item properties onto UA attributes and
properties. The named rows and the "Other Properties" row are implemented, including
array-valued properties and writable property ItemIDs. The mappings do not all
create property nodes; some populate attributes instead.

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
- `ValueRank` is `Scalar` for scalar properties and `OneOrMoreDimensions`
  for array-valued properties;
- `AccessLevel` is readable, and writable when the source gives the property its
  own ItemID.

This includes EU Type and vendor-specific properties. Access Rights, Item Description and **Scan Rate**
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

**An array-valued property is exposed**, with `ValueRank`
`OneOrMoreDimensions`, which is what A.3.1.4 asks for. A DA source does not
declare how many dimensions an array property has before it is read, so the
rank says "one or more" rather than naming a count that could be wrong.

A by-reference property is still left out: the DA layer carries none, so such a
node could be browsed and never read, and a property that exists and cannot
answer is worse than one that is absent.

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

Ordinary DA item nodes currently retain `ValueRank` `Scalar`, including items
whose Read value is an array. This is a metadata limitation, not an indication
that the source value is scalar. Other Properties use `OneOrMoreDimensions`
when their declared VARTYPE is an array. `ServerArray` and `NamespaceArray`
are one-dimensional, as OPC 10000-5 requires.

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

`MultiStateDiscreteType` is never claimed, and the reason has narrowed rather
than gone. Its mandatory `EnumStrings` comes from EU Info, whose DA value is an
array of strings. The DA layer carries arrays now
([ADR-0019](../adr/0019-safearray-representation.md)), so that is no longer the
obstacle.

What remains is the type. `EnumStrings` is `LocalizedText[]`; a DA array of
`VT_BSTR` is `String[]`. Turning one into the other is a conversion this
adapter has not decided — a DA string carries no locale, and inventing one or
publishing an empty one is a choice rather than a transcription. Claiming the
type would mean promising a mandatory property whose shape the adapter would
have to invent.

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

