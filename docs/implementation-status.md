# Implementation Status

## Current phase

Phase 1 — COM Foundation (implemented locally on `feat/com-foundation`; ready
for PR validation).

## Current main SHA

`d1f88c3` — merged Phase 0 bootstrap (PR #1). This Phase 1 branch is based on
that commit.

## Completed

- Phase 0 repository bootstrap, OSS documentation, bounded loopback HTTP
  lifecycle, `GET /v1/status`, CI, and Windows cross-builds merged in PR #1.
- Dedicated DA goroutine locked to one OS thread.
- STA `CoInitializeEx`/`CoUninitialize` pairing and message-aware wait loop.
- ProgID/CLSID resolution and local-only `IOPCServer` activation.
- Owning-thread `IUnknown::Release`, stop lifecycle, truthful status, and
  connection generation `1` only after successful activation.
- Repeated Windows start/stop test and official `IOPCServer` IID assertion.
- Windows-hosted 386/amd64 test jobs added to CI.

## In progress

- Phase 1 PR, GitHub CI, and merge.

## Validation results

- PR #1 CI: Linux quality and Windows 386/amd64 cross-build checks passed.
- Phase 1 `gofmt` completed with no remaining formatting changes.
- Phase 1 `go test ./...` passed on Linux.
- Phase 1 `go vet ./...` passed on Linux.
- Phase 1 Windows 386 and amd64 test binaries cross-compiled successfully.
- Phase 1 `git diff --check` passed.
- Windows-hosted COM lifecycle execution is pending Phase 1 CI.

## Known issues

- Phase 1 intentionally establishes only the base `IOPCServer`; group and
  operation interfaces arrive in Phase 2.
- Activation HRESULT is reflected as disconnected state but is not yet
  included in bounded recent diagnostics.
- No real OPC DA server or local Windows environment is available here.

## External blockers

- **BLOCKED:** Real-DA interoperability, reconnect/server-restart validation,
  x86/x64 runtime validation with an installed DA server, and soak testing
  require an authorized local Windows OPC DA environment. No simulator will be
  installed without explicit approval and EULA review.

## Next exact tasks

1. Push Phase 1, open its PR, confirm all Linux/Windows checks, and merge.
2. Implement `IOPCServer::AddGroup`, `IOPCItemMgt::AddItems`, bounded
   generation-aware registration, and device `IOPCSyncIO::Read` in Phase 2.
3. Implement lossless HTTP Read encoding and item-level partial failures.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
