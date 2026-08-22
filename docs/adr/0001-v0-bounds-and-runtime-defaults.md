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
| concurrent HTTP requests | 32 |
| DA command queue | 64 |
| Read items per request | 100 |
| Write items per request | 100 |
| Browse entries | 1,000 |
| ItemID UTF-8 bytes | 1,024 |
| BSTR UTF-16 code units | 65,536 |
| registered items | 1,024 |
| frontend request deadline | 10 seconds |
| reconnect initial / maximum delay | 1 second / 30 seconds |

The listener is loopback by default and Write remains disabled by default.
Queue saturation fails the request; it never drops or persists operations.
In-flight COM calls are never forcibly cancelled or replayed.

## Consequences

The defaults prevent unbounded memory growth and accidental remote exposure.
Some legitimate installations may need larger limits; operators must opt in
explicitly after testing. These values are engineering defaults, not an OPC DA
interoperability claim.
