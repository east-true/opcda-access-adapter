package opcua

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// fakeDASubscription mirrors the DA core's Subscription contract.
type fakeDASubscription struct {
	info opcda.SubscriptionInfo

	mu      sync.Mutex
	pending []opcda.SubscriptionValue
	closed  bool
	done    chan struct{}
}

func newFakeDASubscription(id opcda.SubscriptionID) *fakeDASubscription {
	return &fakeDASubscription{
		info: opcda.SubscriptionInfo{ID: id, RevisedUpdateRate: 250 * time.Millisecond},
		done: make(chan struct{}),
	}
}

func (f *fakeDASubscription) Info() opcda.SubscriptionInfo { return f.info }
func (f *fakeDASubscription) Updates() <-chan struct{}     { return nil }
func (f *fakeDASubscription) Done() <-chan struct{}        { return f.done }
func (f *fakeDASubscription) Err() error                   { return nil }
func (f *fakeDASubscription) Drain() []opcda.SubscriptionValue {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := f.pending
	f.pending = nil
	return values
}

// push coalesces per ItemID, because opcda.Subscription says its pending set
// does: "between two update-rate ticks a server reports only the latest cache
// value, so the adapter coalesces per item in the same way", and a Drain
// "removes and returns the whole pending set" -- a set with one entry per item.
// A fake that queued every push instead would let a test assert on a batch no
// DA source can produce, and the assertion would pass forever without ever
// having been true.
func (f *fakeDASubscription) push(values ...opcda.SubscriptionValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, value := range values {
		replaced := false
		for index, existing := range f.pending {
			if existing.ItemID == value.ItemID {
				// A handle already pending keeps its first-seen position and
				// takes the newer tuple.
				f.pending[index] = value
				replaced = true
				break
			}
		}
		if !replaced {
			f.pending = append(f.pending, value)
		}
	}
}
func (f *fakeDASubscription) invalidate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.done)
	}
}

// subscribingRuntime records DA subscribe and unsubscribe calls.
type subscribingRuntime struct {
	stubRuntime

	mu            sync.Mutex
	subscriptions []*fakeDASubscription
	requests      []opcda.SubscribeRequest
	unsubscribed  []opcda.SubscriptionID
	err           error
	nextID        int
	// revisedRate is what the source says it settled on, which a vendor may
	// place far from the requested rate.
	revisedRate time.Duration
	// unsubscribeDelay makes releasing a DA group take measurable time, the
	// way a COM call on a real server does, so a test can tell a caller that
	// waited for it from one that did not.
	unsubscribeDelay time.Duration
}

func (r *subscribingRuntime) Subscribe(_ context.Context, request opcda.SubscribeRequest) (opcda.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if r.err != nil {
		return nil, r.err
	}
	r.nextID++
	created := newFakeDASubscription(opcda.SubscriptionID(string(rune('a' + r.nextID))))
	if r.revisedRate > 0 {
		created.info.RevisedUpdateRate = r.revisedRate
	}
	r.subscriptions = append(r.subscriptions, created)
	return created, nil
}

func (r *subscribingRuntime) Unsubscribe(_ context.Context, id opcda.SubscriptionID) error {
	r.mu.Lock()
	delay := r.unsubscribeDelay
	r.mu.Unlock()
	if delay > 0 {
		// Releasing a DA group is a COM call on a real server, so it is not
		// instantaneous. The delay is taken outside the lock, as the real one
		// is taken outside this runtime's.
		time.Sleep(delay)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unsubscribed = append(r.unsubscribed, id)
	return nil
}

func (r *subscribingRuntime) latest() *fakeDASubscription {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.subscriptions) == 0 {
		return nil
	}
	return r.subscriptions[len(r.subscriptions)-1]
}

func (r *subscribingRuntime) subscribeRequests() []opcda.SubscribeRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]opcda.SubscribeRequest(nil), r.requests...)
}

func (r *subscribingRuntime) unsubscribeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.unsubscribed)
}

const testSession = "session-token"

func testSubscriptionService(t *testing.T, runtime opcda.Runtime) (*SubscriptionService, *AddressSpace) {
	t.Helper()
	space := testAddressSpace(t)
	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4), AccessRights: rights},
		// A DA item the source will let nobody read. OPC 10000-4 5.13.2.1 has
		// it monitored anyway, so it needs to exist here.
		{Kind: opcda.BrowseEntryItem, Name: "Closed", ItemID: itemID("Test/Closed"),
			CanonicalType: varType(opcda.VTI4),
			AccessRights:  &opcda.DAAccessRights{Raw: 2, Read: false, Write: true}},
		{Kind: opcda.BrowseEntryBranch, Name: "Folder"},
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewSubscriptionService(space, runtime, DefaultSubscriptionLimits(), 100)
	if err != nil {
		t.Fatalf("NewSubscriptionService: %v", err)
	}
	return service, space
}

func createSubscription(t *testing.T, service *SubscriptionService) uint32 {
	t.Helper()
	return createSubscriptionFor(t, service, testSession)
}

// createSubscriptionFor names the session the subscription belongs to, so a
// test can tie one to a session it created itself.
func createSubscriptionFor(t *testing.T, service *SubscriptionService, session string) uint32 {
	t.Helper()
	response, err := service.CreateSubscription(session, CreateSubscriptionRequest{
		Header:                      RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		RequestedPublishingInterval: 250,
		RequestedMaxKeepAliveCount:  3,
		PublishingEnabled:           true,
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return response.SubscriptionID
}

func monitorItem(t *testing.T, service *SubscriptionService, id uint32, node NodeID, handle uint32) MonitoredItemCreateResult {
	t.Helper()
	return monitorItemFor(t, service, testSession, id, node, handle)
}

// monitorItemFor names the session, for the same reason.
func monitorItemFor(t *testing.T, service *SubscriptionService, session string, id uint32, node NodeID, handle uint32) MonitoredItemCreateResult {
	t.Helper()
	response, err := service.CreateMonitoredItems(context.Background(), session, CreateMonitoredItemsRequest{
		Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: node, AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: handle, SamplingInterval: 250, QueueSize: 10,
				Filter: NullExtensionObject(),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return response.Results[0]
}

func daNotification(itemID opcda.DAItemID, value int32, quality uint16) opcda.SubscriptionValue {
	varType := opcda.VTI4
	canonical := opcda.VTI4
	return opcda.SubscriptionValue{
		ItemID: itemID, VarType: &varType, CanonicalType: &canonical,
		HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{ItemID: itemID, VarType: varType, Value: value, QualityRaw: quality},
	}
}

func TestSubscriptionIdentifiersAndEnums(t *testing.T) {
	ids := map[uint32]uint32{
		CreateSubscriptionRequestEncodingID: 787, CreateSubscriptionResponseEncodingID: 790,
		CreateMonitoredItemsRequestEncodingID: 751, CreateMonitoredItemsResponseEncodingID: 754,
		DeleteMonitoredItemsRequestEncodingID: 781, DeleteSubscriptionsRequestEncodingID: 847,
		SetPublishingModeRequestEncodingID: 799, PublishRequestEncodingID: 826,
		PublishResponseEncodingID: 829, DataChangeNotificationEncodingID: 811,
		MonitoredItemNotificationEncodingID: 808,
	}
	for got, want := range ids {
		if got != want {
			t.Fatalf("encoding id %d, want %d", got, want)
		}
	}
	modes := map[MonitoringMode]int32{
		MonitoringModeDisabled: 0, MonitoringModeSampling: 1, MonitoringModeReporting: 2,
	}
	for mode, want := range modes {
		if int32(mode) != want {
			t.Fatalf("MonitoringMode %d, want %d", int32(mode), want)
		}
	}
}

// Table 82: a zero or negative publishing interval means the fastest supported
// one, a zero keep-alive count the smallest, and the lifetime count is at least
// three times the keep-alive count.
func TestCreateSubscriptionRevisesItsParameters(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	limits := DefaultSubscriptionLimits()

	cases := []struct {
		name            string
		interval        float64
		keepAlive       uint32
		lifetime        uint32
		wantInterval    float64
		wantKeepAlive   uint32
		wantMinLifetime uint32
	}{
		{"zero interval", 0, 5, 100, 100, 5, 15},
		{"negative interval", -1, 5, 100, 100, 5, 15},
		{"below the floor", 10, 5, 100, 100, 5, 15},
		{"above the ceiling", 3_600_000, 5, 100, 60_000, 5, 15},
		{"zero keep-alive", 250, 0, 100, 250, limits.MinKeepAliveCount, limits.MinKeepAliveCount * 3},
		{"short lifetime", 250, 10, 1, 250, 10, 30},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
				Header:                      RequestHeader{AdditionalHeader: NullExtensionObject()},
				RequestedPublishingInterval: testCase.interval,
				RequestedMaxKeepAliveCount:  testCase.keepAlive,
				RequestedLifetimeCount:      testCase.lifetime,
				PublishingEnabled:           true,
			}, channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if response.RevisedPublishingInterval != testCase.wantInterval {
				t.Fatalf("interval = %v, want %v", response.RevisedPublishingInterval, testCase.wantInterval)
			}
			if response.RevisedMaxKeepAliveCount != testCase.wantKeepAlive {
				t.Fatalf("keep-alive = %d, want %d", response.RevisedMaxKeepAliveCount, testCase.wantKeepAlive)
			}
			if response.RevisedLifetimeCount < testCase.wantMinLifetime {
				t.Fatalf("lifetime = %d, want at least %d",
					response.RevisedLifetimeCount, testCase.wantMinLifetime)
			}
			// The rule is a floor, not an equality, so state it as such.
			if response.RevisedLifetimeCount < response.RevisedMaxKeepAliveCount*3 {
				t.Fatalf("lifetime %d is below three times the keep-alive %d",
					response.RevisedLifetimeCount, response.RevisedMaxKeepAliveCount)
			}
			// A subscription with no items creates no DA group.
			if len(runtime.subscribeRequests()) != 0 {
				t.Fatal("an empty subscription created a DA group")
			}
		})
	}
}

// A UA Subscription maps onto one DA subscription, created with its first item.
func TestMonitoredItemsCreateTheDASubscription(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	result := monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	if result.StatusCode != StatusGood || result.MonitoredItemID == 0 {
		t.Fatalf("result = %+v", result)
	}
	// The DA core coalesces per item, so the effective queue is one value.
	if result.RevisedQueueSize != 1 {
		t.Fatalf("revised queue size = %d, want 1", result.RevisedQueueSize)
	}
	requests := runtime.subscribeRequests()
	if len(requests) != 1 {
		t.Fatalf("DA subscribe calls = %d", len(requests))
	}
	if len(requests[0].Items) != 1 || requests[0].Items[0] != "Test/Int32" {
		t.Fatalf("DA items = %v", requests[0].Items)
	}
	// The subscription's publishing interval becomes the DA update rate.
	if requests[0].RequestedUpdateRate != 250*time.Millisecond {
		t.Fatalf("DA update rate = %s", requests[0].RequestedUpdateRate)
	}

	// Adding an item rebuilds the DA subscription over the full set, because a
	// DA group's items are fixed when the group is created.
	monitorItem(t, service, id, ItemNodeID("Test/Float"), 2)
	requests = runtime.subscribeRequests()
	if len(requests) != 2 {
		t.Fatalf("DA subscribe calls = %d, want a rebuild", len(requests))
	}
	if len(requests[1].Items) != 2 {
		t.Fatalf("the rebuilt DA subscription carried %d items", len(requests[1].Items))
	}
	// The superseded DA subscription is released rather than left open.
	if runtime.unsubscribeCount() != 1 {
		t.Fatalf("released %d DA subscriptions", runtime.unsubscribeCount())
	}
}

func TestPublishCarriesDANotifications(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 42)

	runtime.latest().push(daNotification("Test/Int32", 4242, QualityGood))
	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{RequestHandle: 3, AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.SubscriptionID != id {
		t.Fatalf("subscription id = %d", response.SubscriptionID)
	}
	message := response.NotificationMessage
	if !message.HasData || len(message.Notifications) != 1 {
		t.Fatalf("message = %+v", message)
	}
	notification := message.Notifications[0]
	// The client handle is what identifies the item to the client.
	if notification.ClientHandle != 42 {
		t.Fatalf("client handle = %d", notification.ClientHandle)
	}
	if notification.Value.Status != StatusGood {
		t.Fatalf("status = %s", notification.Value.Status.Hex())
	}
	if notification.Value.Value.Value != int32(4242) {
		t.Fatalf("value = %v", notification.Value.Value.Value)
	}
	if message.SequenceNumber == 0 {
		t.Fatal("a notification carried sequence number 0")
	}
}

// With nothing pending the response is a keep-alive, which carries no
// NotificationData at all.
func TestPublishSendsAKeepAliveWhenIdle(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)

	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.NotificationMessage.HasData {
		t.Fatal("an idle publish carried notification data")
	}
	if len(response.NotificationMessage.Notifications) != 0 {
		t.Fatal("a keep-alive carried notifications")
	}
	// A keep-alive does not consume a sequence number, because sequence
	// numbers count notifications rather than responses.
	if response.NotificationMessage.SequenceNumber != 0 {
		t.Fatalf("keep-alive sequence = %d", response.NotificationMessage.SequenceNumber)
	}
}

// The DA quality decides the status, exactly as it does for a Read.
func TestPublishMapsQualityLikeARead(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	monitorItem(t, service, id, ItemNodeID("Test/Float"), 2)

	// Two items, not two values for one item: a DA group's pending set holds
	// one value per item, so two qualities can only be observed on two items.
	runtime.latest().push(
		daNotification("Test/Int32", 1, QualityUncertain),
		daNotification("Test/Float", 2, QualityCommFailure),
	)
	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	notifications := response.NotificationMessage.Notifications
	if len(notifications) != 2 {
		t.Fatalf("notifications = %d", len(notifications))
	}
	if notifications[0].Value.Status != StatusUncertain {
		t.Fatalf("uncertain = %s", notifications[0].Value.Status.Hex())
	}
	if notifications[1].Value.Status != StatusBadNoCommunication {
		t.Fatalf("comm failure = %s", notifications[1].Value.Status.Hex())
	}
	// A bad status carries no value.
	if !notifications[1].Value.Value.IsNull() {
		t.Fatal("a bad status carried a value")
	}
}

// A source disconnect invalidates the DA subscription; the client is told
// through an item status rather than by the stream going quiet.
func TestPublishReportsSourceLoss(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 7)

	runtime.latest().invalidate()
	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	notifications := response.NotificationMessage.Notifications
	if len(notifications) != 1 {
		t.Fatalf("notifications = %d, want the source loss reported", len(notifications))
	}
	if notifications[0].ClientHandle != 7 {
		t.Fatalf("client handle = %d", notifications[0].ClientHandle)
	}
	if notifications[0].Value.Status != StatusBadNotConnected {
		t.Fatalf("status = %s, want Bad_NotConnected", notifications[0].Value.Status.Hex())
	}
	// The DA subscription is released rather than left dangling.
	if runtime.unsubscribeCount() != 1 {
		t.Fatalf("released %d DA subscriptions", runtime.unsubscribeCount())
	}
}

// Only a reporting item produces notifications; disabled and sampling do not.
func TestMonitoringModeControlsReporting(t *testing.T) {
	for _, mode := range []MonitoringMode{MonitoringModeDisabled, MonitoringModeSampling} {
		runtime := &subscribingRuntime{}
		service, _ := testSubscriptionService(t, runtime)
		id := createSubscription(t, service)
		if _, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			SubscriptionID:     id,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor:       ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode:      mode,
				RequestedParameters: MonitoringParameters{ClientHandle: 1, Filter: NullExtensionObject()},
			}},
		}, channelEpoch); err != nil {
			t.Fatal(err)
		}
		runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
		response, err := service.Publish(context.Background(), testSession, PublishRequest{
			Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		}, channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		if response.NotificationMessage.HasData {
			t.Fatalf("mode %d reported a notification", mode)
		}
	}
}

// Publishing disabled stops delivery without touching the DA subscription,
// which Table 82 says does not affect monitoring mode.
func TestSetPublishingModeStopsDeliveryOnly(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	before := runtime.unsubscribeCount()

	if _, err := service.SetPublishingMode(testSession, SetPublishingModeRequest{
		Header:            RequestHeader{AdditionalHeader: NullExtensionObject()},
		PublishingEnabled: false,
		SubscriptionIDs:   []uint32{id},
	}, channelEpoch); err != nil {
		t.Fatal(err)
	}
	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.NotificationMessage.HasData {
		t.Fatal("publishing disabled still delivered a notification")
	}
	if runtime.unsubscribeCount() != before {
		t.Fatal("disabling publishing released the DA subscription")
	}

	// Re-enabling delivers what accumulated.
	if _, err := service.SetPublishingMode(testSession, SetPublishingModeRequest{
		Header:            RequestHeader{AdditionalHeader: NullExtensionObject()},
		PublishingEnabled: true,
		SubscriptionIDs:   []uint32{id},
	}, channelEpoch); err != nil {
		t.Fatal(err)
	}
	response, err = service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if !response.NotificationMessage.HasData {
		t.Fatal("re-enabling publishing delivered nothing")
	}
}

func TestMonitoredItemsRefuseWhatTheyCannotMonitor(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	cases := []struct {
		name    string
		request MonitoredItemCreateRequest
		want    StatusCode
	}{
		// A node identifier that names no DA item at all is unknown; one that
		// names an item is monitored, and the source decides whether it exists.
		{"a node identifier that is not an item", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: StringNodeID(AdapterNamespaceIndex, "not-an-item"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{ClientHandle: 10, Filter: NullExtensionObject()},
		}, StatusBadNodeIdUnknown},
		{"a folder", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: BranchNodeID([]string{"Folder"}, nil), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{ClientHandle: 11, Filter: NullExtensionObject()},
		}, StatusBadAttributeIDInvalid},
		{"a non-Value attribute", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeBrowseName},
			RequestedParameters: MonitoringParameters{ClientHandle: 12, Filter: NullExtensionObject()},
		}, StatusBadAttributeIDInvalid},
		{"an index range", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue, IndexRange: "0"},
			RequestedParameters: MonitoringParameters{ClientHandle: 13, Filter: NullExtensionObject()},
		}, StatusBadIndexRangeInvalid},
		// Silently ignoring a filter would misreport what the client receives.
		// Bad_MonitoredItemFilterUnsupported, not Bad_FilterNotAllowed: the
		// filter is allowed on the Value attribute, this server cannot do it.
		{"a filter that is not a DataChangeFilter", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{
				ClientHandle: 14,
				Filter: ExtensionObject{
					TypeID: NumericNodeID(0, 583), Encoding: ExtensionObjectNoBody},
			},
		}, StatusBadMonitoredItemFilterUnsupported},
		{"an absolute deadband, which a DA group has no form of", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{
				ClientHandle: 15,
				Filter:       dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandAbsolute, 2),
			},
		}, StatusBadMonitoredItemFilterUnsupported},
		{"a percent deadband outside its range", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{
				ClientHandle: 16,
				Filter:       dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandPercent, 101),
			},
		}, StatusBadDeadbandFilterInvalid},
		{"a trigger DA cannot report on", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{
				ClientHandle: 17,
				Filter:       dataChangeFilter(t, DataChangeTriggerStatusValueTimestamp, DeadbandNone, 0),
			},
		}, StatusBadMonitoredItemFilterUnsupported},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := service.CreateMonitoredItems(context.Background(), testSession,
				CreateMonitoredItemsRequest{
					Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
					SubscriptionID:     id,
					TimestampsToReturn: TimestampsBoth,
					ItemsToCreate:      []MonitoredItemCreateRequest{testCase.request},
				}, channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if response.Results[0].StatusCode != testCase.want {
				t.Fatalf("status = %s, want %s",
					response.Results[0].StatusCode.Hex(), testCase.want.Hex())
			}
		})
	}
	// None of those reached the source.
	if len(runtime.subscribeRequests()) != 0 {
		t.Fatal("a refused monitored item created a DA subscription")
	}
}

// Two items sharing a client handle would make notifications ambiguous.
func TestDuplicateClientHandleIsRefused(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 5)
	result := monitorItem(t, service, id, ItemNodeID("Test/Float"), 5)
	if result.StatusCode == StatusGood {
		t.Fatal("a duplicate client handle was accepted")
	}
}

// A subscription belongs to the session that created it.
func TestSubscriptionsAreBoundToTheirSession(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	_, err := service.CreateMonitoredItems(context.Background(), "another-session",
		CreateMonitoredItemsRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			SubscriptionID:     id,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor:       ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				RequestedParameters: MonitoringParameters{ClientHandle: 1, Filter: NullExtensionObject()},
			}},
		}, channelEpoch)
	if err == nil {
		t.Fatal("another session reached this subscription")
	}
	if got := codecStatus(t, err); got != StatusBadSubscriptionIDInvalid {
		t.Fatalf("status = %s", got.Hex())
	}
	// A session with no subscription is told so rather than given an empty one.
	_, err = service.Publish(context.Background(), "another-session", PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err == nil {
		t.Fatal("a session with no subscription received a publish response")
	}
	if got := codecStatus(t, err); got != StatusBadNoSubscription {
		t.Fatalf("status = %s", got.Hex())
	}
}

// A closed session must not leave DA groups open on the source.
func TestReleaseSessionFreesDAGroups(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	before := runtime.unsubscribeCount()

	service.ReleaseSession(context.Background(), testSession)
	if service.Count() != 0 {
		t.Fatalf("%d subscriptions survived the session", service.Count())
	}
	if runtime.unsubscribeCount() != before+1 {
		t.Fatal("the DA subscription was not released")
	}
}

func TestDeleteSubscriptionAndMonitoredItems(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	first := monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	monitorItem(t, service, id, ItemNodeID("Test/Float"), 2)

	deleted, err := service.DeleteMonitoredItems(context.Background(), testSession,
		DeleteMonitoredItemsRequest{
			Header:           RequestHeader{AdditionalHeader: NullExtensionObject()},
			SubscriptionID:   id,
			MonitoredItemIDs: []uint32{first.MonitoredItemID, 9999},
		}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Results[0] != StatusGood {
		t.Fatalf("delete = %s", deleted.Results[0].Hex())
	}
	// An unknown item id is reported per entry rather than failing the call.
	if deleted.Results[1] != StatusBadMonitoredItemIDInvalid {
		t.Fatalf("unknown item = %s", deleted.Results[1].Hex())
	}
	// The DA subscription is rebuilt over what remains.
	requests := runtime.subscribeRequests()
	if len(requests[len(requests)-1].Items) != 1 {
		t.Fatalf("the rebuilt DA subscription carried %d items",
			len(requests[len(requests)-1].Items))
	}

	removed, err := service.DeleteSubscriptions(context.Background(), testSession,
		DeleteSubscriptionsRequest{
			Header:          RequestHeader{AdditionalHeader: NullExtensionObject()},
			SubscriptionIDs: []uint32{id, 4242},
		}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Results[0] != StatusGood || removed.Results[1] != StatusBadSubscriptionIDInvalid {
		t.Fatalf("results = %v", removed.Results)
	}
	if service.Count() != 0 {
		t.Fatalf("%d subscriptions survived deletion", service.Count())
	}
}

// A DA subscribe failure leaves no monitored items behind and reports the
// source's status.
func TestMonitoredItemsReportADASubscribeFailure(t *testing.T) {
	runtime := &subscribingRuntime{
		err: opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "not connected"),
	}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	result := monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	if result.StatusCode != StatusBadNotConnected {
		t.Fatalf("status = %s, want Bad_NotConnected", result.StatusCode.Hex())
	}
	// Nothing was left registered, so a retry starts clean.
	runtime.err = nil
	retry := monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	if retry.StatusCode != StatusGood {
		t.Fatalf("retry status = %s", retry.StatusCode.Hex())
	}
}

func TestPublishAcknowledgements(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)

	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	first, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	sequence := first.NotificationMessage.SequenceNumber
	if len(first.AvailableSequenceNumbers) != 1 {
		t.Fatalf("available sequence numbers = %v", first.AvailableSequenceNumbers)
	}

	second, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		Acknowledgements: []SubscriptionAcknowledgement{
			{SubscriptionID: id, SequenceNumber: sequence},
			{SubscriptionID: id, SequenceNumber: 9999},
		},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if second.Results[0] != StatusGood {
		t.Fatalf("acknowledgement = %s", second.Results[0].Hex())
	}
	// An unknown sequence number is reported rather than silently accepted.
	if second.Results[1] != StatusBadSequenceNumberUnknown {
		t.Fatalf("unknown sequence = %s", second.Results[1].Hex())
	}
	if len(second.AvailableSequenceNumbers) != 0 {
		t.Fatalf("the acknowledged message is still available: %v", second.AvailableSequenceNumbers)
	}
}

// Table 82: a zero maxNotificationsPerPublish means the client imposes no
// limit, so the server's bound applies; a smaller client value tightens it.
func TestMaxNotificationsPerPublishIsHonoured(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	response, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
		Header:                      RequestHeader{AdditionalHeader: NullExtensionObject()},
		RequestedPublishingInterval: 250,
		MaxNotificationsPerPublish:  1,
		PublishingEnabled:           true,
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	id := response.SubscriptionID
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	monitorItem(t, service, id, ItemNodeID("Test/Float"), 2)

	runtime.latest().push(
		daNotification("Test/Int32", 1, QualityGood),
		daNotification("Test/Float", 2, QualityGood),
	)
	published, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(published.NotificationMessage.Notifications) != 1 {
		t.Fatalf("notifications = %d, want the client's limit",
			len(published.NotificationMessage.Notifications))
	}
	// What did not fit is reported as more, not dropped.
	if !published.MoreNotifications {
		t.Fatal("the remainder was not reported as more notifications")
	}
	published, err = service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(published.NotificationMessage.Notifications) != 1 {
		t.Fatal("the remainder was not delivered")
	}
	if published.MoreNotifications {
		t.Fatal("more notifications were reported with nothing left")
	}
}

func TestSubscriptionServiceBounds(t *testing.T) {
	runtime := &subscribingRuntime{}
	space := testAddressSpace(t)
	limits := DefaultSubscriptionLimits()
	limits.MaxSubscriptions = 1
	limits.MaxMonitoredItems = 1
	service, err := NewSubscriptionService(space, runtime, limits, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()}, PublishingEnabled: true,
	}, channelEpoch); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateSubscription(testSession, CreateSubscriptionRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()}, PublishingEnabled: true,
	}, channelEpoch)
	if err == nil {
		t.Fatal("the subscription limit was exceeded")
	}
	if got := codecStatus(t, err); got != StatusBadTooManySubscriptions {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestSubscriptionRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteCreateSubscriptionRequest(CreateSubscriptionRequest{
		Header:                      RequestHeader{AdditionalHeader: NullExtensionObject()},
		RequestedPublishingInterval: 500, RequestedMaxKeepAliveCount: 3,
		RequestedLifetimeCount: 9, MaxNotificationsPerPublish: 20,
		PublishingEnabled: true, Priority: 7,
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != CreateSubscriptionRequestEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	request, err := decoder.ReadCreateSubscriptionRequest()
	if err != nil {
		t.Fatal(err)
	}
	if request.RequestedPublishingInterval != 500 || request.Priority != 7 || !request.PublishingEnabled {
		t.Fatalf("request = %+v", request)
	}

	// A notification round-trips through the ExtensionObject the extensible
	// NotificationData parameter requires.
	encoder = newTestEncoder(t, limits)
	encoder.WritePublishResponse(PublishResponse{
		Header:         ResponseHeader{ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject()},
		SubscriptionID: 11,
		NotificationMessage: NotificationMessage{
			SequenceNumber: 3, PublishTime: channelEpoch, HasData: true,
			Notifications: []MonitoredItemNotification{{
				ClientHandle: 42,
				Value: DataValue{
					Value: Variant{Type: BuiltInInt32, Value: int32(7)}, Status: StatusGood,
				},
			}},
		},
	})
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	response, err := decoder.ReadPublishResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.SubscriptionID != 11 || !response.NotificationMessage.HasData {
		t.Fatalf("response = %+v", response)
	}
	notification := response.NotificationMessage.Notifications[0]
	if notification.ClientHandle != 42 || notification.Value.Value.Value != int32(7) {
		t.Fatalf("notification = %+v", notification)
	}

	// A keep-alive round-trips as carrying no data.
	encoder = newTestEncoder(t, limits)
	encoder.WritePublishResponse(PublishResponse{
		Header:              ResponseHeader{ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject()},
		NotificationMessage: NotificationMessage{SequenceNumber: 3, PublishTime: channelEpoch},
	})
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	response, err = decoder.ReadPublishResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.NotificationMessage.HasData {
		t.Fatal("a keep-alive decoded as carrying data")
	}
}

func TestSubscriptionLimitsValidation(t *testing.T) {
	if err := DefaultSubscriptionLimits().ValidateForConfiguration(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SubscriptionLimits){
		"zero subscriptions":  func(l *SubscriptionLimits) { l.MaxSubscriptions = 0 },
		"zero items":          func(l *SubscriptionLimits) { l.MaxMonitoredItems = 0 },
		"zero interval":       func(l *SubscriptionLimits) { l.MinPublishingInterval = 0 },
		"inverted intervals":  func(l *SubscriptionLimits) { l.MinPublishingInterval = l.MaxPublishingInterval + 1 },
		"zero keep-alive":     func(l *SubscriptionLimits) { l.MinKeepAliveCount = 0 },
		"inverted keep-alive": func(l *SubscriptionLimits) { l.MinKeepAliveCount = l.MaxKeepAliveCount + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultSubscriptionLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("invalid limits were accepted")
			}
		})
	}
}

// A DA server need not implement Browse, so a client that knows its ItemIDs can
// monitor them without the address space having been populated.
func TestMonitorAnItemThatWasNeverBrowsed(t *testing.T) {
	runtime := &subscribingRuntime{}
	space := testAddressSpace(t)
	service, err := NewSubscriptionService(space, runtime, DefaultSubscriptionLimits(), 1)
	if err != nil {
		t.Fatal(err)
	}
	id := createSubscription(t, service)

	// Nothing was browsed; the identifier itself names the item.
	result := monitorItem(t, service, id, ItemNodeID("Vendor/Tag"), 3)
	if result.StatusCode != StatusGood {
		t.Fatalf("status = %s, want the item to be monitored", result.StatusCode.Hex())
	}
	requests := runtime.subscribeRequests()
	if len(requests) != 1 || requests[0].Items[0] != "Vendor/Tag" {
		t.Fatalf("the exact ItemID did not reach the source: %v", requests)
	}

	runtime.latest().push(daNotification("Vendor/Tag", 5, QualityGood))
	response, err := service.Publish(context.Background(), testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.NotificationMessage.Notifications) != 1 {
		t.Fatal("the unbrowsed item produced no notification")
	}
	if response.NotificationMessage.Notifications[0].ClientHandle != 3 {
		t.Fatalf("client handle = %d", response.NotificationMessage.Notifications[0].ClientHandle)
	}
}

// A vendor may revise the update rate far from what was requested, and the
// client is told what the source settled on rather than what was asked for.
func TestRevisedSamplingIntervalComesFromTheSource(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 2 * time.Second}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	result := monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	if result.StatusCode != StatusGood {
		t.Fatalf("status = %s", result.StatusCode.Hex())
	}
	if result.RevisedSamplingInterval != 2000 {
		t.Fatalf("revised sampling = %vms, want the source's 2000ms",
			result.RevisedSamplingInterval)
	}
}

// OPC 10000-4 5.14.5.1 has Publish requests queued in the server: a client
// issues the next Publish as soon as the last response arrives, so answering an
// empty one at once turns the client into a busy loop. A third-party client
// measured thousands of exchanges a second against this listener before Publish
// began holding the request, and the load starved the sampling the subscription
// existed to deliver.
func TestPublishHoldsTheRequestUntilThereIsSomethingToSay(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 7)

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		response PublishResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := service.Publish(ctx, testSession, PublishRequest{
			Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		}, channelEpoch)
		done <- result{response, err}
	}()

	// Nothing has changed, so the request must still be held rather than
	// answered with an empty response.
	select {
	case got := <-done:
		cancel()
		t.Fatalf("Publish answered with nothing to report: %+v %v", got.response, got.err)
	case <-time.After(300 * time.Millisecond):
	}

	// Once the source reports a change the held request is answered.
	runtime.latest().push(daNotification("Test/Int32", 4242, QualityGood))
	select {
	case got := <-done:
		cancel()
		if got.err != nil {
			t.Fatal(got.err)
		}
		if !got.response.NotificationMessage.HasData ||
			len(got.response.NotificationMessage.Notifications) != 1 {
			t.Fatalf("notification message = %+v", got.response.NotificationMessage)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the held Publish was never answered after a change")
	}
}

// A held Publish is released when its connection goes, so a client that
// disappears does not leave the request waiting forever.
func TestPublishStopsWaitingWhenTheConnectionEnds(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 7)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.Publish(ctx, testSession, PublishRequest{
			Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		}, channelEpoch)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the held Publish returned a response after its connection ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held Publish outlived its connection")
	}
}

// With nothing to report the subscription still has to speak eventually, so the
// keep-alive comes after maxKeepAliveCount publishing cycles rather than never.
func TestPublishSendsAKeepAliveAfterTheKeepAliveCount(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 7)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := service.Publish(ctx, testSession, PublishRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	// A keep-alive carries no NotificationData and reuses the last sequence
	// number, because Table 164 counts notifications rather than responses.
	if response.NotificationMessage.HasData {
		t.Fatalf("keep-alive carried data: %+v", response.NotificationMessage)
	}
	if response.SubscriptionID != id {
		t.Fatalf("keep-alive subscription = %d, want %d", response.SubscriptionID, id)
	}
}

// This source offers no OPC DA item properties. PROPERTIES_UNSUPPORTED is the
// same answer a real source without IOPCItemProperties gives.
func (*subscribingRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}

func (*subscribingRuntime) ItemProperties(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}

// A value read and the same value delivered by a subscription must be the same
// DataValue. dataValueForSubscription says so in a comment and keeps it by
// delegating to dataValueForRead; nothing checked that it still delegates, and
// a comment that stops being true is a defect this project has shipped before.
func TestASubscribedValueAndAReadValueCannotDisagree(t *testing.T) {
	actual := opcda.VTI4
	canonical := opcda.VTI4
	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	sourceTime := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	now := time.Date(2026, 3, 4, 5, 6, 9, 0, time.UTC)

	for _, testCase := range []struct {
		name    string
		value   *opcda.DAValue
		hresult opcda.HRESULT
		errCode string
	}{
		{"a good value", &opcda.DAValue{ItemID: "Test/Int32", VarType: actual, Value: int32(7),
			QualityRaw: QualityGood, Timestamp: sourceTime, TimestampPresent: true}, opcda.SOK, ""},
		{"a bad quality", &opcda.DAValue{ItemID: "Test/Int32", VarType: actual, Value: int32(7),
			QualityRaw: QualityCommFailure, Timestamp: sourceTime, TimestampPresent: true}, opcda.SOK, ""},
		{"a quality with limit bits", &opcda.DAValue{ItemID: "Test/Int32", VarType: actual, Value: int32(7),
			QualityRaw: QualityGood | 0x02, TimestampPresent: false}, opcda.SOK, ""},
		{"an item the source refused", nil, opcda.HRESULT(-1073479674), ""},
		{"a value the adapter cannot represent", nil, opcda.SOK, string(opcda.CodeUnsupportedVarType)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			read := dataValueForRead(opcda.ReadResult{
				ItemID: "Test/Int32", Value: testCase.value, VarType: &actual,
				CanonicalType: &canonical, AccessRights: rights,
				HRESULT: testCase.hresult, HRESULTPresent: true, ErrorCode: testCase.errCode,
			}, TimestampsBoth, now)
			subscribed := dataValueForSubscription(opcda.SubscriptionValue{
				ItemID: "Test/Int32", Value: testCase.value, VarType: &actual,
				CanonicalType: &canonical, AccessRights: rights,
				HRESULT: testCase.hresult, HRESULTPresent: true, ErrorCode: testCase.errCode,
			}, TimestampsBoth, now)

			if read.Status != subscribed.Status {
				t.Errorf("status: read %s, subscribed %s", read.Status.Hex(), subscribed.Status.Hex())
			}
			if read.SourceTimestamp != subscribed.SourceTimestamp {
				t.Errorf("source timestamp: read %v, subscribed %v", read.SourceTimestamp, subscribed.SourceTimestamp)
			}
			if read.ServerTimestamp != subscribed.ServerTimestamp {
				t.Errorf("server timestamp: read %v, subscribed %v", read.ServerTimestamp, subscribed.ServerTimestamp)
			}
			if read.Value.Type != subscribed.Value.Type || read.Value.Value != subscribed.Value.Value {
				t.Errorf("value: read %+v, subscribed %+v", read.Value, subscribed.Value)
			}
		})
	}
}

// dataChangeFilter builds the ExtensionObject a client sends.
func dataChangeFilter(t *testing.T, trigger, deadbandType uint32, value float64) ExtensionObject {
	t.Helper()
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	encoder.WriteUInt32(trigger)
	encoder.WriteUInt32(deadbandType)
	encoder.WriteDouble(value)
	body, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("encode the filter: %v", err)
	}
	return ExtensionObject{
		TypeID:   NumericNodeID(0, NodeIDDataChangeFilterEncodingDefaultBinary),
		Encoding: ExtensionObjectByteString,
		Body:     body,
	}
}

// A.3.5 names the percent deadband as the one filter the wrapper supports, and
// the DA core has always been able to carry it. A UA client asking for one now
// gets it applied to the group rather than refused.
func TestAPercentDeadbandReachesTheDAGroup(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, space := testSubscriptionService(t, runtime)
	// Clause 7.2 defines the deadband as a percentage of the EURange, so it
	// applies only to an AnalogItem that has one. Both fixture items are made
	// analog for this test; the refusal without a range is checked below.
	analog := []opcda.AvailableProperty{{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}}
	for _, itemID := range []opcda.DAItemID{"Test/Int32", "Test/Float"} {
		if err := space.AttachItemProperties(itemID, analog, opcda.EUTypeNoEnum, 1000); err != nil {
			t.Fatalf("AttachItemProperties(%s): %v", itemID, err)
		}
	}
	id := createSubscription(t, service)

	response, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: 40, SamplingInterval: 250, QueueSize: 1,
				Filter: dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandPercent, 12.5),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatalf("CreateMonitoredItems: %v", err)
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("status = %s", response.Results[0].StatusCode.Hex())
	}
	requests := runtime.subscribeRequests()
	if len(requests) != 1 {
		t.Fatalf("DA subscriptions = %d", len(requests))
	}
	if requests[0].Deadband != 12.5 {
		t.Fatalf("the DA group was given deadband %v, want 12.5", requests[0].Deadband)
	}

	// A DA group has one deadband. A second item asking for a different one
	// cannot be honoured, and saying so is better than applying somebody
	// else's.
	differing, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Float"), AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: 41, SamplingInterval: 250, QueueSize: 1,
				Filter: dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandPercent, 30),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatalf("CreateMonitoredItems: %v", err)
	}
	if differing.Results[0].StatusCode != StatusBadMonitoredItemFilterUnsupported {
		t.Fatalf("a differing deadband answered %s", differing.Results[0].StatusCode.Hex())
	}
}

// Clause 5.2 puts the bit on **one** data change notification per monitored
// item that samples values at the time the change happened. A server that set
// it on every notification afterwards would tell a client to re-read metadata
// forever.
func TestSemanticsChangedIsCarriedOnceAndThenStops(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, space := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 42)

	publish := func(t *testing.T, handle uint32) DataValue {
		t.Helper()
		response, err := service.Publish(context.Background(), testSession, PublishRequest{
			Header: RequestHeader{RequestHandle: handle, AdditionalHeader: NullExtensionObject()},
		}, channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		if !response.NotificationMessage.HasData || len(response.NotificationMessage.Notifications) != 1 {
			t.Fatalf("message = %+v", response.NotificationMessage)
		}
		return response.NotificationMessage.Notifications[0].Value
	}

	// Before anything changes, the bit is absent.
	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	if value := publish(t, 3); value.Status.HasSemanticsChanged() {
		t.Fatal("the bit was set with no semantic change")
	}

	// The adapter learns the units, then learns they are different.
	units := func(text string) Variant { return Variant{Type: BuiltInString, Value: text} }
	space.NoteSemanticProperty("Test/Int32", "EngineeringUnits", units("degC"))
	space.NoteSemanticProperty("Test/Int32", "EngineeringUnits", units("degF"))

	runtime.latest().push(daNotification("Test/Int32", 2, QualityGood))
	if value := publish(t, 4); !value.Status.HasSemanticsChanged() {
		t.Fatal("the bit was not set on the notification after a semantic change")
	}
	// And once is once.
	runtime.latest().push(daNotification("Test/Int32", 3, QualityGood))
	if value := publish(t, 5); value.Status.HasSemanticsChanged() {
		t.Fatal("the bit was carried a second time")
	}
}

// Clause 7.2: a percent deadband "is defined as the percentage of the EURange.
// That is, it applies only to AnalogItems with an EURange Property." An item
// without one has no range to take a percentage of, and passing the filter to
// the DA group as though it did would apply a percentage of nothing.
func TestAPercentDeadbandNeedsAnEURange(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	response, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: 60, SamplingInterval: 250, QueueSize: 1,
				Filter: dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandPercent, 5),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatalf("CreateMonitoredItems: %v", err)
	}
	// Table 61 names Bad_DeadbandFilterInvalid for this, not a generic
	// unsupported filter: it covers "a PercentDeadband is not supported, since
	// an EURange is not configured".
	if response.Results[0].StatusCode != StatusBadDeadbandFilterInvalid {
		t.Fatalf("a deadband on an item with no EURange answered %s",
			response.Results[0].StatusCode.Hex())
	}
	if len(runtime.subscribeRequests()) != 0 {
		t.Fatal("a refused deadband still reached the source")
	}

	// A filter asking for no deadband is fine on any item: there is no
	// percentage of anything to take.
	none, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: 61, SamplingInterval: 250, QueueSize: 1,
				Filter: dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandNone, 0),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatalf("CreateMonitoredItems: %v", err)
	}
	if none.Results[0].StatusCode != StatusGood {
		t.Fatalf("a filter with no deadband answered %s", none.Results[0].StatusCode.Hex())
	}
}

// monitorAt creates one monitored item asking for a named sampling interval.
func monitorAt(t *testing.T, service *SubscriptionService, id uint32, node NodeID, handle uint32, interval float64) MonitoredItemCreateResult {
	t.Helper()
	response, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: node, AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: handle, SamplingInterval: interval, QueueSize: 1,
				Filter: NullExtensionObject(),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return response.Results[0]
}

// OPC 10000-4 7.21: "The Server shall always return a revisedSamplingInterval
// that is equal or higher than the requested samplingInterval." The DA group's
// rate is the floor -- nothing can be sampled faster -- and an item asking for
// something slower keeps what it asked for.
func TestRevisedSamplingIntervalIsNeverFasterThanRequested(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		requested float64
		want      float64
	}{
		// The group settles on 1000ms, so a faster request cannot be met and
		// the rate actually delivered is reported instead.
		{"faster than the group", 100, 1000},
		{"the group's own rate", 1000, 1000},
		// Slower is met exactly: the item is paced rather than being handed
		// everything the group delivers.
		{"slower than the group", 5000, 5000},
		// "The value 0 indicates that the Server should use the fastest
		// practical rate", which is the group's.
		{"fastest practical", 0, 1000},
		// "Any negative number is interpreted as -1", the publishing interval.
		{"negative means the publishing interval", -7, 1000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &subscribingRuntime{revisedRate: 1000 * time.Millisecond}
			service, _ := testSubscriptionService(t, runtime)
			id := createSubscription(t, service)
			result := monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, testCase.requested)
			if result.StatusCode != StatusGood {
				t.Fatalf("create = %s", result.StatusCode.Hex())
			}
			if result.RevisedSamplingInterval != testCase.want {
				t.Fatalf("revised = %v, want %v", result.RevisedSamplingInterval, testCase.want)
			}
			if testCase.requested > 0 && result.RevisedSamplingInterval < testCase.requested {
				t.Fatalf("revised %v is faster than the requested %v",
					result.RevisedSamplingInterval, testCase.requested)
			}
		})
	}
}

// An item that asked for a slower rate than the group runs at is paced to it:
// the interval it was promised is the interval it gets, and the value it
// finally receives is the newest one, not the one that happened to arrive first.
func TestASlowItemIsPacedToTheIntervalItWasPromised(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 1000 * time.Millisecond}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	result := monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, 5000)
	if result.RevisedSamplingInterval != 5000 {
		t.Fatalf("revised = %v", result.RevisedSamplingInterval)
	}

	// The first notification is not paced: a client that has just subscribed
	// is waiting for the current value.
	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	first := publishOnce(t, service, channelEpoch)
	if len(first) != 1 {
		t.Fatalf("the first notification did not go out at once: %d", len(first))
	}

	// A second value inside the promised interval is held, not sent.
	runtime.latest().push(daNotification("Test/Int32", 2, QualityGood))
	if held := publishNothing(t, service, channelEpoch.Add(time.Second)); held != 0 {
		t.Fatalf("a value inside the sampling interval was reported: %d", held)
	}
	// A third arrives while the second is still held. The newest wins, which
	// is what a queue of one holds.
	runtime.latest().push(daNotification("Test/Int32", 3, QualityGood))

	// Once the interval has passed, the held value goes out -- and it is the
	// newest, not the one that was held first.
	after := publishOnce(t, service, channelEpoch.Add(6*time.Second))
	if len(after) != 1 {
		t.Fatalf("the held value was not reported: %d", len(after))
	}
	if value, ok := after[0].Value.Value.Value.(int32); !ok || value != 3 {
		t.Fatalf("reported %#v, want the newest value", after[0].Value.Value.Value)
	}
}

// publishOnce runs one publishing cycle at a named time and requires it to have
// something to send. tryPublish is used rather than Publish because pacing is
// about when a value is reported, and only tryPublish takes the clock.
func publishOnce(t *testing.T, service *SubscriptionService, now time.Time) []MonitoredItemNotification {
	t.Helper()
	request := PublishRequest{Header: RequestHeader{AdditionalHeader: NullExtensionObject()}}
	response, ready, err := service.tryPublish(context.Background(), testSession, request, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("the cycle had nothing to send")
	}
	return response.NotificationMessage.Notifications
}

// publishNothing runs one publishing cycle and reports how many notifications
// it produced, which a paced item requires to be none.
func publishNothing(t *testing.T, service *SubscriptionService, now time.Time) int {
	t.Helper()
	request := PublishRequest{Header: RequestHeader{AdditionalHeader: NullExtensionObject()}}
	response, ready, err := service.tryPublish(context.Background(), testSession, request, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		return 0
	}
	return len(response.NotificationMessage.Notifications)
}

// OPC 10000-4 5.13.1.2: "If the Server specifies a value for the
// MinimumSamplingInterval Attribute it shall always return a
// revisedSamplingInterval that is equal or higher than the
// MinimumSamplingInterval if the Client subscribes to the Value Attribute."
// Here that attribute is the source's own DA Scan Rate, so a faster promise
// would be a promise the source has already said it will not keep.
func TestRevisedSamplingIntervalRespectsTheScanRate(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 100 * time.Millisecond}
	service, space := testSubscriptionService(t, runtime)
	// The source says this item is only scanned every two seconds.
	space.NoteScanRate(*itemID("Test/Int32"), 2000)
	id := createSubscription(t, service)

	// Neither a fast request nor the group's own faster rate may lower it.
	for _, requested := range []float64{0, 50, 100, 1999} {
		result := monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, requested)
		if result.StatusCode != StatusGood {
			t.Fatalf("requested %v: create = %s", requested, result.StatusCode.Hex())
		}
		if result.RevisedSamplingInterval != 2000 {
			t.Fatalf("requested %v: revised = %v, want the 2000ms scan rate",
				requested, result.RevisedSamplingInterval)
		}
		deleteMonitoredItem(t, service, id, result.MonitoredItemID)
	}

	// A request slower than the scan rate is still honoured as asked.
	result := monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, 9000)
	if result.RevisedSamplingInterval != 9000 {
		t.Fatalf("revised = %v, want the slower requested interval", result.RevisedSamplingInterval)
	}
}

func deleteMonitoredItem(t *testing.T, service *SubscriptionService, id, itemID uint32) {
	t.Helper()
	if _, err := service.DeleteMonitoredItems(context.Background(), testSession, DeleteMonitoredItemsRequest{
		Header:           RequestHeader{AdditionalHeader: NullExtensionObject()},
		SubscriptionID:   id,
		MonitoredItemIDs: []uint32{itemID},
	}, channelEpoch); err != nil {
		t.Fatal(err)
	}
}

// opcda.Subscription drains "preserving first-seen order", which is the
// source's own account of what changed first. Pacing must carry that order
// through rather than replace it with whatever order a map yields.
func TestNotificationsKeepTheSourcesOrder(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		runtime := &subscribingRuntime{}
		service, _ := testSubscriptionService(t, runtime)
		id := createSubscription(t, service)
		monitorItem(t, service, id, ItemNodeID("Test/Float"), 1)
		monitorItem(t, service, id, ItemNodeID("Test/Int32"), 2)

		// The source saw the Int32 change first, whichever order the items
		// were created in.
		runtime.latest().push(
			daNotification("Test/Int32", 1, QualityGood),
			daNotification("Test/Float", 2, QualityGood),
		)
		notifications := publishOnce(t, service, channelEpoch)
		if len(notifications) != 2 {
			t.Fatalf("notifications = %d", len(notifications))
		}
		if notifications[0].ClientHandle != 2 || notifications[1].ClientHandle != 1 {
			t.Fatalf("attempt %d: handles = %d, %d, want the source's order",
				attempt, notifications[0].ClientHandle, notifications[1].ClientHandle)
		}
	}
}

// An item still holding a value keeps its place in the queue, so a paced item
// does not jump ahead of one that began holding before it.
func TestAPacedItemKeepsItsPlaceInTheQueue(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 100 * time.Millisecond}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	slow := monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, 5000)
	fast := monitorAt(t, service, id, ItemNodeID("Test/Float"), 2, 0)
	if slow.RevisedSamplingInterval != 5000 || fast.RevisedSamplingInterval != 100 {
		t.Fatalf("revised = %v, %v", slow.RevisedSamplingInterval, fast.RevisedSamplingInterval)
	}

	// Both report their first value at once.
	runtime.latest().push(
		daNotification("Test/Int32", 1, QualityGood),
		daNotification("Test/Float", 1, QualityGood),
	)
	if first := publishOnce(t, service, channelEpoch); len(first) != 2 {
		t.Fatalf("first = %d", len(first))
	}

	// The slow item changes first and is held; the fast one changes after and
	// is due immediately. The slow item must not lose its place.
	runtime.latest().push(daNotification("Test/Int32", 2, QualityGood))
	if held := publishNothing(t, service, channelEpoch.Add(time.Second)); held != 0 {
		t.Fatalf("the slow item reported early: %d", held)
	}
	runtime.latest().push(daNotification("Test/Float", 2, QualityGood))
	if only := publishOnce(t, service, channelEpoch.Add(2*time.Second)); len(only) != 1 ||
		only[0].ClientHandle != 2 {
		t.Fatalf("the fast item alone should have reported, got %d", len(only))
	}
	after := publishOnce(t, service, channelEpoch.Add(8*time.Second))
	if len(after) != 1 || after[0].ClientHandle != 1 {
		t.Fatalf("the slow item's held value was not reported")
	}
}

// A paced item that keeps changing takes one place in the queue, not one per
// change it absorbs. The output is the same either way -- a place whose value
// has already been reported is skipped -- so this is checked on the queue
// itself, which is where the difference lives.
func TestAPacedItemTakesOnePlaceHoweverOftenItChanges(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 100 * time.Millisecond}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, 5000)

	// The first value goes out at once, so what follows is all held.
	runtime.latest().push(daNotification("Test/Int32", 0, QualityGood))
	publishOnce(t, service, channelEpoch)

	for change := 1; change <= 20; change++ {
		runtime.latest().push(daNotification("Test/Int32", int32(change), QualityGood))
		publishNothing(t, service, channelEpoch.Add(time.Duration(change)*100*time.Millisecond))
	}

	service.mu.Lock()
	held := len(service.subscriptions[id].heldOrder)
	service.mu.Unlock()
	if held != 1 {
		t.Fatalf("the queue holds %d places for one item", held)
	}

	// And the value that finally goes out is the newest of all of them.
	after := publishOnce(t, service, channelEpoch.Add(time.Minute))
	if len(after) != 1 {
		t.Fatalf("notifications = %d", len(after))
	}
	if value, ok := after[0].Value.Value.Value.(int32); !ok || value != 20 {
		t.Fatalf("reported %#v, want the newest change", after[0].Value.Value.Value)
	}
}

// OPC 10000-4 5.13.2.1: "When a user adds a monitored item that the user is
// denied read access to, the add operation for the item shall succeed and the
// bad status Bad_NotReadable or Bad_UserAccessDenied shall be returned in the
// Publish response." Table 65 agrees by omission -- Bad_NotReadable is not an
// operation level result code for CreateMonitoredItems.
func TestAnUnreadableItemIsCreatedAndReportsThroughPublish(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	result := monitorItem(t, service, id, ItemNodeID("Test/Closed"), 1)
	if result.StatusCode != StatusGood {
		t.Fatalf("create = %s, want the add to succeed", result.StatusCode.Hex())
	}
	if result.MonitoredItemID == 0 {
		t.Fatal("no monitored item was created")
	}

	// The status arrives through Publish, which is where the rule puts it.
	notifications := publishOnce(t, service, channelEpoch)
	if len(notifications) != 1 {
		t.Fatalf("notifications = %d", len(notifications))
	}
	if notifications[0].ClientHandle != 1 {
		t.Fatalf("handle = %d", notifications[0].ClientHandle)
	}
	if notifications[0].Value.Status != StatusBadNotReadable {
		t.Fatalf("status = %s, want Bad_NotReadable", notifications[0].Value.Status.Hex())
	}

	// It is reported once. A monitored item reports changes, and this status
	// has nothing further to say.
	if again := publishNothing(t, service, channelEpoch.Add(time.Minute)); again != 0 {
		t.Fatalf("the status was repeated %d times", again)
	}
}

// The item stays out of the DA group: there is nothing for the group to read,
// and a source that refuses AddItems for it would fail the whole rebuild and
// take every readable item in the request down with it.
func TestAnUnreadableItemDoesNotReachTheGroup(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Closed"), 1)
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 2)

	requests := runtime.subscribeRequests()
	last := requests[len(requests)-1]
	if len(last.Items) != 1 || last.Items[0] != *itemID("Test/Int32") {
		t.Fatalf("the group carries %v, want only the readable item", last.Items)
	}

	// The readable item still works, and both statuses reach the client.
	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	notifications := publishOnce(t, service, channelEpoch)
	if len(notifications) != 2 {
		t.Fatalf("notifications = %d, want one for each item", len(notifications))
	}
	byHandle := map[uint32]StatusCode{}
	for _, notification := range notifications {
		byHandle[notification.ClientHandle] = notification.Value.Status
	}
	if byHandle[1] != StatusBadNotReadable || byHandle[2] != StatusGood {
		t.Fatalf("statuses = %s, %s", byHandle[1].Hex(), byHandle[2].Hex())
	}
}

// A subscription whose only item is unreadable has no DA group at all, and the
// status still reaches the client.
func TestAnUnreadableItemAloneNeedsNoGroup(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorItem(t, service, id, ItemNodeID("Test/Closed"), 1)

	if requests := runtime.subscribeRequests(); len(requests) != 0 {
		t.Fatalf("a group was created for nothing readable: %v", requests)
	}
	notifications := publishOnce(t, service, channelEpoch)
	if len(notifications) != 1 || notifications[0].Value.Status != StatusBadNotReadable {
		t.Fatalf("notifications = %v", notifications)
	}
}

// A value waiting for its sampling interval describes a world that no longer
// exists once the source is gone, so invalidation supersedes it rather than
// letting it surface afterwards as though it were current.
func TestInvalidationDiscardsAHeldValue(t *testing.T) {
	runtime := &subscribingRuntime{revisedRate: 100 * time.Millisecond}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)
	monitorAt(t, service, id, ItemNodeID("Test/Int32"), 1, 5000)

	// The first value goes out; a second is held behind the sampling interval.
	runtime.latest().push(daNotification("Test/Int32", 1, QualityGood))
	publishOnce(t, service, channelEpoch)
	runtime.latest().push(daNotification("Test/Int32", 2, QualityGood))
	if held := publishNothing(t, service, channelEpoch.Add(time.Second)); held != 0 {
		t.Fatalf("the held value was reported early: %d", held)
	}

	runtime.latest().invalidate()
	lost := publishOnce(t, service, channelEpoch.Add(2*time.Second))
	if len(lost) != 1 || lost[0].Value.Status != StatusBadNotConnected {
		t.Fatalf("invalidation reported %v", lost)
	}

	// Long enough after that the held value would have been due.
	if stale := publishNothing(t, service, channelEpoch.Add(time.Minute)); stale != 0 {
		t.Fatalf("a value held before the disconnect surfaced afterwards: %d", stale)
	}
}

// Table 82: "when the publishing timer has expired this number of times without
// a Publish request being available to send a NotificationMessage, then the
// Subscription shall be deleted by the Server". For this adapter that means the
// DA group goes back too.
func TestASubscriptionOutlivesItsClientOnlyForItsLifetime(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	response, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
		Header:                      RequestHeader{AdditionalHeader: NullExtensionObject()},
		RequestedPublishingInterval: 100,
		RequestedMaxKeepAliveCount:  3,
		RequestedLifetimeCount:      9,
		PublishingEnabled:           true,
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	id := response.SubscriptionID
	// 9 cycles of 100ms is the lifetime the client was told it had.
	lifetime := time.Duration(response.RevisedLifetimeCount) * 100 * time.Millisecond
	monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)
	if service.Count() != 1 {
		t.Fatalf("subscriptions = %d", service.Count())
	}

	// Just inside the lifetime it survives.
	if expired := service.ExpireStale(context.Background(), channelEpoch.Add(lifetime-time.Millisecond)); expired != 0 {
		t.Fatalf("%d subscriptions expired early", expired)
	}
	if service.Count() != 1 {
		t.Fatal("the subscription was deleted inside its lifetime")
	}

	// Past it, the subscription goes and the DA group with it.
	if expired := service.ExpireStale(context.Background(), channelEpoch.Add(lifetime)); expired != 1 {
		t.Fatalf("%d subscriptions expired", expired)
	}
	if service.Count() != 0 {
		t.Fatal("the subscription outlived its lifetime")
	}
	if runtime.unsubscribeCount() != 1 {
		t.Fatal("the DA group was not released")
	}
}

// 5.14.1.1: "any Service call that uses the SubscriptionId or the processing of
// a Publish response resets the lifetime counter of this Subscription."
func TestUsingASubscriptionResetsItsLifetime(t *testing.T) {
	for _, testCase := range []struct {
		name string
		use  func(t *testing.T, service *SubscriptionService, id uint32, at time.Time)
	}{
		{"CreateMonitoredItems", func(t *testing.T, service *SubscriptionService, id uint32, at time.Time) {
			if _, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
				Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
				SubscriptionID:     id,
				TimestampsToReturn: TimestampsBoth,
				ItemsToCreate: []MonitoredItemCreateRequest{{
					ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Float"), AttributeID: AttributeValue},
					MonitoringMode: MonitoringModeReporting,
					RequestedParameters: MonitoringParameters{
						ClientHandle: 7, QueueSize: 1, Filter: NullExtensionObject(),
					},
				}},
			}, at); err != nil {
				t.Fatal(err)
			}
		}},
		{"SetPublishingMode", func(t *testing.T, service *SubscriptionService, id uint32, at time.Time) {
			if _, err := service.SetPublishingMode(testSession, SetPublishingModeRequest{
				Header:            RequestHeader{AdditionalHeader: NullExtensionObject()},
				PublishingEnabled: false,
				SubscriptionIDs:   []uint32{id},
			}, at); err != nil {
				t.Fatal(err)
			}
		}},
		{"a Publish cycle", func(t *testing.T, service *SubscriptionService, id uint32, at time.Time) {
			request := PublishRequest{Header: RequestHeader{AdditionalHeader: NullExtensionObject()}}
			if _, _, err := service.tryPublish(context.Background(), testSession, request, nil, at); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &subscribingRuntime{}
			service, _ := testSubscriptionService(t, runtime)
			response, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
				Header:                      RequestHeader{AdditionalHeader: NullExtensionObject()},
				RequestedPublishingInterval: 100,
				RequestedMaxKeepAliveCount:  3,
				RequestedLifetimeCount:      9,
				PublishingEnabled:           true,
			}, channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			id := response.SubscriptionID
			lifetime := time.Duration(response.RevisedLifetimeCount) * 100 * time.Millisecond
			monitorItem(t, service, id, ItemNodeID("Test/Int32"), 1)

			// Used just before it would have expired.
			testCase.use(t, service, id, channelEpoch.Add(lifetime-time.Millisecond))

			// The clock that would have killed it now finds it alive.
			if expired := service.ExpireStale(context.Background(), channelEpoch.Add(lifetime)); expired != 0 {
				t.Fatalf("using the subscription did not reset its lifetime")
			}
			// And it still expires a lifetime after that use.
			if expired := service.ExpireStale(context.Background(),
				channelEpoch.Add(2*lifetime)); expired != 1 {
				t.Fatalf("the subscription never expired: %d", expired)
			}
		})
	}
}

// Table 64 names Bad_TimestampsToReturnInvalid for CreateMonitoredItems too,
// and an out-of-range value reaches the service rather than dropping the
// connection at the decoder.
func TestCreateMonitoredItemsNamesTheParameterItRefuses(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, _ := testSubscriptionService(t, runtime)
	id := createSubscription(t, service)

	for _, timestamps := range []TimestampsToReturn{TimestampsInvalid, 6, -2} {
		_, err := service.CreateMonitoredItems(context.Background(), testSession,
			CreateMonitoredItemsRequest{
				Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
				SubscriptionID:     id,
				TimestampsToReturn: timestamps,
				ItemsToCreate: []MonitoredItemCreateRequest{{
					ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
					MonitoringMode: MonitoringModeReporting,
					RequestedParameters: MonitoringParameters{
						ClientHandle: 1, QueueSize: 1, Filter: NullExtensionObject(),
					},
				}},
			}, channelEpoch)
		if err == nil {
			t.Fatalf("timestampsToReturn %d was accepted", timestamps)
		}
		var codecErr *CodecError
		if !errors.As(err, &codecErr) || codecErr.Status != StatusBadTimestampsToReturnInvalid {
			t.Fatalf("timestampsToReturn %d = %v, want Bad_TimestampsToReturnInvalid", timestamps, err)
		}
	}

	// NEITHER is a Table 180 value and is accepted: a monitored item that wants
	// no timestamps is asking for something the table defines.
	result := monitorWithTimestamps(t, service, id, TimestampsNeither)
	if result.StatusCode != StatusGood {
		t.Fatalf("NEITHER = %s", result.StatusCode.Hex())
	}
}

func monitorWithTimestamps(t *testing.T, service *SubscriptionService, id uint32, timestamps TimestampsToReturn) MonitoredItemCreateResult {
	t.Helper()
	response, err := service.CreateMonitoredItems(context.Background(), testSession,
		CreateMonitoredItemsRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			SubscriptionID:     id,
			TimestampsToReturn: timestamps,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode: MonitoringModeReporting,
				RequestedParameters: MonitoringParameters{
					ClientHandle: 3, QueueSize: 1, Filter: NullExtensionObject(),
				},
			}},
		}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return response.Results[0]
}
