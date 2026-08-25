package opcda

import (
	"testing"
	"time"
)

func subscriptionValue(itemID DAItemID, value any) SubscriptionValue {
	varType := VTI4
	return SubscriptionValue{
		ItemID:         itemID,
		VarType:        &varType,
		HRESULT:        0,
		HRESULTPresent: true,
		Value: &DAValue{
			ItemID:  itemID,
			VarType: varType,
			Value:   value,
		},
	}
}

func TestPendingUpdatesCoalescesPerItem(t *testing.T) {
	pending := newPendingUpdates(2)
	pending.merge([]uint32{1, 2}, []SubscriptionValue{
		subscriptionValue("A", int32(1)),
		subscriptionValue("B", int32(2)),
	})
	// A DA server reports only the latest cache value between update-rate
	// ticks, so a second notification for the same item must replace the first.
	pending.merge([]uint32{1}, []SubscriptionValue{subscriptionValue("A", int32(9))})

	if size := pending.size(); size != 2 {
		t.Fatalf("pending size = %d, want 2", size)
	}
	values := pending.drain()
	if len(values) != 2 {
		t.Fatalf("drained %d values, want 2", len(values))
	}
	if values[0].ItemID != "A" || values[1].ItemID != "B" {
		t.Fatalf("drain order = %q,%q, want A,B", values[0].ItemID, values[1].ItemID)
	}
	if got := values[0].Value.Value; got != int32(9) {
		t.Fatalf("coalesced value = %v, want 9", got)
	}
	if values := pending.drain(); values != nil {
		t.Fatalf("second drain returned %d values, want none", len(values))
	}
}

func TestPendingUpdatesKeepsFirstSeenOrder(t *testing.T) {
	pending := newPendingUpdates(3)
	pending.merge([]uint32{7, 3}, []SubscriptionValue{
		subscriptionValue("first", int32(1)),
		subscriptionValue("second", int32(2)),
	})
	pending.merge([]uint32{7}, []SubscriptionValue{subscriptionValue("first", int32(3))})
	pending.merge([]uint32{5}, []SubscriptionValue{subscriptionValue("third", int32(4))})

	values := pending.drain()
	want := []DAItemID{"first", "second", "third"}
	if len(values) != len(want) {
		t.Fatalf("drained %d values, want %d", len(values), len(want))
	}
	for index, itemID := range want {
		if values[index].ItemID != itemID {
			t.Fatalf("value %d = %q, want %q", index, values[index].ItemID, itemID)
		}
	}
}

func TestPendingUpdatesSignalsOnce(t *testing.T) {
	pending := newPendingUpdates(1)
	select {
	case <-pending.notify:
		t.Fatal("empty pending set signalled an update")
	default:
	}
	pending.merge([]uint32{1}, []SubscriptionValue{subscriptionValue("A", int32(1))})
	pending.merge([]uint32{1}, []SubscriptionValue{subscriptionValue("A", int32(2))})
	select {
	case <-pending.notify:
	default:
		t.Fatal("merge did not signal an update")
	}
	// The signal is an edge, not a count; coalescing must not queue signals.
	select {
	case <-pending.notify:
		t.Fatal("notify buffered more than one signal")
	default:
	}
}

func TestPendingUpdatesInvalidateDropsPendingValues(t *testing.T) {
	pending := newPendingUpdates(1)
	pending.merge([]uint32{1}, []SubscriptionValue{subscriptionValue("A", int32(1))})

	err := NewAdapterError(CodeSubscriptionInvalidated, "source disconnected")
	pending.invalidate(err)

	select {
	case <-pending.done:
	default:
		t.Fatal("invalidate did not close done")
	}
	// Values captured under a dead connection generation are never delivered.
	if values := pending.drain(); values != nil {
		t.Fatalf("drain after invalidate returned %d values, want none", len(values))
	}
	if pending.failure() != err {
		t.Fatalf("failure = %v, want %v", pending.failure(), err)
	}
	pending.merge([]uint32{2}, []SubscriptionValue{subscriptionValue("B", int32(2))})
	if values := pending.drain(); values != nil {
		t.Fatalf("merge after invalidate accepted %d values, want none", len(values))
	}
}

func TestPendingUpdatesInvalidateIsIdempotent(t *testing.T) {
	pending := newPendingUpdates(1)
	first := NewAdapterError(CodeSubscriptionInvalidated, "first")
	pending.invalidate(first)
	pending.invalidate(NewAdapterError(CodeSubscriptionInvalidated, "second"))
	if pending.failure() != first {
		t.Fatalf("failure = %v, want the first invalidation cause", pending.failure())
	}
}

func TestSubscribeRequestValidate(t *testing.T) {
	limits := DefaultLimits()
	valid := SubscribeRequest{Items: []DAItemID{"Random.Int2"}, RequestedUpdateRate: time.Second}
	if err := valid.validate(limits); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	cases := []struct {
		name    string
		request SubscribeRequest
		code    ErrorCode
	}{
		{"no items", SubscribeRequest{RequestedUpdateRate: time.Second}, CodeInvalidRequest},
		{"empty item", SubscribeRequest{Items: []DAItemID{""}, RequestedUpdateRate: time.Second}, CodeInvalidRequest},
		{"NUL item", SubscribeRequest{Items: []DAItemID{"a\x00b"}, RequestedUpdateRate: time.Second}, CodeInvalidRequest},
		{"duplicate item", SubscribeRequest{Items: []DAItemID{"A", "A"}, RequestedUpdateRate: time.Second}, CodeInvalidRequest},
		{"zero rate", SubscribeRequest{Items: []DAItemID{"A"}}, CodeInvalidRequest},
		{"sub-millisecond rate", SubscribeRequest{Items: []DAItemID{"A"}, RequestedUpdateRate: 1500 * time.Microsecond}, CodeInvalidRequest},
		{"rate too large", SubscribeRequest{Items: []DAItemID{"A"}, RequestedUpdateRate: 2 * time.Hour}, CodeInvalidRequest},
		{"negative deadband", SubscribeRequest{Items: []DAItemID{"A"}, RequestedUpdateRate: time.Second, Deadband: -1}, CodeInvalidRequest},
		{"deadband over 100", SubscribeRequest{Items: []DAItemID{"A"}, RequestedUpdateRate: time.Second, Deadband: 101}, CodeInvalidRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.request.validate(limits)
			adapterErr, ok := AsAdapterError(err)
			if !ok {
				t.Fatalf("error = %v, want an AdapterError", err)
			}
			if adapterErr.Code != testCase.code {
				t.Fatalf("code = %s, want %s", adapterErr.Code, testCase.code)
			}
		})
	}
}

func TestSubscribeRequestValidateItemLimits(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxSubscriptionItems = 2
	request := SubscribeRequest{Items: []DAItemID{"A", "B", "C"}, RequestedUpdateRate: time.Second}
	adapterErr, ok := AsAdapterError(request.validate(limits))
	if !ok || adapterErr.Code != CodeRequestLimitExceeded {
		t.Fatalf("error = %v, want %s", adapterErr, CodeRequestLimitExceeded)
	}

	limits = DefaultLimits()
	long := make([]byte, limits.MaxItemIDBytes+1)
	for index := range long {
		long[index] = 'x'
	}
	request = SubscribeRequest{Items: []DAItemID{DAItemID(long)}, RequestedUpdateRate: time.Second}
	adapterErr, ok = AsAdapterError(request.validate(limits))
	if !ok || adapterErr.Code != CodeItemIDTooLong {
		t.Fatalf("error = %v, want %s", adapterErr, CodeItemIDTooLong)
	}
}

func TestSubscriptionIDIsGenerationScoped(t *testing.T) {
	if id := subscriptionIDFor(3, 1); id != "sub-3-1" {
		t.Fatalf("id = %q, want sub-3-1", id)
	}
	// A new connection generation never reuses an identifier.
	if subscriptionIDFor(3, 1) == subscriptionIDFor(4, 1) {
		t.Fatal("identifiers collided across connection generations")
	}
}

func TestSubscriptionLimitsValidation(t *testing.T) {
	limits := DefaultLimits()
	if err := limits.ValidateForConfiguration(); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
	for _, mutate := range []func(*Limits){
		func(l *Limits) { l.MaxSubscriptions = 0 },
		func(l *Limits) { l.MaxSubscriptionItems = 0 },
		func(l *Limits) { l.MaxSubscriptions = 257 },
		func(l *Limits) { l.MaxSubscriptionItems = 10001 },
	} {
		candidate := DefaultLimits()
		mutate(&candidate)
		if err := candidate.ValidateForConfiguration(); err == nil {
			t.Fatalf("limits %+v were accepted", candidate)
		}
	}

	// The pending-set budget is bounded by subscriptions x items x BSTR units.
	candidate := DefaultLimits()
	candidate.MaxSubscriptions = 256
	candidate.MaxSubscriptionItems = 10000
	candidate.MaxBSTRCodeUnits = 1048576
	if err := candidate.ValidateForConfiguration(); err == nil {
		t.Fatal("unbounded subscription pending-value budget was accepted")
	}
}

// The DA thread merges while a frontend goroutine drains, so the pending set is
// the one structure crossing the owning-thread boundary.
func TestPendingUpdatesConcurrentMergeAndDrain(t *testing.T) {
	const producers = 4
	const perProducer = 500

	pending := newPendingUpdates(producers)
	done := make(chan struct{})
	var drained int

	go func() {
		defer close(done)
		for {
			select {
			case <-pending.done:
				return
			case <-pending.notify:
				drained += len(pending.drain())
			}
		}
	}()

	finished := make(chan struct{}, producers)
	for producer := 0; producer < producers; producer++ {
		handle := uint32(producer + 1)
		go func() {
			defer func() { finished <- struct{}{} }()
			for iteration := 0; iteration < perProducer; iteration++ {
				pending.merge(
					[]uint32{handle},
					[]SubscriptionValue{subscriptionValue(DAItemID("item"), int32(iteration))},
				)
			}
		}()
	}
	for producer := 0; producer < producers; producer++ {
		<-finished
	}
	pending.invalidate(NewAdapterError(CodeSubscriptionInvalidated, "test complete"))
	<-done

	// Coalescing makes the delivered count a lower bound, never a total.
	if drained > producers*perProducer {
		t.Fatalf("drained %d values, more than the %d merged", drained, producers*perProducer)
	}
}
