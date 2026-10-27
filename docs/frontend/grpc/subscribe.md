# Subscribe

[Frontend overview](README.md) · [Documentation](../../README.md)

One `Subscribe` stream is exactly one OPC DA group. The request carries the
items, `requested_update_rate_ms` (the group's `dwRequestedUpdateRate`), and an
optional `percent_deadband`.

The first stream message is always `created`. It reports the subscription
identifier, the connection generation, the **server's** `revised_update_rate_ms`
rather than the requested rate, and one status per item. An item the source
refused keeps its exact HRESULT in that status instead of failing the whole
subscription, so a subscription can legitimately come back with
`active_item_count` lower than the number of requested items.

Every later message is an `update` carrying one coalesced notification batch.
Its values are `DAReadResult` messages, the same type a device Read returns, so
a subscribed value and a read value cannot drift apart.

## Delivery follows DA sampling, not an event log

OPC DA is a sampling model. Between two update-rate ticks a server reports only
the latest cache value for an item, so the adapter coalesces per item in the
same way. The consequences are deliberate:

- **A slow client observes a slower update rate.** Backpressure is the HTTP/2
  flow-control window and nothing else; the adapter holds no queue of its own.
  While the client is behind, the DA core keeps replacing each item's pending
  value, exactly as a server does between ticks.
- **There is no overflow.** The pending state holds at most one entry per
  active item, so there is nothing to overflow and no drop counter to report.
- **A slow client is never disconnected for being slow.** No DA server does
  that, so the adapter does not either.

## Ending a stream

| Cause | What the client sees |
|---|---|
| client cancels or closes | stream ends; the adapter releases the DA group |
| source disconnect, degraded runtime, or shutdown | `Aborted` with a `DAOperationError` whose code is `SUBSCRIPTION_INVALIDATED` |
| too many concurrent streams | `ResourceExhausted` with code `SUBSCRIPTION_LIMIT_EXCEEDED` |

Ending a stream for any reason releases the subscription. Values still pending
when a subscription is invalidated are **discarded, never delivered**, because
they belong to a connection generation that has ended. The adapter never
resubscribes on its own and never replays a value: after an invalidated stream
the client must call `Subscribe` again, and it receives a new
generation-scoped identifier.

`DARuntimeStatus.subscription_count` reports how many DA groups the runtime
currently holds, and returns to zero once a stream has been released.

## Connection age bounds a stream

`MaxConnectionAge` (30 minutes by default) plus its grace period applies to
subscription streams as well as unary calls, so a long-lived stream is closed
periodically by design. This keeps connection lifetime bounded; the client
reconnects and subscribes again explicitly. Raise
`OPCDA_GRPC_MAX_CONNECTION_AGE` if a deployment needs longer streams, and
understand that doing so extends how long one connection can be held.
