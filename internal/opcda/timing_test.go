package opcda

import (
	"testing"
	"time"
)

// A default build must retain no operation history. That is what the HTTP
// reference says of a production adapter, and it is the reason collection is
// opt-in rather than always-on with a flag read at report time: a collector
// that exists but is ignored still holds samples.
func TestTimingCollectionIsOffUnlessAskedFor(t *testing.T) {
	if collector := newTimingCollector(false); collector != nil {
		t.Fatal("a runtime that did not ask for timings allocated a collector")
	}

	// Recording into the nil collector a default build has must be safe, since
	// the instrumented path calls it on every command.
	var absent *timingCollector
	absent.record(time.Second, time.Second, time.Second)
	if snapshot := absent.snapshot(); snapshot.SourceCall.Count != 0 {
		t.Fatalf("a disabled collector reported %d samples", snapshot.SourceCall.Count)
	}
}

func TestTimingSnapshotSeparatesThePhases(t *testing.T) {
	collector := newTimingCollector(true)
	if collector == nil {
		t.Fatal("collection was asked for and no collector was made")
	}
	for i := 1; i <= 100; i++ {
		collector.record(
			time.Duration(i)*time.Microsecond,
			time.Duration(i)*time.Millisecond,
			time.Duration(i)*time.Nanosecond,
		)
	}
	snapshot := collector.snapshot()

	// The three phases must not be conflated: the whole point is that the
	// source's call can be excluded from the adapter's share.
	if snapshot.QueueWait.Max != 100*time.Microsecond {
		t.Errorf("queue wait max = %v, want 100µs", snapshot.QueueWait.Max)
	}
	if snapshot.SourceCall.Max != 100*time.Millisecond {
		t.Errorf("source call max = %v, want 100ms", snapshot.SourceCall.Max)
	}
	if snapshot.Dispatch.Max != 100*time.Nanosecond {
		t.Errorf("dispatch max = %v, want 100ns", snapshot.Dispatch.Max)
	}
	if snapshot.SourceCall.Count != 100 {
		t.Errorf("count = %d, want 100", snapshot.SourceCall.Count)
	}
	if snapshot.QueueWait.P50 <= 0 || snapshot.QueueWait.P50 >= snapshot.QueueWait.P99 {
		t.Errorf("p50 %v and p99 %v are not ordered", snapshot.QueueWait.P50, snapshot.QueueWait.P99)
	}
}

// The ring is what keeps the state bounded, which is the condition INV-6 puts
// on runtime state. A run longer than the ring must keep reporting, keep the
// true total count, and summarise only what it still holds.
func TestTimingRingStaysBounded(t *testing.T) {
	collector := newTimingCollector(true)
	total := timingSamples + 500
	for i := 0; i < total; i++ {
		// Ascending, so the oldest samples are the smallest and dropping them
		// is visible in the minimum that survives.
		collector.record(time.Duration(i)*time.Nanosecond, 0, 0)
	}
	snapshot := collector.snapshot()
	if snapshot.QueueWait.Count != total {
		t.Errorf("count = %d, want the true total %d", snapshot.QueueWait.Count, total)
	}
	if held := len(collector.queueWait.samples); held != timingSamples {
		t.Errorf("the ring grew to %d; it is meant to stay %d", held, timingSamples)
	}
	if snapshot.QueueWait.Max != time.Duration(total-1)*time.Nanosecond {
		t.Errorf("max = %v; the most recent sample should have survived", snapshot.QueueWait.Max)
	}
}
