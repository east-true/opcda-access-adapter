# ADR-0005: reconnect and COM-hang policy

- Status: Accepted; reconnect validated against the official fixture
- Date: 2026-08-23

## Context

Local COM servers can exit or disconnect while the adapter retains server,
group, and item interface pointers. A failed HTTP call must not cause stale
handles or last-good values to be reused. Conversely, not every failed OPC DA
HRESULT means the connection has disappeared. Some vendor COM calls can hang,
and terminating their owning thread would violate COM ownership and leave a
Write outcome ambiguous.

## Decision

Activation failure and official COM/RPC disconnection HRESULTs schedule a
serialized reconnect on the existing DA thread. The recognized HRESULTs are
`RPC_E_CONNECTION_TERMINATED`, `RPC_E_SERVER_DIED`,
`RPC_E_SERVER_DIED_DNE`, `RPC_E_DISCONNECTED`, `CO_E_OBJNOTCONNECTED`,
`HRESULT_FROM_WIN32(RPC_S_SERVER_UNAVAILABLE)`, and
`HRESULT_FROM_WIN32(RPC_S_CALL_FAILED/RPC_S_CALL_FAILED_DNE)`. Other method and
item HRESULTs remain source errors and do not imply a disconnect.

Disconnect releases interfaces on the owning thread, invalidates the entire
registration cache, and schedules exponential reconnect with 80-120% jitter.
Defaults are one second initially and 30 seconds maximum. Each successful
connection increments a monotonic connection generation and creates a fresh,
empty bounded registration cache. Requests fail fast while not connected;
the adapter never returns or stores a previous process value.

A 30-second configurable watchdog observes serialized connect, Browse, Read,
Write, and shutdown cleanup calls without touching their COM pointers. If a
call exceeds the watchdog, status becomes `degraded`, capabilities become
unavailable, and new DA operations fail fast with a process-restart
instruction. The owning thread is not terminated and an in-flight Write is
not retried or replayed. If the call eventually returns, the runtime remains
degraded until restart.

The HRESULT values and meanings follow Microsoft’s
[COM STG/RPC error definitions](https://learn.microsoft.com/en-us/windows/win32/com/com-error-codes-3)
and [generic COM error definitions](https://learn.microsoft.com/en-us/windows/win32/com/com-error-codes-1).

## Consequences

Recovery is bounded and does not flood the server. A disconnect is normally
detected by the next source operation; v0 does not add a polling health group
or synthetic read. An unrecognized vendor-specific disconnect HRESULT remains
a source error until an actual compatibility result justifies adding it.

The Windows x86 and x64 official-fixture run stopped and unregistered the
active local COM server, observed no successful stale Read, exposed
disconnected/reconnecting state, then re-registered the server and observed a
newer generation, increased reconnect count, and successful lazy item
re-registration. Vendor-specific disconnect HRESULT coverage remains
intentionally unclaimed.

The watchdog provides honest operator visibility but cannot make an unsafe
in-process COM hang recoverable; hard subprocess isolation remains out of v0.
