package opcua

import (
	"strings"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// OPC 10000-8 Table A.1, the OPC COM DA to OPC UA properties mapping.
//
// The table has ten rows and they do not all become properties. Access Rights
// (5) is an attribute the adapter already reports, and from a better source:
// AddItems carries the access rights, so the value is known without asking for
// the property. Item Description (101) is the Description attribute rather than
// a property node. The remaining rows are the property nodes below.
//
// Two rows of the table share one target: High EU and Low EU both map to
// EURange, and High/Low Instrument Range both map to InstrumentRange. A single
// property cannot hold two Doubles as a scalar, so each pair becomes the UA
// Range structure those BrowseNames are defined to carry, which is also what a
// client reading EURange expects to decode.
//
// EngineeringUnits, TrueState and FalseState are String here. On the standard
// AnalogItemType and TwoStateDiscreteType those carry EUInformation and
// LocalizedText, but Table A.1 says String and the DA source supplies a string.
// The table is followed rather than improved on, and the difference is recorded
// in docs/opcua-mapping.md.

const (
	// NodeIDRange is the Range DataType, and NodeIDRangeEncodingDefaultBinary
	// the encoding a Range in a Variant names. Both come from NodeIds.csv.
	NodeIDRange                      uint32 = 884
	NodeIDRangeEncodingDefaultBinary uint32 = 886
)

// itemPropertyBinding is one row of Table A.1 that becomes a property node.
type itemPropertyBinding struct {
	// BrowseName is the UA property the table names.
	BrowseName string
	// DataType is what a client reading it decodes.
	DataType uint32
	// Sources are the DA property identifiers the value is built from, in the
	// order build expects them.
	Sources []opcda.PropertyID
	// build turns the source's answers into the UA value. It is given exactly
	// one result per identifier in Sources, in that order.
	build func(space *AddressSpace, values []opcda.ItemPropertyValue) (Variant, StatusCode)
}

// tableA1 is the mapping, in the table's own order.
var tableA1 = []itemPropertyBinding{
	{
		BrowseName: "EngineeringUnits",
		DataType:   NodeIDString,
		Sources:    []opcda.PropertyID{opcda.PropertyEUUnits},
		build:      buildStringProperty,
	},
	{
		BrowseName: "EURange",
		DataType:   NodeIDRange,
		// Low first, so the Range fields are built in the order the structure
		// encodes them rather than the order the table lists them.
		Sources: []opcda.PropertyID{opcda.PropertyLowEU, opcda.PropertyHighEU},
		build:   buildRangeProperty,
	},
	{
		BrowseName: "InstrumentRange",
		DataType:   NodeIDRange,
		Sources:    []opcda.PropertyID{opcda.PropertyLowIR, opcda.PropertyHighIR},
		build:      buildRangeProperty,
	},
	{
		BrowseName: "TrueState",
		DataType:   NodeIDString,
		Sources:    []opcda.PropertyID{opcda.PropertyCloseLabel},
		build:      buildStringProperty,
	},
	{
		BrowseName: "FalseState",
		DataType:   NodeIDString,
		Sources:    []opcda.PropertyID{opcda.PropertyOpenLabel},
		build:      buildStringProperty,
	},
}

// bindingForBrowseName finds the Table A.1 row a property node stands for.
func bindingForBrowseName(name string) (itemPropertyBinding, bool) {
	for _, binding := range tableA1 {
		if binding.BrowseName == name {
			return binding, true
		}
	}
	return itemPropertyBinding{}, false
}

// bindingsForAvailable reports which Table A.1 property nodes an item has,
// given what the source said it offers. A row appears only when the source
// offers every DA property that row is built from: a Range with one end
// missing is not a Range, and inventing the other end would be synthesis.
func bindingsForAvailable(available []opcda.AvailableProperty) []itemPropertyBinding {
	offered := make(map[opcda.PropertyID]struct{}, len(available))
	for _, property := range available {
		offered[property.ID] = struct{}{}
	}
	bindings := make([]itemPropertyBinding, 0, len(tableA1))
	for _, binding := range tableA1 {
		complete := true
		for _, source := range binding.Sources {
			if _, ok := offered[source]; !ok {
				complete = false
				break
			}
		}
		if complete {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// itemDescriptionOffered reports whether the source offers Item Description,
// which Table A.1 maps to the Description attribute rather than to a property.
func itemDescriptionOffered(available []opcda.AvailableProperty) bool {
	for _, property := range available {
		if property.ID == opcda.PropertyDescription {
			return true
		}
	}
	return false
}

func buildStringProperty(_ *AddressSpace, values []opcda.ItemPropertyValue) (Variant, StatusCode) {
	if len(values) != 1 {
		return NullVariant(), StatusBadInternalError
	}
	if status := propertyStatus(values[0]); status != StatusGood {
		return NullVariant(), status
	}
	text, ok := values[0].Value.(string)
	if !ok {
		// The source answered a type the table does not describe. Reporting the
		// mismatch is the honest answer; converting would be inventing one.
		return NullVariant(), StatusBadTypeMismatch
	}
	return Variant{Type: BuiltInString, Value: text}, StatusGood
}

func buildRangeProperty(space *AddressSpace, values []opcda.ItemPropertyValue) (Variant, StatusCode) {
	if len(values) != 2 || space == nil {
		return NullVariant(), StatusBadInternalError
	}
	bounds := make([]float64, 2)
	for index, value := range values {
		if status := propertyStatus(value); status != StatusGood {
			return NullVariant(), status
		}
		number, ok := asFloat64(value.Value)
		if !ok {
			return NullVariant(), StatusBadTypeMismatch
		}
		bounds[index] = number
	}
	variant, ok := space.extensionObject(NodeIDRangeEncodingDefaultBinary, func(e *Encoder) {
		e.WriteDouble(bounds[0])
		e.WriteDouble(bounds[1])
	})
	if !ok {
		return NullVariant(), StatusBadEncodingLimitsExceeded
	}
	return variant, StatusGood
}

// propertyStatus maps one DA property result onto a UA status. A per-property
// HRESULT is a result rather than a failure, so it goes through the same
// Table A.4 mapping every other read error does.
func propertyStatus(value opcda.ItemPropertyValue) StatusCode {
	if value.OK && value.ValuePresent {
		return StatusGood
	}
	if value.HRESULTPresent && !value.HRESULT.Succeeded() {
		return StatusCodeForReadError(value.HRESULT)
	}
	if value.ErrorCode != "" {
		return StatusBadTypeMismatch
	}
	// The source succeeded and reported nothing, which is not a value.
	return StatusBadNoData
}

// asFloat64 accepts the numeric VARTYPEs a source may answer a Double-valued
// property with. Table A.2 maps each of these to a UA numeric type, and a Range
// bound is a Double, so a narrower source type widens rather than being
// refused. No value is altered: every one of these is exactly representable.
func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	default:
		return 0, false
	}
}

// propertyNodePrefix marks a node identifier as naming a property of a DA item.
const propertyNodePrefix = "property:"

// propertyNodeSeparator cannot appear in a UA BrowseName, so a property node
// identifier can always be split back into its two parts.
const propertyNodeSeparator = "\x1f"

// ItemPropertyNodeID is the node identifier for one Table A.1 property of a DA
// item. Like an item node it is self-describing and carries the exact ItemID,
// so a client that knows its ItemIDs can read a property without browsing --
// which matters because DA Browse is optional.
func ItemPropertyNodeID(itemID opcda.DAItemID, browseName string) NodeID {
	return StringNodeID(AdapterNamespaceIndex,
		propertyNodePrefix+browseName+propertyNodeSeparator+string(itemID))
}

// ItemPropertyForNode recovers the item and property a node identifier names.
func ItemPropertyForNode(id NodeID) (opcda.DAItemID, itemPropertyBinding, bool) {
	if id.Namespace != AdapterNamespaceIndex || id.Type != NodeIDTypeString {
		return "", itemPropertyBinding{}, false
	}
	rest, found := strings.CutPrefix(id.StringID, propertyNodePrefix)
	if !found {
		return "", itemPropertyBinding{}, false
	}
	name, itemID, found := strings.Cut(rest, propertyNodeSeparator)
	if !found || itemID == "" {
		return "", itemPropertyBinding{}, false
	}
	binding, ok := bindingForBrowseName(name)
	if !ok {
		return "", itemPropertyBinding{}, false
	}
	return opcda.DAItemID(itemID), binding, true
}

// AttachItemProperties gives a source variable the Table A.1 property nodes the
// source says it has, and nothing else.
//
// It is called with what QueryAvailableProperties answered, so the address
// space claims a property only when the source offers every DA property that
// property is built from. Values are never stored here: a property node knows
// which item and which DA properties it stands for, and its value is read from
// the source when a client asks for it.
func (s *AddressSpace) AttachItemProperties(itemID opcda.DAItemID, available []opcda.AvailableProperty, maxNodes int) bool {
	bindings := bindingsForAvailable(available)

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.nodes[nodeKey(ItemNodeID(itemID))]
	if !ok || item.Class != NodeClassVariable || item.ItemID != itemID {
		return false
	}
	// Re-attaching replaces the previous set rather than adding to it, so a
	// source that stops offering a property stops reporting it.
	item.References = keepNonPropertyReferences(item.References)
	item.DescriptionOffered = itemDescriptionOffered(available)
	for _, binding := range bindings {
		id := ItemPropertyNodeID(itemID, binding.BrowseName)
		if _, exists := s.nodes[nodeKey(id)]; !exists {
			if maxNodes > 0 && len(s.nodes)-s.standardNodeCount >= maxNodes {
				return false
			}
			s.nodes[nodeKey(id)] = &Node{
				ID:             id,
				Class:          NodeClassVariable,
				BrowseName:     QualifiedName{Namespace: AdapterNamespaceIndex, Name: binding.BrowseName},
				DisplayName:    LocalizedText{Text: binding.BrowseName},
				TypeDefinition: NumericNodeID(0, NodeIDPropertyType),
				DataType:       NumericNodeID(0, binding.DataType),
				DataTypeKnown:  true,
				ValueRank:      ValueRankScalar,
				ItemID:         itemID,
				// A property is readable. Whether the source will actually
				// answer is the source's decision, reported per property.
				AccessLevel:       AccessLevelCurrentRead,
				AccessRightsKnown: true,
			}
		}
		property := s.nodes[nodeKey(id)]
		addForward(item, NumericNodeID(0, NodeIDHasProperty), property)
		if len(property.References) == 0 {
			addInverse(property, NumericNodeID(0, NodeIDHasProperty), item)
		}
	}
	return true
}

// keepNonPropertyReferences drops the HasProperty references a previous attach
// added, leaving every other reference untouched.
func keepNonPropertyReferences(references []Reference) []Reference {
	hasProperty := NumericNodeID(0, NodeIDHasProperty)
	kept := references[:0]
	for _, reference := range references {
		if reference.IsForward && reference.ReferenceTypeID.Equal(hasProperty) {
			continue
		}
		kept = append(kept, reference)
	}
	return kept
}

// IsItemPropertyNode reports whether a node stands for a Table A.1 property of
// a DA item rather than for the item itself.
func (n *Node) IsItemPropertyNode() bool {
	if n == nil {
		return false
	}
	_, _, ok := ItemPropertyForNode(n.ID)
	return ok
}
