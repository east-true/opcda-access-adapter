# Implementation Status

## Current phase

**V0 IMPLEMENTATION COMPLETE**

**V0 VALIDATED** — the scoped local-COM path passed against the source-built
OPC Foundation DA 2.05a test server on both x86/386 and x64/amd64. This is not
a claim of broad vendor compatibility or production readiness.

## Current main SHA

`6dc45a90eba43d58e772afc5bb0e59d1d0853195` — merged isolated real-DA
validation workflow and harness (PR #8). This validation-record branch is
based on that commit.

## Completed

- Phase 0 bootstrap, lifecycle, status, CI, and Windows builds merged in PR #1.
- Phase 1 dedicated locked STA COM thread, local-only activation, ownership,
  message-aware wait, and repeated lifecycle tests merged in PR #2.
- Phase 2 AddGroup/AddItems, bounded lazy registration, device SyncIO Read,
  source VARTYPE/Quality/Timestamp/HRESULT/access-right preservation, partial
  failures, and HTTP Read merged in PR #3.
- Phase 3 optional serialized DA 2.x Browse with exact GetItemID identity,
  bounded results, unsupported behavior, and HTTP Browse merged in PR #4.
- Phase 4 strict scalar value SyncIO Write, owning-thread VARIANT cleanup,
  write-disabled default, per-item HRESULTs, no retry/replay, and HTTP Write
  merged in PR #5.
- Phase 5 conservative disconnect detection, jittered bounded reconnect,
  monotonic connection generation, stale-handle invalidation, disconnected and
  degraded fail-fast, COM-call watchdog, hard resource ceilings, and closure
  documentation merged in PR #6.
- The honest v0 implementation closure and remaining real-server blocker were
  recorded in PR #7.
- The audited source-build workflow and real local-COM validation harness were
  merged in PR #8 after all normal and real-DA checks passed.

## In progress

- Recording the immutable validation evidence and completing the final v0
  requirement audit on `docs/v0-validation`.

## Validation results

- PRs #1 through #7 were merged only after all required checks passed.
- Main CI run `32623072909` passed at the Phase 5 merge SHA with five checks:
  Linux formatting/test/vet, Windows 386 build, Windows amd64 build, Windows
  386 test, and Windows amd64 test.
- Linux `go test ./...`, `go vet ./...`, and `go test -race ./...` passed.
- The full unit suite passed 20 repeated runs.
- Windows 386 and amd64 test binaries cross-compiled and both adapter
  executables built locally.
- GitHub-hosted Windows VMs executed COM initialization/uninitialization,
  386/amd64 ABI layouts, scalar VARIANTs, BSTR allocation/embedded-NUL/cleanup,
  interface layouts, repeated runtime start/stop, queue backpressure,
  generation invalidation, reconnect scheduling, and watchdog degradation.
- No test double, cross-build, or Windows ABI result is recorded as real OPC
  DA interoperability.
- Real-DA workflow run
  [`32628886186`](https://github.com/east-true/opcda-access-adapter/actions/runs/32628886186)
  passed on Windows Server 2025 for native x86/386 and x64/amd64. Both jobs
  source-built the pinned OPC Foundation DA 2.05a fixture, passed pre/post
  Defender scans, then passed local-COM Connect, root/nested Browse, ordered
  partial Read, default-disabled and typed/source-denied Write, actual server
  outage/reconnect with newer generation, and 200-Read bounded soak checks.
- Both real jobs observed actual/canonical `VT_I4`, raw Quality `192`, a
  source-provided timestamp, successful HRESULT `0x00000000`, and invalid
  ItemID HRESULT `0xC0040007`. Exact evidence and bounded resource deltas are
  recorded in `docs/compatibility.md` without process values.

## Known issues

- DA 2.x Browse enumeration does not directly supply canonical VARTYPE/access
  rights. Browse omits those fields rather than inferring them or performing an
  unbounded registration scan.
- A non-zero source FILETIME and `timestampPresent: true` were validated. The
  zero-FILETIME absence sentinel, non-Good Quality, and scalar types beyond the
  fixture's exercised `VT_I4`, `VT_R4`, and `VT_BSTR` remain compatibility
  risks as recorded in ADR-0003.
- The reconnect HRESULT set is deliberately conservative. A vendor-specific
  disconnect code requires an observed compatibility result and ADR update.
- A permanently hung in-process COM call cannot be safely recovered by killing
  its owning thread. v0 reports `degraded`, fails new operations, and requires
  process restart; subprocess hard isolation is out of scope.
- v0 has no production authentication/TLS/RBAC platform. The default listener
  is loopback and Write is disabled unless explicitly enabled.

## External blockers

- None for the scoped v0 completion and official-fixture validation.
- Compatibility with third-party/vendor DA servers remains untested and must
  not be inferred from the OPC Foundation fixture; validating one requires an
  authorized Windows installation and safe test ItemIDs.
- No proprietary simulator or license-restricted binary is authorized or used.

## Next exact tasks

1. Merge this validation record only after normal CI and both isolated real-DA
   jobs pass again.
2. Dispatch the real-DA workflow on the final main SHA and retain its immutable
   run URL in the final report.
3. Before a public `v0.0.0` release, confirm release packaging and security
   reporting; continue to treat the existing Apache-2.0 license as authoritative.
4. Add third-party compatibility rows only from authorized, executed tests;
   do not infer vendor-wide compatibility from the official fixture.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
- [ADR-0005: reconnect and COM-hang policy](adr/0005-reconnect-and-com-hang-policy.md)
- [ADR-0006: real-DA validation fixture and supply-chain controls](adr/0006-real-da-validation-fixture.md)
