package grpcfrontend

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testRuntime struct {
	available      []opcda.AvailableProperty
	propertyValues map[opcda.PropertyID]opcda.ItemPropertyValue

	status     opcda.RuntimeStatus
	browse     func(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error)
	read       func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error)
	write      func(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error)
	writeCalls int

	subscribe        func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error)
	unsubscribe      func(context.Context, opcda.SubscriptionID) error
	unsubscribedMu   sync.Mutex
	unsubscribedList []opcda.SubscriptionID
}

func (runtime *testRuntime) Status(context.Context) opcda.RuntimeStatus { return runtime.status }
func (runtime *testRuntime) Browse(ctx context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
	if runtime.browse == nil {
		return opcda.BrowseResult{}, nil
	}
	return runtime.browse(ctx, request)
}
func (runtime *testRuntime) ReadBatch(ctx context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	if runtime.read == nil {
		return nil, nil
	}
	return runtime.read(ctx, request)
}
func (runtime *testRuntime) WriteBatch(ctx context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	runtime.writeCalls++
	if runtime.write == nil {
		return nil, nil
	}
	return runtime.write(ctx, items)
}

func (runtime *testRuntime) Subscribe(ctx context.Context, request opcda.SubscribeRequest) (opcda.Subscription, error) {
	if runtime.subscribe == nil {
		return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "subscribe is not configured in this test")
	}
	return runtime.subscribe(ctx, request)
}

func (runtime *testRuntime) Unsubscribe(ctx context.Context, id opcda.SubscriptionID) error {
	runtime.unsubscribedMu.Lock()
	runtime.unsubscribedList = append(runtime.unsubscribedList, id)
	runtime.unsubscribedMu.Unlock()
	if runtime.unsubscribe == nil {
		return nil
	}
	return runtime.unsubscribe(ctx, id)
}

func (runtime *testRuntime) unsubscribed() []opcda.SubscriptionID {
	runtime.unsubscribedMu.Lock()
	defer runtime.unsubscribedMu.Unlock()
	return append([]opcda.SubscriptionID(nil), runtime.unsubscribedList...)
}

func (*testRuntime) Shutdown(context.Context) error { return nil }

func TestReadPreservesDAWidthsQualityTimestampHRESULTAndPartialFailure(t *testing.T) {
	actual := opcda.VTI2
	canonical := opcda.VTI4
	timestamp := time.Date(2026, 8, 25, 1, 2, 3, 400, time.FixedZone("source", 9*60*60))
	unknownItem := opcda.HRESULT(-1073479673) // 0xC0040007
	runtime := &testRuntime{read: func(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
		if request.Source != opcda.DADataSourceDevice || len(request.Items) != 2 {
			t.Fatalf("request = %+v", request)
		}
		return []opcda.ReadResult{
			{
				ItemID: "Exact.I2", VarType: &actual, CanonicalType: &canonical,
				AccessRights: &opcda.DAAccessRights{Raw: 3, Read: true, Write: true},
				HRESULT:      opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{ItemID: "Exact.I2", VarType: actual, Value: int16(-123), QualityRaw: 0x80C0, Timestamp: timestamp, TimestampPresent: true, HRESULT: opcda.SOK},
			},
			{ItemID: "Missing", HRESULT: unknownItem, HRESULTPresent: true, ErrorCode: "OPC_E_UNKNOWNITEMID"},
		}, nil
	}}
	server := New(runtime, Config{})
	response, err := server.Read(context.Background(), &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Exact.I2"}, {ItemId: "Missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 2 {
		t.Fatalf("results=%d", len(response.Results))
	}
	first := response.Results[0]
	if !first.Ok || first.ItemId != "Exact.I2" || first.DataType.Raw != uint32(opcda.VTI2) || first.DataType.Name != "VT_I2" {
		t.Fatalf("first result = %+v", first)
	}
	if first.CanonicalDataType.Raw != uint32(opcda.VTI4) || first.QualityRaw != 0x80C0 || !first.QualityPresent || !first.TimestampPresent {
		t.Fatalf("metadata = %+v", first)
	}
	if first.TimestampUnixSeconds != timestamp.Unix() || first.TimestampNanos != int32(timestamp.Nanosecond()) {
		t.Fatalf("timestamp = %d/%d", first.TimestampUnixSeconds, first.TimestampNanos)
	}
	if value, ok := first.Value.Value.(*opcdav1.DAScalarValue_I2Value); !ok || value.I2Value != -123 {
		t.Fatalf("typed value = %#v", first.Value.Value)
	}
	if first.Hresult.Value != 0 || first.Hresult.Raw != 0 || first.Hresult.Hex != "0x00000000" || first.AccessRights.Raw != 3 {
		t.Fatalf("HRESULT/rights = %+v %+v", first.Hresult, first.AccessRights)
	}
	second := response.Results[1]
	if second.Ok || second.ItemId != "Missing" || second.ErrorCode != "OPC_E_UNKNOWNITEMID" || second.Hresult.Raw != 0xC0040007 || second.Hresult.Value >= 0 {
		t.Fatalf("partial failure = %+v", second)
	}
}

func TestReadPreservesSpecialFloatAndTimestampAbsence(t *testing.T) {
	actual := opcda.VTR8
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return []opcda.ReadResult{{
			ItemID: "R8", VarType: &actual, HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{ItemID: "R8", VarType: actual, Value: math.Inf(1), QualityRaw: 0, TimestampPresent: false, HRESULT: opcda.SOK},
		}}, nil
	}}
	response, err := New(runtime, Config{}).Read(context.Background(), &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "R8"}}})
	if err != nil {
		t.Fatal(err)
	}
	result := response.Results[0]
	value, ok := result.Value.Value.(*opcdav1.DAScalarValue_R8Value)
	if !ok || !math.IsInf(value.R8Value, 1) || result.TimestampPresent || !result.QualityPresent || result.QualityRaw != 0 {
		t.Fatalf("result = %+v value=%#v", result, result.Value.Value)
	}
}

func TestBrowsePreservesDAIdentityAndOptionalMetadata(t *testing.T) {
	canonical := opcda.VTBSTR
	itemID := opcda.DAItemID("Channel.Device.Tag")
	runtime := &testRuntime{browse: func(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
		if request.Filter != opcda.BrowseFilterAll || len(request.Path) != 1 || request.Path[0] != "Channel" {
			t.Fatalf("request=%+v", request)
		}
		return opcda.BrowseResult{Path: request.Path, Entries: []opcda.BrowseEntry{
			{Kind: opcda.BrowseEntryBranch, Name: "Device"},
			{Kind: opcda.BrowseEntryItem, Name: "Tag", ItemID: &itemID, CanonicalType: &canonical, AccessRights: &opcda.DAAccessRights{Raw: 1, Read: true}},
		}}, nil
	}}
	response, err := New(runtime, Config{}).Browse(context.Background(), &opcdav1.DABrowseRequest{Path: []string{"Channel"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 || response.Entries[0].ItemIdPresent || response.Entries[1].ItemId != string(itemID) || !response.Entries[1].ItemIdPresent || response.Entries[1].CanonicalDataType.Name != "VT_BSTR" {
		t.Fatalf("response = %+v", response)
	}
}

func TestWriteRequiresEnabledExactVARTYPEAndWidth(t *testing.T) {
	runtime := &testRuntime{status: opcda.RuntimeStatus{WriteEnabled: true}}
	runtime.write = func(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
		if len(items) != 1 || items[0].ItemID != "I2" || items[0].VarType != opcda.VTI2 || items[0].Value != int16(42) {
			t.Fatalf("items = %+v", items)
		}
		return []opcda.WriteResult{{ItemID: "I2", HRESULT: opcda.SFalse, HRESULTPresent: true}}, nil
	}
	server := New(runtime, Config{})
	request := &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "I2", DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI2), Name: "VT_I2"},
		Value: &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: 42}},
	}}}
	response, err := server.Write(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || !response.Results[0].Ok || response.Results[0].Hresult.Value != int32(opcda.SFalse) {
		t.Fatalf("response = %+v", response)
	}

	request.Items[0].Value = &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: math.MaxInt16 + 1}}
	_, err = server.Write(context.Background(), request)
	detail := assertGRPCDetail(t, err, codes.InvalidArgument, string(opcda.CodeTypeMismatch))
	if detail.Layer != "frontend" {
		t.Fatalf("overflow layer = %q", detail.Layer)
	}
	if runtime.writeCalls != 1 {
		t.Fatalf("Write calls after overflow = %d", runtime.writeCalls)
	}

	disabled := &testRuntime{}
	_, err = New(disabled, Config{}).Write(context.Background(), request)
	assertGRPCDetail(t, err, codes.PermissionDenied, string(opcda.CodeWriteDisabled))
	if disabled.writeCalls != 0 {
		t.Fatalf("disabled Write calls = %d", disabled.writeCalls)
	}
}

func TestMethodSourceHRESULTIsTypedGRPCDetail(t *testing.T) {
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return nil, &opcda.SourceError{Operation: "IOPCSyncIO::Read", HRESULT: opcda.HRESULT(-2147467259)}
	}}
	_, err := New(runtime, Config{}).Read(context.Background(), &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "A"}}})
	detail := assertGRPCDetail(t, err, codes.Unavailable, "DA_METHOD_FAILED")
	if detail.Layer != "source" || detail.Operation != "IOPCSyncIO::Read" || detail.Hresult == nil || detail.Hresult.Raw != 0x80004005 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestReadUnsupportedVARTYPEPreservesSourceMetadata(t *testing.T) {
	actual := opcda.VTCY
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return []opcda.ReadResult{{
			ItemID: "Currency", VarType: &actual, HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{ItemID: "Currency", VarType: actual, Value: int64(1234), QualityRaw: 0x40, TimestampPresent: false, HRESULT: opcda.SOK},
		}}, nil
	}}
	response, err := New(runtime, Config{}).Read(context.Background(), &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Currency"}}})
	if err != nil {
		t.Fatal(err)
	}
	result := response.Results[0]
	if result.Ok || result.ErrorCode != string(opcda.CodeUnsupportedVarType) || result.DataType == nil || result.DataType.Raw != uint32(opcda.VTCY) ||
		result.Hresult == nil || result.Hresult.Raw != 0 || !result.QualityPresent || result.QualityRaw != 0x40 || result.TimestampPresent || result.Value != nil {
		t.Fatalf("unsupported result = %+v", result)
	}
}

func TestBrowseUnsupportedAndReadLimitKeepTypedLayers(t *testing.T) {
	runtime := &testRuntime{browse: func(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
		return opcda.BrowseResult{}, opcda.NewAdapterError(opcda.CodeBrowseUnsupported, "source does not expose DA Browse")
	}}
	server := New(runtime, Config{MaxReadItems: 1})
	_, err := server.Browse(context.Background(), &opcdav1.DABrowseRequest{})
	detail := assertGRPCDetail(t, err, codes.Unimplemented, string(opcda.CodeBrowseUnsupported))
	if detail.Layer != "adapter" {
		t.Fatalf("Browse unsupported layer = %q", detail.Layer)
	}

	_, err = server.Read(context.Background(), &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "A"}, {ItemId: "B"}}})
	detail = assertGRPCDetail(t, err, codes.ResourceExhausted, string(opcda.CodeRequestLimitExceeded))
	if detail.Layer != "frontend" {
		t.Fatalf("Read limit layer = %q", detail.Layer)
	}
}

func TestUnaryBackpressureIsBoundedAndRecovers(t *testing.T) {
	server := New(&testRuntime{}, Config{MaxConcurrent: 1})
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := server.boundUnary(context.Background(), struct{}{}, &grpcgo.UnaryServerInfo{}, func(context.Context, any) (any, error) {
			close(entered)
			<-release
			return struct{}{}, nil
		})
		done <- err
	}()
	<-entered
	_, err := server.boundUnary(context.Background(), struct{}{}, &grpcgo.UnaryServerInfo{}, func(context.Context, any) (any, error) { return nil, nil })
	assertGRPCDetail(t, err, codes.ResourceExhausted, string(opcda.CodeQueueFull))
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := server.boundUnary(context.Background(), struct{}{}, &grpcgo.UnaryServerInfo{}, func(context.Context, any) (any, error) { return struct{}{}, nil }); err != nil {
		t.Fatalf("did not recover: %v", err)
	}
}

func assertGRPCDetail(t *testing.T, err error, code codes.Code, errorCode string) *opcdav1.DAOperationError {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("code=%s err=%v", status.Code(err), err)
	}
	for _, raw := range status.Convert(err).Details() {
		if detail, ok := raw.(*opcdav1.DAOperationError); ok {
			if detail.Code != errorCode {
				t.Fatalf("detail code=%q want=%q", detail.Code, errorCode)
			}
			return detail
		}
	}
	t.Fatalf("DAOperationError detail missing: %v", err)
	return nil
}

func (r *testRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	if r.available == nil {
		return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
	}
	return r.available, nil
}

func (r *testRuntime) ItemProperties(_ context.Context, request opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	if r.propertyValues == nil {
		return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
	}
	values := make([]opcda.ItemPropertyValue, 0, len(request.Properties))
	for _, id := range request.Properties {
		value := r.propertyValues[id]
		value.ID = id
		values = append(values, value)
	}
	return values, nil
}

// The DA-native frontends publish capabilities.properties, so a client told a
// source supports item properties has to be able to ask for them. Before this
// the capability named something only the OPC UA frontend could use.
//
// Being DA-native, the frontend passes the source's property identifiers,
// VARTYPEs, description text and HRESULTs through rather than mapping them.
func TestItemPropertiesArePassedThroughUnchanged(t *testing.T) {
	runtime := &testRuntime{
		available: []opcda.AvailableProperty{
			{ID: opcda.PropertyEUUnits, Description: "EU Units", VarType: opcda.VTBSTR},
			{ID: opcda.PropertyHighEU, Description: "High EU", VarType: opcda.VTR8},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
			opcda.PropertyEUUnits: {OK: true, VarType: opcda.VTBSTR, VarTypePresent: true,
				Value: "degC", ValuePresent: true, HRESULTPresent: true},
			// A property the source refuses is a result, not a failure.
			opcda.PropertyHighEU: {HRESULT: -1073479674, HRESULTPresent: true},
		},
	}
	server := New(runtime, Config{})

	available, err := server.AvailableItemProperties(context.Background(),
		&opcdav1.DAAvailableItemPropertiesRequest{ItemId: "Test/Float"})
	if err != nil {
		t.Fatalf("AvailableItemProperties: %v", err)
	}
	if available.ItemId != "Test/Float" || len(available.Properties) != 2 {
		t.Fatalf("available = %+v", available)
	}
	// The source's own order, its own identifiers, its own description text.
	if available.Properties[0].PropertyId != 100 || available.Properties[0].Description != "EU Units" {
		t.Fatalf("first property = %+v", available.Properties[0])
	}
	if available.Properties[0].DataType == nil || available.Properties[0].DataType.Raw != uint32(opcda.VTBSTR) {
		t.Fatalf("first property data type = %+v", available.Properties[0].DataType)
	}

	values, err := server.ItemProperties(context.Background(), &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float", PropertyIds: []uint32{100, 102},
	})
	if err != nil {
		t.Fatalf("ItemProperties: %v", err)
	}
	if len(values.Results) != 2 {
		t.Fatalf("results = %d", len(values.Results))
	}
	granted, refused := values.Results[0], values.Results[1]
	if !granted.Ok || granted.PropertyId != 100 || granted.GetValue().GetBstrValue() != "degC" {
		t.Fatalf("granted = %+v", granted)
	}
	// The source's exact HRESULT survives, and nothing is substituted for the
	// value it did not give.
	if refused.Ok || refused.PropertyId != 102 {
		t.Fatalf("refused = %+v", refused)
	}
	if refused.Hresult == nil || refused.Hresult.Raw != 0xC0040006 || refused.Value != nil {
		t.Fatalf("refused HRESULT = %+v value = %+v", refused.Hresult, refused.Value)
	}
}

// The frontend bounds what it forwards, the way every other request is bounded,
// and a source that offers no properties says so rather than failing.
func TestItemPropertyRequestsAreBoundedAndCapabilityAware(t *testing.T) {
	runtime := &testRuntime{propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{}}
	server := New(runtime, Config{})
	ctx := context.Background()

	if _, err := server.AvailableItemProperties(ctx, &opcdav1.DAAvailableItemPropertiesRequest{ItemId: ""}); err == nil {
		t.Fatal("an empty ItemID was accepted")
	}
	if _, err := server.ItemProperties(ctx, &opcdav1.DAItemPropertiesRequest{ItemId: "Test/Float"}); err == nil {
		t.Fatal("an empty property list was accepted")
	}
	tooMany := make([]uint32, opcda.DefaultLimits().MaxItemProperties+1)
	for index := range tooMany {
		tooMany[index] = 100
	}
	if _, err := server.ItemProperties(ctx, &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float", PropertyIds: tooMany,
	}); err == nil {
		t.Fatal("a property list over the configured limit was accepted")
	}

	// A source without IOPCItemProperties is working correctly, and the error
	// carries that rather than looking like a runtime failure.
	// A source without IOPCItemProperties is the same situation as one without
	// IOPCBrowseServerAddressSpace, and answers the same way.
	unsupported := New(&testRuntime{}, Config{})
	_, err := unsupported.AvailableItemProperties(ctx, &opcdav1.DAAvailableItemPropertiesRequest{ItemId: "Test/Float"})
	assertGRPCDetail(t, err, codes.Unimplemented, string(opcda.CodePropertiesUnsupported))
}
