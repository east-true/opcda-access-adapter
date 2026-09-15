package opcua

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A deeper sweep of this package turned up eight more unpinned bounds. They
// are the same shape as the rest: each is a rule with a value that decides it,
// and every case that existed sat clearly on one side or the other.

// A requested publishing interval is clamped into the range the server
// supports, and both ends of that range are values a client may ask for. Table
// 82 makes a zero or negative request mean the fastest supported interval,
// which is the floor rather than an error.
func TestARevisedPublishingIntervalKeepsWhatItCan(t *testing.T) {
	limits := DefaultSubscriptionLimits()
	milliseconds := func(d time.Duration) float64 { return float64(d / time.Millisecond) }

	for _, testCase := range []struct {
		name      string
		requested float64
		want      float64
	}{
		{"exactly the fastest supported", milliseconds(limits.MinPublishingInterval), milliseconds(limits.MinPublishingInterval)},
		{"exactly the slowest supported", milliseconds(limits.MaxPublishingInterval), milliseconds(limits.MaxPublishingInterval)},
		{"faster than the fastest", milliseconds(limits.MinPublishingInterval) - 1, milliseconds(limits.MinPublishingInterval)},
		{"slower than the slowest", milliseconds(limits.MaxPublishingInterval) + 1, milliseconds(limits.MaxPublishingInterval)},
		// Table 82: zero or negative means the fastest supported interval.
		{"no interval at all", 0, milliseconds(limits.MinPublishingInterval)},
		{"a negative interval", -1, milliseconds(limits.MinPublishingInterval)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := limits.revisePublishingInterval(testCase.requested); got != testCase.want {
				t.Errorf("revisePublishingInterval(%v) = %v, want %v",
					testCase.requested, got, testCase.want)
			}
		})
	}

	// A value between the two ends is kept as asked, or every case above would
	// pass against a function that answered one bound for everything.
	middle := milliseconds(limits.MinPublishingInterval) + 1
	if middle < milliseconds(limits.MaxPublishingInterval) {
		if got := limits.revisePublishingInterval(middle); got != middle {
			t.Errorf("an interval inside the range was revised to %v, want %v", got, middle)
		}
	}
}

// Table 27 caps the picoseconds a timestamp may carry, and the cap is a value
// a source may legitimately report. Clamping it away would lose a hundredth of
// a nanosecond of a timestamp the adapter is required to preserve exactly.
func TestThePicosecondCapIsAValueThatSurvives(t *testing.T) {
	for _, testCase := range []struct {
		value uint16
		want  uint16
	}{
		{0, 0},
		{1, 1},
		{maxPicoseconds - 1, maxPicoseconds - 1},
		{maxPicoseconds, maxPicoseconds},
		{maxPicoseconds + 1, maxPicoseconds},
		{^uint16(0), maxPicoseconds},
	} {
		if got := clampPicoseconds(testCase.value); got != testCase.want {
			t.Errorf("clampPicoseconds(%d) = %d, want %d", testCase.value, got, testCase.want)
		}
	}
}

// The adapter encodes one kind of array Variant, and both halves of what makes
// it that kind have to hold: the Go value is a string slice and the Variant
// says so. Letting either stand alone would either write a slice under a type
// tag that does not describe it, or take the encoding path for a value that is
// not a string slice at all.
func TestAnArrayVariantMustBeTheKindItSaysItIs(t *testing.T) {
	encodes := func(value Variant) bool {
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		encoder.writeVariantArray(value)
		_, bytesErr := encoder.Bytes()
		return bytesErr == nil
	}

	if !encodes(Variant{Type: BuiltInString, IsArray: true, Value: []string{"a", "b"}}) {
		t.Fatal("a String array Variant was refused, so the cases below prove nothing")
	}
	if encodes(Variant{Type: BuiltInInt32, IsArray: true, Value: []string{"a"}}) {
		t.Error("a string slice announced as an Int32 array was encoded")
	}
	if encodes(Variant{Type: BuiltInString, IsArray: true, Value: []int32{1}}) {
		t.Error("an int32 slice announced as a String array was encoded")
	}
	if encodes(Variant{Type: BuiltInString, IsArray: true}) {
		t.Error("an array Variant carrying no value was encoded")
	}
}

// A continuation page ends the same way a first page does: an answer with
// exactly as many references left as the page holds comes back whole, with no
// further point for the client to return. 7.6 has the page size stay the one
// the original Browse asked for, so this bound is the client's number rather
// than the server's.
func TestAContinuationPageIsFullBeforeItContinuesAgain(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		items   int
		maximum int
		pages   int
	}{
		// With a maximum of two, the source folder's own type-definition
		// reference makes n items into n+1 references.
		{"a second page that exactly fills", 3, 2, 2},
		{"a second page that does not", 4, 2, 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			limits := DefaultBrowseLimits()
			limits.MaxReferencesPerNode = testCase.maximum
			service, space := testBrowseService(t, limits)

			entries := make([]opcda.BrowseEntry, 0, testCase.items)
			for index := 0; index < testCase.items; index++ {
				name := string(rune('a' + index))
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
			point := response.Results[0].ContinuationPoint
			if len(point) == 0 {
				t.Fatalf("the first page carried no continuation point")
			}
			seen, pages := len(response.Results[0].References), 1
			for len(point) != 0 {
				if pages > 8 {
					t.Fatal("the answer never finished")
				}
				next, nextErr := service.BrowseNext(testSession, BrowseNextRequest{
					Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
					ContinuationPoints: [][]byte{point},
				}, channelEpoch)
				if nextErr != nil {
					t.Fatal(nextErr)
				}
				if next.Results[0].StatusCode != StatusGood {
					t.Fatalf("continuation status = %s", next.Results[0].StatusCode.Hex())
				}
				seen += len(next.Results[0].References)
				point = next.Results[0].ContinuationPoint
				pages++
			}
			if pages != testCase.pages {
				t.Errorf("the answer took %d pages, want %d", pages, testCase.pages)
			}
			if seen != testCase.items+1 {
				t.Errorf("%d references were returned, want %d", seen, testCase.items+1)
			}
			// Every point was consumed, so none is left held.
			if held := service.ContinuationPointCount(); held != 0 {
				t.Errorf("%d continuation points are still held", held)
			}
		})
	}
}

// A connection that has gone quiet is closed at the read timeout, unless the
// server owes it a Publish response. The counter that decides is a count of
// outstanding Publishes, so comparing it the other way makes every connection
// look like one waiting on a subscription: a client that has gone away would
// be held for the keep-alive window instead of the read timeout, and the
// server would keep sockets for peers that are no longer there.
func TestAQuietConnectionOwedNothingIsClosedAtTheReadTimeout(t *testing.T) {
	config := testListenerConfig()
	config.ReadTimeout = 150 * time.Millisecond
	// Far longer than the read timeout, so the two deadlines cannot be
	// confused for one another.
	config.Subscriptions.MaxPublishingInterval = 10 * time.Second
	_, address := startTestListener(t, config)

	client := dialTestClient(t, address)
	client.hello()
	if _, err := client.openChannel(0, TokenRequestIssue, 1); err != nil {
		t.Fatal(err)
	}

	// Nothing is sent from here on. The server owes this connection nothing,
	// so the read timeout is what applies.
	if err := client.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := client.conn.Read(buffer); err == nil {
		t.Fatal("the server sent something to a connection that asked for nothing")
	} else if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Error("a quiet connection the server owed nothing was still open long past " +
			"the read timeout")
	}
}

// Three survivors here are the clamp pattern already recorded for the gRPC
// frontend's durationMilliseconds, and they are equivalent for the same
// reason: a clamp produces the same number at its own boundary whichever way
// the comparison is written. clampPicoseconds at exactly the cap returns the
// value in one form and the cap in the other, and revisePublishingInterval
// does the same at each end of its range. The cases above still pin the
// values, which is what a reader needs; the mutations are not gaps.
