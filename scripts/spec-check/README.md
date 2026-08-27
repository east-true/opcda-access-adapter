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
| service encoding identifiers | `NodeIds.csv` (`..._Encoding_DefaultBinary`) |
| attribute identifiers | `AttributeIds.csv` |
| request decoder field order | `Opc.Ua.Types.bsd` |

The field-order check is the one worth having: a decoder that reads two fields
in the wrong order is silent against this project's own encoder, which writes
them in the same wrong order, and fails only against a real client.

## Why it pins the schema

The schema is fetched from the OPC Foundation's `UA-Nodeset` repository and
checked against `digests.txt` before anything is compared. Upstream moving
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

Values that no machine-readable source publishes: OPC 10000-8 Tables A.2 to A.5,
which map DA VARTYPEs, qualities and HRESULTs onto UA types and status codes;
the OPC DA quality bit layout; and prose rules such as which channel may carry a
session. Those are checked by tests that quote the clause they implement.
