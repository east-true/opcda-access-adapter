package opcua

import (
	"context"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// daRevisedInterval reports the rate the DA server settled on, and that rate is
// a floor for every monitored item: the group cannot sample faster than it, so
// no item is given anything quicker however fast it asked.
//
// A source that reports no revised rate has not reported a floor of zero. Zero
// is the absence of an answer, and treating it as one would tell every item it
// may sample as fast as it likes -- which is the adapter inventing a capability
// the source never claimed. The subscription's own publishing interval is the
// best answer available, and it is what the guard falls back to.
func TestARevisedRateOfZeroIsNoAnswerRatherThanNoFloor(t *testing.T) {
	const publishing = 500.0

	for _, testCase := range []struct {
		name    string
		revised time.Duration
		want    float64
	}{
		{"a rate the source revised", 250 * time.Millisecond, 250},
		{"the smallest rate a source can report", time.Millisecond, 1},
		{"no rate at all", 0, publishing},
		// A negative rate is not a rate either, and a source that reports one
		// has said something about itself that cannot be true.
		{"a negative rate", -time.Second, publishing},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			da := newFakeDASubscription("s1")
			da.info.RevisedUpdateRate = testCase.revised
			subscription := &uaSubscription{
				publishingInterval: publishing,
				da:                 da,
			}
			if got := subscription.daRevisedInterval(); got != testCase.want {
				t.Errorf("daRevisedInterval = %v, want %v", got, testCase.want)
			}
		})
	}

	// Before a group exists there is no source rate to report, and the
	// subscription's own interval is again the answer.
	withoutGroup := &uaSubscription{publishingInterval: publishing}
	if got := withoutGroup.daRevisedInterval(); got != publishing {
		t.Errorf("a subscription with no DA group reported %v, want %v", got, publishing)
	}
}

// Two survivors around this one are equivalent mutants, checked by mutating and
// running rather than by reading. Both are assignments that only fire when the
// new value differs, so at the boundary each arm produces the same number:
//
//   - the sampling floor's `node.MinimumSamplingInterval > interval` to `>=`:
//     at equal, assigning the minimum assigns what is already there.
//   - the notification batch's `count > maximum` to `>=`: at equal, clamping
//     to the maximum clamps to the count.

// A Read that names no DA item does not reach the source. Every attribute it
// asks for is answered from the address space, and calling the source with an
// empty batch would open a COM conversation to ask nothing -- on the runtime's
// owning thread, where it would queue behind and ahead of work that has
// something to do.
//
// The same holds for a Write whose items were all refused before the source was
// reached. Nothing about the answers changes either way, which is exactly why
// it was removable.
func TestNothingToAskMeansTheSourceIsNotAsked(t *testing.T) {
	t.Run("a Read of attributes the address space answers", func(t *testing.T) {
		runtime := &stubRuntime{}
		service, _ := testDataService(t, runtime)
		node := ItemNodeID("Test/Int32")
		response, err := service.Read(context.Background(), readRequestFor(
			ReadValueID{NodeID: node, AttributeID: AttributeDisplayName},
			ReadValueID{NodeID: node, AttributeID: AttributeDataType},
		), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 2 {
			t.Fatalf("the response carried %d results", len(response.Results))
		}
		if runtime.readCalls != 0 {
			t.Errorf("the source was read %d times for a request that named no item",
				runtime.readCalls)
		}
	})

	t.Run("a Read that does name an item", func(t *testing.T) {
		// The control: without it a service that never read the source would
		// pass the case above.
		runtime := &stubRuntime{readResults: []opcda.ReadResult{{
			ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true,
		}}}
		service, _ := testDataService(t, runtime)
		if _, err := service.Read(context.Background(),
			readRequestFor(readValue(ItemNodeID("Test/Int32"))), time.Now()); err != nil {
			t.Fatal(err)
		}
		if runtime.readCalls != 1 {
			t.Errorf("the source was read %d times for a request that named an item",
				runtime.readCalls)
		}
	})

	t.Run("a Write whose items were all refused first", func(t *testing.T) {
		runtime := &stubRuntime{}
		service, _ := testDataService(t, runtime)
		// A node that is not a DA item has nothing to write to the source.
		response, err := service.Write(context.Background(), WriteRequest{
			Header: RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
			NodesToWrite: []WriteValue{{
				NodeID:      NumericNodeID(0, NodeIDServer),
				AttributeID: AttributeValue,
				Value:       DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
			}},
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 1 || !response.Results[0].IsBad() {
			t.Fatalf("results = %v", response.Results)
		}
		if runtime.writeCalls != 0 {
			t.Errorf("the source was written %d times for a request it never reached",
				runtime.writeCalls)
		}
	})
}
