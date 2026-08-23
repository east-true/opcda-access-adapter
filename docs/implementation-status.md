# Implementation Status

## Current phase

Phase 3 — Browse (implemented locally on `feat/da-browse`; ready for PR
validation).

## Current main SHA

`3ff9712` — merged Phase 2 DA Read Core (PR #3). This Phase 3 branch is based
on that commit.

## Completed

- Phase 0 bootstrap merged in PR #1.
- Phase 1 COM Foundation merged in PR #2 with Windows 386/amd64 lifecycle
  execution.
- Phase 2 AddGroup/AddItems/device Read and HTTP Read merged in PR #3 with all
  Linux/Windows checks green.
- Optional `IOPCBrowseServerAddressSpace` is detected with QueryInterface;
  `E_NOINTERFACE` becomes `unsupported` without disabling known-ItemID Read.
- Hierarchical Browse resets to root and walks each navigation segment on the
  serialized DA thread; flat namespaces remain root-only.
- Source branch/leaf names are enumerated through `IEnumString`, each returned
  string is freed, and each item obtains its exact source ItemID through
  `GetItemID`.
- Browse entry and depth limits fail explicitly without silent truncation.
- `POST /v1/browse` validates bounded exact navigation, preserves branch/item
  distinction, and maps unsupported/limit errors distinctly.

## In progress

- Phase 3 PR, GitHub Windows execution, CI, and merge.

## Validation results

- PR #3: all five checks passed, including Windows 386/amd64 ABI, BSTR,
  VariantClear, and scalar width tests; merged.
- Phase 3 `gofmt`, `go test ./...`, and `go vet ./...` passed on Linux.
- Phase 3 Windows 386/amd64 test binaries cross-compiled and vet passed.
- Actual vendor root/nested/flat Browse remains externally blocked.

## Known issues

- DA 2.x Browse does not directly return canonical VARTYPE/access rights in
  its enumeration contract. These fields are omitted rather than inferred or
  populated by an unbounded registration scan.
- The zero-FILETIME rule and current scalar support remain pending real-server
  validation as recorded in ADR-0003.
- Write, reconnect, diagnostic bounds, and COM-hang degraded policy remain.

## External blockers

- **BLOCKED:** Real-DA Browse/Read/Write, nested/flat namespace quirks,
  reconnect/server restart, installed-server x86/x64 compatibility, and soak
  testing require an authorized local Windows OPC DA server. A simulator will
  not be installed without explicit approval and EULA review.

## Next exact tasks

1. Push Phase 3, run Linux/Windows checks, and merge only when green.
2. Implement strict typed scalar Value Write and `IOPCSyncIO::Write` in Phase
   4, retaining write-disabled default and no retry/replay.
3. Add `POST /v1/write` validation, exact numeric overflow checks, and item
   HRESULT preservation.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
- [ADR-0002: STA runtime and local COM activation](adr/0002-sta-runtime-and-local-com-activation.md)
- [ADR-0003: v0 Read types and FILETIME presence](adr/0003-read-types-and-filetime-presence.md)
