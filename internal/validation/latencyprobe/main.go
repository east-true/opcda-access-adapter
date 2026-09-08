// latencyprobe measures what this adapter adds to a DA Read, separately from
// what the source costs.
//
// The question it answers is "how much delay does the adapter introduce", and
// the honest answer needs the vendor's device read excluded rather than
// estimated around. The runtime timestamps the boundaries of a command's life
// -- enqueue, pick-up, call start, call end -- so the queue wait and the COM
// thread's own overhead are separable from the call itself.
//
// It runs in-process against the real COM path, which is the only place these
// numbers exist: the queue and the dedicated STA thread are Windows-only code.
// The frontends add their own encoding cost on top, which is measurable
// anywhere and is not what this probe is for.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const maximumProbeTimeout = 30 * time.Minute

func main() {
	clsid := flag.String("clsid", "", "exact CLSID of the local OPC DA server")
	itemID := flag.String("item", "Test/Int32", "one scripted ItemID to read repeatedly")
	batch := flag.Int("batch", 1, "items per Read, repeating the same ItemID")
	iterations := flag.Int("iterations", 2000, "measured Reads after the warm-up")
	warmup := flag.Int("warmup", 200, "Reads discarded before measuring, so lazy registration is not counted")
	timeout := flag.Duration("timeout", 5*time.Minute, "bounded probe deadline")
	flag.Parse()
	if flag.NArg() != 0 || *clsid == "" || *batch < 1 || *iterations < 1 ||
		*warmup < 0 || *timeout <= 0 || *timeout > maximumProbeTimeout {
		fmt.Fprintln(os.Stderr,
			"usage: latencyprobe -clsid CLSID [-item ITEMID] [-batch N] [-iterations N] [-warmup N] [-timeout DURATION]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	runtime, err := opcda.New(opcda.Config{
		Source:           opcda.SourceConfig{CLSID: *clsid},
		Limits:           opcda.DefaultLimits(),
		ReconnectInitial: 200 * time.Millisecond,
		ReconnectMax:     2 * time.Second,
		// The whole point of this probe. A default build leaves this false and
		// retains no operation history.
		CollectTimings: true,
	})
	if err != nil {
		fail("start DA runtime", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			fail("shutdown DA runtime", err)
		}
	}()

	if err := waitForConnection(ctx, runtime); err != nil {
		fail("connect to the source", err)
	}

	items := make([]opcda.DAItemID, *batch)
	for i := range items {
		items[i] = opcda.DAItemID(*itemID)
	}
	request := opcda.ReadRequest{Items: items}

	// The warm-up exists because the first Read of an item pays for its
	// AddItems registration. Counting that would report a one-off cost as the
	// steady state.
	for i := 0; i < *warmup; i++ {
		if _, err := runtime.ReadBatch(ctx, request); err != nil {
			fail("warm-up Read", err)
		}
	}

	measured, ok := runtime.(interface{ TimingSnapshot() opcda.TimingSnapshot })
	if !ok {
		fail("collect timings", fmt.Errorf("this runtime does not report timings"))
	}
	before := measured.TimingSnapshot()

	// The whole loop is timed as one block as well as per iteration. A single
	// Read can be shorter than the platform's clock tick -- a Windows CI runner
	// quantises to about half a millisecond -- and a per-iteration figure then
	// reads as zero. Dividing one long measurement by the iteration count is
	// immune to that, and is the number to trust.
	endToEnd := make([]time.Duration, 0, *iterations)
	blockStart := time.Now()
	for i := 0; i < *iterations; i++ {
		start := time.Now()
		results, err := runtime.ReadBatch(ctx, request)
		elapsed := time.Since(start)
		if err != nil {
			fail("measured Read", err)
		}
		if len(results) != *batch {
			fail("measured Read", fmt.Errorf("returned %d results for a batch of %d", len(results), *batch))
		}
		endToEnd = append(endToEnd, elapsed)
	}
	blockMean := time.Since(blockStart) / time.Duration(*iterations)
	after := measured.TimingSnapshot()
	if after.SourceCall.Count == before.SourceCall.Count {
		fail("collect timings", fmt.Errorf("no commands were recorded; instrumentation is not running"))
	}

	report(*batch, *iterations, endToEnd, blockMean, clockResolution(endToEnd), after)
}

// report prints durations and counts only. No ItemID and no value appears
// here, and none is available to: the probe holds a batch of one repeated
// identifier and discards every result it reads.
func report(batch, iterations int, endToEnd []time.Duration, blockMean, resolution time.Duration, timings opcda.TimingSnapshot) {
	sort.Slice(endToEnd, func(a, b int) bool { return endToEnd[a] < endToEnd[b] })
	at := func(q float64) time.Duration { return endToEnd[int(float64(len(endToEnd)-1)*q)] }

	fmt.Printf("LATENCY batch=%d iterations=%d clock=%v\n", batch, iterations, round(resolution))
	fmt.Printf("  %-22s %-14s %-13s %-13s %s\n", "", "mean", "p95", "p99", "max")
	line := func(name string, mean, p95, p99, max time.Duration) {
		fmt.Printf("  %-22s %-14v %-13v %-13v %v\n",
			name, round(mean), round(p95), round(p99), round(max))
	}
	line("read end to end", blockMean, at(0.95), at(0.99), endToEnd[len(endToEnd)-1])
	line("  queue wait", timings.QueueWait.Mean, timings.QueueWait.P95, timings.QueueWait.P99, timings.QueueWait.Max)
	line("  source COM call", timings.SourceCall.Mean, timings.SourceCall.P95, timings.SourceCall.P99, timings.SourceCall.Max)
	line("  dispatch", timings.Dispatch.Mean, timings.Dispatch.P95, timings.Dispatch.P99, timings.Dispatch.Max)

	adapter := timings.QueueWait.Mean + timings.Dispatch.Mean
	fmt.Printf("\n  adapter share: %v of %v per Read; the rest is the source's call\n",
		round(adapter), round(blockMean))
	if resolution > adapter && adapter > 0 {
		fmt.Printf("  the clock ticks every %v, coarser than the adapter's share, so read the\n"+
			"  mean rather than a percentile: a single sample cannot resolve this.\n", round(resolution))
	}
	fmt.Println("\nLATENCY_PROBE_PASS")
}

// clockResolution is the smallest non-zero gap the platform's clock reported
// across the run. It is printed because it decides whether a percentile here
// means anything, and a measurement that hides the limit of its own instrument
// is worse than one that states it.
func clockResolution(samples []time.Duration) time.Duration {
	smallest := time.Duration(0)
	for _, sample := range samples {
		if sample > 0 && (smallest == 0 || sample < smallest) {
			smallest = sample
		}
	}
	return smallest
}

func round(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return d.Round(time.Nanosecond * 100)
	}
	return d.Round(time.Microsecond)
}

func waitForConnection(ctx context.Context, runtime opcda.Runtime) error {
	for {
		status := runtime.Status(ctx)
		if status.State == opcda.RuntimeStateConnected {
			return nil
		}
		if status.State == opcda.RuntimeStateDegraded {
			return fmt.Errorf("runtime is degraded: %s", status.DegradedReason)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("source did not connect: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "LATENCY_PROBE_FAIL %s: %v\n", what, err)
	os.Exit(1)
}
