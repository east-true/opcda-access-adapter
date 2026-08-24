# Implementation Status

## Current phase

**V0 IMPLEMENTATION COMPLETE**

**V0 VALIDATED** — the scoped local-COM path passed against the source-built
OPC Foundation DA 2.05a test server on both x86/386 and x64/amd64. This is not
a claim of broad vendor compatibility or production readiness.

## Current main SHA

`8c166ebafbd562a8c94b4857ff4ba82e10c550e1` — protected `main` after guided
setup and Windows Service support (PR #26). No public tag or GitHub Release has
been created. The local destructive review below remains a release-promotion
gate.

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
- Bounded HTTP connection/header timeouts, deterministic request
  backpressure, abnormal-input/short-cycle/concurrent-load probes, and three
  consecutive real source failure/recovery cycles were merged in PR #11 after
  all seven checks passed.
- Release archives, checksums, embedded version metadata, non-publishing dry
  runs, artifact attestations, and maintainer documentation were merged in PR
  #19 after all eight required checks passed. The merge did not create a tag or
  release.
- Bounded source diagnostics, detailed local COM/DCOM review guidance, a
  disposable-VM destructive harness, loopback HTTP browser/DNS-rebinding
  defenses, aggregate resource ceilings, result-integrity checks, fuzzing, and
  race CI were merged in PR #20 after all eight checks passed. The local VM
  matrix remains unexecuted and is not represented by that merge.
- Unambiguous JSON parsing, exact HTTP request targets and methods, strict
  DA-native result validation, and terminal listener failure handling were
  merged in PR #22 after all eight checks passed.
- Bounded local `OPC_DA_20` registration detection with no vendor activation,
  automatic selection, configuration mutation, remote lookup, or multi-server
  runtime was merged in PR #24 after all eight checks passed.
- Explicit bounded source/frontend/action setup, strict versioned
  configuration, foreground config execution, and an SCM-managed
  `NT AUTHORITY\LocalService` lifecycle were merged in PR #26 after all eight
  checks passed. Setup stores the exact selected CLSID, never auto-selects even
  one candidate, and does not edit COM/DCOM or firewall permissions.

## In progress

- The local KVM/libvirt destructive-validation gate is paused. The dedicated
  `opcda-destructive-review` VM and all of its dedicated host resources were
  removed on 2026-08-24 because this host could not run it alongside another
  project's VM. GitHub-hosted runner evidence is not accepted as a substitute.
  No local PASS is recorded; exact preparation and interruption evidence is in
  `docs/validation/local-vm-destructive.md`.

## Validation results

- On 2026-08-24, `feat/guided-setup` passed `go test ./...`, `go vet ./...`,
  `go test -race ./...`, and 20 consecutive full-suite runs with Go 1.26.0 on
  Linux. All five package test executables cross-compiled for both
  `windows/386` and `windows/amd64`; both adapter executables and
  release-shaped archives built, and both SHA-256 manifests verified.
- `go mod verify` passed. `go list -m all` reports only this module and the
  reviewed Go-project low-level module `golang.org/x/sys v0.47.0`. The stripped
  adapter binary increase over the dependency-free baseline was 428,032 bytes
  (6.68%) on 386 and 464,896 bytes (7.01%) on amd64. License, provenance, and
  runtime impact are recorded in ADR-0011 and `THIRD_PARTY_NOTICES.md`.
- PR #26 CI run
  [`32734190208`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734190208)
  passed quality, race/fuzz checks, release packaging, both Windows builds,
  and native Windows tests on 386 and amd64 at head
  `0fc3684128919ff28f94b9257c0dbc30e34ae328`.
- PR #26 real-DA run
  [`32734190245`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734190245)
  passed on Windows Server 2025 for both x86/386 and x64/amd64. Guided setup
  required explicit fixture and HTTP selections, wrote version 1 configuration
  with the exact CLSID and Write disabled, installed an automatic-start
  Windows Service as `NT AUTHORITY\LocalService`, connected to the real local
  DA fixture, completed a device Read, emitted bounded Application Event Log
  lifecycle metadata, then stopped/uninstalled the service and removed its
  event source. The subsequent full semantics, 5,000 rapid Reads, 3,200 mixed
  concurrent requests, bounded overload recovery, three outage/reconnect
  cycles, and 200-Read soak regression also passed on both architectures.
- Post-merge CI run
  [`32734895495`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734895495)
  passed quality, race/fuzz checks, release packaging, both Windows builds, and
  both native Windows test jobs at main SHA
  `8c166ebafbd562a8c94b4857ff4ba82e10c550e1`. Dependency Graph run
  [`32734900512`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734900512)
  also passed at that SHA.

- On 2026-08-24, `feat/local-da-detection` passed `go test ./...`,
  `go vet ./...`, `go test -race ./...`, and 20 consecutive full-suite runs
  with Go 1.26.0 on Linux. The non-Windows command failed explicitly without
  returning a fake registration inventory.
- All five package test executables cross-compiled for both `windows/386` and
  `windows/amd64`, including the Windows detection ABI/ownership tests. Both
  release-shaped archives built and passed their SHA-256 manifest. The tests
  were not executed locally on Windows in this pass.
- `go mod verify` passed and `go list -m all` still reports only this module;
  local detection adds no third-party dependency.
- The guided setup branch added the pinned Go-project low-level dependency
  `golang.org/x/sys v0.47.0` for Windows SCM, service dispatch, and Event Log
  APIs. Its BSD-3-Clause license, provenance, module graph, and runtime impact
  are reviewed in ADR-0011; it is not an OPC SDK.
- PR #24 CI run
  [`32706109315`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109315)
  passed quality, release packaging, both Windows builds, and native Windows
  tests on 386 and amd64 at head
  `387ba9c269848035980b544cb816dafdef92d2d1`. An initial run exposed and
  removed an invalid test assumption that a shared runner's machine-wide COM
  registration inventory cannot change during the test; no product behavior
  was weakened.
- PR #24 real-DA run
  [`32706109366`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109366)
  passed on Windows Server 2025 for both x86/386 and x64/amd64. After the
  pinned OPC Foundation DA 2.05a fixture was registered, `detect` returned its
  exact ProgID and CLSID exactly once. The fixture process count was zero both
  before and after detection, demonstrating that candidate enumeration did
  not activate the vendor server. The subsequent full Browse/Read/Write,
  failure/reconnect, load, and bounded-soak regression also passed. This is
  registration-detection evidence for that fixture, not broad vendor
  compatibility.
- Post-merge CI run
  [`32706475946`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706475946)
  passed quality, release packaging, both Windows builds, and both native
  Windows test jobs at main SHA
  `1fc9b803973af81efca4ab3bbe47a14334dac123`.
- On 2026-08-24, `security/request-parser-lifecycle` passed `go test ./...`,
  `go vet ./...`, `go test -race ./...`, and 20 consecutive full-suite runs
  with Go 1.26.0 on Linux. The final five-second fuzz runs processed 14,628
  HTTP-body, 120,291 exact-string, and 120,022 typed-Write inputs without a
  crash.
- All five package test executables cross-compiled for both `windows/386` and
  `windows/amd64`; both adapter executables built. A release-shaped dry run
  produced both archives and verified their SHA-256 manifest. These binaries
  were not executed locally on Windows.
- `go mod verify` passed and `go list -m all` still reports only this module.
- PR #22 CI run
  [`32698464078`](https://github.com/east-true/opcda-access-adapter/actions/runs/32698464078)
  passed quality, race/fuzz checks, release packaging, both Windows builds, and
  both native Windows test jobs. Its OPC Foundation regression run
  [`32698464069`](https://github.com/east-true/opcda-access-adapter/actions/runs/32698464069)
  passed the expanded anomalous-input profile and the full local-COM scenario
  on x86/386 and x64/amd64. This hosted evidence does not replace the paused
  local destructive matrix.
- Post-merge CI run
  [`32699178370`](https://github.com/east-true/opcda-access-adapter/actions/runs/32699178370)
  passed at `62a5aae82b3dd8acbe9b7019d3732ca69d072c3a`.
- On 2026-08-24, the VM-free hardening branch passed `go test ./...`,
  `go vet ./...`, `go test -race ./...`, and 20 consecutive full-suite runs
  with Go 1.26.0 on Linux. Three HTTP/JSON fuzz targets each passed a local
  five-second fuzz run without a crash.
- Native `windows/386` and `windows/amd64` adapter executables cross-built,
  and all five package test executables compiled for each architecture. They
  were not executed on Windows in this validation pass.
- A temporary Linux status-only listener verified the real HTTP path without
  simulating DA data: status `200`, non-loopback Host `421`, `text/plain` POST
  `415`, browser Origin `403`, non-Windows Read `503`, default-disabled Write
  `403`, required response security headers, and 500/500 successful status
  requests at 16-way concurrency. The temporary listener was removed.
- `go mod verify` passed and `go list -m all` reported only this module; the
  production code still has no third-party Go module dependency.
- PR #20 CI run
  [`32688123759`](https://github.com/east-true/opcda-access-adapter/actions/runs/32688123759)
  passed formatting, unit tests, race detection, fuzz smoke tests, vet, release
  packaging, both Windows builds, and both native Windows test jobs. Its
  separate OPC Foundation regression run
  [`32688123745`](https://github.com/east-true/opcda-access-adapter/actions/runs/32688123745)
  passed on x86/386 and x64/amd64. Those hosted results are regression evidence,
  not the paused local destructive result.
- Post-merge CI run
  [`32688595542`](https://github.com/east-true/opcda-access-adapter/actions/runs/32688595542)
  passed all six required checks at `c010e6c2042c8c532ab22d747dae1e4afb0bff72`.
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
- Final-main CI run
  [`32635094747`](https://github.com/east-true/opcda-access-adapter/actions/runs/32635094747)
  passed formatting, Linux tests, vet, both Windows builds, and both native
  Windows test jobs at merge SHA
  `f55b4bb8e8c092fa2a21f4f35089a14703c81a8d`.
- Final-main stability run
  [`32635274825`](https://github.com/east-true/opcda-access-adapter/actions/runs/32635274825)
  repeated the complete profile on the same SHA. Both x86/386 and x64/amd64
  passed all stages. Adapter deltas were x86 `+16` handles/`+6,725,632`
  private bytes and x64 `+16` handles/`+5,763,072` private bytes; source and
  built outputs passed Defender scans and all resource ceilings held.

## Final v0 audit

- All Appendix B implementation items in `docs/design.md` have code, unit or
  Windows-platform coverage, and the scoped x86/x64 real-server result above.
- Production Go code has one reviewed general-purpose Windows syscall module
  and no OPC SDK dependency, process-data file or database persistence,
  non-DA source, extra frontend transport, or plugin path.
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
- Service-mode compatibility depends on the vendor accepting the LocalService
  identity and its scoped local COM/DCOM permissions. Setup does not edit
  AppID permissions or silently elevate to LocalSystem.

## External blockers

- None for the scoped v0 completion and official-fixture validation.
- The additional local destructive-validation gate requires a Windows host or
  VM capacity that does not contend with the other project currently using
  this machine. The prior dedicated VM was intentionally deleted before the
  scenario matrix ran.
- Compatibility with third-party/vendor DA servers remains untested and must
  not be inferred from the OPC Foundation fixture; validating one requires an
  authorized Windows installation and safe test ItemIDs.
- No proprietary simulator or license-restricted binary is authorized or used.

## Next exact tasks

1. Recreate the isolated Windows environment only when non-contending VM
   capacity is available, then complete the local destructive review including
   local COM launch/access/RunAs permission failures. Do not use a GitHub runner
   as evidence for this gate.
2. Record exact VM, Defender, x86/x64, load, resource, reboot, DCOM event, and
   cleanup results before proposing a public release.
3. Continue to treat the existing Apache-2.0 license as authoritative.
4. Add third-party compatibility rows only from authorized, executed tests;
   do not infer vendor-wide compatibility from the official fixture.
5. When an authorized server exposes non-Good Quality, an absent timestamp, or
   additional supported scalar types, add exact observations without changing
   source semantics.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
- [ADR-0004: strict typed value Write](adr/0004-strict-typed-value-write.md)
- [ADR-0005: reconnect and COM-hang policy](adr/0005-reconnect-and-com-hang-policy.md)
- [ADR-0006: real-DA validation fixture and supply-chain controls](adr/0006-real-da-validation-fixture.md)
- [ADR-0007: bounded source failure diagnostic](adr/0007-bounded-source-failure-diagnostic.md)
- [ADR-0008: HTTP browser boundary and aggregate resource ceilings](adr/0008-http-origin-and-aggregate-bounds.md)
- [ADR-0009: request parser and lifecycle hardening](adr/0009-request-parser-and-lifecycle-hardening.md)
- [ADR-0010: local OPC DA registration detection](adr/0010-local-da-registration-detection.md)
- [ADR-0011: guided setup and Windows Service lifecycle](adr/0011-guided-setup-and-windows-service.md)
