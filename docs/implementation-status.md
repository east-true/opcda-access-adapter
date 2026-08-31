# Implementation Status

## Current phase

**V0 IMPLEMENTATION COMPLETE**

**V0 VALIDATED** — the scoped local-COM path passed against the source-built
OPC Foundation DA 2.05a test server on both x86/386 and x64/amd64. This is not
a claim of broad vendor compatibility or production readiness.

**PHASE 6 gRPC IMPLEMENTATION COMPLETE AND FIXTURE-VALIDATED** — the DA-native
unary Status/Browse/Read/Write frontend passed PR CI and the source-built OPC
Foundation fixture on both supported architectures. This is not a broad
vendor-compatibility claim.

**PHASE 7 DA SUBSCRIBE CORE IMPLEMENTED AND FIXTURE-VALIDATED** — the
`IOPCDataCallback` connection-point core passed all eight PR checks and
delivered real `OnDataChange` notifications from the source-built OPC
Foundation fixture on both supported architectures, including change-driven
delivery, group and advise cleanup, and invalidation across an induced
disconnect. This is not a broad vendor-compatibility claim.

**PHASE 8 OPC UA FRONTEND IMPLEMENTED AND FIXTURE-VALIDATED** — a hand-written
UA server for `SecurityPolicy None` passed all eight PR checks and completed the
connection sequence, secure channel, `GetEndpoints`, session, Browse and Read
against the source-built OPC Foundation fixture on both architectures. Only
`SecurityMode` `None` is implemented and it is not production ready.

**Three third-party UA clients now run against the frontend** — asyncua,
open62541 and the OPC Foundation .NET stack, 401 checks in total, recorded in
docs/compatibility.md. Three clients are not conformance and no conformance or
interoperability claim is made; ADR-0016 forbids describing this as certified or
compliant. What they establish is narrower and worth having: two of the six
defects they found came from the second and third client against a server the
first had already passed.

**PHASE 7 gRPC SUBSCRIBE STREAMING IMPLEMENTED AND FIXTURE-VALIDATED** — the
DA core is exposed as a server-streaming `Subscribe` RPC and passed all eight
PR checks, including a real-DA stream run on both architectures. Backpressure
is the HTTP/2 flow-control window with no adapter-side buffer, ending a stream
releases the DA group, and an invalidated subscription ends the stream with
`Aborted` and requires an explicit resubscribe. HTTP still exposes no
Subscribe.

## Release state

No public tag or GitHub Release has been created. The local destructive review
below remains a release-promotion gate, and it applies to whatever `main` is at
the moment of promotion.

This section used to pin a `main` SHA. It was forty-eight merges out of date
before anyone noticed, because a recorded commit hash cannot stay true and
nothing was going to keep updating it. `git rev-parse main` answers that
question without rotting.

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
- The gRPC Subscribe server-streaming frontend was merged in PR #34 after all
  eight checks passed. One stream is one subscription is one DA group, values
  reuse `DAReadResult`, backpressure is the HTTP/2 window with no adapter-side
  buffer, ending a stream releases the group, and an invalidated subscription
  ends the stream with `Aborted` so a resubscribe is never mistaken for a
  transparently retryable failure. ADR-0014 records the decision.
- The Subscribe capability probe was merged in PR #35 after all eight checks
  passed. The runtime no longer advertises `capabilities.subscribe` without
  probing the group's `IOPCDataCallback` connection point, and a source without
  one is refused as `SUBSCRIBE_UNSUPPORTED` instead of failing late. ADR-0015
  records the decision, and `docs/compatibility.md` records the vendor
  variations a third-party run must observe. No third-party server was tested.

- The DA-native Subscribe core was merged in PR #31 after all eight checks
  passed: one DA group per subscription advised through `IOPCDataCallback`,
  update-rate sampling with per-item coalescing and therefore no notification
  queue to overflow, non-blocking callbacks safe against a foreign calling
  thread, explicit invalidation on disconnect/unsubscribe/shutdown with pending
  values discarded, explicit resubscribe with generation-scoped identifiers, and
  preserved per-item AddItems HRESULTs. A `subscribeprobe` real-DA harness and
  ADR-0013 were merged with it. No frontend exposes Subscribe.

- DA-native unary gRPC Status/Browse/Read/Write, exact scalar widths, typed
  error layers, explicit frontend selection, aggregate HTTP/2 bounds,
  dependency/provenance review, packaging, and x86/x64 real-DA probes were
  merged in PR #29 after all eight normal and real-DA checks passed. Subscribe
  and simultaneous listeners were not added.

## In progress

- The OPC DA side was verified against the specification on 2026-08-27, and the
  check joined `scripts/spec-check/check.py`. **No defect was found.**

  The DA side has no CSV, so the authority is `opcda.idl` and `opcerror.h` from
  the commit ADR-0006 already pins for the validation fixture — the constants
  are checked against the source the test server itself was built from. What was
  compared, all matching:

  The check has grown since. What `scripts/spec-check/check.py` compares today,
  all matching:

  | | Source | Checked |
  | --- | --- | --- |
  | status code values | `StatusCode.csv` | 78 |
  | service encoding ids | `NodeIds.csv` | 32 |
  | standard node ids | `NodeIds.csv` | 59 |
  | attribute ids | `AttributeIds.csv` | 12 |
  | request decoder field order | `Opc.Ua.Types.bsd` | 14 |
  | DA quality values | `opcda.idl` | 16 |
  | DA item property identifiers | `opcda.idl` | 16 |
  | DA masks, access rights, data source | `opcda.idl` | 4 |
  | **DA COM vtable slot order** | `opcda.idl` | 6 |
  | DA error mappings | Part 8 Tables A.4/A.5 | 19 |
  | DA data type mappings | Part 8 Table A.2 | 14 |
  | DA quality mappings | Part 8 Table A.3 | 16 |

  Interface and category GUIDs, struct field order and type widths, and the
  browse and namespace enumerations are checked by Go tests instead, which can
  see sizes and offsets a text comparison cannot.

  The vtable check is the one worth having: a slot in the wrong position calls a
  different method entirely, with arguments shaped for the one that was
  intended. No Go test can see it, because the vtable belongs to the server;
  only a real COM server can, and then only if the mistake crashes rather than
  corrupts. It was verified by swapping `AddItems` and `ValidateItems` and
  confirming the report.

  Struct **layout** is checked better in Go than by this script, and already is:
  `variant_windows_test.go` asserts the size of `VARIANT`, `OPCITEMDEF`,
  `OPCITEMRESULT` and `OPCITEMSTATE` for both architectures — 16/28/20/32 on
  32-bit and 24/48/24/40 on 64-bit — and those run on the Windows CI. Each was
  recomputed by hand from the IDL field types and matches.

  The `opcerror.h` comparison closed a gap this project had recorded as open:
  OPC 10000-8 Tables A.4 and A.5 used to bind only the two DA error codes
  actually observed, because the rest needed "a verifiable source such as
  opcerror.h". Every row of both tables is now bound, with each OPC value
  checked against `opcerror.h` and each Windows value against
  `golang.org/x/sys/windows`, and the tables themselves re-read from the OPC
  Foundation's published Part 8 export rather than retyped.

  `internal/validation/daerrorprobe` then answered what remained: which rows a
  real server actually produces, and which this adapter can produce at all. Both
  architectures agree — two rows observed and confirmed through the real mapping
  function, three provoked but not produced by this server, and eight
  unreachable, six of them because of decisions made on purpose (ADR-0004's
  strict typing, adapter-owned item handles, unimplemented Table A.1, and a
  2.05a Write that carries a value only). `docs/compatibility.md` records the
  executed result.

  So the remaining exposure is not that ten rows are untested. It is that five
  of them are unreachable **today** and would become reachable the moment the
  decision behind them changes — as one already did: `OPC_E_INVALID_PID` became
  reachable, and then observed, when the adapter began reading item properties.
  The rest is that a different vendor's server may produce the three this one
  does not.

- OPC 10000-8 Table A.1, the DA item property mapping, is **implemented**.
  [ADR-0018](adr/0018-da-item-properties.md) decided to build it, and both
  halves now exist. The adapter queries `IOPCItemProperties` once per
  connection and reports it as a capability the way it reports Browse;
  `EngineeringUnits`, `EURange`, `InstrumentRange`, `TrueState` and `FalseState`
  appear as properties of a source variable when the source offers the DA
  properties they are built from, and Item Description answers the `Description`
  attribute. Values are read live and never cached. `docs/design.md` §11 already
  listed that interface in the DA baseline, so the code now matches the
  document.

  What is deliberately left undecided is whether a node whose EU range is known
  should be promoted from `BaseDataVariableType` to `AnalogItemType`. `EURange`
  is reachable either way, and promotion means claiming a type whose mandatory
  properties must then always exist.

- The local KVM/libvirt destructive-validation gate is paused. The dedicated
  `opcda-destructive-review` VM and all of its dedicated host resources were
  removed on 2026-08-24 because this host could not run it alongside another
  project's VM. GitHub-hosted runner evidence is not accepted as a substitute.
  No local PASS is recorded; exact preparation and interruption evidence is in
  `docs/validation/local-vm-destructive.md`.

## Validation results

- On 2026-08-25, the Phase 7 Subscribe branch passed `gofmt -l .`,
  `go vet ./...`, `go test ./...` (207 tests in 8 packages), and
  `go test -race ./...` with Go 1.26.0 on Linux. `go vet` was additionally run
  under `GOOS=windows` for both `386` and `amd64`, which is what covers the
  Windows-only callback file. All seven package test executables cross-compiled
  for both `windows/386` and `windows/amd64`.
- The PR #32 documentation run exposed a timing flake in the pre-existing
  guided-service check: the amd64 real-DA job failed with "gRPC guided service
  did not write a lifecycle Event Log record" because the Application log became
  queryable later than its ten-second deadline on a loaded shared runner. The
  386 job and an immediate re-run of amd64 both passed, and the Subscribe probe
  had not yet run when the job failed. Both guided-service waits now share one
  bounded helper with a sixty-second deadline, so a pass means the record was
  written rather than that the runner happened to be fast. No product behavior
  and no assertion were weakened.
- PR #53 real-DA run
  [`32928909002`](https://github.com/east-true/opcda-access-adapter/actions/runs/32928909002)
  passed on both x86/386 and x64/amd64. The UA probe created a Subscription and
  MonitoredItem against the fixture, received the server's initial snapshot and
  two change-driven notifications induced through UA Write, and deleted the
  subscription. Exact figures are in `docs/compatibility.md`.
- That run caught the same class of error a second time: OPC DA reports an
  item's canonical type in the AddItems result rather than in Browse, just as it
  does access rights, so every browsed node reported the abstract base type and
  the write path refused it as unmapped. Where the source has not reported a
  type, the client's Variant now decides the VARTYPE and the source answers;
  where it has, the type is enforced locally. The address space also learns both
  the canonical type and the access rights from Read results and subscription
  notifications, since both come back through AddItems.

- PR #51 CI run
  [`32921155729`](https://github.com/east-true/opcda-access-adapter/actions/runs/32921155729)
  passed quality, race/fuzz checks, release packaging, both Windows builds, and
  both native Windows test jobs at head `cc228a16f1b154b2c1e109f5a52161b7db32f7c6`.
- PR #51 real-DA run
  [`32921155696`](https://github.com/east-true/opcda-access-adapter/actions/runs/32921155696)
  passed on both x86/386 and x64/amd64. A new `opcuaprobe` drove the UA frontend
  against the fixture through the connection sequence, a secure channel,
  `GetEndpoints`, a session, a Browse walk down to the fixture's three DA
  items with the address space filled on demand, and a Read returning `Good`
  with the source timestamp present. Exact figures are in
  `docs/compatibility.md`.
- That run caught a design error the unit tests could not: OPC DA carries access
  rights in the `AddItems` result rather than in Browse, so every browsed item
  arrived with no rights and the adapter refused every read as
  `Bad_NotReadable`. Unknown rights now mean the operation reaches the source,
  which is the authority; rights the source did report are still enforced
  locally. No test was weakened to make the check pass.
- An earlier attempt of the same run failed because the validation script
  asserted configuration version 2, which the OPC UA frontend raised to 3. The
  version is now a single script constant.

- PR #34 CI run
  [`32809889776`](https://github.com/east-true/opcda-access-adapter/actions/runs/32809889776)
  passed quality, race/fuzz checks, release packaging, both Windows builds, and
  both native Windows test jobs at head
  `a24c0eca5ab598028d413ef04276c1567257431e`. The protobuf bindings were
  regenerated with the pinned `libprotoc 36.0`, `protoc-gen-go v1.36.12`, and
  `protoc-gen-go-grpc 1.6.2`, after first confirming that toolchain reproduced
  the committed files byte-for-byte.
- PR #34 real-DA run
  [`32809889799`](https://github.com/east-true/opcda-access-adapter/actions/runs/32809889799)
  passed on both x86/386 and x64/amd64. The write-enabled gRPC scenario opened
  the Subscribe stream, received a `created` message reporting the source
  revised rate of `300ms` for a requested `250ms` with all three items active,
  received the initial snapshot and two change-driven notifications induced
  through typed Write, held the coalescing bound, and saw `subscription_count`
  return to zero after the client closed the stream. The DA Subscribe core
  probe and the existing HTTP, gRPC unary, reconnect, failure-cycle, load, and
  200-Read soak regression also passed. Exact figures are in
  `docs/compatibility.md`.
- Post-merge CI run
  [`32806556135`](https://github.com/east-true/opcda-access-adapter/actions/runs/32806556135)
  passed quality, release packaging, both Windows builds, and both native
  Windows test jobs at main SHA `717b04dadadc165cef78efdd70bda21f0af836a2`.
- PR #31 CI run
  [`32803232566`](https://github.com/east-true/opcda-access-adapter/actions/runs/32803232566)
  passed quality, race/fuzz checks, release packaging, both Windows builds, and
  both native Windows test jobs at head
  `a287b215960a251ececa5003480d027cff9f6210`. The Windows jobs run
  `go test ./...` under `GOARCH=386` and `GOARCH=amd64`, so the Windows-only
  `IOPCDataCallback` tests executed there for the first time and passed on both
  architectures: vtable population, COM ABI offsets,
  QueryInterface/AddRef/Release, OnDataChange coalescing, preserved per-item
  HRESULTs, rejected inconsistent notifications, and teardown invalidation. A
  populated vtable also confirms `syscall.NewCallback` accepts every
  IOPCDataCallback signature, including the eleven-argument `OnDataChange`.
- PR #31 real-DA run
  [`32803232555`](https://github.com/east-true/opcda-access-adapter/actions/runs/32803232555)
  passed on Windows Server 2025 for both x86/386 and x64/amd64. A new
  `subscribeprobe` drove the DA runtime directly, since no frontend exposes
  Subscribe. Both architectures created a DA group per subscription with all
  three fixture items active, had the requested `250ms` rate revised to `300ms`
  and reported unchanged, received real `OnDataChange` batches into the Go
  callback vtable with exact ItemID, VARTYPE, canonical type, access rights,
  raw Quality, timestamp presence and HRESULT, observed three change-driven
  notifications induced through the typed Write path, held the coalescing
  bound, completed 24 subscribe/unsubscribe cycles against a limit of 16 with
  no leaked group or advise cookie and no reused identifier, and invalidated
  the subscription across an induced fixture termination without delivering
  pending values, restoring anything implicitly, or reusing the identifier
  after reconnect. The existing HTTP, gRPC, reconnect, failure-cycle, load, and
  200-Read soak regression also passed. Exact figures are in
  `docs/compatibility.md`.
- The first attempt of that run failed and is kept as evidence of what the
  probe actually proves: the fixture created the group and delivered one
  `OnDataChange`, then delivered nothing more because its `Test` items are
  static. The probe was requiring three unsolicited batches; it now requires
  the initial snapshot plus changes it induces itself. No product behavior was
  changed to make the check pass.

- On 2026-08-25, the Phase 6 working tree passed uncached `go test ./...`,
  `go test -race ./...`, `go vet ./...`, and 20 consecutive full-suite runs
  with Go 1.26.0 on Linux. The new protobuf Write decoder completed a five
  second fuzz run with 95,604 executions and no failure. The wire-level gRPC
  tests cover typed Read, message-size rejection, and the intentional absence
  of Subscribe.
- Adapter and gRPC real-DA probe executables cross-built for both
  `windows/386` and `windows/amd64`. Seven package test executables compiled
  for each architecture. Release-shaped ZIPs for both architectures passed
  checksum and archive-integrity verification and contained the authoritative
  protobuf schema and third-party notices.
- `go mod verify` passed. The stripped release-shaped binaries were 12,485,120
  bytes on 386 and 13,092,352 bytes on amd64. Their embedded build metadata
  names six external modules: the existing `golang.org/x/sys` plus reviewed
  gRPC/protobuf runtime modules; no OPC SDK is present. Exact versions,
  licenses, provenance, and binary deltas are recorded in ADR-0012 and
  `THIRD_PARTY_NOTICES.md`.
- PR #29 CI run
  [`32752269432`](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269432)
  passed quality, race/fuzz checks, release packaging, Windows builds, and
  native Windows tests on 386 and amd64 at head
  `b83b7c2b159194cbac94ce66f52b325b9c22031f`.
- PR #29 real-DA run
  [`32752269529`](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269529)
  passed on Windows Server 2025 for x86/386 and x64/amd64. Both jobs passed
  gRPC Status, root/nested Browse, ordered partial Read with exact DA metadata,
  disabled Write, strict typed Write, source-denied Write, explicit guided
  setup, version 2 exact-CLSID configuration, LocalService execution, one
  loopback listener, and no process-value logging. The existing HTTP,
  reconnect, failure-cycle, load, and 200-Read soak regression also passed.
- The first PR #30 documentation run exposed a time-budget flake in the
  pre-existing HTTP string fuzz smoke test (`context deadline exceeded` after
  completing more than 110,000 executions). CI now uses a deterministic
  10,000-execution budget for each fuzz target instead of a wall-clock cutoff;
  no fuzz finding or product failure was suppressed.
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

- The OPC UA listener's shared services were audited for concurrency on
  2026-08-26 after two clients connecting at the same time were found to fault
  the process. The rule is now stated once on the `Listener` type and exercised
  by `internal/opcua/concurrency_test.go`. Any new service the listener holds
  must either be immutable after construction or synchronise itself, and must
  not hand out pointers to state it owns.

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
- The project has no production authentication/TLS/RBAC platform. The selected
  listener is loopback and Write is disabled unless explicitly enabled.
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
   source semantics. `docs/compatibility.md` lists the vendor variations to
   record, including a source without an `IOPCDataCallback` connection point and
   an unrecognized vendor disconnect HRESULT.
6. Phase 7 is the DA callback/Subscribe core before any gRPC stream. Do not
   start OPC UA first or infer a streaming contract from the unary frontend.
7. The Subscribe core is validated against the OPC Foundation fixture only.
   Treat vendor callback behavior as untested: a server may refuse connection
   points, revise update rates differently, or report Quality and timestamps
   differently. Record any such observation before changing source semantics.
8. Three third-party UA clients are not conformance. Run
   `scripts/interop/run.sh` before any change to the UA wire format, and keep
   all three enabled where their toolchains exist — two of the six defects
   found so far came from the second and third clients against a server the
   first already passed. UA Expert stays untested until someone with an account
   runs it. Do not make an "OPC UA Certified" or "OPC UA Compliant" claim;
   ADR-0016 forbids it.
9. The absent-session-nonce acceptance under `SecurityMode None` is a recorded
   deliberate deviation from a literal reading of OPC 10000-4 5.7.2, not an
   oversight. Reverse it by decision if a signed policy is ever served, where
   the nonce does real work and the rule is already enforced as written.

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
- [ADR-0012: DA-native gRPC frontend](adr/0012-grpc-da-native-frontend.md)
- [ADR-0013: DA-native Subscribe core](adr/0013-da-native-subscribe-core.md)
- [ADR-0014: gRPC Subscribe server streaming](adr/0014-grpc-subscribe-streaming.md)
- [ADR-0015: probe the Subscribe capability](adr/0015-subscribe-capability-probe.md)
- [ADR-0016: OPC UA frontend scope and the DA mapping foundation](adr/0016-opcua-frontend-scope-and-mapping.md)
- [ADR-0017: a third-party vendor DA server for validation](adr/0017-third-party-vendor-da-fixture.md) — **proposed, undecided**
