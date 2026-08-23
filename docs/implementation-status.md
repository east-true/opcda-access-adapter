# Implementation Status

## Current phase

**V0 IMPLEMENTATION COMPLETE**

**V0 VALIDATED** — the scoped local-COM path passed against the source-built
OPC Foundation DA 2.05a test server on both x86/386 and x64/amd64. This is not
a claim of broad vendor compatibility or production readiness.

## Current main SHA

`7319d204e318fc749ea7929372ce2ef8521e3101` — current main before the
Windows stability PR. The stability evidence below is from PR #11 head
`ccc28487dfc33e1767e3f42c547a6d59a5ae4ca4` and is not described as final-main
evidence until that green PR is merged.

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
- The immutable compatibility evidence, 100,000-Read bounded soak, and pinned
  normal CI actions/toolchain were merged in PR #9 after all seven checks
  passed.
- Final v0 status documentation was reconciled through PR #10.

## In progress

- PR #11 adds bounded HTTP connection/header/time limits and the Windows
  normal, exceptional, anomalous, rapid, concurrent, overload, and repeated
  failure stability profile. All seven required checks pass at its current
  head; merge and final-main confirmation remain.

## Validation results

- PRs #1 through #9 were merged only after all required checks passed.
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
- Long-running workflow run
  [`32630548279`](https://github.com/east-true/opcda-access-adapter/actions/runs/32630548279)
  repeated the full scenario and passed 100,000 device Reads on each
  architecture. Adapter handle growth was `+18` on each; private-byte growth
  was `+5,373,952` on 386 and `+5,787,648` on amd64, all within the explicit
  harness ceilings. Server deltas also remained within their bounds.
- Final-main CI run
  [`32632024080`](https://github.com/east-true/opcda-access-adapter/actions/runs/32632024080)
  passed formatting, Linux tests, vet, both Windows builds, and both native
  Windows test jobs at main SHA `9e8928d729300a67197da35e7bfee6623a861495`.
- Final-main real-DA run
  [`32632091320`](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320)
  passed the full scenario plus 100,000 device Reads on each architecture at
  the same SHA. Adapter growth was 386 `+16` handles/`+5,287,936` private
  bytes and amd64 `+18` handles/`+5,730,304` private bytes; source and built
  outputs passed Defender scans and all resource ceilings held.
- PR #11 run
  [`32634777223`](https://github.com/east-true/opcda-access-adapter/actions/runs/32634777223)
  passed on both Windows x86/386 and x64/amd64: strict anomalous-input checks,
  48 incomplete-header connections, 5,000 rapid Reads, 3,200 mixed requests
  from 16 workers, deterministic saturation of 32 request slots with 16
  `QUEUE_FULL` results and recovery, three real source failure/recovery cycles,
  and a final 200-Read soak. Adapter resource deltas were x86 `+8` handles and
  `+5,640,192` private bytes, and x64 `+8` handles and `+6,463,488` private
  bytes; all explicit ceilings held.

## Final v0 audit

- All Appendix B implementation items in `docs/design.md` have code, unit or
  Windows-platform coverage, and the scoped x86/x64 real-server result above.
- Production Go code has no third-party module dependency, process-data file
  or database persistence, non-DA source, extra frontend transport, or plugin
  path.
- The only production logs are bounded lifecycle/configuration/listener
  metadata; Read and Write values are not logged.
- Default loopback binding, default-disabled value-only Write, hard resource
  ceilings, serialized COM ownership, explicit cleanup, partial HRESULTs,
  reconnect generation invalidation, and no-stale-value outage behavior were
  rechecked against code and tests.

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

1. Merge green PR #11, then repeat normal and real-DA workflows on final main
   and update this record with the merge SHA.
2. Before a public `v0.0.0` release, confirm release packaging and security
   reporting; continue to treat the existing Apache-2.0 license as authoritative.
3. Add third-party compatibility rows only from authorized, executed tests;
   do not infer vendor-wide compatibility from the official fixture.
4. When an authorized server exposes non-Good Quality, an absent timestamp, or
   additional supported scalar types, add exact observations without changing
   source semantics.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
- [ADR-0005: reconnect and COM-hang policy](adr/0005-reconnect-and-com-hang-policy.md)
- [ADR-0006: real-DA validation fixture and supply-chain controls](adr/0006-real-da-validation-fixture.md)
