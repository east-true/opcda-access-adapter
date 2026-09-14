package grpcfrontend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Every bound in this frontend says what a client may ask for, and the gRPC
// reference publishes each one. The mutation sweep found none of them pinned at
// the value that decides the answer: loosening any comparison by one survived.
// What that means for a client is that nothing in this repository said a
// request of exactly the documented size is accepted -- only that something
// clearly larger is refused.
//
// Both directions matter. Loosened by one, the frontend admits a request the
// published limit says it will not; tightened by one, it refuses a request the
// client was told it could make, and the client has no way to tell that from a
// bug of its own.

func boundedServer(runtime opcda.Runtime, config Config) *Server { return New(runtime, config) }

func TestABrowseAtEachBoundIsAccepted(t *testing.T) {
	const depth = 4
	runtime := &testRuntime{browse: func(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
		// The frontend checks that the runtime answered the path it was asked
		// for, so the stub has to echo it or the bound is never reached.
		return opcda.BrowseResult{Path: request.Path}, nil
	}}
	server := boundedServer(runtime, Config{MaxBrowseDepth: depth, MaxBrowseEntries: 8, MaxItemIDBytes: 64})

	path := func(n int) []string {
		segments := make([]string, n)
		for i := range segments {
			segments[i] = "Branch"
		}
		return segments
	}

	if _, err := server.Browse(context.Background(), &opcdav1.DABrowseRequest{Path: path(depth)}); err != nil {
		t.Errorf("a Browse path of exactly %d segments was refused: %v", depth, err)
	}
	if _, err := server.Browse(context.Background(), &opcdav1.DABrowseRequest{Path: path(depth + 1)}); err == nil {
		t.Errorf("a Browse path of %d segments was accepted", depth+1)
	}
}

// The entry bound is on what the source returns rather than on what the client
// asked for, so exceeding it is the adapter refusing to relay a result it
// cannot bound rather than refusing the request.
func TestABrowseResultAtTheEntryBoundIsRelayed(t *testing.T) {
	const maximum = 4
	entries := func(n int) []opcda.BrowseEntry {
		out := make([]opcda.BrowseEntry, n)
		for i := range out {
			// An item entry has to carry its exact ItemID; the frontend refuses
			// to relay one that does not.
			itemID := opcda.DAItemID(fmt.Sprintf("Item%d", i))
			out[i] = opcda.BrowseEntry{
				Name: string(itemID), Kind: opcda.BrowseEntryItem, ItemID: &itemID,
			}
		}
		return out
	}
	for _, testCase := range []struct {
		count    int
		accepted bool
	}{{maximum, true}, {maximum + 1, false}} {
		t.Run(fmt.Sprintf("%d entries", testCase.count), func(t *testing.T) {
			runtime := &testRuntime{browse: func(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
				return opcda.BrowseResult{Path: request.Path, Entries: entries(testCase.count)}, nil
			}}
			server := boundedServer(runtime, Config{MaxBrowseDepth: 4, MaxBrowseEntries: maximum, MaxItemIDBytes: 64})
			_, err := server.Browse(context.Background(), &opcdav1.DABrowseRequest{})
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("%d entries: accepted = %v, want %v (%v)", testCase.count, accepted, testCase.accepted, err)
			}
		})
	}
}

func TestABatchAtEachItemBoundIsAccepted(t *testing.T) {
	const limit = 3
	readItems := func(n int) []*opcdav1.DAReadItem {
		out := make([]*opcdav1.DAReadItem, n)
		for i := range out {
			out[i] = &opcdav1.DAReadItem{ItemId: fmt.Sprintf("Item%d", i)}
		}
		return out
	}
	writeItems := func(n int) []*opcdav1.DAWriteItem {
		out := make([]*opcdav1.DAWriteItem, n)
		for i := range out {
			out[i] = &opcdav1.DAWriteItem{
				ItemId:   fmt.Sprintf("Item%d", i),
				DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI4), Name: opcda.VTI4.String()},
				Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}},
			}
		}
		return out
	}

	t.Run("Read", func(t *testing.T) {
		runtime := &testRuntime{read: func(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
			results := make([]opcda.ReadResult, len(request.Items))
			for i, id := range request.Items {
				results[i] = opcda.ReadResult{ItemID: id, HRESULT: opcda.HRESULT(-1073479673), HRESULTPresent: true}
			}
			return results, nil
		}}
		server := boundedServer(runtime, Config{MaxReadItems: limit, MaxItemIDBytes: 64})
		if _, err := server.Read(context.Background(), &opcdav1.DAReadRequest{Items: readItems(limit)}); err != nil {
			t.Errorf("a Read of exactly %d items was refused: %v", limit, err)
		}
		if _, err := server.Read(context.Background(), &opcdav1.DAReadRequest{Items: readItems(limit + 1)}); err == nil {
			t.Errorf("a Read of %d items was accepted", limit+1)
		}
	})

	t.Run("Write", func(t *testing.T) {
		runtime := &testRuntime{status: opcda.RuntimeStatus{WriteEnabled: true},
			write: func(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
				results := make([]opcda.WriteResult, len(items))
				for i, item := range items {
					results[i] = opcda.WriteResult{ItemID: item.ItemID, HRESULT: opcda.SOK, HRESULTPresent: true}
				}
				return results, nil
			}}
		server := boundedServer(runtime, Config{MaxWriteItems: limit, MaxItemIDBytes: 64})
		if _, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{Items: writeItems(limit)}); err != nil {
			t.Errorf("a Write of exactly %d items was refused: %v", limit, err)
		}
		if _, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{Items: writeItems(limit + 1)}); err == nil {
			t.Errorf("a Write of %d items was accepted", limit+1)
		}
	})
}

func TestAnItemIDAtTheByteBoundIsAccepted(t *testing.T) {
	const limit = 16
	runtime := &testRuntime{read: func(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
		results := make([]opcda.ReadResult, len(request.Items))
		for i, id := range request.Items {
			results[i] = opcda.ReadResult{ItemID: id, HRESULT: opcda.HRESULT(-1073479673), HRESULTPresent: true}
		}
		return results, nil
	}}
	server := boundedServer(runtime, Config{MaxReadItems: 4, MaxItemIDBytes: limit})

	read := func(itemID string) error {
		_, err := server.Read(context.Background(),
			&opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: itemID}}})
		return err
	}
	if err := read(strings.Repeat("i", limit)); err != nil {
		t.Errorf("an ItemID of exactly %d bytes was refused: %v", limit, err)
	}
	if err := read(strings.Repeat("i", limit+1)); err == nil {
		t.Errorf("an ItemID of %d bytes was accepted", limit+1)
	}
	// The bound counts bytes. Six three-byte characters are eighteen bytes and
	// only six characters, so a rune-based length would admit them.
	if err := read(strings.Repeat("가", 6)); err == nil {
		t.Error("18 bytes in 6 characters was accepted against a 16 byte bound")
	}
}

// Subscribe carries three numeric parameters a client chooses, and the gRPC
// reference publishes the range of each. All three were unpinned at both ends.
func TestSubscribeParametersAcceptTheirWholeRange(t *testing.T) {
	valid := func() *opcdav1.DASubscribeRequest {
		return &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: "Test/Int32"}},
			RequestedUpdateRateMs: 1000,
			PercentDeadband:       0,
		}
	}
	server := boundedServer(&testRuntime{}, Config{MaxSubscribeItems: 4, MaxItemIDBytes: 64})

	for _, testCase := range []struct {
		name     string
		mutate   func(*opcdav1.DASubscribeRequest)
		accepted bool
	}{
		{"the fastest update rate", func(r *opcdav1.DASubscribeRequest) { r.RequestedUpdateRateMs = 1 }, true},
		{"one faster than the fastest", func(r *opcdav1.DASubscribeRequest) { r.RequestedUpdateRateMs = 0 }, false},
		{"the slowest update rate", func(r *opcdav1.DASubscribeRequest) { r.RequestedUpdateRateMs = 3_600_000 }, true},
		{"one slower than the slowest", func(r *opcdav1.DASubscribeRequest) { r.RequestedUpdateRateMs = 3_600_001 }, false},
		{"no deadband", func(r *opcdav1.DASubscribeRequest) { r.PercentDeadband = 0 }, true},
		{"a full deadband", func(r *opcdav1.DASubscribeRequest) { r.PercentDeadband = 100 }, true},
		{"just over a full deadband", func(r *opcdav1.DASubscribeRequest) { r.PercentDeadband = 100.0001 }, false},
		{"a negative deadband", func(r *opcdav1.DASubscribeRequest) { r.PercentDeadband = -0.0001 }, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := valid()
			testCase.mutate(request)
			_, err := server.decodeSubscribeRequest(request)
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}

	t.Run("items at the bound", func(t *testing.T) {
		items := func(n int) []*opcdav1.DASubscribeItem {
			out := make([]*opcdav1.DASubscribeItem, n)
			for i := range out {
				out[i] = &opcdav1.DASubscribeItem{ItemId: fmt.Sprintf("Item%d", i)}
			}
			return out
		}
		request := valid()
		request.Items = items(4)
		if _, err := server.decodeSubscribeRequest(request); err != nil {
			t.Errorf("exactly 4 items was refused: %v", err)
		}
		request.Items = items(5)
		if _, err := server.decodeSubscribeRequest(request); err == nil {
			t.Error("5 items was accepted against a bound of 4")
		}
	})
}
