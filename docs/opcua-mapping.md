# OPC UA frontend

The UA frontend exposes Browse, Read, Write, and subscriptions for one local
OPC DA server. It serves anonymous `SecurityPolicy None` connections only.
It is for local interoperability work and is not production ready or certified.

Use this page for configuration, supported services, and DA-to-UA mappings.
The detailed references explain wire formats, lifecycle rules, and departures
from the specification.

| Topic | Reference |
|---|---|
| Values, Quality, errors, and standard departures | [Data mapping](opcua/data-access.md) |
| DA properties, VariableTypes, and semantic changes | [Item properties](opcua/item-properties.md) |
| DA groups, MonitoredItems, Publish, and retransmission | [Subscriptions](opcua/subscriptions.md) |
| UA-TCP, chunks, binary encoding, and SecureChannel | [Transport and encoding](opcua/protocol.md) |
| Endpoints, nonce handling, sessions, and concurrency | [Endpoints and sessions](opcua/sessions.md) |
| NodeIds, namespaces, Browse, Read, Write, and reconnect | [Address space](opcua/address-space.md) |

## Which services this server answers

Requests outside the supported set receive a `ServiceFault` with
`Bad_ServiceUnsupported`; the channel remains open.

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

The unsupported column is not a backlog. DA has no history, method calls,
query engine, or client-created nodes to expose. Where modification is not
implemented, clients can delete an item or subscription and create it again.

## Value types

OPC 10000-8 Table A.2 defines these mappings. A mapping entry does not promise
that the DA core decodes that VARTYPE: unsupported values still fail explicitly.

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

The specification maps `VT_DATE` to `Double`. The DA core currently refuses
`VT_DATE` and `VT_DECIMAL` values rather than converting them.

Outgoing array values carry their element type and dimensions. Array Writes
are refused, and ordinary DA item nodes retain scalar `ValueRank` metadata. UA refuses a DA array with a
nonzero lower bound as `Bad_NotSupported`, because a UA Variant cannot carry
that bound. By-reference variants are unsupported. No real DA source has yet
validated the array path. [Array details](opcua/data-access.md#arrays) describe
the encoding and type checks.

## Quality

The main quality and substatus follow OPC 10000-8 Table A.3. The DA core and
HTTP, gRPC, and MCP preserve raw 16-bit Quality. UA discards the vendor-specific
upper byte as Part 8 requires.

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

`LAST_KNOWN` is a recorded departure: this adapter uses
`Uncertain_NoCommunicationLastUsableValue` so the source value survives the
mapping. A Bad severity would require a Null value. The specification conflict,
limit bits, and fallback for unlisted substatuses are explained in
[Quality mapping](opcua/data-access.md#quality).

## Errors

Per-item HRESULTs follow OPC 10000-8 Tables A.4 and A.5. Item failures remain
separate from a failure of the source method or adapter request.

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

`OPC_S_CLAMP` is a success that reports a stored value different from the
requested value, so it maps to `Good_Clamped`. The Read and Write mappings for
`OPC_E_INVALID_PID` differ because the specification defines different answers.

Adapter failures have their own mapping:

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

The [error reference](opcua/data-access.md#errors) distinguishes specification
rows, reachable conditions, and HRESULTs observed from a real server. Error
code meanings are defined in the [HTTP reference](http-api.md#errors).

## Item properties

When the source supports `IOPCItemProperties`, Table A.1 maps its properties
onto UA attributes and property variables. Values are read from the source on
request. The [property reference](opcua/item-properties.md) covers names,
VariableTypes, access rights, `SemanticsChanged`, and deliberate exceptions.

## Subscriptions and MonitoredItems

One UA Subscription owns one DA group. The frontend carries the source's
revised update rate and per-item coalescing without additional sampling.
Disconnect discards pending values and invalidates the subscription;
reconnecting does not restore it.

Publish, Republish, lifetime limits, and the retransmission queue follow the
separate UA contract. See [subscriptions](opcua/subscriptions.md) for parameter
rules, bounds, and cleanup.

## Configuration

Select `OPCDA_FRONTEND=opcua` explicitly. HTTP, gRPC, and MCP do not run in the
same process. UA requires configuration version 3 or later; setup now writes
version 4 and still reads versions 1–3. File-based execution does not merge
environment variables.

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

The operator supplies the required identity and profile URIs. Profile URIs
come from the OPC Foundation profile database; the server does not verify them.
The namespace URI must remain stable across restarts. Guided setup uses the
application URI as `productUri` and `OPC DA Access Adapter` as `applicationName`;
the environment-only path leaves those two optional fields empty by default.

Shared DA limits and timeouts are in the [HTTP configuration table](http-api.md#configuration).
Use [guided setup](setup.md) for configuration files and Windows Service execution.

## Validation and limits

The OPC Foundation DA 2.05a fixture has exercised connection, sessions, Browse,
Read, Write, and subscriptions on 386 and amd64. A separate scripted DA source
is used by asyncua, open62541, and OPC Foundation .NET client tests.
These results do not establish broad vendor compatibility or UA conformance.
UA Expert remains untested.

See [executed evidence](compatibility.md) and the [client procedure](validation/ua-client-interop.md).
Certificate-based, signed, and encrypted policies remain out of scope;
[ADR-0016](adr/0016-opcua-frontend-scope-and-mapping.md) records that boundary.
