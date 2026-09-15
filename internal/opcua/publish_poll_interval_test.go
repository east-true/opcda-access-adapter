package opcua

import (
	"testing"
	"time"
)

// A waiting Publish looks for something to send at the shortest publishing
// interval the session asked for, because that is the fastest any of its
// subscriptions expects an answer. Choosing any other one delays the
// subscription that asked for the shortest by the difference -- a client
// polling at a hundred milliseconds alongside one polling at ten seconds would
// wait ten seconds for a notification it asked to receive promptly.
//
// The comparison that finds the shortest is a conjunction, and turning it into
// a disjunction leaves the last subscription examined winning instead of the
// fastest. Nothing had ever put two subscriptions of different intervals on one
// session, so the sweep found it removable.
func TestAPublishPollsAtTheShortestIntervalTheSessionAskedFor(t *testing.T) {
	runtime := &subscribingRuntime{}
	limits := DefaultSubscriptionLimits()
	limits.MinPublishingInterval = 50 * time.Millisecond
	limits.MaxPublishingInterval = 30 * time.Second
	service, _ := testSubscriptionServiceWithLimits(t, runtime, limits)

	create := func(milliseconds float64) {
		t.Helper()
		if _, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
			Header:                      RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
			RequestedPublishingInterval: milliseconds,
			RequestedMaxKeepAliveCount:  3,
			PublishingEnabled:           true,
		}, channelEpoch); err != nil {
			t.Fatal(err)
		}
	}

	// With nothing subscribed the poll falls back to the slowest the server
	// supports; there is nothing waiting for anything sooner.
	if got := service.publishPollInterval(testSession); got != limits.MaxPublishingInterval {
		t.Fatalf("an empty session polls at %v, want %v", got, limits.MaxPublishingInterval)
	}

	// The slow one first, so the fast one that follows has to displace it.
	create(10000)
	if got := service.publishPollInterval(testSession); got != 10*time.Second {
		t.Errorf("one subscription at ten seconds polls at %v", got)
	}

	create(100)
	if got := service.publishPollInterval(testSession); got != 100*time.Millisecond {
		t.Errorf("polls at %v, want the shortest interval on the session (100ms)", got)
	}

	// And the other order, so neither "the last one wins" nor "the first one
	// wins" can pass: a slow subscription created after a fast one does not
	// take the poll back.
	create(20000)
	if got := service.publishPollInterval(testSession); got != 100*time.Millisecond {
		t.Errorf("a slower subscription created later moved the poll to %v", got)
	}

	// Another session's subscriptions are not this session's business. The
	// loop skips them, and a poll that picked them up would have one client's
	// rate decided by another's.
	if _, err := service.CreateSubscription("another-session", CreateSubscriptionRequest{
		Header:                      RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		RequestedPublishingInterval: 50,
		RequestedMaxKeepAliveCount:  3,
		PublishingEnabled:           true,
	}, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if got := service.publishPollInterval(testSession); got != 100*time.Millisecond {
		t.Errorf("another session's faster subscription moved this one's poll to %v", got)
	}
}
