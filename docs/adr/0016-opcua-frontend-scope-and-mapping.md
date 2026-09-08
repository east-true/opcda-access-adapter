# ADR-0016: OPC UA frontend scope and the DA mapping foundation

- Status: Accepted
- Date: 2026-08-25

## Context

Phase 7 completed the DA Subscribe core and its gRPC streaming frontend, both
validated against the OPC Foundation DA 2.05a fixture. Phase 8 is the OPC UA
frontend: the adapter acts as a UA Server on one side and remains a DA Client on
the other.

Two existing constraints shape this phase before any code is written.

Design §5.2 forbids using an existing OPC DA or OPC UA implementation library as
a core implementation dependency. The UA frontend is therefore a hand-written UA
server: UA-TCP, chunking, UA Binary, SecureChannel, Session, address space,
Browse, Read, Write, and later Subscription and MonitoredItem. That is a large
surface with real security exposure, and design §35.5 already lists the wire
bounds it must carry.

Design §35.3 also fixes the semantics: the representation follows the standard
COM DA mapping rather than anything this project invents.

## Decision

### Phase 8 is ordered, and semantics come first

The first slice is the DA-to-UA mapping and nothing else. It is pure, testable
on any platform, needs no listener, and every later slice depends on it. Wire
encoding, transport, session handling, address space, and subscriptions follow
as separate slices, each on its own branch.

`internal/opcua` holds this mapping, following design §5.2's naming. It is not
to be grown into a general-purpose UA SDK.

### The mapping is transcribed from the specification, not recalled

Every mapping row comes from a fetched primary source: OPC 10000-8 Annex A for
the DA mapping, OPC 10000-4 Tables 176 and 177 for the status code structure,
and the OPC Foundation's published `StatusCode.csv` for numeric status codes.
`docs/opcua-mapping.md` records the mapping and its provenance.

This mattered in practice. Two rows contradict what a plausible reading would
assume: `VT_DATE` maps to `Double` rather than `DateTime`, and the DA
`LAST_KNOWN` quality maps to `Bad_OutOfService` rather than to an `Uncertain`
code. Both are transcribed as the specification states them.

### Unmapped is a valid answer

Where Part 8 Table A.2 has no row for a VARTYPE the DA core can produce —
`VT_INT`, `VT_UINT`, `VT_ERROR`, `VT_CY` — the mapping reports it as unmapped
and the operation fails explicitly. Borrowing a numerically similar row would be
an invention presented as a standard mapping.

`VT_EMPTY` and `VT_NULL` are mapped to a DataValue with no value. This is stated
as an adapter decision because the table does not cover them.

Arrays and by-reference variants are reported as unmapped even though Table A.2
maps `VT_ARRAY`, because the DA core decodes no arrays. The mapping describes
what the adapter can actually carry.

### Only observed DA error codes are bound to numbers

Part 8 Tables A.4 and A.5 name eleven more DA error codes than this project has
ever observed. Their numeric HRESULT values are not in the fetched sources, so
only `OPC_E_BADRIGHTS` and `OPC_E_UNKNOWNITEMID` — both confirmed against the
real fixture and recorded in `docs/compatibility.md` — are bound. Everything else
falls into the "Others" row both tables define explicitly, which keeps the
mapping correct while incomplete.

This follows ADR-0005's precedent: an unverified constant is not added on the
strength of recollection.

### The vendor quality loss is documented, not worked around

Part 8 A.3.2.3 requires the vendor-specific quality byte to be discarded. Per
design §35.4 the adapter does not add a custom UA property to smuggle it
through. The DA core and the HTTP and gRPC frontends still expose the raw 16-bit
quality, so the loss exists only at the UA boundary and is recorded there.

### Identity follows the design, not the Annex's convenience option

Annex A offers a configurable ItemID delimiter as one wrapper strategy. Design
§35.2 forbids guessing a server's delimiter or reconstructing an ItemID from a
browse path, so that strategy is rejected. The exact DA ItemID is the identity,
names come from DA Browse, the namespace URI is stable, a namespace index is
never persistent identity, and names are never tidied.

### Security and conformance language are fixed now, not later

`SecurityPolicy=None` with anonymous authentication may be used for a local
interop prototype only. It is never described as production-ready, and
certificate lifecycle and a secure policy are required before any release that
exposes UA.

Per design §35.7 the project will not use "OPC UA Certified", "OPC UA
Compliant", or "OPC Foundation Certified", will not claim compatibility with all
UA clients, and will not display OPC Foundation logos or certification marks.
The implemented subset and actual interoperability results are recorded instead.

## Rejected alternatives

- **Starting with the wire protocol:** rejected because the semantics decide
  what the wire must carry, and a transport with no agreed mapping cannot be
  reviewed for correctness.
- **Adopting a UA SDK for the server side:** rejected by design §5.2. Revisiting
  it would be a design change, not an implementation choice.
- **Writing the mapping tables from knowledge and verifying later:** rejected.
  The two rows that turned out to be counterintuitive would very likely have
  been recorded wrongly and then propagated into the encoder.
- **Binding all Table A.4 and A.5 rows using recalled HRESULT values:** rejected
  for the same reason; the "Others" row makes an incomplete table correct.
- **Adding a custom UA property carrying raw DA quality:** rejected by design
  §35.4; it would make the adapter non-standard to gain fidelity the standard
  mapping deliberately drops.
- **Mapping `VT_INT` and `VT_ERROR` onto the `VT_I4` row:** rejected as an
  invention; the DA core's internal decoding choice is not a UA mapping.

## Later refinements

Two decisions recorded above have since been superseded. The decisions stand as
made -- they were right on what was known then -- and what changed is recorded
here rather than by editing them, which is how the rest of this document treats
a dated fact.

**`LAST_KNOWN` no longer maps to `Bad_OutOfService`.** The Decision cites it as
a row transcribed exactly as the specification states it. Part 8 Table A.3 does
say `Bad_OutOfService`, but OPC 10000-4 Table 61 contradicts it and explains
itself: the fieldbus code `Bad_LastKnown` "shall be mapped to
`Uncertain_NoCommunicationLastUsable`" because "OPC UA requires that the Server
shall return a Null value when the Severity is Bad". A Bad severity therefore
discards exactly the value `LAST_KNOWN` exists to carry. #105 follows the clause
that explains itself, and `scripts/spec-check/check.py` reports the divergence
from A.3 on every run rather than leaving it to be noticed. This is the one
place where "transcribed, not recalled" was not enough: both rows were
transcribed correctly and they disagree.

**Every row of Tables A.4 and A.5 is bound, not two.** The Decision binds only
`OPC_E_BADRIGHTS` and `OPC_E_UNKNOWNITEMID`, because "their numeric HRESULT
values are not in the fetched sources" and an unverified constant is not added
on recollection. That condition ended when `opcerror.h` -- from the commit
ADR-0006 already pins for the validation fixture -- became a checked source in
#68, which made the remaining values available from the same place the test
server was built from. #69 bound the rest. Thirteen DA error codes are bound
today, and the "Others" row still catches everything outside both tables.

The rule this ADR set was never relaxed: a constant is bound when a pinned
source states it. What changed is which sources are pinned.

Two things this ADR left unbound have since been closed from primary sources:
`MessageSecurityMode`'s wire values from OPC 10000-4 Table 139, and
`SecurityTokenRequestType`'s from the OPC Foundation UA NodeSet's `DataType`
definition. The SecurityPolicy URI strings and the per-policy
`LegacySequenceNumbers` flag remain unbound, and the reason is sharper than it
was recorded: OPC 10000-7 governs profiles and does not list them. Its clause 1
says "the actual Profiles are maintained in an online database and accessible
via https://profiles.opcfoundation.org/", and Parts 3, 4, 5 and 6 carry neither.
There is no pinned document to check a transcription against, which is the
condition this ADR set for not writing one. The URI binds with the endpoint
description and is supplied by configuration.

## Consequences

These are the consequences of this slice, which is the mapping. The listener and
the wire encoding this ADR deliberately excluded were added by later slices, and
`docs/opcua-mapping.md` is where what exists today is described.

- `internal/opcua` exists with the mapping and its tests. No listener, no wire
  encoding, and no UA dependency are added **by this decision**.
- `docs/opcua-mapping.md` is the specification the later slices implement.
- The vendor quality byte and the unbound DA error codes are recorded as known,
  bounded gaps with a defined way to close them.
- No OPC UA conformance or interoperability claim is made.
