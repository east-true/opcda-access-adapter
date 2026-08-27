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
	if !space.AttachItemProperties("Test/Float", runtime.available["Test/Float"], 0) {
		t.Fatal("AttachItemProperties did not attach to a known item")
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
