package opcua

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A populated branch is reused for the refresh interval and re-browsed after
// it, and the value that decides is exactly the interval. Nothing had ever
// asked at that moment, so the comparison was free to move by one: a branch
// reused a moment too long is a client reading an address space the source has
// already changed, which is the thing the interval exists to bound.
//
// Time is a parameter here rather than a clock, so the boundary is exact and
// the case does not wait for it.
func TestABranchIsRebrowsedExactlyWhenItsRefreshIntervalIsUp(t *testing.T) {
	const interval = time.Minute
	runtime := newBrowsingRuntime()
	runtime.setEntries(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float")},
	})
	limits := DefaultPopulationLimits()
	limits.RefreshInterval = interval
	populator, _ := newTestPopulator(t, runtime, limits)

	start := time.Now()
	if err := populator.EnsureBranch(context.Background(), nil, start); err != nil {
		t.Fatalf("the first browse failed: %v", err)
	}
	if calls := runtime.calls.Load(); calls != 1 {
		t.Fatalf("the first EnsureBranch made %d browses, want one", calls)
	}

	// One instant short of the interval the branch is still fresh.
	if err := populator.EnsureBranch(context.Background(), nil, start.Add(interval-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if calls := runtime.calls.Load(); calls != 1 {
		t.Errorf("a branch inside its refresh interval was browsed again (%d calls)", calls)
	}

	// At the interval it is not.
	if err := populator.EnsureBranch(context.Background(), nil, start.Add(interval)); err != nil {
		t.Fatal(err)
	}
	if calls := runtime.calls.Load(); calls != 2 {
		t.Errorf("a branch whose refresh interval was up was not re-browsed (%d calls)", calls)
	}
}

// A branch node identifier is the prefix followed by a path. The prefix alone
// names no branch, and the length test that reaches the comparison has to
// admit an identifier exactly as long as the prefix for the empty remainder to
// be the thing that refuses it.
func TestABranchIdentifierNeedsMoreThanItsPrefix(t *testing.T) {
	branchID := func(identifier string) NodeID {
		return NodeID{Namespace: AdapterNamespaceIndex, Type: NodeIDTypeString, StringID: identifier}
	}
	for _, testCase := range []struct {
		identifier string
		path       []string
		isBranch   bool
	}{
		{"branch:Channel1", []string{"Channel1"}, true},
		{"branch:Channel1/Device1", []string{"Channel1", "Device1"}, true},
		// The prefix and nothing else.
		{"branch:", nil, false},
		// Shorter than the prefix, which is what the length test is for.
		{"branch", nil, false},
		{"bran", nil, false},
		{"", nil, false},
	} {
		t.Run(testCase.identifier, func(t *testing.T) {
			path, ok := PathForNode(branchID(testCase.identifier))
			if ok != testCase.isBranch {
				t.Fatalf("PathForNode(%q) reported a branch = %v, want %v",
					testCase.identifier, ok, testCase.isBranch)
			}
			if !testCase.isBranch {
				return
			}
			if strings.Join(path, "/") != strings.Join(testCase.path, "/") {
				t.Errorf("path = %v, want %v", path, testCase.path)
			}
		})
	}
}

// Four survivors elsewhere in this package are equivalent mutants or
// defensive checks rather than gaps, each checked by mutating and running:
//
//   - PathForNode's `len >= len(branchPrefix)` to `>`: an identifier that is
//     exactly the prefix is refused either way -- by the length test in one
//     form and by the empty remainder in the other.
//   - publishPollInterval's `candidate > 0` to `>=`: a zero publishing
//     interval would be adopted as the shortest and then raised again by the
//     clamp below it, and MinPublishingInterval is required to be positive.
//   - handleCloseChannel's NewDecoder check: the payload is a slice of a body
//     the transport has already bounded, so no message the listener accepts
//     can exceed the decoder's own limit. It is defence in depth.
//
// One is a genuine gap rather than an equivalent: the Publish path that
// declines to write a service fault because the connection is already gone
// needs the context cancelled between two statements, and the package has no
// seam for holding it there.
