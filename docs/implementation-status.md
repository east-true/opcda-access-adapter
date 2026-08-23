# Implementation Status

## Current phase

Phase 4 — strict typed value Write (implemented locally on `feat/da-write`;
ready for PR validation).

## Current main SHA

`cf98eb1` — merged Phase 3 Browse (PR #4). This Phase 4 branch is based on that
commit.

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

## In progress

- Phase 4 PR, GitHub Windows execution, CI, and merge.

## Validation results

- PR #3: all five checks passed, including Windows 386/amd64 ABI, BSTR,
  VariantClear, and scalar width tests; merged.
- PR #4: all five checks passed, including Windows 386/amd64 Browse tests;
  merged.
- Phase 4 `gofmt`, `go test ./...`, and `go vet ./...` passed on Linux.
- Phase 4 Windows 386/amd64 test binaries cross-compiled and vet passed.
- Actual vendor root/nested/flat Browse remains externally blocked.

## Known issues

- DA 2.x Browse does not directly return canonical VARTYPE/access rights in
  its enumeration contract. These fields are omitted rather than inferred or
  populated by an unbounded registration scan.
- The zero-FILETIME rule and current scalar support remain pending real-server
  validation as recorded in ADR-0003.
- Reconnect, diagnostic bounds, and COM-hang degraded policy remain.

## External blockers

- **BLOCKED:** Real-DA Browse/Read/Write, nested/flat namespace quirks,
  reconnect/server restart, installed-server x86/x64 compatibility, and soak
  testing require an authorized local Windows OPC DA server. A simulator will
  not be installed without explicit approval and EULA review.

## Next exact tasks

1. Push Phase 4, run Linux/Windows checks, and merge only when green.
2. Implement bounded reconnect/backoff, generation invalidation, disconnected
   fail-fast, and degraded COM-hang policy in Phase 5.
3. Complete reliability tests, shutdown/resource checks, operator docs, and
   honest real-DA/soak blocker recording.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
