# UA endpoints, sessions, and concurrency

[UA overview](README.md) · [Documentation](../../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../../validation/compatibility.md).

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

`EndpointConfig.SecurityPolicyURI` is **required and never defaulted**, and the
same applies to the transport profile URI.

Neither can be transcribed the way every other constant here is. OPC 10000-7 is
the specification that governs profiles, and it does not list them: its clause 1
says "the actual Profiles are maintained in an online database and accessible
via https://profiles.opcfoundation.org/". Nothing in Parts 3, 4, 5 or 6 carries
either URI either, so there is no pinned document to check a transcription
against — which is exactly the condition under which this project does not write
a constant from recollection.

A server that published a wrong policy URI would be unusable by a real client,
so the value is supplied by configuration and an operator takes it from the
profile database rather than from a specification PDF.

What the server does **not** do is verify it. The security mode is fixed to
`None` in code and cannot be misconfigured, but the policy URI is advertised
verbatim: an operator who typed the URI of a signing policy would publish an
endpoint naming it, alongside the `None` mode that contradicts it. Checking that
would mean hard-coding the one URI this adapter implements, from a source that
cannot be pinned, and the endpoint's mode is the field a client acts on.

## Concurrency

The listener serves each connection on its own goroutine, and the owning
application drives expiry and address-space invalidation from another. Every
service the listener holds is therefore reached from several goroutines at
once, and the package follows one rule because of it:

- **A service that holds mutable state synchronises itself.** It does not
  assume a caller serialises access, because no caller does.
- **A service that is immutable after construction needs nothing.** The
  endpoint description and the data-access service are exactly that.
- **A service never hands out a pointer to state it owns.** Callers get value
  snapshots — `ChannelInfo`, `SessionInfo` — so nothing outside can read a
  field while another connection writes it, or hold a stale object across two
  calls. Mutation happens only inside the owning type, under its own lock.

That third point is what makes the rule hold rather than merely be stated. A
registry that locks its map but returns `*Session` has only moved the race one
level down, because callers then read and write the session itself.

Six services already followed this. The channel and session registries did not:
they carried a comment asserting a single-goroutine owner that the listener has
never provided, and nothing checked. Two clients connecting at the same time
faulted the process with a Go runtime `concurrent map read and map write` —
which no `recover` can catch, so the server simply died. Every test until then
had used one connection at a time. `internal/opcua/concurrency_test.go` now
drives concurrent clients against a listener while expiry runs, so a service
that opts out of the rule fails there instead of in production.

## Sessions

`internal/opcua/session.go` implements `CreateSession`, `ActivateSession` and
`CloseSession` from **OPC 10000-4 Tables 15, 17 and 19**, and the listener
serves all three over a real socket.

### The client nonce, and the one deliberate deviation

**A nonce that is present is validated even with `SecurityMode` `None`.** OPC
10000-4 5.7.2 states the server shall check the length and return
`Bad_NonceInvalid` outside 32 to 128 bytes, and Table 16 repeats it. Neither
statement is conditioned on the security mode.

**An absent nonce is accepted when the mode is `None` *and* the endpoint
publishes only the anonymous user token policy.** This is the one place the
adapter knowingly departs from a literal reading of the clause — though not
from what real servers do. open62541 sends no nonce at all on an unsecured
channel, and neither reference server enforces the clause as written: the OPC
Foundation's own `StandardServer` skips the check entirely for an empty nonce
at every security mode, enforces no maximum, and then discards the nonce with
the comment *"ignore nonce if security policy set to none"*. Enforcing the text
literally made this adapter stricter than the specification's own reference
implementation, and unusable with open62541.

**Both conditions are needed, and the second is easy to miss.** 5.7.2 gives the
ClientNonce one job: the server proves possession of its
ApplicationInstanceCertificate in the response, and the same clause says the
server ignores certificates when the `securityPolicyUri` is `None`. That alone
looks like enough — but **Table 101's last row defines a `UserTokenSignature`
specifically for `SecurityMode None`**, over
`ServerNonce | HASH(ServerCertificate) | ClientNonce`. A client authenticating
with a certificate therefore signs the nonce even on an unsecured channel. An
unsecured channel on its own does *not* make the nonce inert.

What makes it inert here is that this endpoint publishes only the anonymous
policy and `ActivateSession` refuses every other token, so no
`UserTokenSignature` can ever be computed. The acceptance is conditioned on
that fact rather than on the security mode alone, so publishing any
non-anonymous policy restores the rule automatically instead of silently
inheriting a justification that no longer holds. A nonce that is present is
always checked against the full 32–128 range, and every other security mode
enforces the rule as written — both stricter than the Foundation's server. The
[interop validation doc](../../validation/ua-client-interop.md) records the deviation
so it can be reversed by decision rather than found by accident.

### Ending a session is one operation

OPC 10000-4 5.7.2 defines session termination with consequences beyond
forgetting the session: *"When a Session is terminated, all outstanding requests
on the Session are aborted and Bad_SessionClosed StatusCodes are returned to the
Client."* A session's subscriptions also hold DA groups open on the source, and
those must be released with it.

There are three routes to a session ending — an explicit `CloseSession`, the
timeout, and a lookup that finds one already expired — and each has to do all of
it. Implementing that at the call sites meant only one route did: the DA release
lived beside the `CloseSession` handler, so **a session that timed out left its
groups open on the source forever**, which is what an ordinary client crash or
lost network produces. The registry deleted a map entry and nothing else
happened.

Termination is now a single operation every route goes through. It removes the
session, closes a per-session channel that wakes anything still serving a
request for it, and invokes an end hook the listener registers once — which
releases the session's subscriptions. The hook runs after the registry lock is
released, because unsubscribing a DA group is a COM call on Windows and holding
a lock across it would stall every other connection's session work.

### A request in flight holds its session open

The clause terminates a session when the client *"fails to issue a Service
request"* within the timeout. A request the server has not answered yet is one
the client did issue, so the idle clock must not run against it.

This is not hypothetical. `Publish` is held until the subscription has something
to say, and the limits permit a subscription to be legitimately silent for a
publishing interval times a keep-alive count — up to 1h40m — against a session
timeout of at most 10 minutes. Before `Publish` was held, a client re-sent
constantly and kept its own session alive by accident; holding the request made
the gap reachable. A session with requests in flight is never stale, and the
idle clock restarts when the request is answered.

### Which channel may carry a session

A session is bound to a SecureChannel, and Table 15 has the authentication
token checked together with the `SecureChannelId` on every request. But
**"the channel that created the session" and "the channel that may use it now"
are two different facts**, and 5.7.3 says so:

> When the ActivateSession Service is called for the first time then the Server
> shall reject the request if the SecureChannel is not same as the one
> associated with the CreateSession request. **Subsequent calls to
> ActivateSession may be associated with different SecureChannels.**

The second sentence is the client's documented way back after a connection
failure — 5.7.2 has it open a new connection and *"call ActivateSession
again"*. Storing both facts in one field and checking every request against the
creating channel refused exactly that: a session survived its connection, as
the clause intends, and then **could never be used again**. It sat holding its
DA groups until it timed out, and the client had to build a new session for
every network blip.

The two facts are now separate. `CreatedOnChannel` never changes and decides one
thing only: the first activation must arrive on it. `BoundChannel` is what every
request is checked against, and ActivateSession may move it — which is also how
*"once the Server accepts the new SecureChannel it shall reject requests sent
via the old SecureChannel"* is satisfied, since the check is against the
updated binding.

A move is allowed subject to the clause's checks. The security ones are made:
the new channel must offer the SecurityMode and SecurityPolicy the session was
created with, and an anonymous identity may not move onto a signed channel. The
certificate and ClientUserId checks are not reproduced, because this endpoint
serves no certificates and accepts no identity but anonymous — there is nothing
to compare. The session records the security it was created under rather than
reading it back from the endpoint, so the comparison keeps working if a second
endpoint is ever published.

**A token that leaked to another channel is refused** with
`Bad_SecureChannelIdInvalid`. This matters more, not less, on an unsecured
endpoint.

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

