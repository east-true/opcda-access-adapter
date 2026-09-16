# Benchmarks

These measure **the adapter's own share** and nothing else. Every benchmark
drives a stub runtime that answers immediately, so no number here includes a
DA server's latency — that is the source's, not the adapter's, and it is not
something this adapter can measure or improve.

Two axes, because an operator has two questions.

**How does cost grow with the data a request carries?** A batch of a hundred
items, or an array of a thousand elements, is not obviously a hundred or a
thousand times a batch of one, and the difference decides whether batching is
worth doing.

**Does it stay there?** An adapter that is fast for a minute and slower every
hour after is not fast. The shape that produces it — something retained per
request — does not appear in a measurement that only reports an average, so
drift and retention are reported as their own metrics.

## Running them

```
go test ./internal/frontend/http/  -run '^$' -bench 'ByBatchSize|ByElementCount'
go test ./internal/frontend/grpc/  -run '^$' -bench 'ByBatchSize|ByElementCount'
go test ./internal/opcua/          -run '^$' -bench 'ByBatchSize|ByElementCount'
go test ./internal/opcda/          -run '^$' -bench 'Array'
go test ./internal/frontend/http/  -run '^$' -bench 'Sustained' -benchtime=30000x
```

Benchmarks do not run under a plain `go test`, so CI is unaffected by them.

## What to compare, and what not to

The three frontends are only comparable when each is measured doing the same
work: **bytes in, bytes out**. `BenchmarkReadByBatchSizeOnTheWire` (gRPC) and
`BenchmarkReadByBatchSizeEncoded` (OPC UA) exist for that reason — a handler
measured without its encoding leaves out most of what a frontend is, and
comparing that against a frontend measured through its transport would flatter
one of them for a reason that has nothing to do with either.

The handler-only benchmarks are still worth having beside them: the difference
between the two is what the encoding costs.

Absolute numbers belong to the machine that produced them. What travels is the
**shape** — whether per-item cost is flat as the batch grows, whether
allocations per item stay constant, whether drift is near zero.

## What a measurement here cannot tell you

- **Nothing about a real source.** The stub answers instantly; a real DA server
  does not. End-to-end latency is dominated by the source on any real
  deployment, which is why the adapter's share is worth knowing separately and
  worth keeping small.
- **Nothing about the COM thread under contention.** These run one request at a
  time. The runtime's owning thread serialises source calls, and what that
  costs under concurrent load is a property of the whole system rather than of
  an encoder.
- **Nothing about Windows.** They run wherever `go test` runs; the SAFEARRAY
  paths are Windows-only and are exercised by the Windows unit cases rather
  than measured here.
