# ADR-0003: v0 Read types and FILETIME presence

- Status: Accepted, pending real-server validation
- Date: 2026-08-23

## Context

OPC DA 2.05a returns `OPCITEMSTATE` values containing a VARIANT, raw Quality,
and FILETIME. JSON cannot directly represent every Automation type, integer
width, or non-finite floating-point value. `OPCITEMSTATE` also has no separate
timestamp-present Boolean. The implementation must choose explicit behavior
without inventing a timestamp or silently converting unsupported values.

## Decision

Read supports the scalar types listed in `docs/http-api.md`. The actual
VARIANT VARTYPE and AddItems canonical VARTYPE are both retained. `VT_I8` and
`VT_UI8` use decimal JSON strings. Non-finite `VT_R4`/`VT_R8` values use the
explicit `float-special` encoding. Unsupported VARTYPEs remain item errors,
and their returned VARIANTs are still passed to `VariantClear`.

An all-zero FILETIME is represented by `timestampPresent: false`; every other
FILETIME is converted directly from Windows 100-nanosecond ticks to UTC. The
adapter never substitutes its current time. This sentinel interpretation must
be checked against actual DA servers during compatibility validation and
revised transparently if a server demonstrates different source semantics.

## Consequences

Clients can distinguish numeric widths through VARTYPE and do not lose 64-bit
integer precision in JSON. SAFEARRAY, DECIMAL, DATE, and CY values fail
explicitly until a lossless representation and corresponding tests exist.
The zero-FILETIME rule preserves absence without synthesizing data, but remains
an interoperability risk until real DA results are available.
