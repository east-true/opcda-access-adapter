package grpcfrontend

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// fakeSubscription mirrors the DA core's contract: a bounded pending set that
// coalesces per item, an edge-triggered update signal, and invalidation that
// discards whatever was pending.
type fakeSubscription struct {
	info opcda.SubscriptionInfo

	mu      sync.Mutex
	order   []opcda.DAItemID
	latest  map[opcda.DAItemID]opcda.SubscriptionValue
	closed  bool
	failure error

	notify chan struct{}
	done   chan struct{}
	drains atomic.Int64
}

func newFakeSubscription(info opcda.SubscriptionInfo) *fakeSubscription {
	return &fakeSubscription{
		info:   info,
		latest: make(map[opcda.DAItemID]opcda.SubscriptionValue),
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

func (subscription *fakeSubscription) Info() opcda.SubscriptionInfo { return subscription.info }
func (subscription *fakeSubscription) Updates() <-chan struct{}     { return subscription.notify }
func (subscription *fakeSubscription) Done() <-chan struct{}        { return subscription.done }

func (subscription *fakeSubscription) Err() error {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.failure
}

func (subscription *fakeSubscription) Drain() []opcda.SubscriptionValue {
	subscription.drains.Add(1)
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if len(subscription.order) == 0 {
		return nil
	}
	values := make([]opcda.SubscriptionValue, 0, len(subscription.order))
	for _, itemID := range subscription.order {
		values = append(values, subscription.latest[itemID])
	}
	subscription.order = nil
	clear(subscription.latest)
	return values
}

func (subscription *fakeSubscription) push(values ...opcda.SubscriptionValue) {
	subscription.mu.Lock()
	if subscription.closed {
		subscription.mu.Unlock()
		return
	}
	for _, value := range values {
		if _, exists := subscription.latest[value.ItemID]; !exists {
			subscription.order = append(subscription.order, value.ItemID)
		}
		subscription.latest[value.ItemID] = value
	}
	subscription.mu.Unlock()
	select {
	case subscription.notify <- struct{}{}:
	default:
	}
}

func (subscription *fakeSubscription) invalidate(err error) {
	subscription.mu.Lock()
	if subscription.closed {
		subscription.mu.Unlock()
		return
	}
	subscription.closed = true
	subscription.failure = err
	subscription.order = nil
	subscription.latest = nil
	subscription.mu.Unlock()
	close(subscription.done)
}

func subscriptionInfo(id opcda.SubscriptionID, items ...opcda.DAItemID) opcda.SubscriptionInfo {
	canonical := opcda.VTI4
	rights := opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	statuses := make([]opcda.SubscriptionItemStatus, len(items))
	for index, itemID := range items {
		statuses[index] = opcda.SubscriptionItemStatus{
			ItemID: itemID, Active: true,
			CanonicalType: &canonical, AccessRights: &rights,
			HRESULT: opcda.SOK, HRESULTPresent: true,
		}
	}
	return opcda.SubscriptionInfo{
		ID:                   id,
		ConnectionGeneration: 7,
		RequestedUpdateRate:  250 * time.Millisecond,
		RevisedUpdateRate:    300 * time.Millisecond,
		Items:                statuses,
		ActiveItemCount:      len(statuses),
	}
}

func notificationValue(itemID opcda.DAItemID, value int32, quality uint16, timestamp time.Time, present bool) opcda.SubscriptionValue {
	varType := opcda.VTI4
	canonical := opcda.VTI4
	rights := opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	return opcda.SubscriptionValue{
		ItemID:         itemID,
		VarType:        &varType,
		CanonicalType:  &canonical,
		AccessRights:   &rights,
		HRESULT:        opcda.SOK,
		HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: itemID, VarType: varType, Value: value,
			QualityRaw: quality, Timestamp: timestamp, TimestampPresent: present,
			HRESULT: opcda.SOK,
		},
	}
}

type subscribeHarness struct {
	runtime *testRuntime
	client  opcdav1.OPCDAAccessClient
}

func newSubscribeHarness(t *testing.T, runtime *testRuntime, config Config) *subscribeHarness {
	t.Helper()
	server := New(runtime, config)
	listener := bufconn.Listen(1 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		"passthrough:///bufconn",
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		<-serveDone
	})
	return &subscribeHarness{runtime: runtime, client: opcdav1.NewOPCDAAccessClient(connection)}
}

func subscribeRequest(items ...string) *opcdav1.DASubscribeRequest {
	encoded := make([]*opcdav1.DASubscribeItem, len(items))
	for index, itemID := range items {
		encoded[index] = &opcdav1.DASubscribeItem{ItemId: itemID}
	}
	return &opcdav1.DASubscribeRequest{Items: encoded, RequestedUpdateRateMs: 250}
}

func TestSubscribeStreamsCreatedThenPreservedUpdates(t *testing.T) {
	subscription := newFakeSubscription(subscriptionInfo("sub-7-1", "Exact.I4"))
	runtime := &testRuntime{subscribe: func(_ context.Context, request opcda.SubscribeRequest) (opcda.Subscription, error) {
		if len(request.Items) != 1 || request.Items[0] != "Exact.I4" {
			t.Errorf("request items = %v", request.Items)
		}
		if request.RequestedUpdateRate != 250*time.Millisecond {
			t.Errorf("requested rate = %s", request.RequestedUpdateRate)
		}
		return subscription, nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}

	first, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	created := first.GetCreated()
	if created == nil {
		t.Fatalf("first stream message = %+v, want created", first)
	}
	if created.SubscriptionId != "sub-7-1" || created.ConnectionGeneration != 7 {
		t.Fatalf("created identity = %+v", created)
	}
	// The server's revised rate is reported, never the requested one.
	if created.RequestedUpdateRateMs != 250 || created.RevisedUpdateRateMs != 300 {
		t.Fatalf("rates = requested %d revised %d", created.RequestedUpdateRateMs, created.RevisedUpdateRateMs)
	}
	if created.ActiveItemCount != 1 || len(created.Items) != 1 || !created.Items[0].Active {
		t.Fatalf("created items = %+v", created.Items)
	}
	if created.Items[0].CanonicalDataType == nil || created.Items[0].CanonicalDataType.Raw != uint32(opcda.VTI4) {
		t.Fatalf("canonical type = %+v", created.Items[0].CanonicalDataType)
	}

	timestamp := time.Date(2026, 8, 25, 1, 2, 3, 400, time.UTC)
	subscription.push(notificationValue("Exact.I4", 4242, 0x80C0, timestamp, true))

	second, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	update := second.GetUpdate()
	if update == nil || len(update.Values) != 1 {
		t.Fatalf("second stream message = %+v, want one update value", second)
	}
	value := update.Values[0]
	if value.ItemId != "Exact.I4" || !value.Ok {
		t.Fatalf("update value = %+v", value)
	}
	if value.QualityRaw != 0x80C0 || !value.QualityPresent {
		t.Fatalf("raw Quality = %d present=%t", value.QualityRaw, value.QualityPresent)
	}
	if !value.TimestampPresent || value.TimestampUnixSeconds != timestamp.Unix() || value.TimestampNanos != 400 {
		t.Fatalf("timestamp = %d.%d present=%t", value.TimestampUnixSeconds, value.TimestampNanos, value.TimestampPresent)
	}
	if value.DataType == nil || value.DataType.Raw != uint32(opcda.VTI4) {
		t.Fatalf("VARTYPE = %+v", value.DataType)
	}
	if scalar, ok := value.Value.Value.(*opcdav1.DAScalarValue_I4Value); !ok || scalar.I4Value != 4242 {
		t.Fatalf("scalar = %#v", value.Value.Value)
	}
}

func TestSubscribeUnsubscribesWhenClientCancels(t *testing.T) {
	subscription := newFakeSubscription(subscriptionInfo("sub-7-2", "Exact.I4"))
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return subscription, nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{})

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		released := runtime.unsubscribed()
		if len(released) == 1 && released[0] == "sub-7-2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("client cancellation did not release the subscription: %v", released)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubscribeEndsWhenSourceInvalidatesTheSubscription(t *testing.T) {
	subscription := newFakeSubscription(subscriptionInfo("sub-7-3", "Exact.I4"))
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return subscription, nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}

	// A pending value from the ending generation must never reach the client.
	subscription.push(notificationValue("Exact.I4", 1, 0xC0, time.Time{}, false))
	subscription.invalidate(opcda.NewAdapterError(
		opcda.CodeSubscriptionInvalidated,
		"OPC DA subscription was invalidated by source disconnect; explicit resubscribe is required",
	))

	for {
		message, err := stream.Recv()
		if err == nil {
			// A racing update that was already drained is acceptable; keep
			// reading until the stream terminates.
			if message.GetUpdate() == nil {
				t.Fatalf("unexpected stream message after invalidation: %+v", message)
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			t.Fatal("invalidated subscription closed the stream without an error")
		}
		// The client is told explicitly and must resubscribe; Aborted keeps the
		// stream from looking transparently retryable.
		if status.Code(err) != codes.Aborted {
			t.Fatalf("stream end code = %s, err = %v", status.Code(err), err)
		}
		detail := operationErrorDetail(t, err)
		if detail.Code != string(opcda.CodeSubscriptionInvalidated) {
			t.Fatalf("detail code = %s", detail.Code)
		}
		return
	}
}

func TestSubscribeRejectsInvalidRequests(t *testing.T) {
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		t.Error("an invalid Subscribe request reached the runtime")
		return nil, errors.New("unreachable")
	}}
	harness := newSubscribeHarness(t, runtime, Config{MaxSubscribeItems: 2, MaxItemIDBytes: 16})

	cases := []struct {
		name    string
		request *opcdav1.DASubscribeRequest
		code    codes.Code
	}{
		{"no items", &opcdav1.DASubscribeRequest{RequestedUpdateRateMs: 250}, codes.InvalidArgument},
		{"too many items", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: "a"}, {ItemId: "b"}, {ItemId: "c"}},
			RequestedUpdateRateMs: 250,
		}, codes.ResourceExhausted},
		{"zero rate", &opcdav1.DASubscribeRequest{Items: []*opcdav1.DASubscribeItem{{ItemId: "a"}}}, codes.InvalidArgument},
		{"rate too large", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: "a"}},
			RequestedUpdateRateMs: 3_600_001,
		}, codes.InvalidArgument},
		{"deadband out of range", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: "a"}},
			RequestedUpdateRateMs: 250, PercentDeadband: 101,
		}, codes.InvalidArgument},
		{"empty item", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: ""}},
			RequestedUpdateRateMs: 250,
		}, codes.InvalidArgument},
		{"item id too long", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: strings.Repeat("x", 17)}},
			RequestedUpdateRateMs: 250,
		}, codes.InvalidArgument},
		{"NUL in item id", &opcdav1.DASubscribeRequest{
			Items:                 []*opcdav1.DASubscribeItem{{ItemId: "a\x00b"}},
			RequestedUpdateRateMs: 250,
		}, codes.InvalidArgument},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := harness.client.Subscribe(ctx, testCase.request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != testCase.code {
				t.Fatalf("code = %s, err = %v", status.Code(err), err)
			}
		})
	}
}

func TestSubscribeBoundsConcurrentStreams(t *testing.T) {
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return newFakeSubscription(subscriptionInfo("sub-7-4", "Exact.I4")), nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{MaxSubscriptionStreams: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Recv(); err != nil {
		t.Fatal(err)
	}

	second, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.Recv()
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second stream code = %s, err = %v", status.Code(err), err)
	}
	if detail := operationErrorDetail(t, err); detail.Code != string(opcda.CodeSubscriptionLimit) {
		t.Fatalf("detail code = %s", detail.Code)
	}
}

// The unary request deadline must not apply to a long-lived stream.
func TestSubscribeStreamOutlivesTheUnaryRequestDeadline(t *testing.T) {
	subscription := newFakeSubscription(subscriptionInfo("sub-7-5", "Exact.I4"))
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return subscription, nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{RequestDeadline: 50 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)
	subscription.push(notificationValue("Exact.I4", 9, 0xC0, time.Time{}, false))
	message, err := stream.Recv()
	if err != nil {
		t.Fatalf("stream ended after the unary deadline: %v", err)
	}
	if update := message.GetUpdate(); update == nil || len(update.Values) != 1 {
		t.Fatalf("message after the unary deadline = %+v", message)
	}
}

// The handler must hold no buffer of its own: every message it sends is exactly
// one drain of the DA core's pending set.
func TestSubscribeSendsOneDrainPerMessage(t *testing.T) {
	subscription := newFakeSubscription(subscriptionInfo("sub-7-6", "Exact.I4"))
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return subscription, nil
	}}
	harness := newSubscribeHarness(t, runtime, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}

	const pushes = 25
	for value := 0; value < pushes; value++ {
		subscription.push(notificationValue("Exact.I4", int32(value), 0xC0, time.Time{}, false))
	}

	received := 0
	var last int32
	for {
		// The final coalesced value must arrive; intermediate ones may have been
		// coalesced away exactly as a DA server coalesces between ticks.
		message, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream ended early after %d updates: %v", received, err)
		}
		update := message.GetUpdate()
		if update == nil || len(update.Values) != 1 {
			t.Fatalf("message = %+v", message)
		}
		scalar, ok := update.Values[0].Value.Value.(*opcdav1.DAScalarValue_I4Value)
		if !ok {
			t.Fatalf("scalar = %#v", update.Values[0].Value.Value)
		}
		received++
		last = scalar.I4Value
		if last == pushes-1 {
			break
		}
		if received > pushes {
			t.Fatalf("received %d updates for %d coalescing pushes", received, pushes)
		}
	}
	// Coalescing means fewer messages than pushes are expected, never more.
	if received > pushes {
		t.Fatalf("received %d updates for %d pushes", received, pushes)
	}
	// Every delivered message came from its own drain, so the handler cannot be
	// holding a buffer that amplifies one drain into several messages. An empty
	// drain is legitimate: the update signal is an edge, not a count.
	if drains := subscription.drains.Load(); drains < int64(received) {
		t.Fatalf("drains = %d for %d delivered messages", drains, received)
	}
}

// A DA server without an IOPCDataCallback connection point must be reported as
// unsupported rather than failing late with a generic source error.
func TestSubscribeReportsSourcesWithoutCallbackSupport(t *testing.T) {
	runtime := &testRuntime{
		status: opcda.RuntimeStatus{Capabilities: opcda.Capabilities{Browse: "supported", Read: true, Write: true}},
		subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
			return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "OPC DA server does not support callback subscriptions")
		},
	}
	harness := newSubscribeHarness(t, runtime, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	statusResponse, err := harness.client.Status(ctx, &opcdav1.DAStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if statusResponse.Capabilities == nil || statusResponse.Capabilities.Subscribe {
		t.Fatal("a source without callback support advertised the Subscribe capability")
	}

	stream, err := harness.client.Subscribe(ctx, subscribeRequest("Exact.I4"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("code = %s, err = %v", status.Code(err), err)
	}
	if detail := operationErrorDetail(t, err); detail.Code != string(opcda.CodeSubscribeUnsupported) {
		t.Fatalf("detail code = %s", detail.Code)
	}
	// A refused subscription must not leave a release attempt behind.
	if released := runtime.unsubscribed(); len(released) != 0 {
		t.Fatalf("an unsupported Subscribe released %v", released)
	}
}

func operationErrorDetail(t *testing.T, err error) *opcdav1.DAOperationError {
	t.Helper()
	for _, detail := range status.Convert(err).Details() {
		if operationError, ok := detail.(*opcdav1.DAOperationError); ok {
			return operationError
		}
	}
	t.Fatalf("error carried no DAOperationError detail: %v", err)
	return nil
}
