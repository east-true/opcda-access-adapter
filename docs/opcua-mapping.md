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

### Checks that are not conditional on security

**The client nonce length is validated even with `SecurityMode` `None`.** OPC
10000-4 states the server shall check it and return `Bad_NonceInvalid` outside
32 to 128 bytes, and that rule is not conditioned on the security mode. It would
have been easy to skip it on the unsecured path; the clause does not allow that.

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

## What is not decided here

Certificate handling and the signed and encrypted security policies, address
space construction, session and secure channel handling, subscription
and MonitoredItem behavior, certificates, and security policies are out of
scope for this document. See ADR-0016 for the phase order
and the conformance language rules.
