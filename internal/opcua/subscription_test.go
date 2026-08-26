package opcua

import (
	"context"
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
func (f *fakeDASubscription) push(values ...opcda.SubscriptionValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, values...)
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
	r.subscriptions = append(r.subscriptions, created)
	return created, nil
}

func (r *subscribingRuntime) Unsubscribe(_ context.Context, id opcda.SubscriptionID) error {
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
	response, err := service.CreateSubscription(testSession, CreateSubscriptionRequest{
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
	response, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
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

	runtime.latest().push(
		daNotification("Test/Int32", 1, QualityUncertain),
		daNotification("Test/Int32", 2, QualityCommFailure),
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
		{"unknown node", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: StringNodeID(AdapterNamespaceIndex, "item:missing"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{ClientHandle: 10, Filter: NullExtensionObject()},
		}, StatusBadNodeIdUnknown},
		{"a folder", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{
				NodeID: BranchNodeID([]string{"Folder"}), AttributeID: AttributeValue},
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
		{"a monitoring filter", MonitoredItemCreateRequest{
			ItemToMonitor: ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
			RequestedParameters: MonitoringParameters{
				ClientHandle: 14,
				Filter: ExtensionObject{
					TypeID: NumericNodeID(0, 583), Encoding: ExtensionObjectNoBody},
			},
		}, StatusBadFilterNotAllowed},
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

	runtime.latest().push(
		daNotification("Test/Int32", 1, QualityGood),
		daNotification("Test/Int32", 2, QualityGood),
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
