# UA subscriptions and DA groups

[UA overview](../opcua-mapping.md) · [Documentation](../README.md)

This reference contains the implementation contract and its specification rationale.
Executed results are recorded in [compatibility evidence](../compatibility.md).

## Subscriptions: the one filter a DA group can apply

A.3.5 says a wrapper uses the SamplingInterval and the Deadband to set up the
DA callback, and that **only `PercentDeadbandType` is supported**. The DA core
has always carried a percent deadband — it is `AddGroup`'s `pPercentDeadband`,
and the gRPC frontend has always exposed it. Only the UA frontend refused it,
along with every other filter.

A `DataChangeFilter` asking for a percent deadband is now accepted and passed to
the group. Everything else is refused rather than accepted and quietly not
applied:

| The client asks for | Answer |
|---|---|
| no filter | accepted; the group gets no deadband |
| `DataChangeFilter`, deadband `None` or `Percent` in 0–100 | accepted |
| `DataChangeFilter`, deadband `Percent` outside 0–100 | `Bad_DeadbandFilterInvalid` |
| `DataChangeFilter`, deadband `Percent` on an item with no `EURange` | `Bad_DeadbandFilterInvalid` — Table 61 names this case too |
| `DataChangeFilter`, deadband `Absolute` | `Bad_MonitoredItemFilterUnsupported` — `AddGroup` takes a percentage and nothing else |
| trigger `StatusValueTimestamp` | `Bad_MonitoredItemFilterUnsupported` — DA notifies on a change of value or quality, never on a timestamp alone |
| any other filter | `Bad_MonitoredItemFilterUnsupported` |

The status changed with the behaviour. A filter this server cannot perform is
`Bad_MonitoredItemFilterUnsupported`; `Bad_FilterNotAllowed`, which the adapter
used to answer, means the filter cannot be used with the attribute — and a
`DataChangeFilter` on a Value attribute plainly can.

**A percent deadband needs an `EURange`.** Clause 7.2 defines the value as "the
percentage of the EURange. That is, it applies only to AnalogItems with an
EURange Property". An item that is not an `AnalogItemType` has no range to take
a percentage of, so the filter has no defined meaning there and is refused with
`Bad_DeadbandFilterInvalid` — which Table 61 defines as covering both "not
between 0.0 and 100.0" **and** "a PercentDeadband is not supported, since an
EURange is not configured". Passing it to the DA group anyway would apply a
percentage of nothing. A filter asking for **no** deadband is accepted
on any item, because there is no percentage in it.

**A deadband belongs to the subscription, because it belongs to the DA group.**
One UA subscription is backed by one DA group, and a group has exactly one
`pPercentDeadband`. A second monitored item asking for a different deadband
cannot be honoured, so it is refused; applying somebody else's deadband to it
would report changes the client did not ask to hear about, or hide ones it did.

### Disabled publishing holds the newest value, it does not bank them

Clause 5.14.1.1: disabling "causes the Subscription to cease sending
NotificationMessages to the Client", while the subscription "continues to execute
cyclically and continues to send keep-alive Messages". The monitored items keep
sampling throughout — `MonitoringMode` is a separate setting — so something has
to hold what they produce.

What holds it is each item's own slot, which is the queue of one it was promised.
A disabled subscription therefore accumulates nothing to send: when publishing
resumes, each item reports once, with the newest value it saw. Banking every
value instead would grow the send queue for as long as the client left publishing
off, and then hand it a history it had explicitly asked not to receive.

### Republish, because the queue is already there

Every Publish response carries its subscription's available sequence numbers,
and 5.14.1.1 says what a non-empty list of them means: that the server "supports
a retransmission queue and acknowledgement of NotificationMessages", and that
clients "are required to acknowledge NotificationMessages as they are received".
A server that answers those numbers and then refuses to act on them is
advertising a queue its clients cannot reach.

So Republish answers from that queue. 5.14.6.1: "this Service requests the
Subscription to republish a NotificationMessage from its retransmission queue.
If the Server does not have the requested Message in its retransmission queue, it
returns an error response." A message that is not there — never sent, already
acknowledged, or dropped when the queue overflowed — is `Bad_MessageNotAvailable`,
and a subscription that is not this session's is `Bad_SubscriptionIdInvalid`,
which are the two codes Table 93 defines.

Republishing does not consume the message. Only an acknowledgement removes one,
and a client that had to ask for a message again has not acknowledged it.

### The retransmission queue is bounded, and drops its oldest

Clause 5.14.1.1 gives a session a retransmission queue "of at least two times
the number of Publish requests per Session the Server supports", and says what
happens when it fills: "in the case of a retransmission queue overflow, the
oldest sent NotificationMessage gets deleted".

This server answers one Publish per session at a time, so two is the floor, and
the queue holds far more than that. It has to be bounded either way: the queue
only shrinks when a client acknowledges, and a client that publishes without
ever acknowledging would otherwise hold every DataValue the server ever sent it.

Order is tracked rather than inferred from the sequence number, because that
number does not reset — 5.14.1.1 has it run past four billion before it repeats,
so comparing two of them says nothing about which was sent first once it wraps.

A client that acknowledges a message the queue has already dropped is told
`Bad_SequenceNumberUnknown`, which is the answer for a sequence number the
server is not holding.

### A subscription's lifetime is enforced, not merely reported

Table 82: "when the publishing timer has expired this number of times without a
Publish request being available to send a NotificationMessage, then the
Subscription **shall be deleted** by the Server", and 5.14.1.1 adds that
"closing the Subscription causes its MonitoredItems to be deleted".

For this adapter that is not bookkeeping. A subscription holds a DA group open
on the source, so a client that stops publishing while keeping its session alive
would otherwise hold that group for as long as it liked. The revised lifetime
count is a promise about when the server will give up on a client, and a server
that reports one it does not act on has told the client something untrue.

The counter resets on "any Service call that uses the SubscriptionId or the
processing of a Publish response". Every such call goes through one lookup, and
the reset lives there rather than at each call site, so a service added later
cannot forget it. An outstanding Publish keeps every subscription in its session
alive, not only the one that cycle happens to pick: 5.14.1.1 counts cycles "in
which there have been no Publish requests available", and while a client is
waiting on one, none of its subscriptions is starving.

Two parts of the clause are departures.

5.14.1.1 also says the server "shall issue a StatusChangeNotification
notificationMessage with the status code Bad_Timeout". This adapter does not:
the condition for expiry is that no Publish request was available, so in the
ordinary case there is nowhere to send it.

Clause 5.7.2.1 says that when "a Server terminates a Session for any other reason,
Subscriptions associated with the Session are not deleted", so they can be
transferred to a new session before their lifetime runs out. This adapter
deletes them, because it does not implement TransferSubscriptions — a
subscription kept past its session would hold a DA group open with nothing able
to reach it, which is the leak the rule exists to avoid, arrived at from the
other side.

### An item nobody may read is still created

OPC 10000-4 5.13.2.1: "When a user adds a monitored item that the user is denied
read access to, the add operation for the item shall succeed and the bad status
Bad_NotReadable or Bad_UserAccessDenied shall be returned in the Publish
response." Table 65 agrees by omission — `Bad_NotReadable` is not among the
operation level result codes CreateMonitoredItems may return.

So a DA item whose access rights carry no read bit is created, and its status
arrives through Publish. The status is reported once: a monitored item reports
changes, and this one has nothing further to say. The item stays out of the DA
group, because there is nothing for the group to read and a source that refuses
`AddItems` for it would fail the whole rebuild, taking every readable item in the
same request down with it.

The rest of the clause is a departure. "If the access rights change to read
rights, the Server shall start sending data for the MonitoredItem" — this adapter
will not notice. It learns an item's access rights once, from the browse entry or
the first read that reports them, and does not revise them afterwards, so an item
that becomes readable stays silent until the client creates it again. Noticing
would mean re-reading rights on a schedule the source never agreed to.

### The sampling interval a monitored item is promised

A DA group has one update rate for every item in it, and OPC UA gives each
monitored item its own sampling interval. The two are reconciled by treating the
group's revised rate as a floor and pacing anything slower.

OPC 10000-4 7.21 requires that "the Server shall always return a
revisedSamplingInterval that is equal or higher than the requested
samplingInterval". An item asking for something faster than the group runs at
cannot be given it, so it is told the group's rate -- which is higher, as the
rule requires. An item asking for something slower keeps its own interval and is
paced to it: it is not handed everything the group delivers, because reporting
an interval and then sending five times as much would break the promise in the
other direction.

The two special values are read as 7.21 defines them. Zero "indicates that the
Server should use the fastest practical rate", which here is the group's. "Any
negative number is interpreted as -1", which asks for the subscription's
publishing interval.

Clause 5.13.1.2 puts a second floor under it: "if the Server specifies a value
for the MinimumSamplingInterval Attribute it shall always return a
revisedSamplingInterval that is equal or higher". That attribute carries the
source's DA Scan Rate, so it is the source's own statement about how often the
item can change, and promising anything faster would promise something the
source has already said it will not do.

### The queue holds one value, which is what the source offers

Every monitored item reports a `revisedQueueSize` of 1, which 7.21 permits: "0
or 1 -- the Server returns the default queue size which shall be 1 ... The queue
has a single entry, effectively disabling queuing."

That is not a limitation this layer imposes; it is the shape of the source. A DA
group's pending set holds one value per item, because between two update-rate
ticks a DA server reports only the latest cache value. There is no second value
to queue.

Clause 5.13.1.5 describes exactly this case: "if the queue size is one, the
queue becomes a buffer that always contains the newest Notification ... The
discard policy is ignored if the queue size is one." So `discardOldest` is
accepted and has no effect, and the Overflow bit is never set -- 5.13.1.5 sets it
only "if a Notification is discarded for a DataValue and the size of the queue
is larger than one".

A paced item holds its newest value rather than dropping values as they arrive,
for the same reason: a queue of one holds the newest, and dropping instead would
leave a client that asked for a slow rate stuck on a stale value whenever the
source went quiet. It also keeps its place while it waits: a DA subscription
drains "preserving first-seen order", which is the source's own account of what
changed first, and pacing carries that order through rather than reordering
around it.

## Subscriptions and MonitoredItems

`internal/opcua/subscription.go` implements `CreateSubscription`,
`CreateMonitoredItems`, `DeleteMonitoredItems`, `DeleteSubscriptions`,
`SetPublishingMode` and `Publish` from **OPC 10000-4 Tables 82, 63, 89, 164,
161, 140 and 148**. This is what makes the DA Subscribe core reachable over UA.

**One UA Subscription is one DA subscription, which is one DA group.** That
keeps the DA sampling model intact: the DA server decides what a client sees
between update-rate ticks, and this layer carries those notifications rather
than re-sampling them. The subscription's revised publishing interval becomes
the DA group's requested update rate.

Because a DA group's item set is fixed when the group is created, adding or
removing a MonitoredItem **replaces** the DA subscription with one covering the
new set, releasing the old one. The DA core never resubscribes on its own, so
doing it here is explicit rather than hidden.

### What the parameters actually mean here

- **`revisedSamplingInterval`** reports the update rate the **DA server settled
  on**, read back from the group once it exists, because that is the real
  sampling rate and the source never saw the client's number. A vendor may
  revise far from what was requested, and the client should be told what the
  source actually does. Before the group exists the subscription's publishing
  interval is the best answer available.
- **`revisedQueueSize` is 1.** The DA core coalesces per item, so the effective
  queue is one value per item; reporting a larger queue would overstate what the
  client will receive.
- **A monitoring filter is refused** with `Bad_FilterNotAllowed`. The DA group's
  percent deadband is the only filtering the source offers, and silently
  ignoring a filter a client asked for would misreport what it receives.
- **Two items may not share a client handle**, since the handle is what
  identifies an item in a notification.

### Publish

`Publish` answers from what the DA core has already delivered, and it **holds
the request** until the subscription has something to report or a keep-alive
comes due. OPC 10000-4 5.14.5.1 is explicit that Publish requests are queued in
the server, and the reason is practical: a client issues the next Publish as soon
as the last response arrives, so an empty response returned at once is answered
at once. Measured against a third-party client before this was fixed, that was
**3,874 exchanges in 40 seconds against one notification actually delivered**,
and the load starved the very sampling the subscription existed to deliver.

Holding a request cannot occupy the connection, because a client keeps a Publish
outstanding while it reads and browses on the same channel. So a Publish is
answered from its own goroutine while the read loop carries on, writes to the
socket are serialised — the send sequence number is assigned under the same lock,
so chunks cannot interleave or go out of order — and a held request is released
when its connection ends. The read deadline is extended while a connection has
Publish requests outstanding: such a connection is idle because **the server owes
it a response**, not because the client has gone away.

Table 89's guidance that a server should limit active Publish requests, while
accepting more than the number of subscriptions created, is implemented as a
per-connection bound answered with `Bad_TooManyPublishRequests`.

With nothing to report for `maxKeepAliveCount` publishing cycles it answers a
keep-alive, which carries no `NotificationData` at all and does not consume a
sequence number.

The first cycle is an exception to the count. Clause 5.14.1.1: "when a
Subscription is created, the first Message is sent at the end of the first
publishing cycle to inform the Client that the Subscription is operational ...
This is the only time a keep-alive Message is sent without waiting for the
maximum keep-alive count to be reached." With a large keep-alive count and a slow
publishing interval, that is the difference between a client learning its
subscription works in one interval and in a hundred.

A keep-alive does not repeat the last sequence number either. Clause 5.14.1.1: each keep-alive
"contains the sequence number of the **next** NotificationMessage that is to be
sent". That number is what tells a client holding a gap whether the message it
is missing is still coming or was never produced — repeating the last one would
have it wait for a message it already has. On a subscription that has sent
nothing, the number is 1, which 5.14.1.1 states outright: the first keep-alive
"contains a sequence number of 1, indicating that the first NotificationMessage
has not yet been sent".

A keep-alive is not held for retransmission. 5.14.1.1 reserves that queue for
responses that "actually contain one or more Notifications", which is the same
distinction the clause uses to define the word NotificationMessage.

Table 82's `maxNotificationsPerPublish` of zero means the client imposes no
limit; a smaller client value tightens the server's bound. What does not fit is
reported through `moreNotifications` rather than dropped.

A subscription belongs to the session that created it, and closing a session
releases its DA groups — a closed session must not leave groups open on the
source.

### When the source goes away

The DA core invalidates its subscription on a source disconnect. The UA
subscription survives, but every reporting item is given a `Bad_NotConnected`
status in the next notification, so a client **learns the source is gone rather
than seeing the stream fall silent**. The DA subscription is released rather
than left dangling; recovering means creating monitored items again, which
matches the DA core's rule that resubscribing is always explicit.

## Shutting the listener down

`Shutdown` takes a context, so it has to mean something. It used to close the
socket and return at once however long a caller was prepared to wait, because
the wait it needed did not exist: `Serve` joined its goroutines privately, and
nothing outside could observe when that had happened. The two other frontends
delegate to `net/http` and `grpc-go` and really do drain, so a caller could not
tell from the signature which behaviour it was getting.

The listener now tracks every goroutine it starts — connection loops *and* the
goroutines holding a `Publish`, which outlive the read loop that accepted them —
and raises a completion signal once `Serve` has joined them all. `Shutdown`
waits on that signal or on the caller's context, whichever comes first.

It also **ends its sessions**, which releases the DA groups their subscriptions
hold on the source. That was previously left to the DA runtime's own teardown,
which worked only because the application happens to stop the runtime
immediately afterwards — the listener's shutdown depended on its caller's
ordering rather than on itself.

