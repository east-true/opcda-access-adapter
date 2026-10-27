# ADR-0007: bounded source failure diagnostic

- Status: Accepted
- Date: 2026-08-23

## Context

The runtime previously discarded the error returned by a failed connection
attempt after scheduling reconnect. `GET /v1/status` exposed only
`disconnected`, so an operator could not distinguish an unregistered class,
an architecture mismatch, a COM launch/access denial, or a server execution
failure. This is particularly unsafe when tightening a server AppID's DCOM
permissions because broadening machine-wide permissions can appear to be the
only available troubleshooting method.

The diagnostic must remain bounded and must not retain ItemIDs, process values,
request bodies, or a history of source operations.

## Decision

Runtime status retains exactly one most recent source-level failure. It
contains only the failing COM/OPC operation and, when the failure supplied
one, the signed and hexadecimal raw HRESULT. A successful connection clears
the record. A connection-loss method failure replaces the record before the
runtime invalidates handles and enters reconnect.

HTTP exposes this record as optional `source.lastError`. This is diagnostic
metadata, not a source value or a synthesized DA error. Item-level HRESULTs
remain only in their ordered Read or Write result.

The adapter continues to request only `CLSCTX_INPROC_SERVER |
CLSCTX_LOCAL_SERVER`; it does not add remote activation, DCOM discovery, ACL
mutation, or automatic permission repair.

## Consequences

Operators can apply least-privilege AppID changes and see the exact activation
failure without enabling remote DCOM or weakening machine defaults. Status
reveals one local COM operation name and HRESULT to callers that can reach the
HTTP listener, which is acceptable for the existing loopback-default
operational endpoint. There is no diagnostic history and no process value is
retained.

Microsoft references:

- [CoCreateInstance](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-cocreateinstance)
- [Setting process-wide security through the registry](https://learn.microsoft.com/en-us/windows/win32/com/setting-processwide-security-through-the-registry)
- [CoInitializeSecurity](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializesecurity)
