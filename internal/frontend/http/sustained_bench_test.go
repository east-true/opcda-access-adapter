package http

import (
	"bytes"
	stdhttp "net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// The volume benchmarks answer how cost grows with the data one request
// carries. This answers the other question an operator has: whether it stays
// there. An adapter that is fast for a minute and slower every hour after is
// not fast, and the shape that produces it -- something retained per request --
// does not show up in a measurement that only ever reports an average.
//
// Both of these measure the adapter's own share. The runtime is a stub that
// answers immediately, so nothing here waits on a source.

// BenchmarkSustainedReadDrift runs one request shape long enough to see
// whether the cost of the last tenth differs from the cost of the first, and
// reports the difference as its own metric. A steady adapter reports something
// close to zero; one that accumulates reports a number that grows with the
// benchmark's length, which is the signature worth looking for.
func BenchmarkSustainedReadDrift(b *testing.B) {
	const items = 50
	runtime, itemIDs := benchmarkReadRuntime(items)
	server := benchmarkServer(runtime)
	body := readBody(itemIDs)

	// Ten buckets, so the first and the last are each a tenth of the run and
	// neither is a single sample.
	const buckets = 10
	if b.N < buckets*10 {
		// Too short to say anything about drift. Do the work so the harness
		// can scale up, and report nothing.
		for i := 0; i < b.N; i++ {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
				bytes.NewReader(body)))
		}
		return
	}

	perBucket := b.N / buckets
	elapsed := make([]time.Duration, buckets)
	b.ReportAllocs()
	b.ResetTimer()
	for bucket := 0; bucket < buckets; bucket++ {
		start := time.Now()
		for i := 0; i < perBucket; i++ {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
				bytes.NewReader(body)))
			if response.Code != stdhttp.StatusOK {
				b.Fatalf("status = %d", response.Code)
			}
		}
		elapsed[bucket] = time.Since(start)
	}
	b.StopTimer()

	first := float64(elapsed[0]) / float64(perBucket)
	last := float64(elapsed[buckets-1]) / float64(perBucket)
	b.ReportMetric(first, "ns/op-first-tenth")
	b.ReportMetric(last, "ns/op-last-tenth")
	b.ReportMetric((last-first)/first*100, "%drift")
}

// BenchmarkSustainedReadRetention reports what the process is still holding
// after a long run. A frontend that retains nothing per request settles back to
// roughly where it started; one that retains something reports a heap that
// grew with the number of requests rather than with their size.
func BenchmarkSustainedReadRetention(b *testing.B) {
	const items = 50
	stub, itemIDs := benchmarkReadRuntime(items)
	server := benchmarkServer(stub)
	body := readBody(itemIDs)

	settle := func() uint64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}

	before := settle()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
			bytes.NewReader(body)))
		if response.Code != stdhttp.StatusOK {
			b.Fatalf("status = %d", response.Code)
		}
	}
	b.StopTimer()
	after := settle()

	// Signed, because a run that ends below where it started is a run that
	// retained nothing and happened to collect more than it began with.
	b.ReportMetric(float64(int64(after)-int64(before))/1024, "KiB-retained")
	b.ReportMetric(float64(int64(after)-int64(before))/float64(b.N), "B-retained/op")
}
