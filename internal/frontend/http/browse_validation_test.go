package http

import (
	"bytes"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// browseResultMatchesRequest decides whether a Browse result the runtime
// returned may be relayed. Each entry's ItemID is checked by a disjunction of
// four conditions, and the sweep found them removable: no case had ever
// violated one on its own, so nothing said any of the four carried weight.
//
// What the checks protect is the adapter's central promise. An ItemID relayed
// from here is what a client will send back in a Read, so an empty one, one
// carrying invalid UTF-8, or one past the configured length would hand a client
// an address it cannot use -- and a NUL would hand it one that means something
// different to the COM layer than it reads as here.
//
// A.3.1.2 lets a branch carry an ItemID too, so the branch arm holds it to the
// same shape when it is present and accepts its absence. That asymmetry is
// tested rather than assumed: an item without one is refused, a branch without
// one is not.

const testItemIDBytes = 32

func browseEntry(kind opcda.BrowseEntryKind, name string, itemID *opcda.DAItemID) opcda.BrowseEntry {
	return opcda.BrowseEntry{Kind: kind, Name: name, ItemID: itemID}
}

func itemIDPointer(value string) *opcda.DAItemID {
	id := opcda.DAItemID(value)
	return &id
}

func TestABrowseItemNeedsEveryPartOfItsItemID(t *testing.T) {
	relayed := func(entry opcda.BrowseEntry) bool {
		return browseResultMatchesRequest(nil,
			opcda.BrowseResult{Entries: []opcda.BrowseEntry{entry}}, testItemIDBytes)
	}

	// The control. Without it every case below would pass against a check that
	// refused everything.
	if !relayed(browseEntry(opcda.BrowseEntryItem, "Ok", itemIDPointer("Channel1.Device1.Tag"))) {
		t.Fatal("a well-formed item entry was refused, so the cases below prove nothing")
	}

	for _, testCase := range []struct {
		name   string
		itemID *opcda.DAItemID
	}{
		{"no ItemID at all", nil},
		{"an empty ItemID", itemIDPointer("")},
		{"an ItemID carrying invalid UTF-8", itemIDPointer(string([]byte{'A', 0x80}))},
		{"an ItemID past the byte limit", itemIDPointer(strings.Repeat("i", testItemIDBytes+1))},
		{"an ItemID carrying a NUL", itemIDPointer("Channel1\x00Device1")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if relayed(browseEntry(opcda.BrowseEntryItem, "Name", testCase.itemID)) {
				t.Errorf("an item with %s was relayed", testCase.name)
			}
		})
	}

	// Exactly the limit is the value that decides the answer, and it must be
	// relayed: a client was told that length is addressable.
	// Filled rather than zeroed: a run of NUL bytes would trip a different
	// check and this case would then be testing that one instead.
	full := make([]byte, testItemIDBytes)
	for i := range full {
		full[i] = 'i'
	}
	atLimit := itemIDPointer(string(full))
	if !relayed(browseEntry(opcda.BrowseEntryItem, "Name", atLimit)) {
		t.Errorf("an ItemID of exactly %d bytes was not relayed", testItemIDBytes)
	}
}

// A branch may carry an ItemID or not, and the difference from an item is the
// point: A.3.1.2 has a wrapper obtain a branch's ItemID from GetItemID, so one
// may be present, and what the adapter must not do is invent one.
func TestABrowseBranchMayOmitItsItemIDButNotMalformIt(t *testing.T) {
	relayed := func(entry opcda.BrowseEntry) bool {
		return browseResultMatchesRequest(nil,
			opcda.BrowseResult{Entries: []opcda.BrowseEntry{entry}}, testItemIDBytes)
	}

	if !relayed(browseEntry(opcda.BrowseEntryBranch, "Channel1", nil)) {
		t.Error("a branch without an ItemID was refused; the source is allowed not to give one")
	}
	if !relayed(browseEntry(opcda.BrowseEntryBranch, "Channel1", itemIDPointer("Channel1"))) {
		t.Error("a branch carrying a well-formed ItemID was refused")
	}
	// The branch arm has its own copy of the byte limit, so it needs its own
	// case at exactly that length: without one, only the item arm's boundary is
	// pinned and this one is free to move.
	if !relayed(browseEntry(opcda.BrowseEntryBranch, "Channel1",
		itemIDPointer(strings.Repeat("b", testItemIDBytes)))) {
		t.Errorf("a branch ItemID of exactly %d bytes was not relayed", testItemIDBytes)
	}
	for _, testCase := range []struct {
		name   string
		itemID *opcda.DAItemID
	}{
		{"an empty ItemID", itemIDPointer("")},
		{"an ItemID carrying invalid UTF-8", itemIDPointer(string([]byte{'A', 0x80}))},
		{"an ItemID past the byte limit", itemIDPointer(strings.Repeat("i", testItemIDBytes+1))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if relayed(browseEntry(opcda.BrowseEntryBranch, "Channel1", testCase.itemID)) {
				t.Errorf("a branch with %s was relayed", testCase.name)
			}
		})
	}
}

// A Browse request omitting the filter means "all", and a path of exactly the
// configured depth is navigable. Neither was pinned: the default's check
// survived inversion, and the depth bound survived being loosened by one, so
// nothing said a client may use the whole depth it was told about.
func TestABrowseRequestDefaultsItsFilterAndUsesItsWholeDepth(t *testing.T) {
	// The frontend refuses to relay a result whose path is not the one asked
	// for, so the stub echoes it. Without that the depth case never reaches the
	// bound it is about and fails on identity instead.
	browseWith := func(body string, path ...string) (*httptest.ResponseRecorder, *browseRuntime) {
		runtime := &browseRuntime{result: opcda.BrowseResult{Path: path}}
		server := newBrowseTestServer(runtime)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/browse",
			bytes.NewReader([]byte(body))))
		return response, runtime
	}

	t.Run("an omitted filter is all", func(t *testing.T) {
		response, runtime := browseWith(`{"path":[]}`)
		if response.Code != stdhttp.StatusOK {
			t.Fatalf("status = %d: %s", response.Code, response.Body.String())
		}
		if runtime.request.Filter != opcda.BrowseFilterAll {
			t.Errorf("an omitted filter reached the runtime as %q, want %q",
				runtime.request.Filter, opcda.BrowseFilterAll)
		}
	})

	t.Run("an explicit filter is carried", func(t *testing.T) {
		_, runtime := browseWith(`{"path":[],"filter":"branch"}`)
		if runtime.request.Filter != opcda.BrowseFilterBranch {
			t.Errorf("filter reached the runtime as %q, want branch", runtime.request.Filter)
		}
	})

	// newBrowseTestServer's depth, read from the server rather than repeated,
	// so this cannot drift from the configuration the helper builds.
	depth := newBrowseTestServer(&browseRuntime{}).config.MaxBrowseDepth
	segments := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"Branch"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}

	t.Run("a path of exactly the configured depth", func(t *testing.T) {
		deep := make([]string, depth)
		for i := range deep {
			deep[i] = "Branch"
		}
		response, runtime := browseWith(fmt.Sprintf(`{"path":%s}`, segments(depth)), deep...)
		if response.Code != stdhttp.StatusOK {
			t.Fatalf("a path of exactly %d segments was refused: %d %s",
				depth, response.Code, response.Body.String())
		}
		if len(runtime.request.Path) != depth {
			t.Errorf("the runtime received %d segments, want %d", len(runtime.request.Path), depth)
		}
	})

	t.Run("a path one segment deeper", func(t *testing.T) {
		response, _ := browseWith(fmt.Sprintf(`{"path":%s}`, segments(depth+1)))
		if response.Code == stdhttp.StatusOK {
			t.Errorf("a path of %d segments was accepted", depth+1)
		}
	})
}
