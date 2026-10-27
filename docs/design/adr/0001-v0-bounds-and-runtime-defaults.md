# ADR-0001: v0 bounds and runtime defaults

- Status: Accepted
- Date: 2026-08-22

## Context

The design baseline requires all externally controlled input and runtime state
to be bounded, but leaves exact defaults open. v0 needs conservative,
configurable limits before real-server benchmarks are available.

## Decision

Defaults are intentionally small and can be changed by explicit configuration:

| Setting | Default |
|---|---:|
| HTTP listener | `127.0.0.1:8080` |
| HTTP body | 1 MiB |
| accepted HTTP connections | 64 |
| concurrent HTTP requests | 32 |
| HTTP request headers | 32 KiB |
| JSON nesting depth | 64 |
| HTTP header / request read timeout | 5 seconds / 15 seconds |
| HTTP response write / idle timeout | 15 seconds / 30 seconds |
| DA command queue | 64 |
| Read items per request | 100 |
| Write items per request | 100 |
| Browse entries | 1,000 |
| Browse path segments | 64 |
| ItemID UTF-8 bytes | 1,024 |
| BSTR UTF-16 code units | 65,536 |
| registered items | 1,024 |
| frontend request deadline | 10 seconds |
| reconnect initial / maximum delay | 1 second / 30 seconds |
| COM call watchdog | 30 seconds |

The listener is loopback by default and Write remains disabled by default.
Queue saturation fails the request; it never drops or persists operations.
In-flight COM calls are never forcibly cancelled or replayed.

Explicit configuration remains subject to startup hard ceilings to prevent an
invalid environment from causing an oversized allocation: 64 MiB HTTP body,
2,048 accepted connections, 1,024 concurrent HTTP requests, 1 MiB request
headers, 4,096 DA commands, 10,000 Read/Write items,
100,000 Browse entries, 256 Browse segments, 1,000,000 registered items,
65,536 ItemID bytes, 1,048,576 BSTR code units, and 24 hours for all HTTP
timeouts, deadlines, reconnect maximum, and the COM watchdog. Values above a
ceiling fail startup.

Individual ceilings are additionally subject to the aggregate admitted-memory
budgets in [ADR-0008](0008-http-origin-and-aggregate-bounds.md). This closes
unsafe combinations without changing the defaults above.

HTTP JSON nesting is independently bounded at 64 containers by default and
256 at the hard ceiling as recorded in
[ADR-0009](0009-request-parser-and-lifecycle-hardening.md).

## Consequences

The defaults prevent unbounded memory growth and accidental remote exposure.
Some legitimate installations may need larger limits; operators must opt in
explicitly after testing. These values are engineering defaults, not an OPC DA
interoperability claim.
