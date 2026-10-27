# Value, quality, and error mapping

[UA overview](../opcua-mapping.md) · [Documentation](../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../compatibility.md).

## Where this adapter departs from Part 8

Every departure below is deliberate, and each says which clause it departs from
and why. They are collected here because a reader deciding whether this adapter
suits their source should not have to find them scattered through the document.

| Clause | What it asks | What this adapter does, and why |
|---|---|---|
| A.3.1.2 | the root branch's BrowseName "should" be the Server ProgId | named from `OPCDA_OPCUA_SOURCE_FOLDER`, default `Source` — a source may be configured by CLSID, where there is no ProgID, and an operator who wants the clause's behaviour can configure it |
| A.3.1.3 | `AnalogItemType` if the item has High and Low EU **or** an Analog EU Type | the EU Type alone does not promote, because 5.3.2.3 makes `EURange` mandatory and there would be no range to publish |
| A.3.1.3 | `MultiStateDiscreteType` for an enumerated EU Type | never claimed: its mandatory `EnumStrings` is `LocalizedText[]` and EU Info is a DA `String[]`, a conversion this adapter has not decided |
| A.3.1.4 | an array-valued property is exposed with `ValueRank` `OneOrMoreDimensions` | applied |
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
- **`VT_ARRAY` has a Table A.2 row** (array of the mapped element type). The DA
  core decodes arrays now ([ADR-0019](../adr/0019-safearray-representation.md)) and
  a Read carries one as a UA array Variant, with one refusal described below.
  `DataTypeFor` still reports an array VARTYPE as unmapped, because what it
  answers is a node's `DataType` attribute and Table A.2's array row is a
  `ValueRank`, not a type id. By-reference variants remain unmapped outright.

### Arrays

UA array support currently applies to outgoing values. Array Writes are
refused. Ordinary DA item nodes still advertise scalar `ValueRank` even when
the returned value is an array; Other Properties report array rank correctly.
These are current limitations of this frontend.

A Read of an array item answers a Variant with the array bit set, a length
prefix, and — for more than one dimension — an ArrayDimensions field, which is
what Tables 25 and 26 describe. A single dimension carries no dimensions field:
the length prefix already states it, and repeating it would give a decoder a
second statement to disagree with.

**An array whose lower bound is not zero answers `Bad_NotSupported`.** A UA
Variant carries array *lengths* and no lower bound anywhere in it, so such an
array has no lossless UA representation. Re-basing it to zero was rejected: a UA
client cannot tell a re-based array from one that was always zero-based, which
would leave this adapter the only party knowing the value it published is not
the value the source holds — the same defect as serving last-good data, reached
through the encoder instead of through a cache. One dimension out of zero is
enough to refuse the whole value, because publishing the others and silently
re-basing that one is the same loss in a smaller place.

How often real sources use non-zero lower bounds is not something this adapter
can assert. Array support is **not validated against any real OPC DA source**;
see [implementation status](../implementation-status.md).

`VT_EMPTY` and `VT_NULL` have no row. The adapter maps them to a DataValue with
no value; this is an adapter decision recorded in ADR-0016.

`VT_INT`, `VT_UINT`, `VT_ERROR`, and `VT_CY` have no row either. `VT_CY` is
therefore reported as **unmapped** and fails explicitly, and so is anything else
Table A.2 does not cover.

The other three are not, and the section below says why: the DA core reads
`VT_INT` and `VT_ERROR` out of the same storage as `VT_I4`, and `VT_UINT` out of
the same storage as `VT_UI4`, so reporting `Int32` and `UInt32` names the value
the adapter actually produced rather than borrowing a row. This paragraph used
to say all four were unmapped; that was true until the node's declared type and
the delivered `Variant` were found to disagree, and it is recorded in
ADR-0016's later refinements.

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
discarded**. The DA core preserves it and the HTTP, gRPC and MCP frontends expose the
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

### The adapter's own failures, which no Part 8 table covers

Tables A.4 and A.5 map what the *source* said. They say nothing about the
adapter refusing or failing before the source is reached, and that mapping is
this project's, so it is recorded here rather than left for a client to infer.

| UA StatusCode | Adapter error codes |
|---|---|
| `Bad_NotConnected` | `RUNTIME_UNAVAILABLE` |
| `Bad_Timeout` | `RUNTIME_DEADLINE_EXCEEDED` |
| `Bad_NotWritable` | `WRITE_DISABLED` |
| `Bad_TooManyOperations` | `QUEUE_FULL`, `REQUEST_LIMIT_EXCEEDED`, `REGISTERED_ITEM_LIMIT_EXCEEDED` |
| `Bad_TypeMismatch` | `TYPE_MISMATCH`, `INVALID_VALUE` |
| `Bad_DataTypeIdUnknown` | `UNSUPPORTED_VARTYPE` |
| `Bad_ServiceUnsupported` | `BROWSE_UNSUPPORTED`, `SUBSCRIBE_UNSUPPORTED`, `PROPERTIES_UNSUPPORTED` |
| `Bad_InternalError` | anything not named above, and any error that is neither a source nor an adapter error |

The codes themselves are described in the
[HTTP reference](../http-api.md#errors), which is the one place they are defined;
this is the mapping, not a second vocabulary.

`Bad_ServiceUnsupported` carries all three optional DA 2.05a interfaces —
Browse, the callback connection point, and `IOPCItemProperties`. A source
lacking one is reporting a capability, not failing, and answering
`Bad_InternalError` would tell a client the server broke. The third was missing
from that list until it was written down here: no client could reach it, because
population treats an absent property interface as "this item has no properties"
and every property read is of a node population attached. It was a trap for the
next path rather than a live defect, and lists like this are where that kind of
omission is visible.

`Bad_InternalError` is the default, so a code this table does not name arrives
as an internal error rather than as something more specific.

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
may become reachable if its underlying decision changes, such as relaxing strict
typing. `OPC_E_INVALID_PID` became reachable when item properties were implemented.
A row that is bound and unreachable costs
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

