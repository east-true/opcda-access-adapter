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
so a later Part 8 gets a new URL rather than new bytes at this one.

The DA side has no CSV, so its authority is the IDL the proxy/stubs are
generated from, taken from the commit
[ADR-0006](../../docs/adr/0006-real-da-validation-fixture.md) pins for the
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
[ADR-0006](../../docs/adr/0006-real-da-validation-fixture.md) applies to the
validation fixture.

## Why it is not in CI

It needs network access to a third-party repository on every run. That is a
supply-chain dependency the ordinary build does not have and should not acquire
silently. Run it when a transcription changes, when adding one, and before a
release.

## What it does not check

Prose rules, such as which channel may carry a session. Those are checked by
tests that quote the clause they implement.

Table A.1, the item property mapping, is not checked because it is not
implemented: the adapter never calls `IOPCItemProperties`. That is recorded in
`docs/opcua-mapping.md` rather than left to be inferred from this script's
silence. Tables A.6 to A.9 are the opposite direction — a UA server seen through
a COM DA client — which this project does not implement either.

It checks that the mapping matches the tables. It cannot check that a real DA
server ever produces a given error: of the thirteen rows in A.4 and A.5, two
have been seen from a real server and eleven have not.

Struct layouts are not checked here either, because Go can check them better:
`internal/opcda/variant_windows_test.go` asserts the size of `VARIANT`,
`OPCITEMDEF`, `OPCITEMRESULT` and `OPCITEMSTATE` for both 32-bit and 64-bit, and
those assertions run on the Windows CI for each architecture. A padding mistake
fails there rather than corrupting a value.
