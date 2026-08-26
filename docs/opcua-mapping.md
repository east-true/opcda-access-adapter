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

## Wire encoding

`internal/opcua/binary.go` implements the UA Binary encoding of the built-in
types from **OPC 10000-6 clause 5.2**: little-endian integers and
floating-point values, `Int32` length prefixes where `-1` means null and `0`
means empty, `Guid` as `UInt32`/`UInt16`/`UInt16`/`Byte[8]`, one-dimensional
arrays preceded by an `Int32` element count, and `DateTime` as 100 nanosecond
intervals since 1601-01-01 UTC with the clause's saturation rules at both ends.

Booleans are written as `1` for true and any non-zero byte decodes as true, and
NaN is normalised to an IEEE quiet NaN, both as the clause requires.

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

`ChunkAccumulator` enforces the negotiated chunk count and message size while a
multi-chunk message arrives, refusing a breach **before** copying anything, as
OPC 10000-6 6.7.3 requires. An abort chunk discards the message.

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

Which rule set applies is a property of the SecurityPolicy, assigned by OPC
10000-7. That specification is **not** transcribed here, so the rule set is a
parameter the caller supplies rather than a value assumed for any policy.

### What is deliberately not bound yet

For the same reason, the `SecurityPolicy` URI strings are not hardcoded. The
framing layer treats `SecurityPolicyUri` as a length-validated opaque string,
which is all Table 58 requires of it. Binding the URI belongs with endpoint
description and `GetEndpoints`, where it can be checked against OPC 10000-7.

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

### What is deliberately not bound yet

`SecurityTokenRequestType`'s wire values are not bound. Its value table was not
obtainable in a transcribable form, and the encoding is only needed once the
`OpenSecureChannel` service **body** is decoded — which additionally depends on
`NodeId` and `ExtensionObject`, neither of which the codec implements yet. The
lifecycle above is complete and testable without them.

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

`EndpointConfig.SecurityPolicyURI` is **required and never defaulted**. The set
of known URIs is defined by **OPC 10000-7**, which this project has not been
able to obtain in a transcribable form. A server that published a wrong policy
URI would be unusable by a real client, which is precisely why the value is
supplied by configuration rather than written from recollection. The same
applies to the transport profile URI.

## Sessions

`internal/opcua/session.go` implements `CreateSession`, `ActivateSession` and
`CloseSession` from **OPC 10000-4 Tables 15, 17 and 19**, and the listener
serves all three over a real socket.

### The client nonce, and the one deliberate deviation

**A nonce that is present is validated even with `SecurityMode` `None`.** OPC
10000-4 5.7.2 states the server shall check the length and return
`Bad_NonceInvalid` outside 32 to 128 bytes, and Table 16 repeats it. Neither
statement is conditioned on the security mode.

**An absent nonce is accepted under `None`, and only there.** This is the one
place the adapter knowingly departs from a literal reading. open62541 sends no
nonce at all on an unsecured channel — deliberately, per a comment in its own
source — so enforcing the clause literally makes this server unusable with a
reference implementation. The clause's own stated purpose for the field is to
prove possession of the client's ApplicationInstanceCertificate, and the same
clause says a server shall ignore that certificate when the `securityPolicyUri`
is `None`: nothing is signed, so there is nothing for the nonce to take part in
and its absence costs no security. Under any other security mode — where the
nonce does real work — the rule is enforced exactly as written. The
[interop validation doc](validation/ua-client-interop.md) records the deviation
so it can be reversed by decision rather than found by accident.

**The session is bound to the SecureChannel it was created on.** Table 15 says
the authentication token is used *together with* the `SecureChannelId` to decide
whether a client may use the session, so a token that leaked to another channel
is refused with `Bad_SecureChannelIdInvalid`. This matters more, not less, on an
unsecured endpoint.

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
  identity.

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

The results array matches `nodesToBrowse` in size and order, so a node that
fails occupies its slot with a per-node status rather than shortening the list —
the service call itself still succeeds. A `referenceTypeId` that names no
reference type is `Bad_ReferenceTypeIdInvalid`, not a filter that silently
matches nothing.

### Continuation points

A continuation point is **consumed by use**: the client receives a new one if
more remains, so a stale point cannot be replayed. Points are bounded in number,
expire if a client abandons a browse, and `BrowseNext` with
`releaseContinuationPoints` returns empty arrays and frees them, as Table 37
requires. When no point can be issued the operation reports
`Bad_NoContinuationPoints` rather than silently truncating the result.

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
| `securityPolicyUri` | Defined by OPC 10000-7. A wrong URI makes the server unusable by a real client. |
| `transportProfileUri` | Same. |
| `endpointUrl`, `applicationUri`, `namespaceUri` | These identify a deployment, and the namespace URI must stay stable across restarts because design §35.2 forbids treating a namespace index as identity. |

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
