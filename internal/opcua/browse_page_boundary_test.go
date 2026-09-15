package opcua

import (
	"context"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A Browse answer either fits in one page or it does not, and the value that
// decides is exactly the maximum. The existing case pages through five
// references with a maximum of two, which never visits the boundary: nothing
// said a node with exactly as many references as the page holds comes back
// whole and with no continuation point.
//
// Both sides of that matter to a client. A continuation point handed out for
// an answer that was already complete is a point the client must return and
// the server must hold, for nothing; a page one reference short would drop a
// reference or hand back a point the client has no reason to expect.
func TestABrowsePageIsFullBeforeItContinues(t *testing.T) {
	// The source folder carries one reference of its own -- its type
	// definition -- so a branch of n items browses as n+1 references. The
	// cases are written in those terms rather than in item counts, because the
	// bound is about the answer rather than about the source.
	for _, testCase := range []struct {
		name       string
		items      []string
		maximum    int
		references int
		continues  bool
	}{
		{"one short of a full page", []string{"a"}, 3, 2, false},
		{"exactly a full page", []string{"a", "b"}, 3, 3, false},
		{"one past a full page", []string{"a", "b", "c"}, 3, 3, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			limits := DefaultBrowseLimits()
			limits.MaxReferencesPerNode = testCase.maximum
			service, space := testBrowseService(t, limits)

			entries := make([]opcda.BrowseEntry, 0, len(testCase.items))
			for _, name := range testCase.items {
				entries = append(entries, opcda.BrowseEntry{
					Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
				})
			}
			if err := space.PopulateBranch(nil, entries); err != nil {
				t.Fatal(err)
			}

			response, err := service.Browse(context.Background(), testSession,
				browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			result := response.Results[0]
			if result.StatusCode != StatusGood {
				t.Fatalf("status = %s", result.StatusCode.Hex())
			}
			if len(result.References) != testCase.references {
				t.Errorf("the page carried %d references, want %d",
					len(result.References), testCase.references)
			}
			if continues := len(result.ContinuationPoint) != 0; continues != testCase.continues {
				t.Errorf("a continuation point was issued = %v, want %v", continues, testCase.continues)
			}
			// A point that was not issued is not one the server is holding.
			wantHeld := 0
			if testCase.continues {
				wantHeld = 1
			}
			if held := service.ContinuationPointCount(); held != wantHeld {
				t.Errorf("%d continuation points are held, want %d", held, wantHeld)
			}
		})
	}
}
