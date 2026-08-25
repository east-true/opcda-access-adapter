# ADR-0014: gRPC Subscribe server streaming

- Status: Accepted
- Date: 2026-08-25

## Context

ADR-0013 added the DA-native Subscribe core and it was validated against the
OPC Foundation DA 2.05a fixture on both architectures. The design orders the
streaming frontend after that core, so this phase exposes Subscribe over the
existing typed gRPC service and adds nothing to HTTP.

The core already answers the hard question. OPC DA is a sampling model: between
two update-rate ticks a server reports only the latest cache value, so the core
keeps a per-subscription pending set holding at most one entry per active item.
The frontend's job is to carry that to a client without inventing a second
buffering model on top of it.

## Decision

### One stream is one subscription is one DA group

`rpc Subscribe(DASubscribeRequest) returns (stream DASubscribeResponse)` is the
only stream in the service and is server-streaming only. A client never pushes
into an open subscription; changing the item set means opening a new stream.
This keeps the RPC a direct expression of the DA group lifecycle instead of a
session protocol.

The first message is always `created`, carrying the subscription identifier,
connection generation, the server's `revised_update_rate_ms`, and one status per
item. Later messages are `update` batches.

### Notification values reuse `DAReadResult`

A subscribed value and a device Read value have identical DA semantics, so they
use the same message type rather than a parallel one. The design forbids
frontends from having different semantic paths for the same source data; reusing
the type makes that structural instead of a convention that can drift.

### Backpressure is the HTTP/2 window and nothing else

The handler holds no buffer. It waits for the core's update signal, drains the
pending set once, and blocks in `Send` while the client is behind. During that
block the core keeps coalescing per item, so a slow client observes the values
it would have observed at a slower requested rate. This is the same conclusion
ADR-0013 reached, carried across the transport unchanged: no queue to overflow,
no drop counter, and no disconnect for slow consumption.

### The stream never hides a retry

Ending a stream for any reason releases the DA group, including the client
cancellation path, which needs a detached context because the stream context is
already done. Values pending when a subscription is invalidated are discarded
rather than delivered, because they belong to an ended connection generation.
After an invalidated stream the client must call `Subscribe` again and receives
a new generation-scoped identifier; the adapter never resubscribes on its own.

Invalidation ends the stream with `Aborted` rather than `Unavailable`, so the
failure does not present itself as transparently retryable. `Unavailable` is
what generic gRPC retry configurations act on, and a resubscribe is a decision
the client must make.

### Streams are bounded separately from unary requests

Server-streaming RPCs bypass the unary interceptor, which is what the frontend
wants: a long-lived stream must not inherit the unary request deadline.
Admission is therefore handled in the handler with its own
`MaxSubscriptionStreams` semaphore, so long-lived streams cannot starve unary
requests out of the shared concurrency budget. The DA core enforces its own
`MaxSubscriptions` ceiling independently.

`MaxConnectionAge` continues to apply to subscription streams, so a long-lived
stream is closed periodically by design and the client resubscribes explicitly.
Bounded connection lifetime is kept; an unbounded stream is not introduced as a
side effect of adding Subscribe.

## Rejected alternatives

- **A frontend-side buffer or ring between the core and `Send`:** rejected
  because it would reintroduce exactly the overflow policy ADR-0013 showed OPC
  DA does not have, and would decouple delivery from the source's own sampling.
- **Bidirectional streaming to modify a live subscription:** rejected because
  DA has no such operation on an existing group; it would be an adapter-invented
  session protocol.
- **A dedicated notification message type instead of `DAReadResult`:** rejected
  because two types describing the same DA value will drift.
- **Ending an invalidated stream with `Unavailable`:** rejected because generic
  client retry policies act on `Unavailable`, which would make a resubscribe
  look automatic when the adapter deliberately requires it to be explicit.
- **Exempting subscription streams from `MaxConnectionAge`:** rejected because
  it would remove a bound in order to hide a reconnect the client should own.
- **Sharing the unary concurrency budget:** rejected because long-lived streams
  would starve unary Status/Browse/Read/Write.
- **An HTTP streaming frontend in this phase:** out of scope; HTTP Subscribe,
  SSE, and WebSocket remain absent.

## Consequences

- The service gains one streaming method; the four unary methods are unchanged.
- `DARuntimeStatus` gains `subscription_count`, which returns to zero when a
  stream is released.
- Configuration gains `MaxSubscribeItems` and `MaxSubscriptionStreams`, plus
  `OPCDA_MAX_SUBSCRIPTIONS` and `OPCDA_MAX_SUBSCRIPTION_ITEMS` for the runtime
  limits.
- The HTTP frontend is unchanged and still exposes no Subscribe.
