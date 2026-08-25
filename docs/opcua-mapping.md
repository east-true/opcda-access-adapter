# OPC DA to OPC UA mapping

This is the specification the future OPC UA frontend implements. Its normative
source is **OPC 10000-8 (Part 8: Data Access), Annex A — "OPC COM DA to UA
mapping"**, with the status code structure from **OPC 10000-4 Table 176 and
Table 177** and numeric status codes from the OPC Foundation's published
`StatusCode.csv` for the UA namespace.

Nothing here is derived from recollection. Where the specification has no row
for something the DA core can produce, that is stated as an adapter decision
rather than presented as standard behavior.

There is no UA frontend yet. This document and `internal/opcua` define the
semantics; the wire protocol is a later slice.

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
| `LAST_KNOWN` | `Bad_OutOfService` |
| `OUT_OF_SERVICE` | `Bad_OutOfService` |
| `WAITING_FOR_INITIAL_DATA` | `Bad_WaitingForInitialData` |

Note that **`LAST_KNOWN` maps to `Bad_OutOfService`**, not to an `Uncertain`
code. Both `LAST_KNOWN` and `OUT_OF_SERVICE` map to the same UA code, so that
distinction does not survive the mapping.

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

| DA error | UA StatusCode (Read) | UA StatusCode (Write) |
|---|---|---|
| `OPC_E_BADRIGHTS` | `Bad_NotReadable` | `Bad_NotWritable` |
| `OPC_E_UNKNOWNITEMID` | `Bad_NodeIdUnknown` | `Bad_NodeIdUnknown` |
| Others | `Bad_UnexpectedError` | `Bad_UnexpectedError` |

Tables A.4 and A.5 have more rows than this — `E_OUTOFMEMORY`,
`OPC_E_INVALIDHANDLE`, `E_INVALIDITEMID`, `E_INVALID_PID`, `E_ACCESSDENIED`,
`DISP_E_TYPEMISMATCH`, `E_BADTYPE`, `E_RANGE`, `DISP_E_OVERFLOW`,
`E_NOTSUPPORTED`, and `S_CLAMP`.

Only the two DA error codes this project has **observed against a real server**
are bound to numeric values. The rest need their DA numeric constants confirmed
against the OPC DA specification before being added, and until then they fall
into the "Others" row that both tables define explicitly. The mapping is
therefore correct but incomplete, and completing it is a documentation task with
a verifiable source, not a guess.

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

## What is not decided here

The UA wire protocol, address space construction, session and secure channel
handling, subscription and MonitoredItem behavior, certificates, and security
policies are out of scope for this document. See ADR-0016 for the phase order
and the conformance language rules.
