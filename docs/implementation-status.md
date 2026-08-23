# Implementation Status

## Current phase

Phase 5 — reliability and v0 closure (implemented locally on
`feat/v0-reliability`; ready for PR validation).

## Current main SHA

`2f83821` — merged Phase 4 typed Write (PR #5). This Phase 5 branch is based on
that commit.

## Completed

- Phase 0 bootstrap merged in PR #1.
- Phase 1 COM Foundation merged in PR #2 with Windows 386/amd64 lifecycle
  execution.
- Phase 2 AddGroup/AddItems/device Read and HTTP Read merged in PR #3 with all
  Linux/Windows checks green.
- Phase 3 optional serialized DA Browse and HTTP Browse merged in PR #4 with
  all Linux/Windows checks green.
- Value-only `IOPCSyncIO::Write` uses explicit scalar VARIANTs, exact canonical
  VARTYPE matching, bounded BSTR allocation, owning-thread `VariantClear`, and
  one non-retried source call per admitted batch.
- `POST /v1/write` preserves request/result order and per-item HRESULTs. It
  rejects numeric overflow, width ambiguity, unsupported types, and malformed
  encodings before source work.
- Write remains disabled by default. The endpoint returns HTTP 403 without
  calling `WriteBatch` while disabled.
- Phase 4 strict typed value Write merged in PR #5 with all Linux/Windows
  checks green.
- Official COM/RPC disconnect HRESULTs trigger bounded jittered exponential
  reconnect on the same DA thread. Each successful connection increments a
  monotonic generation and starts with no registered handles.
- Disconnected/reconnecting and watchdog-degraded runtimes fail new operations
  before queue admission. No stale process value is retained or returned.
- The COM-call watchdog reports `degraded` without terminating the owning
  thread or replaying an ambiguous Write.

## In progress

- Phase 5 PR, GitHub Windows execution, CI, and merge.

## Validation results

- PR #3: all five checks passed, including Windows 386/amd64 ABI, BSTR,
  VariantClear, and scalar width tests; merged.
- PR #4: all five checks passed, including Windows 386/amd64 Browse tests;
  merged.
- PR #5: all five checks passed. Windows 386/amd64 executed exact Write
  VARIANT, BSTR ownership/cleanup, and scalar-width tests; merged.
- Phase 5 `gofmt`, `go test ./...`, `go vet ./...`, Linux race tests, and 20
  repeated unit-suite runs passed.
- Phase 5 Windows 386/amd64 test binaries cross-compiled, vet passed, and both
  adapter executables built. Actual Windows execution awaits PR CI.
- Actual vendor root/nested/flat Browse remains externally blocked.

## Known issues

- DA 2.x Browse does not directly return canonical VARTYPE/access rights in
  its enumeration contract. These fields are omitted rather than inferred or
  populated by an unbounded registration scan.
- The zero-FILETIME rule and current scalar support remain pending real-server
  validation as recorded in ADR-0003.
- The reconnect HRESULT set is deliberately conservative and may need an ADR
  update after a vendor demonstrates another documented disconnect result.
- In-process recovery from a permanently hung COM call is impossible without
  violating COM ownership; v0 honestly requires a process restart.

## External blockers

- **BLOCKED:** Real-DA Browse/Read/Write, nested/flat namespace quirks,
  reconnect/server restart, installed-server x86/x64 compatibility, and soak
  testing require an authorized local Windows OPC DA server. A simulator will
  not be installed without explicit approval and EULA review.

## Next exact tasks

1. Complete Phase 5 Linux/race/vet and Windows 386/amd64 execution checks.
2. Push Phase 5, merge only after all required checks are green, and update
   this document to the merged main SHA.
3. Run the real-DA compatibility procedure when an authorized local server is
   available; otherwise retain `V0 REAL-DA VALIDATION BLOCKED`.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
- [ADR-0005: reconnect and COM-hang policy](adr/0005-reconnect-and-com-hang-policy.md)
