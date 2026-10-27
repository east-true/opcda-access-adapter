# UA transport and binary encoding

[UA overview](README.md) · [Documentation](../../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../../validation/compatibility.md).

## Wire encoding

`internal/opcua/binary.go` implements the UA Binary encoding of the built-in
types from **OPC 10000-6 clause 5.2**: little-endian integers and
floating-point values, `Int32` length prefixes where `-1` means null and `0`
means empty, `Guid` as `UInt32`/`UInt16`/`UInt16`/`Byte[8]`, one-dimensional
arrays preceded by an `Int32` element count, and `DateTime` as 100 nanosecond
intervals since 1601-01-01 UTC with the clause's saturation rules at both ends.

Those ends are where 5.2.2.5 puts them, not where the arithmetic happens to run
out. Anything "equal to or earlier than 1601-01-01 12:00AM UTC" encodes as 0 and
anything "equal to or greater than 9999-12-31 11:59:59PM UTC" encodes as the
`Int64` maximum — the boundary instants included, which a threshold set a tick
either side would quietly exclude. Go can represent instants well beyond the year
9999, so it is the clause's boundary that binds here rather than the platform's.

Both bounds round-trip exactly, which they have to: the clause closes by calling
them "invalid date/time values" that applications "should treat as such", and an
application cannot recognise a sentinel the encoder and decoder disagree about.
A wire value between the upper bound and the maximum can only arrive from
another implementation, never from this encoder, and decodes to the bound.

Booleans are written as `1` for true and any non-zero byte decodes as true, and
NaN is normalised to an IEEE quiet NaN, both as the clause requires.

### Picoseconds are dropped at a saturated timestamp

Clause 5.2.2 requires that "the Picoseconds shall be set to 0 when the DateTime
value is `DateTime.MinValue` or `DateTime.MaxValue`", and an encoded DataValue
here omits them there. Those two values are sentinels for "outside the
representable range" rather than instants, so a fraction of a tick past one says
nothing — and saying it would suggest the timestamp were exact when it is the
opposite.

Nothing this adapter reads from a DA source carries picoseconds, so the rule
bites only on a DataValue assembled with them. It is applied where it is stated
rather than where it happens to matter.

### An array shape the decoder was told to reject

Incoming array Variant elements are skipped after their shape is validated;
array Writes are refused. OPC 10000-6 Table 26 gives the decoder two rules: "all dimensions shall be specified and shall be
greater than zero", and "if ArrayDimensions are inconsistent with the ArrayLength
then the decoder shall stop and raise a `Bad_DecodingError`". Both are applied. A
decoder that accepts a shape it was told to reject is one that answers a message
it should have refused.

The `ArrayDimensions` flag on a scalar Variant is refused outright. Table 26
gives the field to arrays alone, and reading the flag without reading the field
would leave its bytes in the stream for the next field to take as its own —
turning one malformed Variant into a whole message decoded as something else.

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

### `MaxMessageSize` and `MaxChunkCount` mean opposite things in each direction

They appear in both the Hello and the Acknowledge with the same names and
different subjects, which is the trap.

**Table 74**, in the Hello, makes them the client's bound on **responses**:
`MaxMessageSize` is "the maximum size for any response Message", and the server
"shall return an Error Message with a `Bad_ResponseTooLarge` error if a response
Message exceeds this value" when no chunk has been sent. Responses are held to
that number.

**Table 75**, in the Acknowledge, makes them this server's bound on
**requests**: "the maximum size for any request Message" and "the maximum number
of chunks in any request Message". They are the server's own limits, announced,
and incoming requests are held to them.

Neither is a negotiation between the two. Tightening the Acknowledge by the
Hello would tell a client that asking for small responses had shrunk what it is
allowed to send. The buffer sizes *are* negotiated, and Table 75 says how: each
"shall not be larger than" its counterpart in the Hello.

Zero means the side that sent it imposes no bound, which both tables state. A
client that names no response limit gets whole responses, so zero has to keep
meaning "no limit" rather than "nothing may be sent".

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

A request may arrive in several chunks, which OPC 10000-6 6.7.3 sends
"sequentially" — so one accumulator per connection is enough, and chunks of two
messages never interleave. Each chunk carries its own security and sequence
header and is checked as it arrives, because the sequence number increments per
chunk and 6.7.3 has the receiver "check the security on the abort MessageChunk
before processing it".

`ChunkAccumulator` enforces the chunk count and message size the Acknowledge
announced while a multi-chunk message arrives, refusing a breach **before**
copying anything.

A server may announce no bound at all, which Table 75 allows with a zero
`MaxMessageSize`. That cannot mean an unbounded buffer: a peer would otherwise
decide how much memory this process spends, one intermediate chunk at a time and
never sending a final one. The binary message bound is the ceiling in that case,
and it caps an announced bound larger than itself too — a message past it could
not be decoded even if it were kept, so buffering that far only delays the same
refusal. An abort chunk discards the partial message and leaves the
channel open, which 6.7.3 requires in as many words: the receiver "shall ignore
the Message but shall not close the SecureChannel".

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

Which rule set applies is a property of the SecurityPolicy. OPC 10000-7 governs
profiles and does not list the per-policy assignment: its clause 1 puts the
profiles in an online database. So the rule set is a parameter the caller
supplies rather than a value assumed for any policy.

### What is deliberately not bound yet

For the same reason, the `SecurityPolicy` URI strings are not hardcoded. The
framing layer treats `SecurityPolicyUri` as a length-validated opaque string,
which is all Table 58 requires of it. Binding the URI belongs with endpoint
description and `GetEndpoints`, where an operator supplies it from the profile
database — there is no pinned document to check it against.

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

`SecurityTokenRequestType`'s wire values are bound: `Issue` 0 and `Renew` 1,
taken from the OPC Foundation UA NodeSet's DataType definition. This section
used to say they were not, on the grounds that the `OpenSecureChannel` body was
not decoded and that `NodeId` and `ExtensionObject` were unimplemented. All
three became untrue, and the paragraph immediately below this one has listed
`NodeId` and `ExtensionObject` as implemented the whole time it stayed.

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

The DA frontend uses string `NodeId` values to carry exact DA ItemIDs, following
the [identity rules](address-space.md).

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

