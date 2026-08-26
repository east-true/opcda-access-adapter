package opcua

import (
	"context"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// stubRuntime stands in for the DA runtime so the mapping can be exercised
// without a DA server.
type stubRuntime struct {
	readRequest  opcda.ReadRequest
	readResults  []opcda.ReadResult
	readErr      error
	writeItems   []opcda.WriteItem
	writeResults []opcda.WriteResult
	writeErr     error
}

func (r *stubRuntime) Status(context.Context) opcda.RuntimeStatus { return opcda.RuntimeStatus{} }
func (r *stubRuntime) Browse(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return opcda.BrowseResult{}, nil
}
func (r *stubRuntime) ReadBatch(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	r.readRequest = request
	return r.readResults, r.readErr
}
func (r *stubRuntime) WriteBatch(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	r.writeItems = items
	return r.writeResults, r.writeErr
}
func (r *stubRuntime) Subscribe(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
	return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "not used here")
}
func (r *stubRuntime) Unsubscribe(context.Context, opcda.SubscriptionID) error { return nil }
func (r *stubRuntime) Shutdown(context.Context) error                          { return nil }

func testDataService(t *testing.T, runtime *stubRuntime) (*DataAccessService, *AddressSpace) {
	t.Helper()
	space := testAddressSpace(t)
	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	readOnly := &opcda.DAAccessRights{Raw: 1, Read: true}
	noAccess := &opcda.DAAccessRights{Raw: 0}
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4), AccessRights: rights},
		{Kind: opcda.BrowseEntryItem, Name: "String", ItemID: itemID("Test/String"),
			CanonicalType: varType(opcda.VTBSTR), AccessRights: readOnly},
		{Kind: opcda.BrowseEntryItem, Name: "Closed", ItemID: itemID("Test/Closed"),
			CanonicalType: varType(opcda.VTI4), AccessRights: noAccess},
		// OPC DA carries access rights in AddItems, not in Browse, so a browsed
		// item normally arrives without them.
		{Kind: opcda.BrowseEntryItem, Name: "Unknown", ItemID: itemID("Test/Unknown"),
			CanonicalType: varType(opcda.VTI4)},
		{Kind: opcda.BrowseEntryBranch, Name: "Folder"},
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewDataAccessService(space, runtime, DefaultDataAccessLimits())
	if err != nil {
		t.Fatalf("NewDataAccessService: %v", err)
	}
	return service, space
}

func readValue(node NodeID) ReadValueID {
	return ReadValueID{NodeID: node, AttributeID: AttributeValue}
}

func readRequestFor(values ...ReadValueID) ReadRequest {
	return ReadRequest{
		Header:             RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		TimestampsToReturn: TimestampsBoth,
		NodesToRead:        values,
	}
}

func TestReadWriteIdentifiersAndEnums(t *testing.T) {
	ids := map[uint32]uint32{
		ReadRequestEncodingID: 631, ReadResponseEncodingID: 634,
		WriteRequestEncodingID: 673, WriteResponseEncodingID: 676,
	}
	for got, want := range ids {
		if got != want {
			t.Fatalf("encoding id %d, want %d", got, want)
		}
	}
	timestamps := map[TimestampsToReturn]int32{
		TimestampsSource: 0, TimestampsServer: 1, TimestampsBoth: 2,
		TimestampsNeither: 3, TimestampsInvalid: 4,
	}
	for value, want := range timestamps {
		if int32(value) != want {
			t.Fatalf("TimestampsToReturn %d, want %d", int32(value), want)
		}
	}
}

// The Part 8 mapping decides the status: the DA quality becomes the UA status
// and the DA timestamp becomes the SourceTimestamp.
func TestReadMapsDAValueQualityAndTimestamp(t *testing.T) {
	sourceTime := time.Date(2026, time.August, 26, 1, 2, 3, 400, time.UTC)
	runtime := &stubRuntime{}
	service, space := testDataService(t, runtime)

	varTypeI4 := opcda.VTI4
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Int32", VarType: varTypeI4, Value: int32(4242),
			QualityRaw: QualityGood, Timestamp: sourceTime, TimestampPresent: true,
		},
	}}

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	node := ItemNodeID("Test/Int32")
	response, err := service.Read(context.Background(), readRequestFor(readValue(node)), now)
	if err != nil {
		t.Fatal(err)
	}
	// The DA core is asked for a device read, never a cache read.
	if runtime.readRequest.Source != opcda.DADataSourceDevice {
		t.Fatalf("source = %s, want device", runtime.readRequest.Source)
	}
	if len(runtime.readRequest.Items) != 1 || runtime.readRequest.Items[0] != "Test/Int32" {
		t.Fatalf("items = %v", runtime.readRequest.Items)
	}

	result := response.Results[0]
	if result.Status != StatusGood {
		t.Fatalf("status = %s", result.Status.Hex())
	}
	if result.Value.Type != BuiltInInt32 || result.Value.Value != int32(4242) {
		t.Fatalf("value = %+v", result.Value)
	}
	if !result.SourceTimestamp.Equal(sourceTime) {
		t.Fatalf("source timestamp = %s, want the DA timestamp", result.SourceTimestamp)
	}
	if !result.ServerTimestamp.Equal(now) {
		t.Fatalf("server timestamp = %s, want the adapter's time", result.ServerTimestamp)
	}
	_ = space
}

// An absent DA timestamp is preserved rather than filled in with the adapter's
// own clock.
func TestReadPreservesAbsentSourceTimestamp(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	varTypeI4 := opcda.VTI4
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Int32", VarType: varTypeI4, Value: int32(1),
			QualityRaw: QualityGood, TimestampPresent: false,
		},
	}}
	now := time.Now().UTC()
	response, err := service.Read(context.Background(), readRequestFor(readValue(ItemNodeID("Test/Int32"))), now)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Results[0].SourceTimestamp.IsZero() {
		t.Fatalf("an absent source timestamp was filled in: %s", response.Results[0].SourceTimestamp)
	}
	if response.Results[0].ServerTimestamp.IsZero() {
		t.Fatal("the server timestamp is missing")
	}
}

// Non-Good quality maps through Part 8 Table A.3 and a bad status carries no
// value, as Table 131 requires.
func TestReadMapsNonGoodQuality(t *testing.T) {
	cases := []struct {
		name     string
		quality  uint16
		want     StatusCode
		hasValue bool
	}{
		{"good", QualityGood, StatusGood, true},
		{"uncertain", QualityUncertain, StatusUncertain, true},
		{"last usable", QualityLastUsable, StatusUncertainLastUsableValue, true},
		{"bad", QualityBad, StatusBad, false},
		{"comm failure", QualityCommFailure, StatusBadNoCommunication, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &stubRuntime{}
			service, _ := testDataService(t, runtime)
			varTypeI4 := opcda.VTI4
			runtime.readResults = []opcda.ReadResult{{
				ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{
					ItemID: "Test/Int32", VarType: varTypeI4, Value: int32(7),
					QualityRaw: testCase.quality,
				},
			}}
			response, err := service.Read(context.Background(),
				readRequestFor(readValue(ItemNodeID("Test/Int32"))), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			result := response.Results[0]
			if result.Status != testCase.want {
				t.Fatalf("status = %s, want %s", result.Status.Hex(), testCase.want.Hex())
			}
			if result.Value.IsNull() == testCase.hasValue {
				t.Fatalf("value presence = %t for status %s", !result.Value.IsNull(), result.Status.Hex())
			}
		})
	}
}

// A per-item DA HRESULT maps through Part 8 Table A.4.
func TestReadMapsPerItemHRESULT(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Int32", HRESULT: OPCEUnknownItemID, HRESULTPresent: true,
		ErrorCode: "OPC_E_UNKNOWNITEMID",
	}}
	response, err := service.Read(context.Background(),
		readRequestFor(readValue(ItemNodeID("Test/Int32"))), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusBadNodeIdUnknown {
		t.Fatalf("status = %s, want Bad_NodeIdUnknown", response.Results[0].Status.Hex())
	}
	if !response.Results[0].Value.IsNull() {
		t.Fatal("a failed read carried a value")
	}
}

// Scalar widths survive the mapping: the Go type the DA core produced decides
// the built-in type, so nothing is widened or narrowed.
func TestReadPreservesScalarWidths(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  BuiltInTypeID
	}{
		{"int8", int8(-5), BuiltInSByte},
		{"uint8", uint8(200), BuiltInByte},
		{"int16", int16(-300), BuiltInInt16},
		{"uint16", uint16(60000), BuiltInUInt16},
		{"int32", int32(-70000), BuiltInInt32},
		{"uint32", uint32(4000000000), BuiltInUInt32},
		{"int64", int64(-5000000000), BuiltInInt64},
		{"uint64", uint64(18000000000000000000), BuiltInUInt64},
		{"float32", float32(1.5), BuiltInFloat},
		{"float64", float64(2.25), BuiltInDouble},
		{"bool", true, BuiltInBoolean},
		{"string", "text", BuiltInString},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &stubRuntime{}
			service, _ := testDataService(t, runtime)
			varTypeI4 := opcda.VTI4
			runtime.readResults = []opcda.ReadResult{{
				ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{ItemID: "Test/Int32", Value: testCase.value, QualityRaw: QualityGood},
			}}
			response, err := service.Read(context.Background(),
				readRequestFor(readValue(ItemNodeID("Test/Int32"))), time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			result := response.Results[0]
			if result.Value.Type != testCase.want {
				t.Fatalf("built-in type = %d, want %d", result.Value.Type, testCase.want)
			}
			if result.Value.Value != testCase.value {
				t.Fatalf("value = %v, want %v", result.Value.Value, testCase.value)
			}
		})
	}
}

// The results match nodesToRead in size and order, so a node that cannot be
// read occupies its slot rather than shortening the list.
func TestReadResultsKeepRequestOrder(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	varTypeI4 := opcda.VTI4
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{ItemID: "Test/Int32", Value: int32(1), QualityRaw: QualityGood},
	}}

	response, err := service.Read(context.Background(), readRequestFor(
		readValue(StringNodeID(AdapterNamespaceIndex, "item:missing")),
		readValue(ItemNodeID("Test/Int32")),
		readValue(ItemNodeID("Test/Closed")),
	), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(response.Results))
	}
	if response.Results[0].Status != StatusBadNodeIdUnknown {
		t.Fatalf("unknown node = %s", response.Results[0].Status.Hex())
	}
	if response.Results[1].Status != StatusGood {
		t.Fatalf("readable node = %s", response.Results[1].Status.Hex())
	}
	// A right the source actually reported is enforced without asking it again.
	if response.Results[2].Status != StatusBadNotReadable {
		t.Fatalf("unreadable node = %s, want Bad_NotReadable", response.Results[2].Status.Hex())
	}
	// Only the readable node reached the source.
	if len(runtime.readRequest.Items) != 1 {
		t.Fatalf("the source was asked for %d items", len(runtime.readRequest.Items))
	}
}

// An item whose rights the source never reported is read anyway: the adapter
// imposes no restriction it cannot verify, and the source answers
// OPC_E_BADRIGHTS if it does not permit the read.
func TestReadAsksTheSourceWhenRightsAreUnknown(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	varTypeI4 := opcda.VTI4
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Unknown", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{ItemID: "Test/Unknown", Value: int32(5), QualityRaw: QualityGood},
	}}
	response, err := service.Read(context.Background(),
		readRequestFor(readValue(ItemNodeID("Test/Unknown"))), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("status = %s, want the source's answer", response.Results[0].Status.Hex())
	}
	if len(runtime.readRequest.Items) != 1 {
		t.Fatal("the read was refused locally instead of reaching the source")
	}

	// When the source does refuse, its HRESULT decides.
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Unknown", HRESULT: OPCEBadRights, HRESULTPresent: true,
	}}
	response, err = service.Read(context.Background(),
		readRequestFor(readValue(ItemNodeID("Test/Unknown"))), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusBadNotReadable {
		t.Fatalf("status = %s, want the source's refusal", response.Results[0].Status.Hex())
	}
}

// The same rule applies to Write.
func TestWriteAsksTheSourceWhenRightsAreUnknown(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	runtime.writeResults = []opcda.WriteResult{{
		ItemID: "Test/Unknown", HRESULT: opcda.SOK, HRESULTPresent: true,
	}}
	response, err := service.Write(context.Background(), WriteRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Unknown"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Hex())
	}
	if len(runtime.writeItems) != 1 {
		t.Fatal("the write was refused locally instead of reaching the source")
	}
}

func TestReadNonValueAttributes(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	node := ItemNodeID("Test/Int32")

	cases := []struct {
		attribute uint32
		check     func(*testing.T, DataValue)
	}{
		{AttributeNodeClass, func(t *testing.T, value DataValue) {
			if value.Value.Value != int32(NodeClassVariable) {
				t.Fatalf("node class = %v", value.Value.Value)
			}
		}},
		{AttributeBrowseName, func(t *testing.T, value DataValue) {
			name, ok := value.Value.Value.(QualifiedName)
			if !ok || name.Name != "Int32" {
				t.Fatalf("browse name = %v", value.Value.Value)
			}
		}},
		{AttributeDataType, func(t *testing.T, value DataValue) {
			id, ok := value.Value.Value.(NodeID)
			if !ok || id.Numeric != NodeIDInt32 {
				t.Fatalf("data type = %v", value.Value.Value)
			}
		}},
		{AttributeValueRank, func(t *testing.T, value DataValue) {
			if value.Value.Value != ValueRankScalar {
				t.Fatalf("value rank = %v", value.Value.Value)
			}
		}},
		{AttributeAccessLevel, func(t *testing.T, value DataValue) {
			level, ok := value.Value.Value.(byte)
			if !ok || level&AccessLevelCurrentRead == 0 {
				t.Fatalf("access level = %v", value.Value.Value)
			}
		}},
	}
	for _, testCase := range cases {
		response, err := service.Read(context.Background(),
			readRequestFor(ReadValueID{NodeID: node, AttributeID: testCase.attribute}), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if response.Results[0].Status != StatusGood {
			t.Fatalf("attribute %d = %s", testCase.attribute, response.Results[0].Status.Hex())
		}
		testCase.check(t, response.Results[0])
	}
	// A non-Value attribute never reaches the source.
	if len(runtime.readRequest.Items) != 0 {
		t.Fatal("an attribute read reached the DA source")
	}

	// An attribute this adapter does not expose is refused.
	response, err := service.Read(context.Background(),
		readRequestFor(ReadValueID{NodeID: node, AttributeID: 9999}), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusBadAttributeIDInvalid {
		t.Fatalf("unknown attribute = %s", response.Results[0].Status.Hex())
	}
	// A variable-only attribute on a folder is refused too.
	response, err = service.Read(context.Background(), readRequestFor(
		ReadValueID{NodeID: BranchNodeID([]string{"Folder"}), AttributeID: AttributeDataType},
	), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusBadAttributeIDInvalid {
		t.Fatalf("folder data type = %s", response.Results[0].Status.Hex())
	}
}

// This adapter exposes no arrays, so an indexRange cannot apply.
func TestReadRefusesAnIndexRange(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	response, err := service.Read(context.Background(), readRequestFor(
		ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue, IndexRange: "0:1"},
	), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusBadIndexRangeInvalid {
		t.Fatalf("status = %s", response.Results[0].Status.Hex())
	}
}

// A method-level source failure gives every requested node the same status
// rather than pretending some succeeded.
func TestReadMapsSourceFailure(t *testing.T) {
	runtime := &stubRuntime{
		readErr: opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "not connected"),
	}
	service, _ := testDataService(t, runtime)
	response, err := service.Read(context.Background(), readRequestFor(
		readValue(ItemNodeID("Test/Int32")),
		readValue(ItemNodeID("Test/Float")),
	), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range response.Results {
		if result.Status != StatusBadNotConnected {
			t.Fatalf("result %d = %s, want Bad_NotConnected", index, result.Status.Hex())
		}
	}
}

func TestReadRefusesInvalidRequests(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	node := readValue(ItemNodeID("Test/Int32"))

	if _, err := service.Read(context.Background(), readRequestFor(), time.Now().UTC()); err == nil {
		t.Fatal("an empty read was accepted")
	} else if got := codecStatus(t, err); got != StatusBadNothingToDo {
		t.Fatalf("status = %s", got.Hex())
	}

	// Table 47: negative values are invalid for maxAge.
	request := readRequestFor(node)
	request.MaxAge = -1
	if _, err := service.Read(context.Background(), request, time.Now().UTC()); err == nil {
		t.Fatal("a negative maxAge was accepted")
	}

	request = readRequestFor(node)
	request.TimestampsToReturn = TimestampsInvalid
	if _, err := service.Read(context.Background(), request, time.Now().UTC()); err == nil {
		t.Fatal("an invalid timestampsToReturn was accepted")
	}

	limits := DefaultDataAccessLimits()
	limits.MaxNodesPerRead = 1
	bounded, err := NewDataAccessService(testAddressSpace(t), runtime, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Read(context.Background(), readRequestFor(node, node), time.Now().UTC()); err == nil {
		t.Fatal("an oversized read was accepted")
	} else if got := codecStatus(t, err); got != StatusBadTooManyOperations {
		t.Fatalf("status = %s", got.Hex())
	}
}

// The node's canonical DataType decides the VARTYPE and the Variant must
// already carry exactly that type: nothing is widened, narrowed, or converted.
func TestWriteIsStrictlyTyped(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	runtime.writeResults = []opcda.WriteResult{{ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true}}

	request := WriteRequest{
		Header: RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(99)}},
		}},
	}
	response, err := service.Write(context.Background(), request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Hex())
	}
	if len(runtime.writeItems) != 1 {
		t.Fatalf("write items = %d", len(runtime.writeItems))
	}
	item := runtime.writeItems[0]
	if item.ItemID != "Test/Int32" || item.VarType != opcda.VTI4 || item.Value != int32(99) {
		t.Fatalf("write item = %+v", item)
	}

	// A Double for an Int32 node is a type mismatch, not a conversion.
	request.NodesToWrite[0].Value.Value = Variant{Type: BuiltInDouble, Value: float64(99)}
	response, err = service.Write(context.Background(), request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusBadTypeMismatch {
		t.Fatalf("status = %s, want Bad_TypeMismatch", response.Results[0].Hex())
	}
	// A narrower type is refused too, rather than widened.
	request.NodesToWrite[0].Value.Value = Variant{Type: BuiltInInt16, Value: int16(99)}
	response, err = service.Write(context.Background(), request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusBadTypeMismatch {
		t.Fatalf("narrower type = %s", response.Results[0].Hex())
	}
}

// Table 53: a server returns Bad_WriteNotSupported if it cannot write
// timestamps or an indexRange. The DA core writes values only.
func TestWriteRefusesTimestampsStatusAndIndexRange(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	base := WriteValue{
		NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
		Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
	}
	cases := map[string]func(*WriteValue){
		"source timestamp": func(v *WriteValue) { v.Value.SourceTimestamp = time.Now().UTC() },
		"server timestamp": func(v *WriteValue) { v.Value.ServerTimestamp = time.Now().UTC() },
		"status code":      func(v *WriteValue) { v.Value.Status = StatusUncertain },
		"index range":      func(v *WriteValue) { v.IndexRange = "0" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			response, err := service.Write(context.Background(), WriteRequest{
				Header:       RequestHeader{AdditionalHeader: NullExtensionObject()},
				NodesToWrite: []WriteValue{value},
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if response.Results[0] != StatusBadWriteNotSupported {
				t.Fatalf("status = %s, want Bad_WriteNotSupported", response.Results[0].Hex())
			}
			if len(runtime.writeItems) != 0 {
				t.Fatal("a refused write reached the source")
			}
		})
	}
}

func TestWriteRefusesUnwritableTargets(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	cases := []struct {
		name  string
		value WriteValue
		want  StatusCode
	}{
		{"read-only item", WriteValue{
			NodeID: ItemNodeID("Test/String"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInString, Value: "x"}},
		}, StatusBadNotWritable},
		{"unknown node", WriteValue{
			NodeID: StringNodeID(AdapterNamespaceIndex, "item:missing"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}, StatusBadNodeIdUnknown},
		{"a folder", WriteValue{
			NodeID: BranchNodeID([]string{"Folder"}), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}, StatusBadAttributeIDInvalid},
		{"a non-Value attribute", WriteValue{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeBrowseName,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}, StatusBadNotWritable},
		{"a null value", WriteValue{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
			Value: DataValue{Value: NullVariant()},
		}, StatusBadTypeMismatch},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := service.Write(context.Background(), WriteRequest{
				Header:       RequestHeader{AdditionalHeader: NullExtensionObject()},
				NodesToWrite: []WriteValue{testCase.value},
			}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if response.Results[0] != testCase.want {
				t.Fatalf("status = %s, want %s", response.Results[0].Hex(), testCase.want.Hex())
			}
		})
	}
}

// A per-item DA write HRESULT maps through Part 8 Table A.5.
func TestWriteMapsPerItemHRESULT(t *testing.T) {
	runtime := &stubRuntime{}
	service, _ := testDataService(t, runtime)
	runtime.writeResults = []opcda.WriteResult{{
		ItemID: "Test/Int32", HRESULT: OPCEBadRights, HRESULTPresent: true,
	}}
	response, err := service.Write(context.Background(), WriteRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusBadNotWritable {
		t.Fatalf("status = %s, want Bad_NotWritable", response.Results[0].Hex())
	}
}

// Write disabled in the DA core surfaces as Bad_NotWritable rather than a
// generic failure.
func TestWriteMapsDisabledWrite(t *testing.T) {
	runtime := &stubRuntime{
		writeErr: opcda.NewAdapterError(opcda.CodeWriteDisabled, "write is disabled"),
	}
	service, _ := testDataService(t, runtime)
	response, err := service.Write(context.Background(), WriteRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}},
		}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0] != StatusBadNotWritable {
		t.Fatalf("status = %s", response.Results[0].Hex())
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteReadRequest(readRequestFor(readValue(ItemNodeID("Test/Float"))))
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != ReadRequestEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	request, err := decoder.ReadReadRequest()
	if err != nil {
		t.Fatal(err)
	}
	if request.TimestampsToReturn != TimestampsBoth || len(request.NodesToRead) != 1 {
		t.Fatalf("request = %+v", request)
	}
	// The exact DA ItemID survives inside the NodeId.
	if request.NodesToRead[0].NodeID.StringID != "item:Test/Float" {
		t.Fatalf("node id = %q", request.NodesToRead[0].NodeID.StringID)
	}

	encoder = newTestEncoder(t, limits)
	encoder.WriteWriteRequest(WriteRequest{
		Header: RequestHeader{AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Float"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInFloat, Value: float32(1.5)}},
		}},
	})
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	write, err := decoder.ReadWriteRequest()
	if err != nil {
		t.Fatal(err)
	}
	if write.NodesToWrite[0].Value.Value.Value != float32(1.5) {
		t.Fatalf("value = %v", write.NodesToWrite[0].Value.Value.Value)
	}
}

func TestDataAccessLimitsValidation(t *testing.T) {
	if err := DefaultDataAccessLimits().ValidateForConfiguration(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DataAccessLimits){
		"zero reads":   func(l *DataAccessLimits) { l.MaxNodesPerRead = 0 },
		"zero writes":  func(l *DataAccessLimits) { l.MaxNodesPerWrite = 0 },
		"zero timeout": func(l *DataAccessLimits) { l.RequestTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultDataAccessLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("invalid limits were accepted")
			}
		})
	}
	// A service needs both an address space and a runtime.
	if _, err := NewDataAccessService(nil, &stubRuntime{}, DefaultDataAccessLimits()); err == nil {
		t.Fatal("a service was built with no address space")
	}
	if _, err := NewDataAccessService(testAddressSpace(t), nil, DefaultDataAccessLimits()); err == nil {
		t.Fatal("a service was built with no runtime")
	}
}
