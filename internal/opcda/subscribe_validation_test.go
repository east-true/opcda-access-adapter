package opcda

import (
	"strings"
	"testing"
	"time"
)

// SubscribeRequest.validate is what a Subscribe is held to before any DA group
// exists, and every bound in it was unpinned: the item count, both ends of the
// update rate and both ends of the deadband all survived being loosened by one.
// What that means is that nothing said a request of exactly the documented
// shape is accepted -- only that something clearly outside it is refused.
//
// The update rate has a second rule beside its range. A DA group takes whole
// milliseconds, so a rate that is not one cannot be honoured, and asking the
// source for 1500 microseconds would get a group running at something else
// with the client never told.

func validSubscribe() SubscribeRequest {
	return SubscribeRequest{
		Items:               []DAItemID{"Test/Int32"},
		RequestedUpdateRate: time.Second,
		Deadband:            0,
	}
}

func TestSubscribeAcceptsTheWholeRangeOfItsParameters(t *testing.T) {
	limits := DefaultLimits()
	if err := validSubscribe().validate(limits); err != nil {
		t.Fatalf("the baseline request is invalid, so every case below proves nothing: %v", err)
	}

	for _, testCase := range []struct {
		name     string
		mutate   func(*SubscribeRequest)
		accepted bool
	}{
		{"the fastest rate", func(r *SubscribeRequest) { r.RequestedUpdateRate = minimumUpdateRate }, true},
		{"one below the fastest", func(r *SubscribeRequest) { r.RequestedUpdateRate = minimumUpdateRate - 1 }, false},
		{"the slowest rate", func(r *SubscribeRequest) { r.RequestedUpdateRate = maximumUpdateRate }, true},
		{"one past the slowest", func(r *SubscribeRequest) { r.RequestedUpdateRate = maximumUpdateRate + time.Millisecond }, false},
		// A DA group takes whole milliseconds and nothing finer.
		{"a rate that is not whole milliseconds", func(r *SubscribeRequest) {
			r.RequestedUpdateRate = 1500 * time.Microsecond
		}, false},
		{"no deadband", func(r *SubscribeRequest) { r.Deadband = 0 }, true},
		{"a full deadband", func(r *SubscribeRequest) { r.Deadband = 100 }, true},
		{"just past a full deadband", func(r *SubscribeRequest) { r.Deadband = 100.0001 }, false},
		{"a negative deadband", func(r *SubscribeRequest) { r.Deadband = -0.0001 }, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := validSubscribe()
			testCase.mutate(&request)
			err := request.validate(limits)
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}
}

func TestSubscribeHoldsItsItemsToTheirBounds(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxSubscriptionItems = 4
	limits.MaxItemIDBytes = 16

	items := func(n int) []DAItemID {
		out := make([]DAItemID, n)
		for i := range out {
			out[i] = DAItemID(strings.Repeat("i", i+1))
		}
		return out
	}

	atLimit := validSubscribe()
	atLimit.Items = items(limits.MaxSubscriptionItems)
	if err := atLimit.validate(limits); err != nil {
		t.Errorf("exactly %d items was refused: %v", limits.MaxSubscriptionItems, err)
	}
	overLimit := validSubscribe()
	overLimit.Items = items(limits.MaxSubscriptionItems + 1)
	if err := overLimit.validate(limits); err == nil {
		t.Errorf("%d items was accepted", limits.MaxSubscriptionItems+1)
	}

	for _, testCase := range []struct {
		name  string
		items []DAItemID
	}{
		{"no items at all", nil},
		{"an empty ItemID", []DAItemID{""}},
		{"an ItemID carrying a NUL", []DAItemID{"Channel\x00Device"}},
		{"an ItemID past the byte limit", []DAItemID{DAItemID(strings.Repeat("i", limits.MaxItemIDBytes+1))}},
		// A DA group cannot hold the same item twice, so a duplicate is a
		// request the source could not satisfy as asked.
		{"the same item twice", []DAItemID{"Test/Int32", "Test/Int32"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := validSubscribe()
			request.Items = testCase.items
			if err := request.validate(limits); err == nil {
				t.Errorf("%s was accepted", testCase.name)
			}
		})
	}

	// Exactly the byte limit is addressable, and a client was told so.
	full := validSubscribe()
	full.Items = []DAItemID{DAItemID(strings.Repeat("i", limits.MaxItemIDBytes))}
	if err := full.validate(limits); err != nil {
		t.Errorf("an ItemID of exactly %d bytes was refused: %v", limits.MaxItemIDBytes, err)
	}
}

// The registration cache is scoped to a connection generation, so a
// registration from an earlier one is not merely stale but wrong: the handle it
// carries belongs to a session the source has forgotten.
func TestTheRegistrationCacheIsScopedToItsGeneration(t *testing.T) {
	const generation = 7
	cache := newRegistrationCache(2, generation)

	if !cache.put(itemRegistration{ItemID: "A", Generation: generation}) {
		t.Fatal("a registration from the current generation was refused")
	}
	if _, ok := cache.get("A"); !ok {
		t.Fatal("a registration just stored was not found")
	}

	// A registration from another generation is neither stored nor served.
	if cache.put(itemRegistration{ItemID: "B", Generation: generation - 1}) {
		t.Error("a registration from an earlier generation was stored")
	}
	if _, ok := cache.get("B"); ok {
		t.Error("a registration from an earlier generation was served")
	}

	// And one that was stored under this generation stops being served when the
	// cache belongs to another, which is what makes a reconnect invalidate
	// handles rather than leave them to be reused.
	stale := newRegistrationCache(2, generation)
	stale.items["C"] = itemRegistration{ItemID: "C", Generation: generation - 1}
	if _, ok := stale.get("C"); ok {
		t.Error("a registration left over from an earlier generation was served")
	}
}

func TestTheRegistrationCacheStopsAtItsCapacity(t *testing.T) {
	const generation = 1
	cache := newRegistrationCache(2, generation)

	if !cache.put(itemRegistration{ItemID: "A", Generation: generation}) ||
		!cache.put(itemRegistration{ItemID: "B", Generation: generation}) {
		t.Fatal("the cache refused a registration before reaching its capacity")
	}
	if remaining := cache.remaining(); remaining != 0 {
		t.Errorf("a full cache reports %d remaining, want 0", remaining)
	}
	if cache.put(itemRegistration{ItemID: "C", Generation: generation}) {
		t.Error("a third registration was stored in a cache of two")
	}

	// Replacing an item already held is not a new entry, so it is allowed even
	// when the cache is full: refusing it would strand an item whose handle the
	// source has just reissued.
	if !cache.put(itemRegistration{ItemID: "A", Generation: generation, ServerHandle: 42}) {
		t.Error("replacing an item already held was refused by a full cache")
	}
	if registration, ok := cache.get("A"); !ok || registration.ServerHandle != 42 {
		t.Errorf("the replacement did not take: %+v (found=%v)", registration, ok)
	}
}

// Three survivors in this package are equivalent mutants rather than gaps,
// checked by mutating and running rather than by reading, and written down so
// the next sweep does not spend the time again:
//
//   - registrationCache.remaining's `remaining < 0` to `<=`: both arms answer
//     zero when the cache is exactly full, and remaining cannot go negative
//     because put refuses a new entry at capacity. The guard is defensive.
//   - LocalDetectionLimits.Validate's `MaxServers <= 0` and
//     `MaxProgIDCodeUnits <= 0` to `< 0`: withDefaults replaces a zero with the
//     default before the check runs, so only a negative ever reaches it and
//     both forms catch that. The ceilings on the next line are not equivalent
//     and are pinned in detection_ceiling_test.go.
//   - sortDetectedLocalServers' comparators to their inclusive forms: the
//     detected set has distinct CLSIDs, so a less function that also answers
//     true for equal keys produces the same order.
