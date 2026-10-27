# Checking the transcriptions against the specification

Every numeric constant and structure field order in `internal/opcua` is a
transcription of a table somebody read once. Three have been wrong so far, and
all three round-tripped perfectly against this project's own decoder:

- `MessageSecurityMode` declared with `iota`, so `None` became 0 and inverted
  the exact safety property `Invalid = 0` exists to provide;
- `Bad_SecurityModeRejected` carrying `Bad_SecureChannelIdInvalid`'s value;
- `DiagnosticInfo` written in mask-bit order rather than stream order — a
  difference only a foreign decoder can see.

So the transcriptions are checked against the source they came from,
mechanically and repeatably:

```bash
python3 scripts/spec-check/check.py
```

It exits non-zero on any mismatch, and reports what the code says beside what
the specification says.

## What it checks

| | Source |
| --- | --- |
| status code values | `StatusCode.csv` |
| **DA VARTYPE to UA DataType** | `OPC-10000-8.md` Table A.2 |
| **DA quality to UA status code** | `OPC-10000-8.md` Table A.3 |
| **DA error to UA status mapping** | `OPC-10000-8.md` Tables A.4 and A.5 |
| DA error numeric values | `opcerror.h` |
| service encoding identifiers | `NodeIds.csv` (`..._Encoding_DefaultBinary`) |
| attribute identifiers | `AttributeIds.csv` |
| request decoder field order | `Opc.Ua.Types.bsd` |
| DA quality values | `opcda.idl` |
| DA masks, access rights, data source | `opcda.idl` |
| **DA COM vtable slot order** | `opcda.idl` |

It also checks that no status code value is declared twice. Two constants for
one code compile, pass every test, and let each call site pick a spelling at
random; one such pair existed — `StatusBadAttributeIDInvalid` beside
`StatusBadAttributeIdInvalid` — and this check is what found it.

Annex A is prose, not a schema, so it is read from the Markdown export the OPC
Foundation publishes for each specification version. The URL names the version,
but digest verification is still required because versioned URLs can change.

The DA side has no CSV, so its authority is the IDL the proxy/stubs are
generated from, taken from the commit
[ADR-0006](../../docs/design/adr/0006-real-da-validation-fixture.md) pins for the
validation fixture. The constants are therefore checked against the source the
server this project tests against was itself built from.

Two of these are worth having above the rest.

A decoder that reads two fields in the wrong order is silent against this
project's own encoder, which writes them in the same wrong order, and fails only
against a real client.

A **vtable slot in the wrong position calls a different method entirely** —
`ValidateItems` where `AddItems` was meant — with arguments shaped for the one
that was intended. No Go test can see it, because the vtable is the server's;
only a real COM server can, and then only if the mistake happens to crash rather
than corrupt.

## Why it pins the schema

Every source — the `UA-Nodeset` schema, the Classic IDL and header, and the
Part 8 Markdown export — is fetched and checked against `digests.txt` before
anything is compared. Upstream moving
would otherwise silently change what "conformant" means here.

If a digest no longer matches, the script stops and prints both. Read what
changed upstream, decide whether the adapter should follow, and only then
refresh the digest — the same discipline
[ADR-0006](../../docs/design/adr/0006-real-da-validation-fixture.md) applies to the
validation fixture.

## CI and source review

Every PR runs `spec-check`. Network errors and digest mismatches fail the job;
digest verification is never bypassed. Even a URL containing a version and
download identifier can return changed bytes. Review the source before updating
`digests.txt`.

On 2026-10-11 the Part 3 Markdown was reviewed after the common failure in
PR #224 and #225. The same `PART3_MARKDOWN` URL returned HTTP 200, version
`1.05.06`, and publication date `2025-10-22`. Its SHA-256 changed from
`b41f19bb865d961f22f1a60f0f1433b82847ebd86240793b4a583198f1b3858d` to
`9b017f477fb707745acc61d95b8d9bb5c1074cab1fa53fa18f5cc7d2f2da3c52`.
The old source was unavailable, so a complete diff was impossible. Instead,
Table 18 was checked against existing mandatory-attribute tests: 10 Variable
and 5 Object attributes matched. Rules in 4.6, 5.5.1, 5.6.2, 5.6.3, 8.3, and
MinimumSamplingInterval were reread against the implementation. Object
HasTypeDefinition citations pointing to ObjectType clause 5.5.2 were corrected
to 5.5.1. The full check then passed source digests, attributes, constants,
structures, and citations.

## What it does not check

Prose rules, such as which channel may carry a session. Those are checked by
tests that quote the clause they implement.

Table A.1's item-property mapping is implemented, but this script does not
cross-check that prose mapping. Go tests and fixture probes cover it. Tables
A.6 to A.9 describe the opposite direction, a UA server exposed through a COM
DA client, which this project does not implement.

The script checks mappings against specification tables, not whether a source
produces an error. The fixture has produced three of the thirteen A.4/A.5 rows:
`OPC_E_UNKNOWNITEMID`, `OPC_E_BADRIGHTS`, and `OPC_E_INVALID_PID`.
See `docs/validation/compatibility.md` for observed, provoked, and unreachable conditions.

Struct layouts are not checked here either, because Go can check them better:
`internal/opcda/variant_windows_test.go` asserts the size of `VARIANT`,
`OPCITEMDEF`, `OPCITEMRESULT` and `OPCITEMSTATE` for both 32-bit and 64-bit, and
those assertions run on the Windows CI for each architecture. A padding mistake
fails there rather than corrupting a value.
