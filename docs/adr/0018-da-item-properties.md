# ADR-0018: reading OPC DA item properties

- Status: **Accepted and implemented**, DA layer and UA mapping both
- Date: 2026-08-27
- Relates to: [ADR-0016](0016-opcua-frontend-scope-and-mapping.md),
  [ADR-0001](0001-v0-bounds-and-runtime-defaults.md)

## Context

OPC 10000-8 Table A.1 maps the OPC COM DA item properties onto UA attributes and
properties: EU Units onto `EngineeringUnits`, High/Low EU onto `EURange`,
High/Low Instrument Range onto `InstrumentRange`, Item Description onto the
`Description` attribute, and Close/Open Label onto `TrueState`/`FalseState`.

None of it was implemented, and until recently that was not written down
anywhere. It is now: a client that wants engineering units or a range to render
a faceplate finds neither. Only the table's Access Rights row was satisfied, by
another route — `OPCITEMRESULT.dwAccessRights` from `AddItems`.

That was not a conformance defect. Source nodes are `BaseDataVariableType`, so
none of those properties is mandatory on them, and reading a missing attribute
answers `Bad_AttributeIdInvalid`. It was a missing feature, and this ADR is the
decision to build it.

`docs/design.md` §11 already listed `IOPCItemProperties` in the DA baseline,
which overstated what the adapter did. This makes the code match the document
rather than the other way round.

## Decision

Read item properties through `IOPCItemProperties`, and treat it exactly the way
`IOPCBrowseServerAddressSpace` is treated.

**It is optional.** The interface is queried once per connection. A source that
answers `E_NOINTERFACE` is working correctly and simply has no properties to
offer. The runtime reports this as a capability — `supported`, `unsupported` or
`unavailable` — rather than as a bool, because a source that has not been asked
is not the same as one that said no. Every property path on such a source
answers `PROPERTIES_UNSUPPORTED`.

**Discovery and reading are separate operations**, because they are separate
questions. `AvailableItemProperties` reports which properties a source offers
for an item, in the source's own order, from `QueryAvailableProperties`.
`ItemProperties` reads the values of named properties, from `GetItemProperties`.
Combining them behind one method with a mode flag would be one call carrying two
meanings.

**A per-property HRESULT stays a result, not a transport failure.** A source may
offer a property for one item and refuse it for another. The request succeeds
and the refusal is reported against that property, with the source's exact
HRESULT, the same way per-item Read and Write results work.

**The item's value, quality and timestamp are never read as properties.** They
are available as properties 2, 3 and 4, and the adapter refuses to fetch them
that way. Read and Subscribe deliver the value together with its timestamp and
its raw quality; a property fetch would answer the same question a second time,
without them, and the two answers could differ. `ItemProperties` rejects those
three identifiers before reaching the source.

**The number of properties per item is bounded**, by `OPCDA_MAX_ITEM_PROPERTIES`
(default 64, hard ceiling 1024), applied both to what a request may ask for and
to what a source may report. Every other DA bound is explicit; a new one that
nobody bounded would be a way to ask a source for an unbounded amount of work.

**The property identifiers are checked, not transcribed.** `opcda.idl` declares
all sixteen, and `scripts/spec-check/check.py` verifies each value and the
`IOPCItemProperties` vtable slot order against the pinned IDL. A vtable slot in
the wrong position calls a different method entirely, which no Go test can see.

## Consequences

The runtime interface gains two methods, so every source implementation must
answer them — including the test doubles, which answer `PROPERTIES_UNSUPPORTED`,
which is what a real source without the interface does.

Property values are read live. Nothing is cached and nothing is synthesised, so
a property that a source stops offering stops being reported, and the adapter
never serves a remembered one.

Which properties an item has still costs a call to find out. The UA mapping in
the follow-up change has to decide when to ask, and that decision — not this one
— determines whether the address space carries a property node for an item
before anyone asks for it.

Whether a node whose EU range is known should be promoted from
`BaseDataVariableType` to `AnalogItemType` is deliberately **not** decided here.
`EURange` is reachable as a property of a `BaseDataVariableType` node, so the
value is delivered either way, and promoting a node means claiming a type whose
mandatory properties must then always exist.
