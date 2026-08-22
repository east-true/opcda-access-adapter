# OPC DA Access Adapter

## Purpose

A thin, Windows-only access adapter for exactly one local OPC DA server. v0
exposes DA-native Browse, Read, and typed value Write over HTTP/JSON without
changing source semantics.

## Authoritative documents

- `docs/design.md` is the design baseline and overrides implementation ideas.
- `docs/implementation-status.md` is the continuation record.
- `docs/adr/` records reversible engineering decisions and any required design
  change.

## Critical invariants

- Source is OPC DA only; one adapter instance serves one local-COM server.
- Do not add remote DCOM, aggregation, mapping, renaming, scaling, common
  Asset/Metric models, persistence, gateway transports, or a plugin framework.
- Preserve exact ItemID, VARTYPE, raw Quality, timestamp presence, HRESULT,
  access rights, and per-item errors. Never synthesize a source timestamp or
  return last-good data after disconnect.
- DA COM pointers and cleanup remain on the dedicated locked OS thread.
- Write is disabled by default, value-only, strictly typed, and never retried
  or replayed automatically.
- Process values, including Write values, must not be logged by default.

## Build and test

```text
gofmt -w .
go test ./...
go vet ./...
GOOS=windows GOARCH=386 go build ./cmd/adapter
GOOS=windows GOARCH=amd64 go build ./cmd/adapter
```

## Git workflow

`main` is updated only through a green PR. Work one implementation phase per
branch, update the status document, run the commands above, push, open a PR,
confirm CI, merge, and continue. Do not claim real DA compatibility without a
real server result in `docs/compatibility.md`.

## Current v0 definition

v0 is HTTP-only and targets OPC DA 2.05a. It includes status, Browse, device
Read, and strict typed value Write plus reconnect and bounds. gRPC, OPC UA,
Subscribe, UI, storage, and all non-DA sources are explicitly out of scope.
