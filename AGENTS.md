# OPC DA Access Adapter

## Purpose

A thin, Windows-only access adapter for exactly one local OPC DA server. The
completed HTTP/JSON v0 and the current typed gRPC frontend expose DA-native
Browse, Read, and typed value Write without changing source semantics.

## Authoritative documents

- `docs/design.md` is the design baseline and overrides implementation ideas.
- `docs/implementation-status.md` is the continuation record.
- `docs/adr/` records reversible engineering decisions and any required design
  change.

## Critical invariants

- Source is OPC DA only; one adapter instance serves one local-COM server.
- Local detection may enumerate bounded DA 2.0 registrations, but must not
  activate candidates, auto-select a source, or accept a remote machine.
- Guided setup must require explicit source/frontend/action choices, create a
  new bounded configuration without implicit overwrite, and keep each
  foreground process or Windows Service bound to exactly one local source.
- Do not add remote DCOM, aggregation, mapping, renaming, scaling, common
  Asset/Metric models, persistence, gateway transports, or a plugin framework.
- Preserve exact ItemID, VARTYPE, raw Quality, timestamp presence, HRESULT,
  access rights, and per-item errors. Never synthesize a source timestamp or
  return last-good data after disconnect.
- DA COM pointers and cleanup remain on the dedicated locked OS thread.
- Subscribe follows the OPC DA group model exactly. Delivery is update-rate
  sampling with per-item coalescing, so there is no notification queue to
  overflow, no adapter-invented drop counter, and no subscription termination
  for a slow consumer. A callback must never block. Disconnect invalidates a
  subscription and discards its pending values; resubscribe is always explicit.
- A Subscribe stream holds no frontend buffer. Backpressure is the transport
  window, ending a stream releases the DA group, and an invalidated stream ends
  with an explicit error rather than looking transparently retryable.
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

## Current implementation definition

The completed v0 is HTTP-only and targets OPC DA 2.05a. Phase 6 adds an
explicitly selected typed unary gRPC frontend for Status, Browse, device Read,
and strict typed value Write. It does not add simultaneous frontend listeners.
Phase 8 adds a hand-written OPC UA server frontend for `SecurityPolicy None`:
UA-TCP framing, SecureChannel, sessions, an address space filled from DA Browse
on demand, and Browse/Read/Write. It is selectable in configuration version 3,
is for local interoperability work only, and is never described as production
ready. UA Subscriptions and MonitoredItems are not implemented.

Phase 7 adds a DA-native Subscribe core plus its gRPC frontend: one
subscription is one DA group advised through `IOPCDataCallback`, exposed as a
server-streaming `Subscribe` RPC. The core is validated against the OPC
Foundation DA 2.05a fixture on both architectures. HTTP exposes no Subscribe,
and HTTP streaming, SSE, and WebSocket remain out of scope. OPC UA,
UI, storage, and all non-DA sources remain out of scope. Local CLI detection is
registration inventory only and does not alter the single explicitly configured
runtime source.
