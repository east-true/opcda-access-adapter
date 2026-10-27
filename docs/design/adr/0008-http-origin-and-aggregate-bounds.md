# ADR-0008: HTTP browser boundary and aggregate resource ceilings

- Status: Accepted
- Date: 2026-08-24

## Context

The v0 HTTP API has no browser frontend, CORS contract, authentication, or
authorization layer. A loopback listener alone does not prevent a malicious
web page from attempting a simple `text/plain` POST, and DNS rebinding can
present a non-loopback `Host` while the TCP connection still reaches a
loopback address. This matters most when an operator has explicitly enabled
Write.

The existing configuration also bounded every resource setting separately.
Individually valid maxima could be combined into an unsafe aggregate memory
budget, especially on windows/386. A limit is not effective if multiplying it
by another configured limit can create a multi-gigabyte admitted workload.

Finally, the HTTP contract requires one ordered result per requested Read or
Write item. Returning a mismatched runtime result would silently break item
identity even if the mismatch came from an internal defect rather than the DA
server.

## Decision

All three POST endpoints require an `application/json` media type. Requests
with an `Origin` header are rejected because direct browser access is not a v0
frontend. When the configured listener host is a loopback literal or
`localhost`, every request must also carry a loopback IP or `localhost` Host;
this check is not inferred for an explicitly external bind. Responses use
`no-store`, `nosniff`, a same-origin resource policy, no-referrer,
frame-denial, and a deny-by-default content security policy.

Read and Write responses fail closed with `INTERNAL_RESULT_MISMATCH` unless
their length and ordered ItemIDs exactly match the admitted request. No
partial or reordered response is synthesized.

Startup rejects configurations whose combined budgets exceed these hard
ceilings, even when every individual setting is below its own ceiling:

| Aggregate | Hard ceiling |
|---|---:|
| concurrent HTTP request bodies | 256 MiB |
| accepted-connection HTTP headers | 64 MiB |
| Read or Write batch BSTR payload | 8,388,608 UTF-16 code units |
| Browse retained name and ItemID strings | 134,217,728 UTF-16 code units |
| Read or Write batch ItemIDs | 67,108,864 UTF-8 bytes |
| registered-item cache ItemIDs | 134,217,728 UTF-8 bytes |

Products are calculated as unsigned 64-bit values after positivity and
individual-ceiling checks, so validation itself does not overflow on 386.
Existing defaults remain unchanged.

## Consequences

HTTP clients must send `Content-Type: application/json`. A browser cannot call
the adapter directly; an explicitly authorized browser application would need
a separate same-origin backend that enforces its own authentication and CSRF
policy. External binds still require the deployment boundary documented in
`SECURITY.md`; this decision does not add an auth platform.

Some extreme combinations of configurable limits now fail startup. Operators
can trade one bound against another within the aggregate ceiling rather than
admitting an unsafe worst case. These ceilings bound admitted representations;
they are not a claim that a DA server will successfully return values near the
maximum.
