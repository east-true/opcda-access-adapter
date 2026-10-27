# Design baseline

Baseline date: 2026-08-22. This document records the original product boundary,
implementation plan, and its later clarifications. Planned features, examples,
and unchecked phase checklists describe that baseline, not the current backlog.
Accepted [ADRs](adr/README.md) record subsequent decisions.

For the current implementation, start with [architecture](../architecture.md) and
[implementation status](../implementation-status.md). API contracts live in the
individual [frontend references](../frontend/README.md).

## Reading this baseline

| Question | Sections |
|---|---|
| Product purpose and boundaries | [1. Purpose](#1-document-purpose), [4. Invariants](#4-invariants), 1–6 |
| Runtime, COM ownership, and DA operations | [7. Architecture](#7-architecture), [10. COM threading](#10-com-threading), 7–20 |
| Frontends and the original HTTP contract | [21. Frontend architecture](#21-frontend-architecture), 21–35 |
| Security, limits, failures, and correctness | [36. Security](#36-security-model), 36–49 |
| Testing, deployment, and scope decisions | [50. Tests](#50-test-strategy), [62. Phases](#62-implementation-phases), [review checklist](#appendix-a-design-review-checklist) |

## 1. Document purpose

This project provides DA access through a bounded adapter rather than a general DA-to-UA gateway.

Its objective is:

> Build a thin access adapter that lets modern applications use an OPC DA server without handling COM/DCOM or changing the source's data and behavior.

The document establishes the constraints behind the feature set:

1. Why the project exists.
2. Which source information must survive.
3. Which behaviors are prohibited.
4. Which extensions fit the product boundary.
5. How OPC DA/COM constraints are isolated from the Go runtime.
6. How additional frontends retain the adapter's scope.
7. Why HTTP is the first access frontend and a DA runtime validation tool.
8. Why gRPC and later OPC UA share the same DA-native contract.

A reader should understand the implementation direction and boundaries without knowing earlier discussions.

## 2. Background and problem

### 2.1 Existing systems

Industrial systems still use OPC DA.

Replacing stable equipment or software with OPC UA has costs beyond replacing an old protocol:

- DA server replacement or upgrade.
- Vendor licenses.
- Equipment downtime and validation.
- Operator and developer training.
- Running old and new systems together.
- Validating new client/server combinations.

Modern applications may be unable or unwilling to manage the DA server's COM/DCOM environment directly.

Example:

- Go, Java, and Python backend services.
- Containers or separate service layers.
- Programs that support UA clients only.
- Internal tools that use HTTP APIs.
- Services that need typed RPC.
- Applications that should not embed DCOM configuration and COM threading.

The adapter places a small compatibility layer in front of the existing DA server without replacing it.

### 2.2 The original DA-to-UA approach

The initial idea was:

```text
Existing OPC DA Server
        |
        | COM / OPC DA
        v
   DA -> UA Adapter
        |
        | OPC UA
        v
Existing OPC UA Client
```

Commercial and open-source DA-to-UA wrappers already address this use case.

Another implementation alone would not justify the project:

> Another OPC DA-to-UA converter written in Go.

A different implementation language or library would not provide enough additional product value.

### 2.3 Access adapter scope

The problem was reframed around source access.

The objective is modern access to OPC DA itself; UA is one access method.

```text
                    OPC DA Server
                          |
                          v
                  +----------------+
                  | OPC DA Runtime |
                  +--------+-------+
                           |
                    DA-native contract
                           |
              +------------+------------+
              |            |            |
              v            v            v
          HTTP/JSON       gRPC        OPC UA
```

The set of source protocols does not expand.

Only the access frontends expand.

## 3. Product definition

### 3.1 Definition

> OPC DA Access Adapter exposes one OPC DA server's Browse, Read, Write, and later Subscribe capabilities through modern frontends while preserving source semantics.

### 3.2 Product boundary

The project does not provide:

- An industrial integration gateway.
- Protocol Converter Platform
- IoT Gateway
- Historian
- SCADA
- OPC UA SDK
- OPC DA SDK
- Message Broker
- Asset Modeling Platform
- ETL/Transformation Engine
- Data Normalization Service
- Rule Engine
- Multi-source Aggregator

These exclusions constrain the architecture, not just the product name.

## 4. Invariants

These rules define the product boundary.

### INV-1. OPC DA is the only source protocol

Allowed:

```text
OPC DA -> Adapter -> Frontend
```

Forbidden:

```text
Modbus -----+
S7 ---------+
BACnet -----+--> Common Core --> Frontends
OPC DA -----+
```

Adding any of these sources would break that boundary:

- Modbus
- Siemens S7
- BACnet
- EtherNet/IP
- MQTT source
- OPC UA source
- REST source
- Database source
- Other industrial protocols.

Use a separate project when another source protocol is required.

### INV-2. Preserve source semantics

The adapter must not change the meaning supplied by the DA server.

Prohibited examples:

- Unit conversion.
- scaling
- offset
- tag rename
- tag merge
- tag split
- derived value
- Calculated expressions.
- smoothing
- interpolation
- Returning a last-good value as a current valid value.
- Arbitrarily repairing an invalid value.
- Regenerating a timestamp and presenting it as source time.
- Changing Bad Quality to Good.
- Inferring vendor-specific meaning.
- Inferring an Asset/Device hierarchy.

Only deterministic representation mapping required by the target protocol is permitted.

### INV-3. Preserve source information

A thin adapter must not discard source metadata merely to simplify its model.

Preserve at least:

- ItemID
- Canonical VARTYPE / DataType
- Value
- Quality
- Timestamp
- Timestamp presence
- HRESULT
- Access Rights
- Relevant item properties supplied by the source.

Document any information the target frontend cannot represent.

### INV-4. Do not introduce a common industrial model

Do not add models such as:

```text
Asset
Device
Metric
Measurement
Telemetry
NormalizedTag
Point
Signal
CommonValue
```

The core must retain DA-native vocabulary.

Example:

```text
DAItem
DAValue
DAQuality
DAError
DAAccessRights
DABrowseEntry
```

### INV-5. Frontends expose DA capabilities

The primary frontend operations are:

```text
Browse
Read
Write
Subscribe
```

A frontend generally does not belong here if it requires:

- Topic routing
- Asset query
- Rule execution
- Storage query
- Aggregation query
- Transformation pipeline
- Data enrichment
- Schema registry
- Workflow
- Event processing DSL

### INV-6. Do not persist process data

Forbidden:

- Historian
- Time-series databases.
- last-good-value DB
- tag value cache DB
- event history DB
- persistent telemetry store

Allowed:

- Configuration files.
- Logs.
- Bounded in-process runtime state.
- COM handle cache
- Temporary Browse and request state.

Operational logs must not record process values by default.

### INV-7. One instance serves one DA server

The default architecture is:

```text
Adapter Process A -> DA Server A
Adapter Process B -> DA Server B
```

Do not aggregate multiple DA servers into one namespace in a single process.

This keeps the following concerns bounded:

- Source routing.
- Namespace collisions.
- Multi-server gateway behavior.
- Failure boundaries.
- Server-specific compatibility problems.
- Configuration complexity.

Run separate adapter instances for separate DA servers.

### INV-8. Remote DCOM is out of scope

The initial target is a DA server accessible through local COM on the same Windows machine as the adapter.

```text
[Windows Machine]
  OPC DA Server
       ^
       | Local COM
       |
  Access Adapter
```

Remote DCOM configuration and tunneling are separate concerns.

If remote DA is required, use a separate DA tunneler or DCOM-less proxy project.

Do not add tunneling to this adapter.

### INV-9. Correctness precedes concurrency

Do not call COM objects directly from multiple goroutines merely because goroutines are convenient.

The initial implementation serializes COM access on a dedicated OS thread.

Optimize only after collecting benchmarks and vendor compatibility evidence.

### INV-10. Unsupported operations fail explicitly

Do not hide an unsupported VARTYPE, Browse capability, or Write form behind an invented conversion.

Example:

```text
VT_UNKNOWN -> string       X
SAFEARRAY -> flat JSON      X
Bad Quality -> last good    X
unknown HRESULT -> success  X
```

Instead:

```text
explicit unsupported/error result
+
raw source code/type information where available
```

Return an explicit error with the available raw source code and type information.

## 5. Technology and implementation constraints

### 5.1 Language

Use Go.

Go supports the deployment and runtime goals:

- A single executable.
- Direct Windows API access.
- A small runtime footprint.
- Network frontend implementation.
- Clear separation between goroutines and the COM runtime.
- x86 and x64 builds.
- Long-running foreground and service operation.

### 5.2 OPC DA and OPC UA dependencies

Do not use an existing OPC DA or OPC UA implementation library as a core implementation dependency.

The following may be references or interoperability targets, but not implementation bases:

- konimarti/opc
- huskar-t/opcda
- OpenOPC/OpenOPC-DA
- TitaniumAS
- asyncua
- open62541
- Other general-purpose OPC DA/UA SDKs.

The constraint does not require reimplementing every general-purpose facility.

It keeps the adapter focused on the required vertical slices.

Examples of acceptable general-purpose dependencies:

- Windows syscall/low-level helper
- General HTTP libraries.
- A later official or general-purpose gRPC runtime.
- test library

Review supply chain, license, injection surface, and maintenance before adding a dependency.

### 5.3 Keep UA implementation bounded

If the UA frontend is implemented directly, keep `internal/opcua` from becoming a general-purpose UA SDK.

Implement the required features only:

Example:

```text
UA-TCP framing
UA Binary
SecureChannel minimum
Session
Browse
Read
Write
Later Subscription/MonitoredItem/Publish
```

Do not implement Events, Historical Access, or PubSub unless the DA access adapter requires them.

## 6. Runtime platform

### 6.1 Windows

OPC DA uses COM/DCOM, so the DA runtime executes on Windows.

Initial build targets:

```text
windows/386
windows/amd64
```

### 6.2 Bitness

Validate 32-bit and 64-bit environments separately.

COM registration and server behavior can depend on bitness. Do not assume:

> One 64-bit binary can connect to every DA server.

Record these in the compatibility matrix:

- Windows version
- Adapter architecture
- DA Server vendor/product/version
- DA server bitness or COM registration characteristics.
- Connect
- Browse
- Read
- Write
- Subscribe, once implemented.

## 7. Architecture

```text
                           +-----------------------+
                           |   OPC DA Server       |
                           |  Existing / Legacy    |
                           +-----------+-----------+
                                       ^
                                       |
                                  Local COM
                                       |
                           +-----------+-----------+
                           |                       |
                           |   OPC DA Runtime      |
                           |                       |
                           | - COM Apartment       |
                           | - Connection          |
                           | - Browse              |
                           | - Group/Item handles  |
                           | - Read                |
                           | - Write               |
                           | - Reconnect           |
                           | - Subscribe (future)  |
                           |                       |
                           +-----------+-----------+
                                       |
                              DA-native Contract
                                       |
                +----------------------+----------------------+
                |                      |                      |
                v                      v                      v
        +---------------+      +---------------+      +---------------+
        | HTTP/JSON     |      | gRPC          |      | OPC UA        |
        | first         |      | future        |      | future        |
        +---------------+      +---------------+      +---------------+
```

Dependency direction:

```text
Frontend -> DA Runtime
```

Forbidden:

```text
DA Runtime -> HTTP
DA Runtime -> gRPC
DA Runtime -> UA
```

The DA runtime must not know which frontend calls it.

## 8. Process structure

Suggested package structure:

```text
cmd/
  adapter/
    main.go

internal/
  opcda/
    runtime_windows.go
    com_windows.go
    server_windows.go
    group_windows.go
    item_windows.go
    browse_windows.go
    read_windows.go
    write_windows.go
    variant_windows.go
    quality.go
    hresult.go

  domain/
    item.go
    value.go
    error.go
    operation.go

  frontend/
    http/
      server.go
      browse.go
      read.go
      write.go
      encoding.go

  app/
    service.go
    config.go
    lifecycle.go
```

Planned extension:

```text
internal/frontend/grpc/
internal/frontend/opcua/
```

Dependency direction matters more than directory names.

## 9. OPC DA Runtime

### 9.1 Responsibilities

The DA runtime is the core of the adapter.

Responsibilities:

- COM initialization and cleanup.
- DA Server activation
- server connection lifecycle
- group lifecycle
- item registration
- Browse
- ReadBatch
- WriteBatch
- Obtaining source metadata.
- Preserving HRESULTs.
- reconnect
- handle invalidation
- future subscription callbacks

Outside its responsibilities:

- HTTP JSON encoding
- gRPC protobuf
- UA StatusCode
- Authentication policy.
- UI
- DB
- data normalization

## 10. COM threading

### 10.1 Thread ownership

Every thread that uses COM must initialize the COM library.

The Go runtime can move goroutines between OS threads, so ordinary goroutines must not directly own COM objects.

Initial structure:

```text
HTTP/gRPC/UA goroutines
        |
        | request
        v
+-------------------------+
| DA command queue        |
+------------+------------+
             |
             v
+-------------------------+
| Dedicated DA OS Thread  |
| runtime.LockOSThread()  |
| CoInitializeEx(...)     |
| COM objects owned here  |
+------------+------------+
             |
             v
        OPC DA Server
```

### 10.2 Apartment

The initial design prioritizes compatibility with a dedicated apartment-threaded runtime.

The dedicated thread performs these steps:

1. `runtime.LockOSThread()`
2. `CoInitializeEx`
3. Create COM objects.
4. Process DA commands.
5. Pump messages as required.
6. COM object Release
7. `CoUninitialize`
8. Exit the thread.

All steps run on that owning thread.

Do not pass COM interface pointers directly to another goroutine or thread.

MTA or multiple workers may be considered later, but require real vendor compatibility evidence.

### 10.3 Message Pump

An STA implementation must satisfy its message-loop requirements.

Message dispatch becomes essential when callbacks such as these are introduced:

- `IOPCDataCallback`
- `IOPCShutdown`
- connection point callback

Even synchronous v0 Read/Write must allow safe STA message processing.

## 11. OPC DA Baseline

The initial baseline is OPC DA 2.05a.

Principal interfaces:

- `IOPCServer`
- `IOPCItemMgt`
- `IOPCSyncIO`
- `IOPCBrowseServerAddressSpace` — optional
- `IOPCItemProperties`
- `IOPCCommon`
- `IConnectionPointContainer`
- A later connection point for `IOPCDataCallback`.
- Group-state interfaces where required.

OPC DA 3.0 is not required for v0.

## 12. Stateful protocol runtime

The adapter does not store process data, but it does retain protocol state.

The distinction is:

> **No persistent process-data storage, but stateful protocol runtime.**

Examples of runtime state:

- COM initialized state
- DA server COM interface
- Group server handle
- Group object/interface
- item server handles
- item registration cache
- canonical VARTYPE
- access rights
- Browse current position during one operation
- reconnect state
- generation/incarnation
- future callback registrations

A DA 2.05a implementation cannot treat this protocol state as absent.

## 13. Server Connection Lifecycle

Example lifecycle:

```text
STARTING
   |
   v
CONNECTING
   |
   +------ success ------> CONNECTED
   |                         |
   |                         |
   +------ failure ----------+
                             |
                             v
                        RECONNECTING
                             |
                             v
                         CONNECTED

shutdown -> STOPPING -> STOPPED
```

A serious COM hang or internal invariant violation may also enter:

```text
DEGRADED
```

This is a separate runtime state.

### 13.1 Source configuration

v0 source identifier:

- ProgID
- Or a CLSID.

Specify exactly one identifier.

Do not accept a remote host.

Example:

```text
source.progId = "Matrikon.OPC.Simulation.1"
```

### 13.2 Reconnect

On disconnect:

1. Mark the current connection `disconnected`.
2. Invalidate every group and item handle.
3. Do not reuse stale handles.
4. Attempt reconnect in the background.
5. Create a new connection generation.
6. Recreate the internal group.
7. Lazily register items again when needed.
8. Return to `connected` after success.

Do not return an old value as Good during reconnect.

### 13.3 Backoff

Connection failures must not create a busy loop.

Reconnect may use bounded exponential backoff with jitter.

Choose the default intervals before implementation, subject to these requirements:

- No unbounded tight retries.
- No log flood.
- Automatic recovery when the server becomes available.
- An observable current state for operators.

## 14. Group and item handles

### 14.1 Required protocol state

DA 2.05a Read/Write normally use this sequence:

```text
IOPCServer::AddGroup
        |
        v
IOPCItemMgt::AddItems
        |
        v
IOPCSyncIO::Read / Write
```

An HTTP request cannot be modeled as a stateless read that supplies only an ItemID string.

### 14.2 Internal group

A v0 instance serves one DA server and maintains a simple internal group.

Default strategy:

- Create the internal group when connecting.
- Add items lazily on first use.
- server handle cache
- Discard all handles on reconnect.

### 14.3 Item registration cache

The handle cache is a runtime optimization, not persistent data.

Example retained fields:

```text
ItemID
ServerHandle
CanonicalVARTYPE
AccessRights
ConnectionGeneration
```

Rules:

- Bound the cache.
- Never reuse handles from an earlier connection generation.
- An invalid-handle or unknown-item result may trigger entry revalidation.
- Never use the cache to hide a source error.
- Do not persist the cache.

Choose the maximum item count after implementation benchmarks.

## 15. Browse

### 15.1 Optional capability

`IOPCBrowseServerAddressSpace` is optional in DA 2.x; not every DA 2.05a server supports Browse.

Therefore:

```text
Browse unsupported != Adapter unusable
```

Known ItemIDs may still support Read and Write.

### 15.2 No invented Browse fallback

Do not compensate for an unsupported Browse interface by adding:

- CSV tag database
- manual tag mapping DB
- persisted tag registry
- inferred hierarchy
- A hierarchy inferred from delimiters.

Report the capability explicitly:

```text
browse: unsupported
read: available
write: source-dependent
```

Users can supply known ItemIDs for Read and Write.

### 15.3 Browse the source

The rule is:

> Do not maintain Browse results as a separate adapter data model.

Forbidden:

```text
startup full scan -> DB -> Browse from DB
```

Use:

```text
Frontend Browse request
      |
      v
DA Runtime Browse
      |
      v
OPC DA Server
```

For dynamic address spaces, use what the source currently exposes.

### 15.4 Browse state serialization

DA 2.x Browse is stateful: it uses a current Browse position.

Concurrent requests that move the same Browse object can produce incorrect results.

Serialize Browse on the dedicated DA thread in v0.

Keep each HTTP Browse operation independent where possible:

1. Reset to the root position.
2. Follow the requested path in order.
3. Enumerate at that position.
4. Collect results.
5. End the operation.

Do not retain a frontend session's Browse position between operations.

### 15.5 Branch and item identity

Item:

- Use the exact DA ItemID as identity.
- Do not infer ItemIDs from display names or Browse paths.

Branch:

- Use a stable branch identity if the DA server supplies one.
- Otherwise use a sequence of Browse names for frontend navigation.
- That sequence is a navigation representation, not a source semantic identity.

## 16. Read

### 16.1 Batch first

The internal API is batch-first.

```go
ReadBatch(ctx, []ItemID, ReadOptions) -> []DAReadResult
```

A single-item HTTP read can use a batch containing one item.

### 16.2 Read source

The internal contract may represent both DA `device` and `cache` sources.

The v0 HTTP default prioritizes `device` for correctness.

Exposing DA cache reads later would remain within scope because they are a native DA capability.

### 16.3 Partial failures

A failed item must not turn an otherwise handled batch into a generic 500 response.

Example:

```text
Item A -> S_OK
Item B -> OPC_E_UNKNOWNITEMID
Item C -> S_OK
```

Keep each item's result.

## 17. Write

### 17.1 Write through to the source

```text
Frontend Write
   |
   v
DA Runtime Write
   |
   v
IOPCSyncIO::Write
   |
   v
DA Server
```

A command queue must not transform Write semantics.

### 17.2 DA 2.05a Write scope

The v0 DA 2.05a contract writes values only.

Do not pretend to support writes to Status, Quality, or Timestamp.

DA 3.0 `WriteVQT` is a separate extension.

### 17.3 Default Write policy

An industrial Write can cause a physical control action.

The policy is:

- Implement the capability.
- Keep Write disabled by default.
- Require explicit enablement.
- Never retry Write automatically.
- Document that a timeout can leave the outcome unknown.
- Do not promise rollback.

### 17.4 Type validation

Do not coerce every JSON number into the source's canonical DA type.

The request must remove type ambiguity.

Suggested request:

```json
{
  "itemId": "Random.Int2",
  "dataType": "VT_I2",
  "value": 42
}
```

The runtime checks:

- Whether the transport value is losslessly representable as the requested VARTYPE.
- Its relationship to the source's canonical type.
- Whether the requested conversion is supported; otherwise fail.

No silent narrowing or overflow is permitted.

## 18. Subscribe — core capability

Subscribe is a DA capability shared by frontends.

The plan places it in the DA runtime itself.

```text
DA Runtime Subscribe
        |
        +----> gRPC server stream
        |
        +----> OPC UA Subscription/MonitoredItem
        |
        +----> possible later HTTP streaming frontend
```

Do not create subscriptions just to Browse DA.

### 18.1 DA side

Implement later DA 2.x subscriptions through `IOPCDataCallback` and connection points.

Callback COM object ownership, apartment/thread affinity, and message pumping must remain correct.

### 18.2 Excluded from v0

The first HTTP validation phase does not implement Subscribe.

Its validation scope is:

- Connect
- Browse
- Read
- Write
- VARTYPE
- Quality
- Timestamp
- HRESULT
- reconnect

These are the v0 validation goals.

## 19. DA-native Internal Contract

Define the core contract independently of the frontend.

### 19.1 DAItemID

```go
type DAItemID string
```

Do not canonicalize the source string.

Forbidden transformations:

- trim
- lower/upper case
- delimiter change
- dot/slash replacement
- whitespace normalization

Use the exact source string as identity.

### 19.2 DAVarType

Do not replace VARTYPE with a Go reflection type.

Preserve the original COM VARTYPE.

Example:

```text
VT_I2
VT_I4
VT_R4
VT_R8
VT_BSTR
VT_BOOL
...
```

Rationale:

```text
VT_I2 -> Go int -> source type lost
VT_I4 -> Go int -> source type lost
```

These conversions lose the source type distinction.

The internal representation must contain at least:

```text
Raw VARTYPE code
Symbolic name if known
Array/byref flags
```

### 19.3 DAValue

Conceptual model:

```go
type DAValue struct {
    ItemID           DAItemID
    VarType          DAVarType
    Value            TypedValue
    QualityRaw       uint16
    Timestamp        time.Time
    TimestampPresent bool
    HRESULT          int32
}
```

Keep COM VARIANT ownership separate from the Go value representation.

### 19.4 Quality

Preserve raw 16-bit DA Quality.

```text
lower 8 bits: QQSSSSLL
upper 8 bits: vendor-specific
```

The core, HTTP, and gRPC preserve all 16 bits.

Raw Quality remains authoritative even when interpretation helpers exist.

### 19.5 Timestamp

Preserve a source timestamp when present.

Do not substitute adapter processing time for source time.

A separate observation-time field may be added if needed, but must remain clearly distinct.

### 19.6 HRESULT

Do not reduce HRESULT to a generic error string.

Distinguish:

1. COM method-level HRESULT
2. item-level HRESULT array

Use COM `SUCCEEDED/FAILED` semantics, not just `hr == 0`.

Preserve the raw HRESULT as well.

### 19.7 Access Rights

Preserve source Read/Write access rights.

Expose them unchanged when the frontend can represent them.

Do not infer a new adapter authorization policy from those rights.

## 20. VARIANT and value representation

### 20.1 Type preservation

A conversion to Go must not discard the source VARTYPE.

### 20.2 Scalars first in v0

Start with scalar VARTYPEs needed for real-site validation.

Expected priority:

- signed/unsigned integer
- float/double
- boolean
- BSTR
- Date and decimal types where needed.

Track the supported set explicitly against test results.

### 20.3 Unsupported VARTYPE

For an unsupported type, return:

```text
UNSUPPORTED_VARTYPE
raw vartype code
item id
```

Keep this an explicit failure.

Do not convert it to a different type.

### 20.4 SAFEARRAY

A SAFEARRAY implementation must preserve:

- element VARTYPE
- dimensions
- lower bound
- length per dimension
- element values

Flattening an array while discarding its dimensional metadata is prohibited.

If v0 lacks full SAFEARRAY support, return an explicit unsupported result.

The DA layer now preserves all five fields above. Its representation is defined by
[ADR-0019](adr/0019-safearray-representation.md), including element order,
lower bounds, and frontend-specific encodings. Validation status is recorded in
[implementation status](../implementation-status.md): implementation alone
does not establish compatibility, and promising support before validation is a non-goal.

### 20.5 COM memory ownership

Ownership is a required review topic for long-running stability.

Review:

- `IUnknown::Release`
- `CoTaskMemFree`
- `VariantClear`
- BSTR ownership / `SysFreeString`
- SAFEARRAY lifecycle
- enumerator Release
- returned buffer ownership
- callback reference counting

Do not rely on eventual garbage collection to release COM resources.

## 21. Frontend Architecture

Frontend responsibilities:

1. transport request decode
2. Convert to a DA-native request.
3. Call the DA runtime.
4. Encode the DA-native result for the transport.

A frontend does not perform:

- source routing
- data transformation
- tag mapping
- tag renaming
- normalization
- Changes to caching semantics.
- Changes to retry semantics.

## 22. Frontend Admission Rule

A new frontend must meet every condition:

1. Express DA Browse, Read, Write, and Subscribe directly.
2. Preserve ItemID, VARTYPE, Quality, Timestamp, and HRESULT.
3. Require no new industrial data model.
4. Require no persistent process-data storage.
5. Require no routing or topic semantics.
6. Retain OPC DA as the only source protocol.
7. Keep core behavior consistent across frontends.

An extension that fails these conditions belongs in a separate project.

## 23. Frontend scope

### 23.1 Approved directions

The baseline identifies these access frontends:

- HTTP/JSON
- gRPC
- OPC UA

### 23.2 Conditional candidates

Consider only when an actual requirement exists:

- WebSocket
- Named Pipe / local IPC
- XML-DA
- custom TCP

### 23.3 Exclusions

Excluded by default:

- MQTT
- Kafka
- NATS
- GraphQL
- AMQP
- event bus

These commonly introduce topic, event, or query semantics beyond access transport.

## 24. Purpose of v0

v0 is a technical validation release rather than a complete industrial gateway.

Its goal is to validate the DA runtime itself.

Validation questions:

- Is direct COM OPC DA access stable from Go?
- Does Browse work against a real DA server?
- Is item registration correct?
- Is batch Read correct?
- Is Write correct?
- Is VARTYPE preserved?
- Are all 16 raw Quality bits preserved?
- Is the timestamp preserved?
- Are per-item HRESULTs preserved?
- Are stale handles rejected after reconnect?
- How do x86 and x64 behavior differ?
- Does a long run avoid accumulating COM resource leaks?

## 25. v0 Frontend: HTTP/JSON

HTTP was selected first because it supports:

- Immediate checks with curl or Postman.
- No separate client SDK.
- No `.proto` or stub generation.
- Straightforward DA runtime debugging.
- A small frontend implementation.
- Human inspection of values, types, quality, and errors.

This does not make HTTP the permanent primary frontend.

## 26. v0 HTTP API

Initial endpoints:

```text
GET  /v1/status
POST /v1/browse
POST /v1/read
POST /v1/write
```

v0 has no Subscribe endpoint.

## 27. GET /v1/status

Purpose:

- Adapter state.
- DA connection state.
- source capability
- HTTP listener state.
- Write enablement.

Example:

```json
{
  "state": "connected",
  "source": {
    "progId": "Matrikon.OPC.Simulation.1",
    "clsid": "{...}",
    "connectionGeneration": 3
  },
  "capabilities": {
    "browse": "supported",
    "read": true,
    "write": true,
    "subscribe": false,
    "properties": "supported"
  },
  "writeEnabled": false,
  "frontend": {
    "http": {
      "listening": true
    }
  }
}
```

Status must not contain process values.

## 28. POST /v1/browse

### 28.1 Request

Do not maintain a frontend session's Browse position on the server.

Pass the navigation path sequence in each request.

```json
{
  "path": ["Channel1", "Device1"],
  "filter": "all"
}
```

`path=[]` selects the root.

`path` represents DA Browse navigation, not a semantic ItemID.

### 28.2 Response

```json
{
  "path": ["Channel1", "Device1"],
  "entries": [
    {
      "kind": "branch",
      "name": "GroupA",
      "itemId": null
    },
    {
      "kind": "item",
      "name": "Temperature",
      "itemId": "Channel1.Device1.Temperature",
      "dataType": {
        "code": 4,
        "name": "VT_R4"
      },
      "accessRights": {
        "read": true,
        "write": false
      }
    }
  ]
}
```

Leave unavailable source metadata as `null/unknown`.

Do not infer replacement metadata.

### 28.3 Browse result limit

Bound every Browse response.

v0 may fail explicitly when a result exceeds the hard limit instead of implementing continuation prematurely.

```text
BROWSE_RESULT_LIMIT_EXCEEDED
```

Silent truncation is prohibited.

Adding continuation requires a separate ADR covering dynamic source semantics and COM enumerator lifetime.

## 29. POST /v1/read

### 29.1 Request

```json
{
  "source": "device",
  "items": [
    { "itemId": "Random.Int2" },
    { "itemId": "Random.Real8" }
  ]
}
```

### 29.2 Response

Keep `results` in request `items` order. Do not reorder requests for optimization or remove failed items.

```json
{
  "results": [
    {
      "itemId": "Random.Int2",
      "ok": true,
      "dataType": {
        "code": 2,
        "name": "VT_I2"
      },
      "valueEncoding": "json",
      "value": 42,
      "quality": 192,
      "timestamp": "2026-08-22T12:30:00.1234567Z",
      "timestampPresent": true,
      "hresult": {
        "value": 0,
        "hex": "0x00000000"
      }
    },
    {
      "itemId": "Missing.Item",
      "ok": false,
      "hresult": {
        "value": -1073479672,
        "hex": "0xC0040008"
      }
    }
  ]
}
```

These numbers are examples, not a promise of a particular HRESULT.

## 30. JSON Value Encoding

JSON cannot represent every COM VARIANT losslessly without an encoding contract.

Use explicit transport representation rules:

| DA type class | JSON representation |
|---|---|
| BOOL | JSON boolean |
| I1/UI1/I2/UI2/I4/UI4 | JSON integer |
| I8/UI8 | decimal string |
| R4/R8 finite | JSON number |
| R4/R8 NaN/Inf | explicit special-float encoding or unsupported until implemented |
| BSTR | JSON string |
| DECIMAL | exact decimal string |
| DATE | explicit OLE DATE representation or an explicitly supported mapping |
| SAFEARRAY | v0 unsupported unless full dimensional metadata preserved |
| unknown/custom | explicit unsupported |

Rule:

> Do not coerce a source type merely to make JSON encoding convenient.

## 31. POST /v1/write

### 31.1 Request

```json
{
  "items": [
    {
      "itemId": "Random.Int2",
      "dataType": "VT_I2",
      "valueEncoding": "json",
      "value": 42
    }
  ]
}
```

### 31.2 Response

Keep Write results in request order. Do not reorder Writes for throughput or semantically merge Writes from separate requests.

```json
{
  "results": [
    {
      "itemId": "Random.Int2",
      "ok": true,
      "hresult": {
        "value": 0,
        "hex": "0x00000000"
      }
    }
  ]
}
```

### 31.3 Write disabled

When Write is disabled, do not forward the request to the source.

Example:

```text
HTTP 403
WRITE_DISABLED
```

## 32. HTTP Error Boundary

### 32.1 Request-level failure

Example:

- malformed JSON
- invalid schema
- A batch exceeding the item limit.
- Adapter not ready
- write disabled
- internal runtime unavailable

Represent these as HTTP status failures.

### 32.2 Item-level failure

If DA rejects only particular items:

```text
HTTP request handled successfully
+
results[] retains each item's ok/hresult
```

Do not hide a partial failure behind a generic 500.

### 32.3 Suggested HTTP statuses

| Condition | Status |
|---|---:|
| Handled batch, including partial item failure | 200 |
| malformed request | 400 |
| write disabled | 403 |
| Source does not support Browse | 422 |
| DA Runtime disconnected/unavailable | 503 |
| Adapter-level request deadline | 504 |
| Implementation bug or unexpected internal failure | 500 |

Finalize the error body during implementation, keeping DA HRESULTs distinct from adapter errors.

## 33. Timeouts and cancellation

### 33.1 HTTP disconnect != COM call cancellation

Do not kill the COM thread when an HTTP client disconnects during a synchronous COM call.

Forbidden:

- `TerminateThread`
- unsafe COM object destruction
- Forcibly interrupting a Write whose outcome is unknown.

### 33.2 queued request

Context cancellation may remove a request that has not started on the COM thread.

### 33.3 in-flight request

For an in-flight COM call:

- The runtime may wait for the result.
- The frontend deadline may expire first.
- The response may be discarded for that client.
- Write is never retried automatically.

### 33.4 COM hang

A vendor COM call that blocks indefinitely is an operational risk.

v0 must not claim recovery by forcibly terminating its thread.

Possible policy:

- Mark the runtime degraded through a watchdog.
- Fail new requests immediately.
- Require an operator to restart the process.

Hard isolation through a DA runtime subprocess needs a separate design and is outside v0.

## 34. gRPC Frontend — Phase 6

gRPC supports:

- typed client API
- Go, Java, and .NET service integration.
- Later server streaming for Subscribe.

Phase 6 unary service:

```text
Status
Browse
Read
Write
```

Phase 6 excludes Subscribe and server streaming. Add them in a later phase
after the DA callback core is implemented and validated.

Protobuf retains DA-native fields.

Example:

```text
item_id
vartype_raw
quality_raw
timestamp
timestamp_present
hresult
typed value
```

Do not introduce Asset/Metric protobuf models.

## 35. OPC UA frontend

OPC UA is one of the adapter's access frontends.

Structure:

```text
Existing OPC DA Server
        |
        v
   DA Runtime
        |
  DA-native Contract
        |
        v
  Minimal UA Server
        |
        v
Existing UA Client
```

### 35.1 Roles

The adapter acts as a UA server on the UA side.

It acts as a DA client on the DA side.

### 35.2 Address Space

Default mapping:

```text
DA Branch -> UA Folder/Object
DA Item   -> UA Variable
```

Use the exact DA ItemID as item identity.

Do not treat Browse paths as ItemIDs or reconstruct ItemIDs by guessing a vendor delimiter.

UA identity rules:

- Keep the namespace URI stable.
- Do not use a namespace index such as `ns=2` as persistent identity.
- Map an item NodeId directly to its exact DA ItemID where possible.
- Prefer a source-supplied stable branch identity; otherwise use a navigation representation.
- Prefer BrowseName and DisplayName values supplied by DA Browse.
- Do not rename tags or replace dots and spaces with underscores for convenience.

### 35.3 Standard mapping

Use the COM DA mapping in OPC UA Part 8.

Example:

```text
VT_I2 -> Int16
VT_I4 -> Int32
VT_R4 -> Float
VT_R8 -> Double
...
```

DA Quality:

```text
raw DA quality -> standard UA StatusCode mapping
```

DA Timestamp:

```text
DA Timestamp -> UA SourceTimestamp
```

HRESULT:

```text
DA error -> UA StatusCode
```

### 35.4 Quality vendor bits

The core preserves the vendor-specific upper byte of DA Quality.

The standard COM DA-to-UA mapping may not preserve that byte in a UA StatusCode.

Follow the standard mapping; do not invent a custom UA property solely to carry raw Quality.

Document this loss.

### 35.5 UA implementation scope

The initial UA vertical slice includes:

- UA-TCP Hello/Acknowledge
- message framing/chunking
- UA Binary
- minimum SecureChannel
- endpoint discovery
- Session create/activate/close
- required AddressSpace
- Browse/BrowseNext
- Read
- Write

After Subscribe is implemented in the DA runtime, add:

- Subscription
- MonitoredItem
- Publish

These build on the DA subscription core.

If a UA ServerTimestamp is needed, it may use the operation's adapter processing time. Keep it distinct from SourceTimestamp, whose authority is the DA timestamp.

A directly implemented UA wire parser must enforce:

- max message size
- max chunk count
- max array length
- max string/ByteString length
- max nesting/decode depth
- max sessions/subscriptions/monitored items
- Malformed-packet handling.
- Integer and length overflow protection.
- fuzz test

Direct implementation does not justify weaker UA protocol validation.

### 35.6 UA security

An initial interoperability prototype may use `SecurityPolicy=None + Anonymous`, subject to:

- Local and test use only.
- No production-readiness claim.
- Certificate lifecycle and secure policies before a real production release.

### 35.7 Conformance claim

Without official certification, do not claim:

- OPC UA Certified
- OPC UA Compliant
- OPC Foundation Certified

Do not claim compatibility with every UA client while only a limited subset is implemented.

Describe the implemented subset and executed interoperability results. Do not use the OPC Foundation logo or certification marks as project badges.

## 36. Security Model

### 36.1 Network exposure

An existing DA deployment may have used local COM only.

Opening HTTP, gRPC, or UA adds a network attack surface.

A relay still requires a security boundary.

### 36.2 v0 HTTP

The v0 HTTP frontend exists to validate DA access.

Default policy:

- Bind to loopback by default.
- Require explicit configuration for a remote bind.
- Disable Write by default.
- Make no production security claim.

### 36.3 Remote exposure

Later remote network use requires:

- TLS
- An authentication strategy.
- request limits
- connection limits
- audit/logging boundary
- write authorization boundary

Do not expand this into an RBAC platform.

Choose the authentication mechanism through a separate ADR.

## 37. Resource Limits

Direct protocol and COM handling requires hard resource limits.

Bound the following:

- HTTP request body size
- items per Read batch
- items per Write batch
- Browse entry count
- ItemID length
- BSTR length
- array size
- SAFEARRAY dimensions
- concurrent frontend requests
- queued DA commands
- recent diagnostic operations
- log size/rotation

Choose defaults after benchmarks and real-server validation.

Never use unlimited as the default.

## 38. Observability

Observability describes adapter operation, not process-data analysis.

Allowed:

- DA connection state
- connection generation
- Frontend listener state
- request count
- error count
- reconnect count
- queue depth
- operation latency
- bounded recent operation metadata

Forbidden:

- value trend chart
- historian
- value history
- alarm engine
- process dashboard
- persisted telemetry

## 39. Logging

Default logs must not contain process values.

Suggested log fields:

```text
timestamp
level
operation
item_count
duration
result
hresult
connection_generation
```

ItemIDs may also be sensitive; review verbose and debug policies separately.

Never put Write values in plaintext logs.

## 40. Persistence

No process-data state needs restoration after a restart.

Permitted persistent files:

- config
- logs
- Certificates and keys, after a secure frontend is introduced.
- compatibility/test artifact

Forbidden:

- cached values
- tag DB
- persisted browse tree
- write queue replay log
- historian

Never replay a Write after restart.

## 41. Configuration

Core v0 settings:

```text
source ProgID/CLSID
HTTP listen address
write enabled
timeouts
resource limits
logging
```

Alongside environment-based foreground execution, guided setup may write a bounded,
versioned configuration file. File execution does not merge the ambient
environment and names exactly one source and one HTTP
frontend. Never overwrite an existing file implicitly.

Prohibited configuration:

```text
tag mapping table
tag rename table
scaling
unit conversion
asset model
multi-server routing
```

## 42. Server Discovery

Bounded detection may inspect registrations in the local DA 2.0 component category
for setup convenience. It returns candidate registrations only;
it neither activates vendor servers nor guarantees connectivity.

Runtime execution still requires an explicit ProgID or CLSID. Never auto-select
a sole candidate or connect several candidates to one runtime.

Detection:

- Does not change runtime architecture.
- Is a local CLI operation, not an HTTP endpoint.
- Returns local registration metadata only.
- Does not perform remote DCOM discovery.

Guided setup requires an explicit operator choice even for one candidate.
It records the exact selected CLSID in configuration and never activates
a candidate during detection.

## 43. Concurrency

Frontends may accept concurrent requests.

The initial COM side executes them serially.

```text
many HTTP requests
        |
        v
bounded queue
        |
        v
single DA runtime OS thread
```

Benefits:

- apartment correctness
- Safe Browse position handling.
- Simpler handle lifetimes.
- Vendor compatibility first.
- Fewer race conditions.

Do not parallelize COM access until measurements establish a need.

## 44. Backpressure

Bound the DA command queue.

When it is full:

- Do not wait indefinitely.
- Do not grow memory without a limit.
- Return overload or unavailable to the frontend.

Do not silently drop Read or Write requests.

## 45. Consistent frontend semantics

HTTP, gRPC, and UA implementations share one source behavior.

Forbidden:

```text
HTTP -> cached read
gRPC -> device read
UA -> custom normalized value
```

Frontends must not choose different semantic paths implicitly.

Any selectable behavior must be requested explicitly as a DA-native option.

## 46. Failure Model

Keep three failure layers separate.

### 46.1 Transport/Frontend error

Example:

- malformed JSON
- invalid protobuf
- UA decoding error

### 46.2 Adapter/Runtime error

Example:

- queue full
- runtime stopped
- unsupported VARTYPE
- browse unsupported
- write disabled
- timeout

### 46.3 DA source error

Example:

- COM activation failure
- method HRESULT failure
- per-item HRESULT
- Bad Quality
- invalid ItemID
- bad rights
- server disconnected

Do not merge them into one generic error.

## 47. Stale Data Policy

After disconnect, do not return a saved result from an earlier Read.

Forbidden:

```text
DA disconnected
    |
return last value as Good
```

Instead:

```text
DA disconnected
    |
explicit unavailable/error
```

A previous value may exist only in an observability or debugging context, never as a successful current Read.

## 48. Correctness requirements

These take priority over performance:

- exact ItemID
- exact VARTYPE
- raw Quality
- source Timestamp
- raw HRESULT
- per-item failure
- COM apartment correctness
- ownership correctness
- reconnect generation correctness

## 49. Performance priorities

Optimize in this order:

1. correctness
2. interoperability
3. leak-free long run
4. batching
5. handle reuse
6. bounded cache
7. profiling
8. Concurrency, if needed.

Do not begin with multiple COM threads or complicated lock-free structures.

## 50. Test strategy

### 50.1 Unit Test

Required unit coverage:

- HRESULT success and failure semantics.
- HRESULT hex/decimal representation
- VARTYPE parsing
- numeric overflow detection
- JSON value encoding
- Quality raw preservation
- Timestamp conversion
- connection generation
- item handle invalidation
- batch partial failure model
- write disabled
- queue backpressure
- unsupported VARTYPE

### 50.2 COM marshalling test

Windows-specific test:

- VARIANT read/write
- BSTR
- integer widths
- float widths
- BOOL
- memory cleanup
- interface Release
- CoTaskMem memory

### 50.3 Integration Test

Run integration tests against a real DA server.

Minimum scenarios:

- connect
- disconnect
- reconnect
- Browse root
- nested Browse
- known ItemID Read
- batch Read
- Bad Quality
- invalid ItemID
- Write success
- Write denied
- type mismatch
- server restart
- stale handle invalidation

### 50.4 Architecture coverage

Validate each target separately:

```text
windows/386
windows/amd64
```

### 50.5 Vendor Compatibility

Use different server implementations where possible.

Example candidates:

- Matrikon simulation/server
- Kepware
- Real legacy vendor servers.

A pass against one vendor must not become a general OPC DA compatibility claim.

### 50.6 Soak Test

Observe during a long-running test:

- process memory
- Indicators of COM reference-count behavior.
- handle count
- goroutine count
- queue growth
- Repeated reconnects.
- repeated Browse
- repeated Add/Remove item
- repeated Write

Verify that resources stop growing; absence of a crash alone is insufficient.

## 51. HTTP Acceptance Test

The minimum flow must be testable with curl or Postman.

Example:

```text
1. status
2. browse
3. read known item
4. read multiple items
5. invalid item
6. write disabled
7. enable write
8. write
9. server stop
10. read -> unavailable
11. server restart
12. automatic reconnect
13. read -> success
```

## 52. Compatibility Matrix

Record executed results in the repository.

Example:

| DA Server | Version | Windows | Adapter Arch | Browse | Read | Write | Notes |
|---|---|---|---|---|---|---|---|
| Matrikon ... | ... | ... | amd64 | PASS | PASS | PASS | ... |

For later frontends:

| Client | Version | Frontend | Browse | Read | Write | Subscribe |
|---|---|---|---|---|---|---|

Support claims require executed evidence rather than inference.

## 53. CI

Initial CI:

```text
gofmt check
go test
go vet
static analysis
windows/386 build
windows/amd64 build
```

A hosted runner may lack a DA server; COM integration can require a separately provisioned or self-hosted environment.

A successful cross-build is not DA interoperability evidence.

## 54. Release

Longer-term distribution goals:

- windows/386 binary
- windows/amd64 binary
- checksums
- version metadata
- changelog/release notes
- A build procedure approaching reproducibility.

One executable provides foreground operation and an SCM service dispatcher.
Guided background setup explicitly installs and starts a Windows Service,
not a detached child process.

The service uses LocalService, automatic startup, SCM Stop/Shutdown, and
bounded graceful shutdown. It does not change AppID/DCOM permissions or RunAs
automatically. Each service name owns one configuration and source.
ADR-0011 records the details.

## 55. UI

v0 has no GUI.

Any later operational UI must retain a narrow scope.

Allowed:

- connection state
- listener state
- request/error counters
- config status
- recent operation metadata

Forbidden:

- value dashboard
- trend
- chart
- historian
- alarm
- control panel

## 56. Open-source policy

Publish the project in a public GitHub repository.

Initial repository-name candidate:

```text
opc-da-access-adapter
```

Display name:

```text
OPC DA Access Adapter
```

Example description:

> A thin access adapter for exposing OPC DA through modern interfaces without changing source semantics.

## 57. License

The baseline leaves the open-source license for an explicit final decision.

Recommended candidate at that point:

```text
Apache License 2.0
```

This recommendation is not itself a final license decision.

Choose the license explicitly when creating the repository.

## 58. Reusing existing projects

Existing open-source projects may be used for:

- behavior reference
- interoperability comparison
- bug/edge-case discovery
- client/server test target

Check the individual license and attribution requirements before copying code.

Direct implementation does not remove license obligations.

Do not copy official specification documents into this repository.

## 59. Product value

The number of frontends is not the product's value.

The intended benefits are:

1. Focus on one OPC DA source protocol.
2. DA-native semantics preservation
3. No separate common industrial model.
4. No tag mapping.
5. No data transformation.
6. A small deployment.
7. direct DA Runtime
8. Frontends that expose native DA capabilities.
9. Publicly recorded vendor compatibility results.

## 60. Gateway boundary

These proposals indicate gateway scope:

```text
"Add Modbus"
"Aggregate several servers"
"Rename tags"
"Convert units"
"Publish to MQTT"
"Store values in a database"
"Add an asset model"
"Add rules"
```

For each such proposal, ask:

> Is this required to provide access to OPC DA?

If not, it is outside scope.

## 61. Feature admission test

Before adding a feature, confirm every condition:

1. Is OPC DA still the only source protocol?
2. Are source data semantics unchanged?
3. Does it correspond directly to a DA capability?
4. Does it avoid persistent process data?
5. Does it avoid routing and aggregation?
6. Does it avoid a common model?
7. Does it preserve existing frontend semantics?
8. Is there an actual user requirement?

A clear failure means a separate project or rejection.

## 62. Implementation phases

### Phase 0 — Repository / Skeleton

Goals:

- OSS repository
- Go module
- Windows build
- CI
- scope/design docs
- empty DA Runtime lifecycle
- HTTP status endpoint

Excluded:

- fake data production behavior

### Phase 1 — COM Foundation

Goals:

- dedicated OS thread
- COM init/uninit
- ProgID/CLSID activation
- connect/disconnect
- correct Release/memory helpers
- status

Acceptance:

- Check for leaks across repeated start/stop cycles.
- x86/x64 build

### Phase 2 — DA Read Core

Goals:

- AddGroup
- AddItems
- handle cache
- VARTYPE
- AccessRights
- `IOPCSyncIO::Read`
- item-level HRESULT
- Quality
- Timestamp

HTTP:

```text
POST /v1/read
```

This is the first core milestone.

### Phase 3 — Browse

Goals:

- capability detection
- `IOPCBrowseServerAddressSpace`
- serialized browse
- path navigation
- branch/item distinction
- exact ItemID

HTTP:

```text
POST /v1/browse
```

Known-ItemID Read must remain usable when Browse is unsupported.

### Phase 4 — Write

Goals:

- strict typed write
- `IOPCSyncIO::Write`
- per-item HRESULT
- write disabled default
- no auto-retry

HTTP:

```text
POST /v1/write
```

### Phase 5 — Reliability

Goals:

- reconnect
- generation invalidation
- server restart scenario
- queue bounds
- timeout behavior
- soak tests
- resource leak fixes
- multiple vendor verification

### Phase 6 — gRPC Frontend

Add these unary services over the validated DA runtime:

```text
Status
Browse
Read
Write
```

Rationale:

- typed API
- future Subscribe streaming

Protobuf preserves DA-native vocabulary and scalar widths. Each process
explicitly selects HTTP or gRPC. This phase excludes Subscribe/streaming,
a frontend registry, and multi-source routing.

### Phase 7 — Subscribe

Implement Subscribe in the DA core first.

Then expose it through:

- gRPC stream
- UA Subscription

These frontends share that core capability.

### Phase 8 — OPC UA Frontend

Proceed after the DA runtime and mappings are stable.

Keep UA implementation work from delaying DA runtime validation.

## 63. Explicit v0 non-goals

v0 excludes:

- OPC UA
- gRPC
- Subscribe
- WebSocket
- SSE
- MQTT
- Kafka
- UI
- DB
- Historian
- Tag Mapping
- Tag rename
- Scaling
- Normalization
- Asset Model
- Multi DA Server
- Remote DCOM
- DCOM Tunneling
- Full OPC DA 3.0 support.
- Promising full SAFEARRAY support before validation.
- production auth/RBAC
- arbitrary plugin framework

## 64. No plugin framework

Frontend extensibility does not justify introducing these upfront:

```go
type Plugin interface { ... }
type FrontendRegistry struct { ... }
dynamic plugin loader
plugin marketplace
runtime extension API
```

Implement frontends explicitly in the source tree.

A plugin system requires an established need.

## 65. Public API versioning

HTTP:

```text
/v1/...
```

Use this versioned HTTP path prefix.

The API may remain experimental until v1 is declared publicly stable.

After a stable release, treat these as breaking changes:

- Removing a field.
- Changing its meaning.
- Changing its type.

They require a major compatibility change.

## 66. HTTP client retries

Clients may retry Read and Browse, but the adapter does not perform hidden unbounded retries.

Write requires particular care:

> A Write response timeout does not prove that the DA server did not apply the Write.

Never replay a timed-out Write automatically.

State this in the API documentation.

## 67. Process Shutdown

Normal shutdown order:

```text
1. Stop accepting new frontend requests.
2. Apply the queued-request completion or cancellation policy.
3. DA Runtime stop command
4. callback unadvise (future)
5. item/group cleanup
6. COM interface Release
7. CoUninitialize
8. Close HTTP/gRPC listeners.
9. process exit
```

Release COM resources on the owning OS thread.

## 68. Source Metadata

Do not discard source properties merely to keep the adapter thin.

Example:

- canonical data type
- access rights
- item properties

Do not rebuild them as Asset metadata.

If arbitrary vendor properties are required, expose a DA-native capability such as `GetProperties`.

## 69. Naming

Prefer these terms in code and documentation.

Preferred:

```text
OPC DA Source
DA Runtime
Access Frontend
DA-native Contract
ItemID
VARTYPE
Quality
Timestamp
HRESULT
```

Avoid:

```text
Asset
Metric
Normalized Tag
Telemetry Model
Protocol Gateway Core
Universal Data Model
```

## 70. Known risks

### 70.1 COM correctness

Incorrect Go/COM apartment and thread affinity can cause intermittent vendor-specific failures.

### 70.2 COM memory ownership

VARIANT, BSTR, CoTaskMem, and IUnknown ownership mistakes can cause long-term leaks or crashes.

### 70.3 Browse vendor differences

Browse is optional, and implementation quality varies between vendors.

### 70.4 x86/x64

Legacy DA deployments can differ by bitness and registration view.

### 70.5 Vendor quirks

Implementing the standard alone does not prove compatibility with every real DA server.

### 70.6 Write safety

A network Write API can expose additional physical equipment controls.

### 70.7 Frontend scope creep

Additional frontends can push the product toward a gateway.

Frontend count must not become a success metric.

## 71. Success criteria

Evaluate the project against operational outcomes.

Useful criteria:

- Stable operation against real legacy DA servers.
- Simple installation.
- Preserved source semantics.
- Published compatibility results.
- No accumulating long-term leaks.
- Automatic recovery after recoverable failures.
- Consistent results across frontends.

Unsuitable criteria:

- Number of protocols.
- Number of UI screens.
- Number of integrations.
- Number of configuration options.
- Code volume.

## 72. Decisions fixed in the baseline

The baseline fixes:

- Product direction: OPC DA Access Adapter.
- Source: OPC DA only
- Implementation language: Go.
- Windows runtime
- Local COM first.
- No remote DCOM or tunneler.
- DA 2.05a baseline
- direct DA implementation
- No existing OPC DA/UA implementation library as a core dependency.
- Separate DA runtime and frontends.
- Preserve DA-native semantics.
- no normalization/transformation
- no common industrial model
- no process-data persistence
- Browse, Read, Write, and later Subscribe.
- Initial frontend: HTTP/JSON.
- Unary gRPC extension: Status, Browse, Read, and Write.
- Later candidates: OPC UA and gRPC Subscribe after the DA callback core.
- COM access dedicated OS thread
- Treat Browse as optional.
- Preserve partial per-item failures.
- Discard stale handles on reconnect.
- v0 Write value only
- Write disabled by default
- No Subscribe in v0.
- Use HTTP first to validate the DA runtime.
- Keep gateway behavior out of scope.

## 73. Decisions requiring later ADRs

Decide these before implementation or before starting the corresponding feature:

- Final open-source license.
- Final repository name.
- HTTP default port
- exact request/body/batch hard limits
- reconnect backoff default
- COM apartment details, including the STA message loop.
- scalar VARTYPE v0 exact support matrix
- SAFEARRAY support timing
- production authentication mechanism
- Timing of gRPC introduction.
- Timing of Subscribe introduction.
- OPC UA security profile
- release signing

Track unresolved decisions explicitly.
ADR-0011 later fixed the v0 Windows Service packaging approach.

## 74. Standards and references

Use official specifications as implementation authority.

The planned UA frontend uses the COM DA-to-UA mapping in OPC UA Part 8 Annex A.

References:

- OPC UA Part 8: Data Access — COM DA to UA mapping  
  https://reference.opcfoundation.org/specs/OPC-10000-8/full

- Microsoft — Initializing the COM Library  
  https://learn.microsoft.com/en-us/windows/win32/learnwin32/initializing-the-com-library

- Microsoft — CoInitializeEx  
  https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializeex

- Microsoft — Single-Threaded Apartments  
  https://learn.microsoft.com/en-us/windows/win32/com/single-threaded-apartments

Do not copy official specifications into the repository.

## 75. Architecture summary

The structure is:

```text
                     ONE OPC DA SERVER
                            |
                         Local COM
                            |
                            v
                  +-------------------+
                  |   DA Runtime      |
                  |-------------------|
                  | Browse            |
                  | Read              |
                  | Write             |
                  | Subscribe(future) |
                  | VARTYPE           |
                  | Quality           |
                  | Timestamp         |
                  | HRESULT           |
                  +---------+---------+
                            |
                     DA-native Contract
                            |
                +-----------+-----------+
                |           |           |
                v           v           v
             HTTP        gRPC        OPC UA
             first       future      future
```

The central boundaries are:

```text
Do not add source protocols.
Do not change source semantics.
Do not persist process data.
Do not introduce a common model.
Frontends provide access methods only.
```

Any later feature must preserve these five boundaries to remain part of this adapter.

## Appendix A. Design review checklist

Review before opening a PR or proposing a feature.

- [ ] OPC DA is the only source protocol.
- [ ] Each instance serves one DA server.
- [ ] No remote DCOM or tunneling is introduced.
- [ ] ItemIDs remain unchanged.
- [ ] VARTYPE is preserved.
- [ ] Raw Quality is preserved.
- [ ] Source timestamps remain source timestamps.
- [ ] HRESULTs remain visible.
- [ ] Partial failures are preserved.
- [ ] Unsupported data is not coerced into another type.
- [ ] No database or persistent process-data store is introduced.
- [ ] No tag mapping, renaming, or scaling is introduced.
- [ ] No common Asset/Metric model is introduced.
- [ ] The frontend introduces no new source semantics.
- [ ] COM objects do not escape their owning thread.
- [ ] COM memory ownership is explicit.
- [ ] Queues, caches, and results are bounded.
- [ ] Write is never retried or replayed automatically.
- [ ] Disconnected Reads return no stale Good value.
- [ ] An actual user requirement or compatibility result supports the change.

## Appendix B. v0 completion checklist

- [ ] windows/386 build
- [ ] windows/amd64 build
- [ ] dedicated COM thread
- [ ] COM init/uninit
- [ ] DA Server connect/disconnect
- [ ] AddGroup
- [ ] AddItems
- [ ] ReadBatch
- [ ] canonical VARTYPE
- [ ] raw Quality
- [ ] source Timestamp
- [ ] per-item HRESULT
- [ ] Browse capability detection
- [ ] Browse if supported
- [ ] known ItemID Read when Browse unsupported
- [ ] typed Write
- [ ] Write disabled by default
- [ ] reconnect
- [ ] stale handle invalidation
- [ ] HTTP status
- [ ] HTTP browse
- [ ] HTTP read
- [ ] HTTP write
- [ ] partial failure
- [ ] hard resource limits
- [ ] no process-value persistence
- [ ] no process-value logging by default
- [ ] unit tests
- [ ] Windows integration test
- [ ] x86/x64 compatibility test
- [ ] long-running soak test
- [ ] at least one public compatibility matrix entry
