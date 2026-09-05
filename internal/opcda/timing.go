package opcda

import (
	"sort"
	"sync"
	"time"
)

// The adapter's own share of a DA operation is the part it can be held to. The
// vendor's device read is the source's, and it dominates, so the two have to be
// separable before either number means anything.
//
// A command's life has three parts. It waits in the queue, the dedicated COM
// thread runs it, and the caller is answered. The middle part is the vendor's
// call; the rest is this adapter. Timestamping the boundaries subtracts the
// source's share rather than estimating around it.
//
// Collection is off unless a Config asks for it. INV-6 allows bounded runtime
// state, but a production adapter that retains no operation history at all is
// a stronger thing to be able to say, and the HTTP reference says it. So the
// samples exist only in a run that opted in -- a validation probe -- and a
// default build allocates no ring and records nothing.

// timingSamples is a bounded ring. It keeps the most recent samples and the
// running count, so a long run reports percentiles over a recent window
// without growing.
const timingSamples = 4096

// PhaseTiming is what one phase of a command cost.
//
// Mean is the figure to read. The percentiles come from individual samples and
// are therefore limited by the platform's clock: a Windows CI runner was
// observed quantising to about half a millisecond, which is coarser than the
// adapter's whole share, so every sample read as either zero or one tick. Mean
// is computed from a running sum over every command rather than from the
// retained samples, and because the tick boundary falls at random relative to
// the operation, it converges on the true value where a percentile cannot.
type PhaseTiming struct {
	Count int
	Mean  time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

// TimingSnapshot separates the adapter's share of a DA operation from the
// source's. QueueWait and Dispatch are this adapter; SourceCall is the vendor's
// COM call and is reported so it can be excluded rather than guessed at.
type TimingSnapshot struct {
	QueueWait  PhaseTiming // enqueue until the COM thread picks the command up
	SourceCall PhaseTiming // the vendor's call: not this adapter's to answer for
	Dispatch   PhaseTiming // the COM thread's own per-command overhead
}

type timingCollector struct {
	mu         sync.Mutex
	queueWait  *ring
	sourceCall *ring
	dispatch   *ring
}

func newTimingCollector(enabled bool) *timingCollector {
	if !enabled {
		return nil
	}
	return &timingCollector{
		queueWait:  newRing(timingSamples),
		sourceCall: newRing(timingSamples),
		dispatch:   newRing(timingSamples),
	}
}

// record is a no-op on a nil collector, which is what a default build has, so
// the instrumented path costs one nil check when it is switched off.
func (c *timingCollector) record(queueWait, sourceCall, dispatch time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.queueWait.add(queueWait)
	c.sourceCall.add(sourceCall)
	c.dispatch.add(dispatch)
	c.mu.Unlock()
}

func (c *timingCollector) snapshot() TimingSnapshot {
	if c == nil {
		return TimingSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return TimingSnapshot{
		QueueWait:  c.queueWait.summarise(),
		SourceCall: c.sourceCall.summarise(),
		Dispatch:   c.dispatch.summarise(),
	}
}

type ring struct {
	samples []time.Duration
	next    int
	filled  bool
	count   int
	// sum is over every sample ever recorded, not only those retained, which
	// is what makes Mean independent of the ring size.
	sum time.Duration
}

func newRing(size int) *ring {
	return &ring{samples: make([]time.Duration, size)}
}

func (r *ring) add(sample time.Duration) {
	r.samples[r.next] = sample
	r.next = (r.next + 1) % len(r.samples)
	if r.next == 0 {
		r.filled = true
	}
	r.count++
	r.sum += sample
}

func (r *ring) summarise() PhaseTiming {
	held := r.next
	if r.filled {
		held = len(r.samples)
	}
	if held == 0 {
		return PhaseTiming{}
	}
	mean := r.sum / time.Duration(r.count)
	sorted := make([]time.Duration, held)
	copy(sorted, r.samples[:held])
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	at := func(q float64) time.Duration {
		index := int(float64(held-1) * q)
		return sorted[index]
	}
	return PhaseTiming{
		Count: r.count,
		Mean:  mean,
		P50:   at(0.50),
		P95:   at(0.95),
		P99:   at(0.99),
		Max:   sorted[held-1],
	}
}
