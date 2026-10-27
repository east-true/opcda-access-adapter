# ADR-0013: DA-native Subscribe core

- Status: Accepted
- Date: 2026-08-25

## Context

The unary gRPC frontend closed the last access phase and explicitly deferred
Subscribe until a DA callback core exists. The design orders that core before
any streaming frontend, so this phase adds Subscribe to the DA runtime only.
No HTTP, gRPC, SSE, or WebSocket surface is added.

The v0 runtime already owns the preconditions: a dedicated locked STA OS
thread, an `MsgWaitForMultipleObjectsEx`/`PeekMessageW` message pump, a bounded
command queue, monotonic connection generations, and stale-handle
invalidation. The remaining work is the OPC DA 2.05a subscription model itself.

The first design question was what the adapter should do when a consumer is
slower than the notification rate. Framing that as a queue-overflow policy —
terminate the subscription, drop oldest, or drop newest with a loss counter —
turned out to be the wrong frame, because OPC DA does not define any of those.

## Decision

### The subscription unit is the DA group

One subscription is exactly one DA group created by `IOPCServer::AddGroup`
with an explicit `dwRequestedUpdateRate`, an optional `pPercentDeadband`, and
active items added through `IOPCItemMgt::AddItems`. Notifications arrive on
`IOPCDataCallback::OnDataChange` after
`IConnectionPointContainer::FindConnectionPoint` and
`IConnectionPoint::Advise`. The adapter reports the server's
`dwRevisedUpdateRate` unchanged and never presents the requested rate as the
effective one.

Each subscription owns its own group, advise cookie, callback object, client
handle space, and pending state. The v0 shared SyncIO group keeps group client
handle 1 and is untouched; subscription groups start at 2, so no
`OnDataChange` can be attributed ambiguously.

### Delivery follows DA sampling, not an event log

OPC DA is already a sampling model. Between two update-rate ticks a server
reports only the latest cache value for an item; a value that changed five
times in one interval produces one notification. The update rate *is* the
decimation policy, and `dwRevisedUpdateRate` already states that the requested
rate is not guaranteed.

The adapter therefore coalesces per item in exactly the same way. The pending
state is a per-subscription map from client handle to the latest
`SubscriptionValue`, drained as one batch so a tick keeps its batch shape.
Re-notification of a pending item keeps the item's first-seen position and
replaces its tuple.

Three consequences follow, and they are the point of this decision:

- **There is no queue-full condition.** The pending set holds at most one entry
  per active item, so its size is bounded by the subscription's item count,
  which is bounded by `MaxSubscriptionItems`. There is no arbitrary queue depth
  to overflow.
- **A slow consumer observes a slower update rate**, which is a first-class DA
  concept, not a failure. It never terminates the subscription, because no DA
  server terminates a subscription for slow consumption.
- **No synthetic drop counter is exposed.** DA has no such concept, and a
  counter would describe adapter-invented loss rather than source behavior.

`RejectedNotifications` is not a loss counter. It counts `OnDataChange` calls
refused because the server supplied an inconsistent count or a null array, and
is diagnostic only.

### The producer never blocks

`OnDataChange` is a call from the DA server into the adapter. Blocking inside
it would stall the server's calling thread, stall the STA message pump, and
therefore stall Read, Write, and Browse, and could trip a vendor callback
timeout. Merging into the pending set is non-blocking and holds only a short
mutex with no COM call inside it. VARIANT payloads are decoded before the lock
is taken, and the server-owned arrays are never cleared or freed by the
callback.

Because a DA server may be registered as an in-process handler, the callback
can arrive on a thread the adapter does not own. The callback registry and the
pending set are synchronised accordingly, and the callback resolves its owner
through an integer identity rather than a Go pointer read from server memory.

### Invalidation is explicit and never replayed

Disconnect, degraded transition, unsubscribe, and shutdown all invalidate the
subscription: the advise is dropped, the group is removed, and **the pending
set is discarded rather than delivered**, because those values belong to a
connection generation that has ended. This preserves the existing invariant
that no last-good data is returned after disconnect.

Reconnect re-creates neither groups nor items and re-advises nothing. A client
must subscribe again explicitly, and the new subscription receives a new
generation-scoped identifier. Identifiers are never reused.

### Per-item failures are preserved

`AddItems` failures are reported per item in `SubscriptionInfo.Items` with the
exact source HRESULT; they never fail the whole subscription. A subscription
whose items all failed is returned live with `ActiveItemCount` zero rather
than being converted into a request-level error that could not carry the
per-item detail. Inside a notification, an item the source marked failed keeps
its HRESULT and carries no value.

### Bounds and lifetime

`MaxSubscriptions` (default 16) and `MaxSubscriptionItems` (default 100) are
added to the existing runtime limits, with aggregate ceilings on the pending
value and ItemID budgets. `Subscribe` deliberately does not abandon an
in-flight COM call on a caller deadline: returning early would leave an
advised DA group with no owner. The existing COM watchdog bounds the call.

The callback object is pinned with `runtime.Pinner` for as long as the server
can reach it. If a server leaks a reference, the allocation stays pinned
rather than risking a use-after-free; that leak is bounded by
`MaxSubscriptions`.

## Rejected alternatives

- **Terminate the subscription on a full queue:** rejected because it invents a
  failure mode OPC DA does not have. No DA server ends a subscription because
  the client is slow.
- **Drop-oldest or drop-newest with a loss counter:** rejected for the same
  reason, and because a slow consumer is a sustained condition rather than an
  occasional glitch — the counter would report steady-state adapter loss where
  DA defines rate-based sampling.
- **Blocking the callback until the consumer catches up:** rejected because it
  applies backpressure to the DA server's thread and the STA pump, stalling
  every other operation.
- **One shared notification queue for all subscriptions:** rejected because the
  DA subscription unit is the group; per-group state keeps a slow consumer on
  one subscription from affecting another.
- **Automatic resubscribe after reconnect:** rejected because it would replay
  client intent across a connection generation boundary and could silently
  resurrect a subscription against a changed address space.
- **Reusing the shared SyncIO group for subscriptions:** rejected because a
  subscription needs its own update rate, deadband, active state, and advise
  cookie, and because the shared group must stay inactive for device reads.
- **Adding a streaming frontend in this phase:** rejected by the design order.
  The DA core is validated against a real server first.

## Consequences

- The DA runtime gains `Subscribe` and `Unsubscribe`; the `Subscribe`
  capability and a subscription count appear in runtime status.
- No frontend exposes Subscribe yet. The HTTP and gRPC surfaces are unchanged.
- Real-server validation of callback delivery, reconnect invalidation, and
  cleanup remains outstanding and is not claimed by this ADR.
