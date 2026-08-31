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

### The VariableType a source item is given

This was left open here as "should a node whose EU range is known be promoted to
`AnalogItemType`?", framed as a nicety, on the grounds that `EURange` is
reachable either way. **That framing was wrong**, and reading Annex A.3.1.3
end to end is what showed it: the specification does not leave the type open. It
prescribes a four-way choice driven by the DA properties an item has.

> A.3.1.3: "DA items (leafs) are represented in the COM UA Wrapper as Variables.
> The VariableType depends on the existance of special DA properties as follows"

| The item has | VariableType | Carrying |
|---|---|---|
| High EU and Low EU, **or** EU Type is Analog | `AnalogItemType` (i=2368) | `EURange`, `EngineeringUnits`, `InstrumentRange` |
| Open Label and Close Label | `TwoStateDiscreteType` (i=2373) | `TrueState` from Close Label, `FalseState` from Open Label |
| EU Type is enumerated | `MultiStateDiscreteType` (i=2376) | `EnumStrings` from EU Info |
| none of the above | `DataItemType` (i=2365) | — |

The adapter gives every source item `BaseDataVariableType`, which **appears
nowhere in Annex A**. So this is not an undecided improvement; it is a deviation
that had not been identified as one. The floor the specification sets is
`DataItemType`, not `BaseDataVariableType`.

Two consequences worth stating before anyone implements it.

**A promoted type is a promise.** `AnalogItemType` *requires* `EURange`
(clause 5.3.2.3). Once a node claims that type, the property has to be there
whenever the node is — including after a reconnect in which the source stops
offering High/Low EU. Today's property nodes are attached from what the source
says it offers and dropped when it stops; a type definition cannot be dropped
the same way without the node changing type underneath a client.

**Annex A contradicts itself about the property types, and the adapter picked
the wrong half.** Table A.1 gives "String" as the OPC UA DataType for EU Units,
Close Label and Open Label, and this adapter followed that column literally.
A.3.1.3 puts those same values on `EngineeringUnits`, `TrueState` and
`FalseState` of the standard types, where they are `EUInformation` and
`LocalizedText`. Reading A.1's third column as the DA value's mapped type rather
than the UA property's type reconciles them — and that reading is the one
A.3.1.3 forces. `docs/opcua-mapping.md` records the current choice as following
A.1; that note needs revisiting with this, not independently of it.

**Decided: implemented.** The floor is `DataItemType`, `AnalogItemType` and
`TwoStateDiscreteType` are chosen from the properties the source offers, and the
property types are the standard types' rather than A.1's column.

Two departures, both from the rule that a claimed type is a promise. An item
whose EU Type is Analog but which offers neither EU bound is **not** promoted,
because `EURange` is mandatory on `AnalogItemType` and there is no range to
publish. `MultiStateDiscreteType` is never claimed, because its mandatory
`EnumStrings` comes from EU Info, an array of strings, and the DA layer does not
carry array VARIANTs. Both are recorded in `docs/opcua-mapping.md`.

What is still open is what happens if a source stops offering the properties a
claimed type requires. Today a re-attach recomputes the type, so an item can
change type between browses. That is visible to a client and is not obviously
better than keeping a stale type; it is the honest behaviour for now because the
alternative is reporting a range that no longer exists.
