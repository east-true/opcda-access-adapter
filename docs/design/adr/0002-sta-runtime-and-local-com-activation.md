# ADR-0002: STA runtime and local COM activation

- Status: Accepted
- Date: 2026-08-23

## Context

The design requires every DA COM pointer and its cleanup to stay on one locked
OS thread. It also requires a structure that can service apartment messages
when vendor proxies or future callbacks need them. The activation flags must
not permit remote DCOM.

## Decision

The Windows runtime uses one goroutine locked with `runtime.LockOSThread`. That
thread calls `CoInitializeEx` with `COINIT_APARTMENTTHREADED` and
`COINIT_DISABLE_OLE1DDE`, creates a Win32 event for bounded-command wakeups,
and waits with `MsgWaitForMultipleObjectsEx`. Pending Windows messages are
drained with `PeekMessageW`, `TranslateMessage`, and `DispatchMessageW`.

Source identifiers are resolved with `CLSIDFromProgID` or `CLSIDFromString`.
Activation requests only `CLSCTX_INPROC_SERVER | CLSCTX_LOCAL_SERVER`; it does
not use `CLSCTX_REMOTE_SERVER` or `CLSCTX_ALL`. The requested interface is the
OPC Foundation `IOPCServer` IID from the DA IDL.

All interface `Release` calls and the balancing `CoUninitialize` run on the
owning DA thread. `S_OK` and `S_FALSE` from `CoInitializeEx` are both treated
as successful calls that require `CoUninitialize`.

## Consequences

COM work is serialized and cannot be force-cancelled while a vendor call is in
flight. This favors apartment and ownership correctness over concurrency. The
message-aware wait loop is slightly more involved than blocking on a Go
channel, but it leaves the v0 runtime able to dispatch STA work without moving
COM pointers between goroutines.

Microsoft Win32 documentation and the OPC Foundation DA IDL are the
authoritative sources; their documents are referenced, not copied into this
repository.
