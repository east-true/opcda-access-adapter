# Implementation Status

## Current phase

**V0 IMPLEMENTATION COMPLETE**

**V0 REAL-DA VALIDATION IN PROGRESS** — an isolated Windows validation path is
being added for the MIT-licensed OPC Foundation DA 2.05a test server. No result
is claimed until the workflow has actually passed and its run is recorded.

## Current main SHA

`06c38c3c700fde3cea69865d29b485532068b899` — merged v0 closure documentation
(PR #7). The real-DA validation branch is based on that commit.

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

## In progress

- Supply-chain review, Windows VM automation, and real local-COM validation
  against the pinned OPC Foundation test server are being implemented on
  `test/real-da-validation`.

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

## Known issues

- DA 2.x Browse enumeration does not directly supply canonical VARTYPE/access
  rights. Browse omits those fields rather than inferring them or performing an
  unbounded registration scan.
- The zero-FILETIME absence rule and the current scalar support matrix remain
  pending real-server validation as recorded in ADR-0003.
- The reconnect HRESULT set is deliberately conservative. A vendor-specific
  disconnect code requires an observed compatibility result and ADR update.
- A permanently hung in-process COM call cannot be safely recovered by killing
  its owning thread. v0 reports `degraded`, fails new operations, and requires
  process restart; subprocess hard isolation is out of scope.
- v0 has no production authentication/TLS/RBAC platform. The default listener
  is loopback and Write is disabled unless explicitly enabled.

## External blockers

- Compatibility with third-party/vendor DA servers remains untested and must
  not be inferred from the OPC Foundation fixture.
- No proprietary simulator or license-restricted binary is authorized or used.

## Next exact tasks

1. Merge the audited validation workflow only after its normal CI and isolated
   real-DA jobs pass on both architectures.
2. Record the exact run, server commit, V/Q/T/HRESULT observations, reconnect,
   and resource results in `docs/compatibility.md`; fix any observed adapter
   defect through a focused PR.
3. Audit every v0 completion requirement and mark `V0 VALIDATED` only when the
   evidence is sufficient. Do not infer vendor-wide compatibility.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
- [ADR-0005: reconnect and COM-hang policy](adr/0005-reconnect-and-com-hang-policy.md)
- [ADR-0006: real-DA validation fixture and supply-chain controls](adr/0006-real-da-validation-fixture.md)
