# Implementation Status

## Current phase

Phase 0 — Repository bootstrap and HTTP lifecycle (ready for PR from
`chore/bootstrap`).

## Current main SHA

`195949b` — design document upload. The authoritative copy is being moved to
`docs/design.md` in the bootstrap PR.

## Completed

- Design baseline reviewed in full.
- Existing Apache-2.0 `LICENSE` confirmed; no license decision is needed.
- Official Microsoft COM/VARIANT guidance and OPC DA 2.05a interface material
  identified for use during the Windows implementation.

## In progress

- Go module and DA-native, frontend-independent contract (including HRESULT
  and VARTYPE preservation primitives).
- Bounded loopback HTTP lifecycle and `GET /v1/status`; it exposes no fake DA
  data and reports an unavailable/non-configured runtime truthfully.
- Root continuation instructions, OSS/security/contribution documentation,
  compatibility procedure, ADR-0001, and CI.
- Local assistant-state/scratch patterns are ignored; required repository
  documents remain versioned.

## Validation results

- `gofmt` completed with no remaining formatting changes.
- `go test ./...` passed on Linux.
- `go vet ./...` passed on Linux.
- `GOOS=windows GOARCH=386 go build ./cmd/adapter` passed (cross-build only).
- `GOOS=windows GOARCH=amd64 go build ./cmd/adapter` passed (cross-build only).
- `git diff --check` passed.
- GitHub CI has not run for this branch yet.

## Known issues

- No real OPC DA server is available in the current environment.
- No Windows runner is available locally; Windows artifacts can be cross-built
  but COM behavior cannot be executed here.

## External blockers

- **BLOCKED:** Real-DA interoperability, reconnect/server-restart validation,
  x86/x64 runtime validation, and soak testing require a local Windows machine
  with an installed, licensed/authorized OPC DA server. No simulator will be
  installed without explicit approval/EULA review.

## Next exact tasks

1. Finish and merge Phase 0 after local tests, vet, cross-build, and CI.
2. Implement the Windows dedicated COM runtime and activation on
   `feat/com-foundation`.
3. Implement AddGroup/AddItems/device Read with VARIANT ownership on
   `feat/da-read`.

## Decisions

- [ADR-0001: v0 bounds and runtime defaults](adr/0001-v0-bounds-and-runtime-defaults.md)
