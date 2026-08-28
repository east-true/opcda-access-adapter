package opcua

import (
	"context"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// OPC 10000-8 Table A.1, transcribed row for row. Access Rights and Item
// Description are not here: the first is an attribute the adapter already
// reports from AddItems, and the second is the Description attribute.
func TestItemPropertyBindingsFollowPart8TableA1(t *testing.T) {
	for _, row := range []struct {
		browseName string
		dataType   uint32
		sources    []opcda.PropertyID
	}{
		{"EngineeringUnits", NodeIDString, []opcda.PropertyID{opcda.PropertyEUUnits}},
		{"EURange", NodeIDRange, []opcda.PropertyID{opcda.PropertyLowEU, opcda.PropertyHighEU}},
		{"InstrumentRange", NodeIDRange, []opcda.PropertyID{opcda.PropertyLowIR, opcda.PropertyHighIR}},
		{"TrueState", NodeIDString, []opcda.PropertyID{opcda.PropertyCloseLabel}},
		{"FalseState", NodeIDString, []opcda.PropertyID{opcda.PropertyOpenLabel}},
	} {
		t.Run(row.browseName, func(t *testing.T) {
			binding, ok := bindingForBrowseName(row.browseName)
			if !ok {
				t.Fatalf("%s is not bound", row.browseName)
			}
			if binding.DataType != row.dataType {
				t.Fatalf("DataType = %d, want %d", binding.DataType, row.dataType)
			}
			if len(binding.Sources) != len(row.sources) {
				t.Fatalf("sources = %v, want %v", binding.Sources, row.sources)
			}
			for index := range row.sources {
				if binding.Sources[index] != row.sources[index] {
					t.Fatalf("sources = %v, want %v", binding.Sources, row.sources)
				}
			}
		})
	}
	if len(tableA1) != 5 {
		t.Fatalf("Table A.1 has %d property rows, want 5", len(tableA1))
	}
}

// A Range with one end missing is not a Range. The adapter reports the property
// only when the source offers both ends, because supplying the other end would
// be inventing a number the source never gave.
func TestARangeIsNotClaimedWithOnlyOneEnd(t *testing.T) {
	half := []opcda.AvailableProperty{{ID: opcda.PropertyHighEU}}
	for _, binding := range bindingsForAvailable(half) {
		if binding.BrowseName == "EURange" {
			t.Fatal("EURange was claimed from High EU alone")
		}
	}
	both := []opcda.AvailableProperty{{ID: opcda.PropertyHighEU}, {ID: opcda.PropertyLowEU}}
	found := false
	for _, binding := range bindingsForAvailable(both) {
		found = found || binding.BrowseName == "EURange"
	}
	if !found {
		t.Fatal("EURange was not claimed when both ends are offered")
	}
}

// A property node identifier carries the exact ItemID and the property it
// stands for, so a client that knows its ItemIDs can read a property without
// browsing -- which matters because DA Browse is optional.
func TestPropertyNodeIdentifiersAreSelfDescribing(t *testing.T) {
	for _, name := range []string{"Test/Float", "Weird/Item:With\\Punctuation", "a b c"} {
		id := ItemPropertyNodeID(opcda.DAItemID(name), "EURange")
		itemID, binding, ok := ItemPropertyForNode(id)
		if !ok {
			t.Fatalf("%q did not round-trip", name)
		}
		if string(itemID) != name {
			t.Fatalf("ItemID = %q, want %q", itemID, name)
		}
		if binding.BrowseName != "EURange" {
			t.Fatalf("binding = %s", binding.BrowseName)
		}
	}
	// An item node is not a property node, and neither is an unknown property.
	if _, _, ok := ItemPropertyForNode(ItemNodeID("Test/Float")); ok {
		t.Fatal("an item node was read as a property node")
	}
	if _, _, ok := ItemPropertyForNode(StringNodeID(AdapterNamespaceIndex, "property:NotATableRow\x1fTest/Float")); ok {
		t.Fatal("a property outside Table A.1 was accepted")
	}
}

// A per-property HRESULT is a result, not a failure, so it goes through the
// same Table A.4 mapping every other read error does.
func TestAPropertyFailureCarriesTheSourceStatus(t *testing.T) {
	denied := opcda.ItemPropertyValue{HRESULT: -1073479674, HRESULTPresent: true} // OPC_E_BADRIGHTS
	if got := propertyStatus(denied); got != StatusBadNotReadable {
		t.Fatalf("status = %s, want Bad_NotReadable", got.Hex())
	}
	// A source that succeeded and reported nothing has not given a value, and
	// nothing is substituted for it.
	empty := opcda.ItemPropertyValue{OK: true, HRESULTPresent: true}
	if got := propertyStatus(empty); got != StatusBadNoData {
		t.Fatalf("status = %s, want Bad_NoData", got.Hex())
	}
}

// A source may answer a Double-valued property with a narrower numeric type.
// Every accepted type is exactly representable as a Double, so widening changes
// no value; anything else is reported as a mismatch rather than converted.
func TestRangeBoundsAcceptOnlyExactlyRepresentableNumbers(t *testing.T) {
	for _, value := range []any{float64(1), float32(1), int16(1), uint16(1), int32(1), uint32(1)} {
		if _, ok := asFloat64(value); !ok {
			t.Fatalf("%T was refused", value)
		}
	}
	for _, value := range []any{"1", true, int64(1), uint64(1)} {
		if _, ok := asFloat64(value); ok {
			t.Fatalf("%T was accepted as a Range bound", value)
		}
	}
}

func TestReadingAPropertyGoesToTheSourceEveryTime(t *testing.T) {
	runtime := &stubRuntime{
		available: map[string][]opcda.AvailableProperty{
			"Test/Float": {{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits}},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
			opcda.PropertyLowEU:   {OK: true, Value: float64(-50), ValuePresent: true},
			opcda.PropertyHighEU:  {OK: true, Value: float64(250), ValuePresent: true},
			opcda.PropertyEUUnits: {OK: true, Value: "degC", ValuePresent: true},
		},
	}
	service, space := testDataService(t, runtime)
	if err := space.AttachItemProperties("Test/Float", runtime.available["Test/Float"], 0); err != nil {
		t.Fatalf("AttachItemProperties: %v", err)
	}

	units := ItemPropertyNodeID("Test/Float", "EngineeringUnits")
	for attempt := 1; attempt <= 2; attempt++ {
		response, err := service.Read(context.Background(), readRequestFor(readValue(units)), time.Now())
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if response.Results[0].Status != StatusGood {
			t.Fatalf("status = %s", response.Results[0].Status.Hex())
		}
		if response.Results[0].Value.Value != "degC" {
			t.Fatalf("value = %v", response.Results[0].Value.Value)
		}
		if runtime.propertyCalls != attempt {
			t.Fatalf("read %d asked the source %d times; a property must not be cached", attempt, runtime.propertyCalls)
		}
	}

	// A Range is built from both ends, in one call, and carries no source
	// timestamp: it is metadata read now, not a process value.
	rangeNode := ItemPropertyNodeID("Test/Float", "EURange")
	response, err := service.Read(context.Background(), readRequestFor(readValue(rangeNode)), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("EURange status = %s", response.Results[0].Status.Hex())
	}
	if !response.Results[0].SourceTimestamp.IsZero() {
		t.Fatal("a property carried a source timestamp")
	}
	object, ok := response.Results[0].Value.Value.(ExtensionObject)
	if !ok {
		t.Fatalf("EURange value is %T, want an ExtensionObject", response.Results[0].Value.Value)
	}
	if object.TypeID.Numeric != NodeIDRangeEncodingDefaultBinary {
		t.Fatalf("EURange encoding = %d", object.TypeID.Numeric)
	}
	if len(object.Body) != 16 {
		t.Fatalf("a Range body is two Doubles, got %d bytes", len(object.Body))
	}
}

// The Description attribute is answered only for an item whose source offers
// Item Description. Nothing is remembered about the text itself.
func TestDescriptionIsAnsweredOnlyWhenTheSourceOffersIt(t *testing.T) {
	runtime := &stubRuntime{
		available: map[string][]opcda.AvailableProperty{
			"Test/Float": {{ID: opcda.PropertyDescription}},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
			opcda.PropertyDescription: {OK: true, Value: "reactor outlet", ValuePresent: true},
		},
	}
	service, space := testDataService(t, runtime)

	described := ReadValueID{NodeID: ItemNodeID("Test/Float"), AttributeID: AttributeDescription}
	response, err := service.Read(context.Background(), readRequestFor(described), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusBadAttributeIDInvalid {
		t.Fatalf("before discovery the attribute answered %s", response.Results[0].Status.Hex())
	}

	space.AttachItemProperties("Test/Float", runtime.available["Test/Float"], 0)
	response, err = service.Read(context.Background(), readRequestFor(described), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Status.Hex())
	}
	text, ok := response.Results[0].Value.Value.(LocalizedText)
	if !ok || text.Text != "reactor outlet" {
		t.Fatalf("description = %#v", response.Results[0].Value.Value)
	}

	// An item the source offers no description for keeps answering that the
	// attribute does not exist, which is the correct answer.
	other := ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeDescription}
	response, err = service.Read(context.Background(), readRequestFor(other), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusBadAttributeIDInvalid {
		t.Fatalf("an undescribed item answered %s", response.Results[0].Status.Hex())
	}
}

// A source that offers no properties is working correctly. Browsing such an
// item succeeds and it simply has none.
func TestASourceWithoutPropertiesLeavesItemsWithoutThem(t *testing.T) {
	runtime := &stubRuntime{}
	space := testAddressSpace(t)
	populator, err := NewPopulator(space, runtime, DefaultPopulationLimits())
	if err != nil {
		t.Fatalf("NewPopulator: %v", err)
	}
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := populator.EnsureItemProperties(context.Background(), "Test/Float", time.Now()); err != nil {
		t.Fatalf("a source without IOPCItemProperties failed a browse: %v", err)
	}
	node, ok := space.Node(ItemNodeID("Test/Float"))
	if !ok {
		t.Fatal("the item disappeared")
	}
	for _, reference := range node.References {
		if reference.IsForward && reference.ReferenceTypeID.Equal(NumericNodeID(0, NodeIDHasProperty)) {
			t.Fatal("a source with no properties produced a property node")
		}
	}
}

// Browsing an item must make its Table A.1 properties appear. This is the whole
// path -- Browse asks the populator, the populator asks the source, the address
// space gains the nodes, and the same Browse reports them -- and nothing else
// covers it end to end.
func TestBrowsingAnItemExposesItsTableA1Properties(t *testing.T) {
	runtime := &stubRuntime{
		available: map[string][]opcda.AvailableProperty{
			"Test/Float": {
				{ID: opcda.PropertyEUUnits, VarType: 8},
				{ID: opcda.PropertyLowEU, VarType: 5},
				{ID: opcda.PropertyHighEU, VarType: 5},
			},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{},
	}
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	populator, err := NewPopulator(space, runtime, DefaultPopulationLimits())
	if err != nil {
		t.Fatalf("NewPopulator: %v", err)
	}
	browse, err := NewBrowseService(space, DefaultBrowseLimits())
	if err != nil {
		t.Fatalf("NewBrowseService: %v", err)
	}
	browse.AttachPopulator(populator)

	response, err := browse.Browse(context.Background(), BrowseRequest{
		NodesToBrowse: []BrowseDescription{{
			NodeID:          ItemNodeID("Test/Float"),
			BrowseDirection: BrowseDirectionForward,
			ResultMask:      ResultMaskAll,
		}},
	}, time.Now())
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].StatusCode != StatusGood {
		t.Fatalf("browse status = %v", response.Results)
	}
	names := map[string]bool{}
	for _, reference := range response.Results[0].References {
		names[reference.BrowseName.Name] = true
	}
	for _, want := range []string{"EngineeringUnits", "EURange"} {
		if !names[want] {
			t.Errorf("browsing the item did not report %s; got %v", want, names)
		}
	}
	if runtime.availableCalls == 0 {
		t.Error("browsing the item never asked the source which properties it has")
	}
}

// A Table A.1 property node carries the ItemID of the item it describes, so
// every path that turns a node into a DA item has to exclude it. Monitoring one
// would otherwise subscribe to the item's value and deliver a process value
// under the property's client handle, reported as an engineering range.
//
// OPC DA has no change notification for item properties -- a group notifies on
// item values only -- so the refusal is honest rather than a limitation being
// hidden.
func TestAPropertyNodeCannotBeMonitored(t *testing.T) {
	runtime := &subscribingRuntime{}
	service, space := testSubscriptionService(t, runtime)
	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
	}, 0)

	id := createSubscription(t, service)
	response, err := service.CreateMonitoredItems(context.Background(), testSession, CreateMonitoredItemsRequest{
		Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
		SubscriptionID:     id,
		TimestampsToReturn: TimestampsBoth,
		ItemsToCreate: []MonitoredItemCreateRequest{{
			ItemToMonitor:  ReadValueID{NodeID: ItemPropertyNodeID("Test/Float", "EURange"), AttributeID: AttributeValue},
			MonitoringMode: MonitoringModeReporting,
			RequestedParameters: MonitoringParameters{
				ClientHandle: 7, SamplingInterval: 250, QueueSize: 10,
				Filter: NullExtensionObject(),
			},
		}},
	}, channelEpoch)
	if err != nil {
		t.Fatalf("CreateMonitoredItems: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d", len(response.Results))
	}
	if response.Results[0].StatusCode != StatusBadNotSupported {
		t.Fatalf("status = %s, want Bad_NotSupported", response.Results[0].StatusCode.Hex())
	}
	// The refusal has to happen before the source is involved. A subscription
	// created here would be one nobody asked for.
	runtime.mu.Lock()
	created := len(runtime.subscriptions)
	runtime.mu.Unlock()
	if created != 0 {
		t.Fatalf("a refused property monitor still created %d DA subscriptions", created)
	}
}

// A property describes an item; it is not a place to put a value.
//
// A property node is created read-only, so the access-level check refuses this
// on its own -- which means asserting the ordinary case proves nothing about
// the rule. The node is therefore given write access first, so what is being
// tested is that a property is refused because it is a property. Without that,
// the ItemID a property node carries would send the value to the item it
// describes.
func TestAPropertyNodeCannotBeWritten(t *testing.T) {
	runtime := &stubRuntime{}
	service, space := testDataService(t, runtime)
	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		{ID: opcda.PropertyEUUnits},
	}, 0)
	property, ok := space.Node(ItemPropertyNodeID("Test/Float", "EngineeringUnits"))
	if !ok {
		t.Fatal("the property node was not created")
	}
	property.AccessLevel = AccessLevelCurrentRead | AccessLevelCurrentWrite

	response, err := service.Write(context.Background(), WriteRequest{
		Header: RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID:      ItemPropertyNodeID("Test/Float", "EngineeringUnits"),
			AttributeID: AttributeValue,
			Value:       DataValue{Value: Variant{Type: BuiltInString, Value: "degF"}, Status: StatusGood},
		}},
	}, time.Now())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if response.Results[0] != StatusBadNotWritable {
		t.Fatalf("status = %s, want Bad_NotWritable", response.Results[0].Hex())
	}
	if len(runtime.writeItems) != 0 {
		t.Fatalf("a refused property write reached the source as %d items", len(runtime.writeItems))
	}
}

// Re-attaching replaces the property set rather than adding to it, so a source
// that stops offering a property stops reporting it.
func TestAPropertyTheSourceStopsOfferingStopsBeingReported(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	both := []opcda.AvailableProperty{
		{ID: opcda.PropertyEUUnits}, {ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
	}
	space.AttachItemProperties("Test/Float", both, 0)
	if names := propertyNames(t, space); !names["EURange"] || !names["EngineeringUnits"] {
		t.Fatalf("first attach reported %v", names)
	}

	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{{ID: opcda.PropertyEUUnits}}, 0)
	names := propertyNames(t, space)
	if names["EURange"] {
		t.Fatal("a property the source stopped offering is still reported")
	}
	if !names["EngineeringUnits"] {
		t.Fatal("re-attaching dropped a property the source still offers")
	}
}

func propertyNames(t *testing.T, space *AddressSpace) map[string]bool {
	t.Helper()
	node, ok := space.Node(ItemNodeID("Test/Float"))
	if !ok {
		t.Fatal("the item is missing")
	}
	names := map[string]bool{}
	hasProperty := NumericNodeID(0, NodeIDHasProperty)
	for _, reference := range node.References {
		if reference.IsForward && reference.ReferenceTypeID.Equal(hasProperty) {
			names[reference.BrowseName.Name] = true
		}
	}
	return names
}

// "Is this a DA item?" is one question, and ResolveNode is the one place that
// answers it. It used to be re-derived at three call sites as Class ==
// Variable && ItemID != "", which a property node satisfies, and two of the
// three got it wrong.
func TestResolveNodeSeparatesItemsFromTheirProperties(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
		{Kind: opcda.BrowseEntryBranch, Name: "Folder"},
	}); err != nil {
		t.Fatal(err)
	}
	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{{ID: opcda.PropertyEUUnits}}, 0)

	for _, testCase := range []struct {
		name string
		id   NodeID
		want NodeKind
	}{
		{"a browsed item", ItemNodeID("Test/Float"), NodeKindItem},
		{"an item never browsed", ItemNodeID("Never/Browsed"), NodeKindItem},
		{"an attached property", ItemPropertyNodeID("Test/Float", "EngineeringUnits"), NodeKindItemProperty},
		// A property identifier resolves as a property even before the source
		// has said the item has it: reading it asks the source, which decides.
		{"a property never attached", ItemPropertyNodeID("Test/Float", "EURange"), NodeKindItemProperty},
		{"the source folder", space.SourceFolderID(), NodeKindOther},
		{"the Server object", NumericNodeID(0, NodeIDServer), NodeKindOther},
		{"nothing at all", StringNodeID(AdapterNamespaceIndex, "neither:one\x1fnor/the/other"), NodeKindUnknown},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			node, kind := space.ResolveNode(testCase.id, 0)
			if kind != testCase.want {
				t.Fatalf("kind = %d, want %d", kind, testCase.want)
			}
			if kind == NodeKindItem && (node == nil || node.ItemID == "") {
				t.Fatal("an item resolved without an ItemID")
			}
			// A property must never come back as something a caller would
			// hand to the DA runtime as an item.
			if kind == NodeKindItemProperty && node != nil && !node.IsItemPropertyNode() {
				t.Fatal("a property resolved as a node that does not know it is one")
			}
		})
	}
}

// The node budget must refuse a property set it cannot hold, not attach part of
// it. A client cannot tell a short list from a complete one, and the populator
// would record the truncated answer as the discovered one.
func TestAPropertySetThatDoesNotFitIsNotAttachedInPart(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	available := []opcda.AvailableProperty{
		{ID: opcda.PropertyEUUnits}, {ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
		{ID: opcda.PropertyCloseLabel}, {ID: opcda.PropertyOpenLabel},
	}
	// One item node exists, and the set needs four more. A budget of three
	// cannot hold them.
	budget := space.SourceNodeCount() + 3
	if err := space.AttachItemProperties("Test/Float", available, budget); err == nil {
		t.Fatal("a property set that does not fit the node budget was accepted")
	}
	if names := propertyNames(t, space); len(names) != 0 {
		t.Fatalf("a refused attach left %v behind", names)
	}
}

// A discovery that could not be attached must not be recorded as done, or the
// refresh interval would keep a client from ever seeing the properties.
func TestARefusedDiscoveryIsRetriedRatherThanRemembered(t *testing.T) {
	runtime := &stubRuntime{
		available: map[string][]opcda.AvailableProperty{
			"Test/Float": {
				{ID: opcda.PropertyEUUnits}, {ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
				{ID: opcda.PropertyCloseLabel}, {ID: opcda.PropertyOpenLabel},
			},
		},
	}
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	limits := DefaultPopulationLimits()
	limits.MaxNodes = space.SourceNodeCount() + 3
	populator, err := NewPopulator(space, runtime, limits)
	if err != nil {
		t.Fatalf("NewPopulator: %v", err)
	}

	now := time.Now()
	if err := populator.EnsureItemProperties(context.Background(), "Test/Float", now); err == nil {
		t.Fatal("a property set that does not fit was reported as discovered")
	}
	if names := propertyNames(t, space); len(names) != 0 {
		t.Fatalf("a refused discovery left %v behind", names)
	}
	// Immediately afterwards, well inside the refresh interval, it tries again
	// rather than serving the refusal as a remembered answer.
	if err := populator.EnsureItemProperties(context.Background(), "Test/Float", now); err == nil {
		t.Fatal("the second attempt was answered from the cache")
	}
	if runtime.availableCalls != 2 {
		t.Fatalf("the source was asked %d times, want 2", runtime.availableCalls)
	}
}
