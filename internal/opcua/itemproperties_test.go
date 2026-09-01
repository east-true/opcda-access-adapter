package opcua

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// OPC 10000-8 Annex A.3.1.3 chooses a DA item's UA VariableType from the
// properties its source offers, and puts different properties on each type.
// The adapter used to give every item BaseDataVariableType, which A.3.1.3 does
// not offer as a choice.
func TestVariableTypeFollowsPart8AnnexA313(t *testing.T) {
	property := func(ids ...opcda.PropertyID) []opcda.AvailableProperty {
		available := make([]opcda.AvailableProperty, 0, len(ids))
		for _, id := range ids {
			available = append(available, opcda.AvailableProperty{ID: id})
		}
		return available
	}
	numeric := &Node{DataType: NumericNodeID(0, NodeIDFloat), DataTypeKnown: true}
	boolean := &Node{DataType: NumericNodeID(0, NodeIDBoolean), DataTypeKnown: true}
	text := &Node{DataType: NumericNodeID(0, NodeIDString), DataTypeKnown: true}
	unknown := &Node{DataType: NumericNodeID(0, NodeIDBaseDataType)}
	for _, testCase := range []struct {
		name      string
		available []opcda.AvailableProperty
		euType    opcda.EUType
		item      *Node
		wantType  uint32
		wantProps []string
	}{
		{
			name:      "High and Low EU make an analog item",
			available: property(opcda.PropertyHighEU, opcda.PropertyLowEU),
			item:      numeric,
			wantType:  NodeIDAnalogItemType,
			wantProps: []string{"EURange"},
		},
		{
			name: "an analog item carries the optional properties it has",
			available: property(opcda.PropertyHighEU, opcda.PropertyLowEU,
				opcda.PropertyEUUnits, opcda.PropertyHighIR, opcda.PropertyLowIR),
			item:      numeric,
			wantType:  NodeIDAnalogItemType,
			wantProps: []string{"EURange", "EngineeringUnits", "InstrumentRange"},
		},
		{
			name:      "Open and Close Label make a two-state discrete item",
			available: property(opcda.PropertyCloseLabel, opcda.PropertyOpenLabel),
			item:      boolean,
			wantType:  NodeIDTwoStateDiscreteType,
			wantProps: []string{"TrueState", "FalseState"},
		},
		{
			name:      "anything else is a data item",
			available: property(opcda.PropertyScanRate),
			item:      numeric,
			wantType:  NodeIDDataItemType,
		},
		{
			// A.3.1.3 says High and Low EU *or* an Analog EU Type, and clause
			// 5.3.2.3 makes EURange mandatory on the type. An item with the
			// EU Type and neither bound cannot have an EURange, so claiming
			// AnalogItemType would promise a property the adapter knows it
			// cannot supply.
			name:      "an Analog EU Type without a range is not promoted",
			available: property(opcda.PropertyEUType),
			euType:    opcda.EUTypeAnalog,
			item:      numeric,
			wantType:  NodeIDDataItemType,
		},
		{
			// MultiStateDiscreteType requires EnumStrings, which comes from EU
			// Info -- an array of strings, which the DA layer does not carry.
			name:      "an enumerated EU Type is not promoted either",
			available: property(opcda.PropertyEUType, opcda.PropertyEUInfo),
			euType:    opcda.EUTypeEnumerated,
			item:      numeric,
			wantType:  NodeIDDataItemType,
		},
		{
			// 5.3.2.3 gives AnalogItemType the DataType Number. A string item
			// with EU bounds cannot have that type without contradicting it.
			name:      "EU bounds on a string item do not make it analog",
			available: property(opcda.PropertyHighEU, opcda.PropertyLowEU),
			item:      text,
			wantType:  NodeIDDataItemType,
		},
		{
			// 5.3.3.2 gives TwoStateDiscreteType the DataType Boolean.
			name:      "state labels on a numeric item do not make it discrete",
			available: property(opcda.PropertyCloseLabel, opcda.PropertyOpenLabel),
			item:      numeric,
			wantType:  NodeIDDataItemType,
		},
		{
			// Promoting before the source has stated the item's type would be
			// a guess, and the same contradiction arriving later.
			name:      "an item whose DataType is unknown is not promoted",
			available: property(opcda.PropertyHighEU, opcda.PropertyLowEU),
			item:      unknown,
			wantType:  NodeIDDataItemType,
		},
		{
			// DataTypeKnown is what says the source stated the type. A node
			// carrying a numeric DataType it was never told is a guess, and
			// the flag is what keeps the two apart.
			name:      "a numeric DataType nobody stated is still unknown",
			available: property(opcda.PropertyHighEU, opcda.PropertyLowEU),
			item:      &Node{DataType: NumericNodeID(0, NodeIDFloat)},
			wantType:  NodeIDDataItemType,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chosen := variableTypeFor(testCase.available, testCase.euType, testCase.item)
			if chosen.TypeID != testCase.wantType {
				t.Fatalf("type = %d (%s), want %d", chosen.TypeID, chosen.Name, testCase.wantType)
			}
			if len(chosen.Properties) != len(testCase.wantProps) {
				t.Fatalf("properties = %d, want %d", len(chosen.Properties), len(testCase.wantProps))
			}
			for index, want := range testCase.wantProps {
				if chosen.Properties[index].BrowseName != want {
					t.Fatalf("property %d = %s, want %s", index, chosen.Properties[index].BrowseName, want)
				}
			}
		})
	}
}

// The UA types these properties carry are the standard types', not Table A.1's
// "String" column. A.1 gives the DA value's mapped type; A.3.1.3 assigns those
// values to properties the standard VariableTypes define.
func TestPropertyTypesAreTheStandardTypesNotTableA1sColumn(t *testing.T) {
	for _, testCase := range []struct {
		browseName string
		dataType   uint32
	}{
		{"EURange", NodeIDRange},
		{"InstrumentRange", NodeIDRange},
		{"EngineeringUnits", NodeIDEUInformation},
		{"TrueState", NodeIDLocalizedText},
		{"FalseState", NodeIDLocalizedText},
	} {
		binding, ok := bindingForBrowseName(testCase.browseName)
		if !ok {
			t.Fatalf("%s is not bound", testCase.browseName)
		}
		if binding.DataType != testCase.dataType {
			t.Errorf("%s carries %d, want %d", testCase.browseName, binding.DataType, testCase.dataType)
		}
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
	if err := space.AttachItemProperties("Test/Float", runtime.available["Test/Float"], opcda.EUTypeNoEnum, testNodeBudget); err != nil {
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
		// EngineeringUnits carries EUInformation, with the DA unit string as
		// its DisplayName. The raw string is not what a client decodes.
		object, ok := response.Results[0].Value.Value.(ExtensionObject)
		if !ok {
			t.Fatalf("value is %T, want an ExtensionObject", response.Results[0].Value.Value)
		}
		if object.TypeID.Numeric != NodeIDEUInformationEncodingDefaultBinary {
			t.Fatalf("EngineeringUnits named encoding %d", object.TypeID.Numeric)
		}
		if !bytes.Contains(object.Body, []byte("degC")) {
			t.Fatalf("the unit string is not in the EUInformation body")
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

	space.AttachItemProperties("Test/Float", runtime.available["Test/Float"], opcda.EUTypeNoEnum, testNodeBudget)
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

	response, err := browse.Browse(context.Background(), testSession, BrowseRequest{
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
	}, opcda.EUTypeNoEnum, testNodeBudget)

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
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits},
	}, opcda.EUTypeNoEnum, testNodeBudget)
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
	space.AttachItemProperties("Test/Float", both, opcda.EUTypeNoEnum, testNodeBudget)
	if names := propertyNames(t, space); !names["EURange"] || !names["EngineeringUnits"] {
		t.Fatalf("first attach reported %v", names)
	}

	// The source stops offering EU Units. The item stays analog, because it
	// still has both ends of its range, and loses only the optional property.
	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
	}, opcda.EUTypeNoEnum, testNodeBudget)
	names := propertyNames(t, space)
	if names["EngineeringUnits"] {
		t.Fatal("a property the source stopped offering is still reported")
	}
	if !names["EURange"] {
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
	space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits},
	}, opcda.EUTypeNoEnum, testNodeBudget)

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
			node, kind := space.ResolveNode(testCase.id, testNodeBudget)
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
	// An analog item with a range, its units and its instrument range: four
	// property nodes.
	available := []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits},
		{ID: opcda.PropertyLowIR}, {ID: opcda.PropertyHighIR},
	}
	// The set needs three more nodes than exist. A budget of two cannot hold
	// them.
	budget := space.SourceNodeCount() + 2
	if err := space.AttachItemProperties("Test/Float", available, opcda.EUTypeNoEnum, budget); err == nil {
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
				{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits},
				{ID: opcda.PropertyLowIR}, {ID: opcda.PropertyHighIR},
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
	limits.MaxNodes = space.SourceNodeCount() + 2
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

// Browsing asks what a node is; it must not create one. A client that browses
// ItemIDs it invents would otherwise grow the address space without bound,
// because the node budget is what Read, Write and Subscribe pass and Browse has
// none to pass.
func TestBrowsingAnUnknownItemDoesNotCreateIt(t *testing.T) {
	runtime := &stubRuntime{}
	space := testAddressSpace(t)
	populator, err := NewPopulator(space, runtime, DefaultPopulationLimits())
	if err != nil {
		t.Fatalf("NewPopulator: %v", err)
	}
	browse, err := NewBrowseService(space, DefaultBrowseLimits())
	if err != nil {
		t.Fatalf("NewBrowseService: %v", err)
	}
	browse.AttachPopulator(populator)

	before := space.SourceNodeCount()
	for index := 0; index < 25; index++ {
		if _, err := browse.Browse(context.Background(), testSession, BrowseRequest{
			NodesToBrowse: []BrowseDescription{{
				NodeID:          ItemNodeID(opcda.DAItemID(fmt.Sprintf("Invented/Item%d", index))),
				BrowseDirection: BrowseDirectionForward,
				ResultMask:      ResultMaskAll,
			}},
		}, time.Now()); err != nil {
			t.Fatalf("Browse: %v", err)
		}
	}
	if grew := space.SourceNodeCount() - before; grew != 0 {
		t.Fatalf("browsing 25 invented ItemIDs added %d nodes", grew)
	}
}

// testNodeBudget is a budget large enough for any test here. Tests name one
// rather than passing zero: zero is not "unlimited", it is "create nothing".
const testNodeBudget = 1000

// There is no unlimited node budget. A caller that passes a non-positive one
// creates nothing, so forgetting to pass a budget is refused rather than
// quietly allowed to grow the address space without bound -- which is what let
// a Browse create a node for every ItemID a client cared to invent.
func TestANonPositiveNodeBudgetCreatesNothing(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	before := space.SourceNodeCount()

	for _, budget := range []int{0, -1} {
		if _, ok := space.ResolveVariable(ItemNodeID("Never/Seen"), budget); ok {
			t.Fatalf("a budget of %d created an item node", budget)
		}
		if _, kind := space.ResolveNode(ItemNodeID("Never/Seen"), budget); kind != NodeKindUnknown {
			t.Fatalf("a budget of %d resolved an item that does not exist", budget)
		}
		err := space.AttachItemProperties("Test/Float",
			[]opcda.AvailableProperty{{ID: opcda.PropertyEUUnits}}, opcda.EUTypeNoEnum, budget)
		if err == nil {
			t.Fatalf("a budget of %d attached a property node", budget)
		}
	}
	if grew := space.SourceNodeCount() - before; grew != 0 {
		t.Fatalf("a non-positive budget added %d nodes", grew)
	}
}

// Choosing the type is not the same as giving it to the node. Annex A.3.1.3
// chooses from properties that are only known once the source has been asked,
// so the TypeDefinition is set when they are attached rather than when the item
// node is created -- and until a client browses, an item carries the type it
// was created with.
func TestTheChosenVariableTypeReachesTheNode(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
		{Kind: opcda.BrowseEntryItem, Name: "Switch", ItemID: itemID("Test/Switch"),
			CanonicalType: varType(opcda.VTBool)},
		{Kind: opcda.BrowseEntryItem, Name: "Plain", ItemID: itemID("Test/Plain"),
			CanonicalType: varType(opcda.VTI4)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		itemID    opcda.DAItemID
		available []opcda.AvailableProperty
		want      uint32
	}{
		{"Test/Float", []opcda.AvailableProperty{
			{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}}, NodeIDAnalogItemType},
		{"Test/Switch", []opcda.AvailableProperty{
			{ID: opcda.PropertyCloseLabel}, {ID: opcda.PropertyOpenLabel}}, NodeIDTwoStateDiscreteType},
		{"Test/Plain", []opcda.AvailableProperty{
			{ID: opcda.PropertyScanRate}}, NodeIDDataItemType},
	} {
		if err := space.AttachItemProperties(testCase.itemID, testCase.available,
			opcda.EUTypeNoEnum, testNodeBudget); err != nil {
			t.Fatalf("%s: %v", testCase.itemID, err)
		}
		node, ok := space.Node(ItemNodeID(testCase.itemID))
		if !ok {
			t.Fatalf("%s disappeared", testCase.itemID)
		}
		if node.TypeDefinition.Numeric != testCase.want {
			t.Errorf("%s is type %d, Annex A.3.1.3 gives it %d",
				testCase.itemID, node.TypeDefinition.Numeric, testCase.want)
		}
		// Whatever the type, it is never the one Annex A never mentions.
		if node.TypeDefinition.Numeric == NodeIDBaseDataVariableType {
			t.Errorf("%s kept BaseDataVariableType, which Annex A does not offer", testCase.itemID)
		}
	}
}

// A claimed type and its mandatory property must never disagree. AnalogItemType
// requires EURange, and TwoStateDiscreteType requires TrueState and FalseState;
// a node carrying one without the other would be a promise the address space
// itself breaks.
//
// The interesting case is a source that changes what it offers. Re-attaching
// recomputes the type and replaces the property set together, under one lock,
// so the two move as a unit -- an item can change type between browses, but it
// is never briefly a type whose mandatory property is missing.
func TestAClaimedTypeAlwaysCarriesItsMandatoryProperty(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	analog := []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU}, {ID: opcda.PropertyEUUnits},
	}
	discrete := []opcda.AvailableProperty{
		{ID: opcda.PropertyCloseLabel}, {ID: opcda.PropertyOpenLabel},
	}
	plain := []opcda.AvailableProperty{{ID: opcda.PropertyScanRate}}

	// Every order of every transition, including back to where it started.
	for _, available := range [][]opcda.AvailableProperty{
		analog, discrete, plain, analog, plain, discrete, analog,
	} {
		if err := space.AttachItemProperties("Test/Float", available,
			opcda.EUTypeNoEnum, testNodeBudget); err != nil {
			t.Fatalf("attach: %v", err)
		}
		node, ok := space.Node(ItemNodeID("Test/Float"))
		if !ok {
			t.Fatal("the item disappeared")
		}
		names := propertyNames(t, space)
		switch node.TypeDefinition.Numeric {
		case NodeIDAnalogItemType:
			if !names["EURange"] {
				t.Fatal("AnalogItemType without its mandatory EURange")
			}
		case NodeIDTwoStateDiscreteType:
			if !names["TrueState"] || !names["FalseState"] {
				t.Fatalf("TwoStateDiscreteType without its mandatory states: %v", names)
			}
		case NodeIDDataItemType:
			// DataItemType has no mandatory property. It may still carry the
			// unnamed properties of Table A.1's last row; what it must not
			// carry is one that belongs to a type it is not.
			for _, forbidden := range []string{"EURange", "EngineeringUnits", "InstrumentRange", "TrueState", "FalseState"} {
				if names[forbidden] {
					t.Fatalf("DataItemType kept %s, which belongs to another type", forbidden)
				}
			}
		default:
			t.Fatalf("item took type %d, which Annex A.3.1.3 does not choose", node.TypeDefinition.Numeric)
		}
	}
}

// An item addressed without being browsed is created on demand, and it sits at
// the Annex A.3.1.3 floor like a browsed one. A source need not implement
// Browse at all, so this is the only type such an item ever has until a client
// browses it -- and BaseDataVariableType is not a type Annex A offers.
func TestAnItemAddressedWithoutBrowsingSitsAtTheAnnexAFloor(t *testing.T) {
	space := testAddressSpace(t)
	node, kind := space.ResolveNode(ItemNodeID("Never/Browsed"), testNodeBudget)
	if kind != NodeKindItem || node == nil {
		t.Fatalf("kind = %d", kind)
	}
	if node.TypeDefinition.Numeric != NodeIDDataItemType {
		t.Fatalf("type definition = %s, want DataItemType", node.TypeDefinition)
	}
}

// Table A.1's last row: everything the source offers that the nine named rows
// do not claim becomes a Variable of PropertyType, named by the DA property's
// own description and typed from its own VARTYPE. This was recorded as
// unimplemented and is what a client browsing a real source actually finds --
// Scan Rate, EU Type and whatever a vendor adds.
func TestUnnamedDAPropertiesBecomePropertiesOfTheItem(t *testing.T) {
	runtime := &stubRuntime{
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
			opcda.PropertyEUType: {OK: true, VarType: opcda.VTI4, VarTypePresent: true,
				Value: int32(1), ValuePresent: true, HRESULTPresent: true},
		},
	}
	service, space := testDataService(t, runtime)
	available := []opcda.AvailableProperty{
		{ID: opcda.PropertyScanRate, Description: "Scan Rate", VarType: opcda.VTR4},
		{ID: opcda.PropertyEUType, Description: "EU Type", VarType: opcda.VTI4},
		// An array property is left out: A.3.1.4 would expose it with
		// OneOrMoreDimensions, and the DA layer cannot read one, so the node
		// would exist and never answer.
		{ID: opcda.PropertyEUInfo, Description: "EU Info", VarType: opcda.VTBSTR | opcda.VTArray},
		// Access Rights and Item Description are attributes, not properties.
		{ID: opcda.PropertyAccessRights, Description: "Access Rights", VarType: opcda.VTI4},
		{ID: opcda.PropertyDescription, Description: "Item Description", VarType: opcda.VTBSTR},
	}
	if err := space.AttachItemProperties("Test/Float", available, opcda.EUTypeNoEnum, testNodeBudget); err != nil {
		t.Fatalf("AttachItemProperties: %v", err)
	}

	names := propertyNames(t, space)
	if !names["EU Type"] {
		t.Errorf("EU Type was not exposed; got %v", names)
	}
	// Access Rights, Item Description and Scan Rate map onto attributes --
	// Table A.1 names the first two and A.3.1.3's common mappings name Scan
	// Rate -- so none of them is exposed a second time as a property.
	for _, unwanted := range []string{"EU Info", "Access Rights", "Item Description", "Scan Rate"} {
		if names[unwanted] {
			t.Errorf("%s was exposed as a property", unwanted)
		}
	}

	// A.3.1.4: the node is a PropertyType, typed from the DA VARTYPE, scalar,
	// and readable.
	node, ok := space.Node(OtherPropertyNodeID("Test/Float", opcda.PropertyEUType))
	if !ok {
		t.Fatal("the EU Type node is missing")
	}
	if node.TypeDefinition.Numeric != NodeIDPropertyType {
		t.Fatalf("type definition = %s, want PropertyType", node.TypeDefinition)
	}
	if !node.DataTypeKnown || node.DataType.Numeric != NodeIDInt32 {
		t.Fatalf("data type = %s, want Int32 from VT_I4", node.DataType)
	}
	if node.ValueRank != ValueRankScalar || node.AccessLevel != AccessLevelCurrentRead {
		t.Fatalf("rank = %d, access = %d", node.ValueRank, node.AccessLevel)
	}

	// And it reads, carrying the source's own value with no mapping applied.
	response, err := service.Read(context.Background(),
		readRequestFor(readValue(node.ID)), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Status.Hex())
	}
	if response.Results[0].Value.Value != int32(1) {
		t.Fatalf("value = %#v", response.Results[0].Value.Value)
	}
}

// A.3.1.4: a property the source also exposes as an item of its own is
// writable, and the write goes to that item. Reporting it writable and then
// refusing the write would be worse than reporting it readable, so the access
// level and the Write path have to agree.
func TestAPropertyWithItsOwnItemIDIsWritable(t *testing.T) {
	runtime := &stubRuntime{
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{},
		writeResults: []opcda.WriteResult{
			{ItemID: "Test/Float.EUType", HRESULT: opcda.SOK, HRESULTPresent: true},
		},
	}
	service, space := testDataService(t, runtime)
	available := []opcda.AvailableProperty{{
		ID: opcda.PropertyEUType, Description: "EU Type", VarType: opcda.VTI4,
		ItemID: "Test/Float.EUType", ItemIDPresent: true,
	}}
	if err := space.AttachItemProperties("Test/Float", available, opcda.EUTypeNoEnum, testNodeBudget); err != nil {
		t.Fatalf("AttachItemProperties: %v", err)
	}

	id := OtherPropertyNodeID("Test/Float", opcda.PropertyEUType)
	node, ok := space.Node(id)
	if !ok {
		t.Fatal("the property node is missing")
	}
	if node.AccessLevel&AccessLevelCurrentWrite == 0 {
		t.Fatal("a property with its own ItemID was not reported writable")
	}

	response, err := service.Write(context.Background(), WriteRequest{
		Header: RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		NodesToWrite: []WriteValue{{
			NodeID: id, AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(2)}, Status: StatusGood},
		}},
	}, time.Now())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if response.Results[0] != StatusGood {
		t.Fatalf("status = %s; a property reported writable must accept a write",
			response.Results[0].Hex())
	}
	// It must reach the property's own ItemID, not the item it describes.
	if len(runtime.writeItems) != 1 {
		t.Fatalf("the source received %d writes", len(runtime.writeItems))
	}
	if runtime.writeItems[0].ItemID != "Test/Float.EUType" {
		t.Fatalf("the write went to %q", runtime.writeItems[0].ItemID)
	}
}

// OPC 10000-8 clause 5.2: a Data Access server shall set the SemanticsChanged
// bit in notifications when certain property values change, so a client
// re-reads the metadata before trusting the value.
func TestSemanticPropertiesAreExactlyTheOnesPart8Names(t *testing.T) {
	// BaseAnalogType names EURange and EngineeringUnits; 5.3.3.2 names
	// TrueState and FalseState. InstrumentRange is named only for
	// ArrayItemType, which this adapter never claims.
	for _, name := range []string{"EURange", "EngineeringUnits", "TrueState", "FalseState"} {
		if !isSemanticProperty(name) {
			t.Errorf("%s is named by Part 8 and is not treated as semantic", name)
		}
	}
	for _, name := range []string{"InstrumentRange", "Scan Rate", ""} {
		if isSemanticProperty(name) {
			t.Errorf("%s is treated as semantic and Part 8 does not name it", name)
		}
	}
}

// The bit belongs on a notification and nowhere else: clause 5.2 says a
// StatusCode in any other context shall always set it to zero.
func TestASemanticChangeReachesOneNotificationAndNotTheRead(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	units := func(text string) Variant {
		return Variant{Type: BuiltInString, Value: text}
	}

	// The first sighting is not a change: there is nothing to have changed from.
	if space.NoteSemanticProperty("Test/Float", "EngineeringUnits", units("degC")) {
		t.Fatal("the first sighting of a property was reported as a change")
	}
	if got := space.SemanticGeneration("Test/Float"); got != 0 {
		t.Fatalf("generation = %d after one sighting", got)
	}
	// Seeing the same value again is not a change either.
	if space.NoteSemanticProperty("Test/Float", "EngineeringUnits", units("degC")) {
		t.Fatal("an unchanged property was reported as a change")
	}
	// A different value is.
	if !space.NoteSemanticProperty("Test/Float", "EngineeringUnits", units("degF")) {
		t.Fatal("a changed property was not reported")
	}
	if got := space.SemanticGeneration("Test/Float"); got != 1 {
		t.Fatalf("generation = %d after one change", got)
	}
	// A property Part 8 does not name never moves the generation.
	if space.NoteSemanticProperty("Test/Float", "InstrumentRange", units("anything")) {
		t.Fatal("InstrumentRange was reported as a semantic change")
	}
	if got := space.SemanticGeneration("Test/Float"); got != 1 {
		t.Fatalf("generation = %d after a non-semantic property changed", got)
	}
}

// A.3.1.3's common mappings: "The ScanRate property value in the DA server is
// assigned to the MinimumSamplingInterval Attribute." It is an attribute, not a
// property, so it is not also exposed as one.
func TestScanRateBecomesMinimumSamplingInterval(t *testing.T) {
	runtime := &stubRuntime{propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{}}
	service, space := testDataService(t, runtime)
	target := ReadValueID{
		NodeID: ItemNodeID("Test/Float"), AttributeID: AttributeMinimumSamplingInterval}

	// Before the source has stated it, the attribute is absent. Zero would say
	// the server samples as fast as possible, which nobody claimed.
	response, err := service.Read(context.Background(), readRequestFor(target), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusBadAttributeIDInvalid {
		t.Fatalf("an unstated interval answered %s", response.Results[0].Status.Hex())
	}

	space.NoteScanRate("Test/Float", 250)
	response, err = service.Read(context.Background(), readRequestFor(target), time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Status.Hex())
	}
	if response.Results[0].Value.Value != float64(250) {
		t.Fatalf("interval = %#v", response.Results[0].Value.Value)
	}
}

// Clause 5.6.2: "If a limit is not known a NaN shall be used." A source that
// answers a bound and gives nothing has said the limit is unknown, which the
// Range structure has a representation for. Refusing the whole property would
// throw away the end the source did give.
func TestARangeWithAnUnknownLimitUsesNaN(t *testing.T) {
	space := testAddressSpace(t)
	known := opcda.ItemPropertyValue{OK: true, Value: float64(-50), ValuePresent: true, HRESULTPresent: true}
	// Answered, with nothing in it.
	unknown := opcda.ItemPropertyValue{OK: true, HRESULTPresent: true}
	// Refused outright, which is not the same thing.
	refused := opcda.ItemPropertyValue{HRESULT: -1073479674, HRESULTPresent: true}

	variant, status := buildRangeProperty(space, []opcda.ItemPropertyValue{known, unknown})
	if status != StatusGood {
		t.Fatalf("a range with one unknown limit answered %s", status.Hex())
	}
	object, ok := variant.Value.(ExtensionObject)
	if !ok || len(object.Body) != 16 {
		t.Fatalf("value = %#v", variant.Value)
	}
	low := math.Float64frombits(binary.LittleEndian.Uint64(object.Body[:8]))
	high := math.Float64frombits(binary.LittleEndian.Uint64(object.Body[8:]))
	if low != -50 {
		t.Fatalf("the known limit became %v", low)
	}
	if !math.IsNaN(high) {
		t.Fatalf("the unknown limit became %v, want NaN", high)
	}

	// A refusal is a failure to answer, not a statement that the limit is
	// unknown, and NaN would report the second when the first happened.
	if _, status := buildRangeProperty(space, []opcda.ItemPropertyValue{known, refused}); status != StatusBadNotReadable {
		t.Fatalf("a refused limit answered %s", status.Hex())
	}
}

// Clause 5.6.4.3: EUInformation's unitId is an "identifier for programmatic
// lookup. -1 is used if a unitId is not available." DA supplies a unit string
// and no identifier, so -1 is the answer. Zero is a number a code set may
// legitimately use, and writing it would say "unit zero" where the truth is
// "no unit identifier".
func TestEngineeringUnitsCarriesNoUnitIdentifierAsMinusOne(t *testing.T) {
	space := testAddressSpace(t)
	variant, status := buildEngineeringUnits(space, []opcda.ItemPropertyValue{
		{OK: true, Value: "degC", ValuePresent: true, HRESULTPresent: true},
	})
	if status != StatusGood {
		t.Fatalf("status = %s", status.Hex())
	}
	object, ok := variant.Value.(ExtensionObject)
	if !ok {
		t.Fatalf("value = %#v", variant.Value)
	}
	// namespaceUri is an empty String: four bytes of length, all zero for "".
	// unitId is the Int32 that follows it.
	if len(object.Body) < 8 {
		t.Fatalf("body is %d bytes", len(object.Body))
	}
	unitID := int32(binary.LittleEndian.Uint32(object.Body[4:8]))
	if unitID != -1 {
		t.Fatalf("unitId = %d, clause 5.6.4.3 says -1 when it is not available", unitID)
	}
	if !bytes.Contains(object.Body, []byte("degC")) {
		t.Fatal("the unit string is not in the displayName")
	}
}

// OPC 10000-3 8.3: "if they want to provide a standard Property, its BrowseName
// shall have the namespace of the standards body although the namespace of the
// NodeId reflects something else, for example the local Server."
//
// EURange, InstrumentRange, EngineeringUnits, TrueState and FalseState are the
// OPC Foundation's own properties of AnalogItemType and TwoStateDiscreteType. A
// client following the standard VariableType model looks for 0:EURange, and
// would not find one named in any other namespace.
func TestStandardPropertiesCarryTheStandardNamespace(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
		{ID: opcda.PropertyLowIR}, {ID: opcda.PropertyHighIR},
		{ID: opcda.PropertyEUUnits},
	}, opcda.EUTypeAnalog, testNodeBudget); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"EURange", "InstrumentRange", "EngineeringUnits"} {
		node, ok := space.Node(ItemPropertyNodeID("Test/Float", name))
		if !ok {
			t.Fatalf("%s was not attached", name)
		}
		if node.BrowseName.Namespace != 0 {
			t.Errorf("%s has BrowseName namespace %d, want the standards body's 0",
				name, node.BrowseName.Namespace)
		}
		if node.BrowseName.Name != name {
			t.Errorf("browse name = %q, want %q", node.BrowseName.Name, name)
		}
		// Only the name is the standards body's. The identifier stays this
		// server's, which is the case 8.3 describes.
		if node.ID.Namespace != AdapterNamespaceIndex {
			t.Errorf("%s has NodeId namespace %d, want this server's %d",
				name, node.ID.Namespace, AdapterNamespaceIndex)
		}
	}
}

// OPC 10000-3 5.6.3: "the BrowseName of a Property shall be unique in the
// context of the Node containing the Properties". A vendor property is named by
// its own description, and nothing stops a source describing two of an item's
// properties identically.
func TestAPropertyNameIsUniqueWithinItsItem(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4)},
	}); err != nil {
		t.Fatal(err)
	}
	varType := opcda.VTR4
	if err := space.AttachItemProperties("Test/Float", []opcda.AvailableProperty{
		// The standard EURange, so the vendor property below has something
		// real to collide with.
		{ID: opcda.PropertyLowEU}, {ID: opcda.PropertyHighEU},
		{ID: 5001, Description: "Loop Gain", VarType: varType},
		{ID: 5002, Description: "Loop Gain", VarType: varType},
		{ID: 5003, Description: "Distinct", VarType: varType},
		// A vendor description that repeats a standard property's name is kept
		// apart by its namespace, not by its text. OPC 10000-3 8.3: the
		// namespace "is provided to make the BrowseName unique in some cases
		// in the context of a Node (e.g. Properties of a Node)".
		{ID: 5004, Description: "EURange", VarType: varType},
	}, opcda.EUTypeAnalog, testNodeBudget); err != nil {
		t.Fatal(err)
	}

	item, ok := space.Node(ItemNodeID("Test/Float"))
	if !ok {
		t.Fatal("the item is not in the address space")
	}
	seen := map[QualifiedName]string{}
	for _, reference := range item.References {
		if reference.ReferenceTypeID.Numeric != NodeIDHasProperty || !reference.IsForward {
			continue
		}
		if previous, clash := seen[reference.BrowseName]; clash {
			t.Fatalf("two properties share the browse name %v: %s and %s",
				reference.BrowseName, previous, reference.TargetID.NodeID.StringID)
		}
		seen[reference.BrowseName] = reference.TargetID.NodeID.StringID
	}

	// The repeated description keeps its text and gains what tells the two
	// apart; the distinct one is left alone.
	names := map[string]bool{}
	for name := range seen {
		names[name.Name] = true
	}
	for _, want := range []string{"Loop Gain (5001)", "Loop Gain (5002)", "Distinct"} {
		if !names[want] {
			t.Fatalf("property names = %v, want %q among them", names, want)
		}
	}
	// Both EURanges are present and told apart by namespace alone.
	standard := QualifiedName{Namespace: 0, Name: "EURange"}
	vendor := QualifiedName{Namespace: AdapterNamespaceIndex, Name: "EURange"}
	if seen[standard] == "" {
		t.Fatalf("the standard EURange is missing: %v", seen)
	}
	if seen[vendor] == "" {
		t.Fatalf("the vendor EURange is missing: %v", seen)
	}
}
