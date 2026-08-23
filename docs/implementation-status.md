# Implementation Status

## Current phase

Phase 2 — DA Read Core (implemented locally on `feat/da-read`; ready for PR
validation).

## Current main SHA

`b1b2b19` — merged Phase 1 COM Foundation (PR #2). This Phase 2 branch is based
on that commit.

## Completed

- Phase 0 bootstrap merged in PR #1.
- Phase 1 dedicated STA COM runtime, local activation, owning-thread cleanup,
  and Windows 386/amd64 lifecycle tests merged in PR #2.
- `IOPCServer::AddGroup`, `IOPCItemMgt::AddItems`, and
  `IOPCSyncIO::Read(OPC_DS_DEVICE)` implemented from the official OPC DA IDL.
- Bounded lazy registration stores exact ItemID, server handle, canonical
  VARTYPE, raw access rights, and connection generation.
- Request ordering, item HRESULTs, partial failure, raw Quality, source
  FILETIME/presence, actual VARIANT type, and canonical VARTYPE are preserved.
- Returned blobs/result arrays use `CoTaskMemFree`; every returned VARIANT is
  cleared with `VariantClear`; group interfaces are released on the DA thread.
- Width-preserving scalar decode and lossless HTTP JSON representation added.
- `POST /v1/read` includes strict bounded JSON/schema validation and distinct
  frontend, adapter, source-method, and item-level error handling.
- Windows ABI assertions cover 386/amd64 VARIANT and OPC DA structure sizes and
  vtable offsets; BSTR cleanup and scalar width tests run on Windows CI.

## In progress

- Phase 2 PR, GitHub Windows execution, CI, and merge.

## Validation results

- PR #2: all five checks passed, including Windows 386/amd64 tests; merged.
- Phase 2 `gofmt` check passed locally.
- Phase 2 `go test ./...` passed on Linux.
- Phase 2 `go vet ./...` passed on Linux.
- Phase 2 Windows 386/amd64 test binaries and adapter binaries cross-compiled.
- Phase 2 Windows 386/amd64 `go vet ./...` passed.
- Real DA AddGroup/AddItems/Read execution remains externally blocked.

## Known issues

- Current Read scalar support is explicit in `docs/http-api.md`. `VT_DATE`,
  `VT_DECIMAL`, `VT_CY`, SAFEARRAY, BYREF, and nested VARIANT values are not
  silently converted and remain unsupported.
- Browse, Write, reconnect, bounded recent diagnostics, and COM-hang degraded
  policy remain for later phases.
- No real OPC DA server is available in the current environment.

## External blockers

- **BLOCKED:** Real-DA connect/Read V-Q-T verification, vendor error behavior,
  reconnect/server restart, installed-server x86/x64 compatibility, and soak
  testing require an authorized local Windows OPC DA server. Cross-builds,
  mocks, and COM-only Windows tests are not interoperability results.

## Next exact tasks

1. Push Phase 2, run all Linux/Windows checks, fix any ABI/runtime failures,
   and merge only when green.
2. Implement optional `IOPCBrowseServerAddressSpace` capability detection and
   serialized root/nested Browse in Phase 3.
3. Add `POST /v1/browse` with bounded, non-truncating results and exact source
   ItemIDs.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
