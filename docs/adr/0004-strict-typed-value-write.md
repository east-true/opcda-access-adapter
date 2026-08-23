# ADR-0004: strict typed value Write

- Status: Accepted, pending real-server validation
- Date: 2026-08-23

## Context

OPC DA 2.05a `IOPCSyncIO::Write` consumes an array of Automation VARIANTs.
JSON numbers do not carry an integer or floating-point width, and converting a
request to the server's canonical type could silently narrow or reinterpret a
value. Write can also have taken effect even when the HTTP deadline expires.

## Decision

Every Write item supplies a symbolic scalar VARTYPE. The adapter accepts only
the exact transport representation documented in `docs/http-api.md`, builds
that exact VARIANT, and requires it to equal the canonical VARTYPE returned by
`IOPCItemMgt::AddItems`. A mismatch is the item-level adapter error
`TYPE_MISMATCH` and is not sent to `IOPCSyncIO::Write`.

The supported v0 Write set is `VT_EMPTY`, `VT_NULL`, `VT_I1`, `VT_UI1`,
`VT_I2`, `VT_UI2`, `VT_I4`, `VT_UI4`, `VT_I8`, `VT_UI8`, `VT_INT`, `VT_UINT`,
`VT_ERROR`, `VT_R4`, `VT_R8`, `VT_BOOL`, and `VT_BSTR`. `VT_I8`/`VT_UI8` use
decimal strings. Non-finite floats require the explicit `float-special`
encoding. Arrays, byref values, `VT_CY`, `VT_DATE`, `VT_DECIMAL`, and
`VT_VARIANT` are rejected rather than converted.

An admitted batch results in at most one `IOPCSyncIO::Write` call; it is never
retried or replayed. A frontend timeout reports that the source outcome may be
unknown and does not terminate the COM thread. Every constructed input
VARIANT, including BSTR ownership, is cleared on the owning DA thread after
the call. Write remains disabled by default.

## Consequences

Clients must know the source canonical VARTYPE and cannot rely on convenient
numeric coercion. Some servers that accept convertible, non-canonical
VARIANTs will be handled more conservatively. The behavior prevents silent
narrowing and keeps replay risk explicit, but real-server handling of partial
Write results and vendor canonical types remains to be validated.
