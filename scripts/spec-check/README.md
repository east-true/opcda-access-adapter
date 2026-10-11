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

## CI 실행과 원본 재검토

이 검사는 모든 PR의 `spec-check` 작업에서 실행된다. 네트워크 오류나
검토 해시 불일치는 작업을 실패시키며, 해시 검증을 생략하지 않는다.
버전과 다운로드 식별자가 포함된 URL도 같은 바이트를 영구히 보장하지
않으므로 원본을 재검토한 뒤에만 `digests.txt`를 갱신한다.

2026-10-11에 PR #224와 #225의 공통 실패 원인인 Part 3 Markdown을
재검토했다. 동일한 `PART3_MARKDOWN` URL의 응답은 HTTP 200이고, 문서의
버전은 `1.05.06`, 발행일은 `2025-10-22`로 표시되어 있다. SHA-256은
`b41f19bb865d961f22f1a60f0f1433b82847ebd86240793b4a583198f1b3858d`에서
`9b017f477fb707745acc61d95b8d9bb5c1074cab1fa53fa18f5cc7d2f2da3c52`로
달라졌다. 이전 원본은 저장되어 있지 않아 전체 변경분 비교는 불가능했다.
대신 현재 원본의 Table 18을 기존 필수 속성 테스트와 대조하여 Variable
10개와 Object 5개가 일치함을 확인했다. 코드와 문서가 의존하는 4.6,
5.5.1, 5.6.2, 5.6.3, 8.3 및 MinimumSamplingInterval 규칙도 다시 읽고
기존 구현과 대조했다. 이 과정에서 Object의 HasTypeDefinition 규칙을
ObjectType 절인 5.5.2로 잘못 가리키던 주석과 문서를 5.5.1로 수정했다.
해시 갱신 후 전체 규격 검사를 실행하여 나머지 원본 해시와
속성·상수·구조·인용 검증이 모두 통과함을 확인했다.

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
