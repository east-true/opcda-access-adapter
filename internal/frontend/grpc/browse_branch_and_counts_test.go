package grpcfrontend

import (
	"context"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A.3.1.2 lets a branch carry an ItemID and lets it not: a wrapper obtains one
// from GetItemID, so it may be present, and what the adapter must not do is
// invent one. The HTTP frontend has had that pair of cases since the browse
// validation went in; the gRPC frontend never sent a branch at all, so the
// check that reads the optional pointer was untested -- and inverting it, which
// dereferences the nil, survived.
func TestABrowsedBranchOverGRPCMayOmitItsItemID(t *testing.T) {
	branch := func(name string, itemID *opcda.DAItemID) opcda.BrowseEntry {
		return opcda.BrowseEntry{Kind: opcda.BrowseEntryBranch, Name: name, ItemID: itemID}
	}
	identifier := func(value string) *opcda.DAItemID {
		id := opcda.DAItemID(value)
		return &id
	}

	browseFor := func(t *testing.T, entries ...opcda.BrowseEntry) (*opcdav1.DABrowseResponse, error) {
		t.Helper()
		runtime := &testRuntime{browse: func(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
			return opcda.BrowseResult{Entries: entries}, nil
		}}
		server := New(runtime, Config{MaxBrowseDepth: 4, MaxBrowseEntries: 8, MaxItemIDBytes: 32})
		return server.Browse(context.Background(), &opcdav1.DABrowseRequest{})
	}

	t.Run("a branch without one is relayed and says so", func(t *testing.T) {
		response, err := browseFor(t, branch("Channel1", nil))
		if err != nil {
			t.Fatalf("a branch without an ItemID was refused: %v", err)
		}
		entry := response.Entries[0]
		if entry.ItemIdPresent {
			t.Error("a branch with no ItemID was relayed as having one")
		}
		if entry.ItemId != "" {
			t.Errorf("a branch with no ItemID carried %q", entry.ItemId)
		}
	})

	t.Run("a branch with one carries it", func(t *testing.T) {
		response, err := browseFor(t, branch("Channel1", identifier("Channel1")))
		if err != nil {
			t.Fatalf("a branch carrying an ItemID was refused: %v", err)
		}
		entry := response.Entries[0]
		if !entry.ItemIdPresent || entry.ItemId != "Channel1" {
			t.Errorf("the branch ItemID was relayed as %q (present=%v)", entry.ItemId, entry.ItemIdPresent)
		}
	})

	t.Run("a branch with a malformed one is not relayed", func(t *testing.T) {
		if _, err := browseFor(t, branch("Channel1", identifier("Channel1\x00Device"))); err == nil {
			t.Error("a branch ItemID carrying a NUL was relayed")
		}
	})
}

// A subscription reports how many of the items asked for are active. The count
// comes from the source, and a client uses it to decide whether what it asked
// for is what it got, so a count outside the range the request defines is the
// source and the adapter disagreeing rather than a number to pass on. Both ends
// of the check were removable: with &&, only a count that was simultaneously
// negative and too large would have been caught, which is no count at all.
func TestAnActiveItemCountMustFitTheSubscriptionItDescribes(t *testing.T) {
	items := []opcda.SubscriptionItemStatus{{ItemID: "Test/A"}, {ItemID: "Test/B"}}
	for _, testCase := range []struct {
		name     string
		count    int
		accepted bool
	}{
		{"none of them active", 0, true},
		{"some of them active", 1, true},
		{"all of them active", 2, true},
		{"more active than were asked for", 3, false},
		{"a negative count", -1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := encodeSubscriptionCreated(opcda.SubscriptionInfo{
				ID:              "s1",
				Items:           items,
				ActiveItemCount: testCase.count,
			}, 64)
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}
}

// The gRPC server fills in a default for every bound left unset, and each
// default sits behind its own check. A check that fired for a value the
// operator did set would silently replace it, and the configuration an
// operator can see would no longer be the one the server runs.
func TestAnExplicitGRPCBoundIsNotReplacedByItsDefault(t *testing.T) {
	explicit := Config{
		MaxConcurrent:          1,
		MaxConcurrentStream:    1,
		MaxReceiveBytes:        1,
		MaxSendBytes:           1,
		MaxMetadataBytes:       1,
		ConnectionTimeout:      time.Nanosecond,
		MaxConnectionIdle:      time.Nanosecond,
		MaxConnectionAge:       time.Nanosecond,
		MaxConnectionGrace:     time.Nanosecond,
		KeepaliveMinTime:       time.Nanosecond,
		RequestDeadline:        time.Nanosecond,
		MaxReadItems:           1,
		MaxWriteItems:          1,
		MaxBrowseEntries:       1,
		MaxBrowseDepth:         1,
		MaxItemIDBytes:         1,
		MaxItemProperties:      1,
		MaxSubscribeItems:      1,
		MaxSubscriptionStreams: 1,
	}
	server := New(&testRuntime{}, explicit)
	if got := server.config; got != explicit {
		t.Errorf("the server runs a configuration the operator did not set:\ngot  %+v\nwant %+v",
			got, explicit)
	}

	// And a bound left at zero does get a default, or the checks would be
	// doing nothing at all.
	defaulted := New(&testRuntime{}, Config{}).config
	if defaulted.MaxConcurrent == 0 || defaulted.MaxConcurrentStream == 0 ||
		defaulted.ConnectionTimeout == 0 || defaulted.MaxConnectionIdle == 0 {
		t.Errorf("an unset configuration was left unfilled: %+v", defaulted)
	}
}
