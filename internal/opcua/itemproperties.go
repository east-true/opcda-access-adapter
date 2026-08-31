package opcua

import (
	"fmt"
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
	// the encoding a Range in a Variant names. Both come from NodeIds.csv, as
	// do the rest of these.
	NodeIDRange                      uint32 = 884
	NodeIDRangeEncodingDefaultBinary uint32 = 886

	NodeIDLocalizedText                      uint32 = 21
	NodeIDEUInformation                      uint32 = 887
	NodeIDEUInformationEncodingDefaultBinary uint32 = 889

	// The VariableTypes Annex A.3.1.3 chooses between.
	NodeIDDataItemType           uint32 = 2365
	NodeIDAnalogItemType         uint32 = 2368
	NodeIDTwoStateDiscreteType   uint32 = 2373
	NodeIDMultiStateDiscreteType uint32 = 2376
)

// itemVariableType is one row of OPC 10000-8 Annex A.3.1.3, which chooses a DA
// item's UA VariableType from the properties the source offers for it.
//
// The adapter used to give every item BaseDataVariableType, which A.3.1.3 does
// not offer as a choice and which appears nowhere in Annex A.
type itemVariableType struct {
	Name       string
	TypeID     uint32
	Properties []itemPropertyBinding
}

// variableTypeFor applies A.3.1.3, in the order it lists.
//
// It departs from the clause in one way, deliberately. A.3.1.3 says an item is
// AnalogItemType if it has High and Low EU **or** its EU Type is Analog, and
// clause 5.3.2.3 says AnalogItemType requires EURange. An item whose EU Type is
// Analog but which offers no High and Low EU cannot have an EURange built for
// it, so claiming the type would mean claiming one whose mandatory property the
// adapter knows it cannot supply. Such an item is given DataItemType instead.
//
// MultiStateDiscreteType is never claimed, for the same reason: its mandatory
// EnumStrings comes from EU Info, whose DA value is an array of strings, and
// the DA layer does not carry array VARIANTs. A type is a promise, and the
// adapter does not make one it cannot keep.
func variableTypeFor(available []opcda.AvailableProperty, euType opcda.EUType) itemVariableType {
	offered := make(map[opcda.PropertyID]struct{}, len(available))
	for _, property := range available {
		offered[property.ID] = struct{}{}
	}
	has := func(ids ...opcda.PropertyID) bool {
		for _, id := range ids {
			if _, ok := offered[id]; !ok {
				return false
			}
		}
		return true
	}

	switch {
	case has(opcda.PropertyHighEU, opcda.PropertyLowEU):
		analog := itemVariableType{Name: "AnalogItemType", TypeID: NodeIDAnalogItemType,
			Properties: []itemPropertyBinding{euRangeBinding}}
		if has(opcda.PropertyEUUnits) {
			analog.Properties = append(analog.Properties, engineeringUnitsBinding)
		}
		if has(opcda.PropertyHighIR, opcda.PropertyLowIR) {
			analog.Properties = append(analog.Properties, instrumentRangeBinding)
		}
		return analog
	case has(opcda.PropertyCloseLabel, opcda.PropertyOpenLabel):
		return itemVariableType{Name: "TwoStateDiscreteType", TypeID: NodeIDTwoStateDiscreteType,
			Properties: []itemPropertyBinding{trueStateBinding, falseStateBinding}}
	default:
		_ = euType
		return itemVariableType{Name: "DataItemType", TypeID: NodeIDDataItemType}
	}
}

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

// The properties Annex A.3.1.3 puts on each type. Their UA types are the ones
// the standard VariableTypes carry, not Table A.1's third column: A.1 gives
// "String" for EU Units and the two labels, and A.3.1.3 assigns those same
// values to EngineeringUnits, TrueState and FalseState, where the standard
// types define EUInformation and LocalizedText. Reading A.1's column as the DA
// value's mapped type rather than the UA property's reconciles the two, and
// A.3.1.3 is the clause that forces the reading.
var (
	euRangeBinding = itemPropertyBinding{
		BrowseName: "EURange",
		DataType:   NodeIDRange,
		// Low first, so the Range fields are built in the order the structure
		// encodes them rather than the order the table lists them.
		Sources: []opcda.PropertyID{opcda.PropertyLowEU, opcda.PropertyHighEU},
		build:   buildRangeProperty,
	}
	instrumentRangeBinding = itemPropertyBinding{
		BrowseName: "InstrumentRange",
		DataType:   NodeIDRange,
		Sources:    []opcda.PropertyID{opcda.PropertyLowIR, opcda.PropertyHighIR},
		build:      buildRangeProperty,
	}
	engineeringUnitsBinding = itemPropertyBinding{
		BrowseName: "EngineeringUnits",
		DataType:   NodeIDEUInformation,
		Sources:    []opcda.PropertyID{opcda.PropertyEUUnits},
		build:      buildEngineeringUnits,
	}
	trueStateBinding = itemPropertyBinding{
		BrowseName: "TrueState",
		DataType:   NodeIDLocalizedText,
		Sources:    []opcda.PropertyID{opcda.PropertyCloseLabel},
		build:      buildLocalizedTextProperty,
	}
	falseStateBinding = itemPropertyBinding{
		BrowseName: "FalseState",
		DataType:   NodeIDLocalizedText,
		Sources:    []opcda.PropertyID{opcda.PropertyOpenLabel},
		build:      buildLocalizedTextProperty,
	}

	// Every binding, for resolving a property node identifier back to what it
	// stands for. A BrowseName belongs to at most one binding.
	allItemPropertyBindings = []itemPropertyBinding{
		euRangeBinding, instrumentRangeBinding, engineeringUnitsBinding,
		trueStateBinding, falseStateBinding,
	}
)

// bindingForBrowseName finds the Table A.1 row a property node stands for.
func bindingForBrowseName(name string) (itemPropertyBinding, bool) {
	for _, binding := range allItemPropertyBindings {
		if binding.BrowseName == name {
			return binding, true
		}
	}
	return itemPropertyBinding{}, false
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

// buildLocalizedTextProperty carries a DA string as the LocalizedText the
// standard TwoStateDiscreteType properties are defined to hold. No locale is
// invented: DA supplies text without one, so the LocalizedText has text only.
func buildLocalizedTextProperty(space *AddressSpace, values []opcda.ItemPropertyValue) (Variant, StatusCode) {
	text, status := buildStringProperty(space, values)
	if status != StatusGood {
		return text, status
	}
	return Variant{Type: BuiltInLocalizedText, Value: LocalizedText{Text: text.Value.(string)}}, StatusGood
}

// buildEngineeringUnits carries the DA EU Units string as the DisplayName of an
// EUInformation, which is what AnalogItemType's EngineeringUnits holds.
//
// DA supplies a unit string and nothing else. The NamespaceUri and UnitId of a
// UNECE code are not derived from it: guessing a code from a unit's name would
// be inventing an identity the source never gave, and a client that reads
// UnitId would then act on it.
func buildEngineeringUnits(space *AddressSpace, values []opcda.ItemPropertyValue) (Variant, StatusCode) {
	text, status := buildStringProperty(space, values)
	if status != StatusGood {
		return text, status
	}
	if space == nil {
		return NullVariant(), StatusBadInternalError
	}
	variant, ok := space.extensionObject(NodeIDEUInformationEncodingDefaultBinary, func(e *Encoder) {
		e.WriteString("")
		e.WriteInt32(0)
		e.WriteLocalizedText(LocalizedText{Text: text.Value.(string)})
		e.WriteLocalizedText(LocalizedText{})
	})
	if !ok {
		return NullVariant(), StatusBadEncodingLimitsExceeded
	}
	return variant, StatusGood
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
//
// The node budget is checked over the whole set before anything is changed, the
// way a branch is, so a set that does not fit is refused rather than attached in
// part. A client cannot tell a truncated property list from a complete one.
func (s *AddressSpace) AttachItemProperties(itemID opcda.DAItemID, available []opcda.AvailableProperty, euType opcda.EUType, maxNodes int) error {
	variableType := variableTypeFor(available, euType)
	bindings := variableType.Properties

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.nodes[nodeKey(ItemNodeID(itemID))]
	if !ok || item.Class != NodeClassVariable || item.ItemID != itemID {
		return fmt.Errorf("no DA item node for %q", itemID)
	}
	needed := 0
	for _, binding := range bindings {
		if _, exists := s.nodes[nodeKey(ItemPropertyNodeID(itemID, binding.BrowseName))]; !exists {
			needed++
		}
	}
	// The same rule as ResolveVariable: a non-positive budget attaches nothing.
	if len(s.nodes)-s.standardNodeCount+needed > max(maxNodes, 0) {
		return fmt.Errorf("%d property nodes for %q would exceed the %d node limit",
			needed, itemID, maxNodes)
	}
	// Re-attaching replaces the previous set rather than adding to it, so a
	// source that stops offering a property stops reporting it.
	item.References = keepNonPropertyReferences(item.References)
	item.DescriptionOffered = itemDescriptionOffered(available)
	// Annex A.3.1.3 chooses the type from the properties the source offers, so
	// the type is set here, where those are known, rather than at item creation
	// where they are not.
	item.TypeDefinition = NumericNodeID(0, variableType.TypeID)
	for _, binding := range bindings {
		id := ItemPropertyNodeID(itemID, binding.BrowseName)
		if _, exists := s.nodes[nodeKey(id)]; !exists {
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
	return nil
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

// NodeKind classifies what a node identifier stands for, so that "is this a DA
// item I can read, write or monitor?" is answered once.
//
// It used to be re-derived at each call site as Class == Variable && ItemID
// != "", which a Table A.1 property node satisfies: it is a variable and it
// carries the ItemID of the item it describes. Two of the three call sites got
// that wrong, and the Subscribe one monitored the item's value and reported it
// as a property.
type NodeKind int

const (
	// NodeKindUnknown is a node identifier the address space does not resolve.
	NodeKindUnknown NodeKind = iota
	// NodeKindItem is a variable standing for a DA item, which can be read,
	// written and monitored.
	NodeKindItem
	// NodeKindItemProperty is a Table A.1 property of a DA item, which can
	// only be read.
	NodeKindItemProperty
	// NodeKindOther is a node that is not a DA item at all: a folder, the
	// Server object, or one of the server's own variables.
	NodeKindOther
)

// ClassifyNode says what a node identifier stands for **without creating
// anything**. A caller that only wants to know what it is asking about uses
// this; a caller that will then act on a DA item uses ResolveNode.
//
// The distinction matters because the node budget is passed by the callers that
// resolve. Browse has none to pass, so a Browse that resolved would let a client
// grow the address space without bound by browsing ItemIDs it invented.
func (s *AddressSpace) ClassifyNode(id NodeID) (*Node, NodeKind) {
	if node, ok := s.Node(id); ok {
		switch {
		case node.IsItemPropertyNode():
			return node, NodeKindItemProperty
		case node.Class == NodeClassVariable && node.ItemID != "":
			return node, NodeKindItem
		default:
			return node, NodeKindOther
		}
	}
	if _, _, ok := ItemPropertyForNode(id); ok {
		// The identifier names a property, but the source has not said the
		// item has it. Reading it will ask the source, which is the authority.
		return nil, NodeKindItemProperty
	}
	return nil, NodeKindUnknown
}

// ResolveNode classifies a node identifier and returns the node behind it,
// creating a DA item on demand from its self-describing identifier.
//
// The creation is deliberate: a source need not implement Browse, and a client
// of such a source knows its ItemIDs from elsewhere. maxNodes bounds it. A
// property is never created here -- it exists only once the source has said the
// item has it.
func (s *AddressSpace) ResolveNode(id NodeID, maxNodes int) (*Node, NodeKind) {
	if node, kind := s.ClassifyNode(id); kind != NodeKindUnknown {
		return node, kind
	}
	if node, ok := s.ResolveVariable(id, maxNodes); ok {
		return node, NodeKindItem
	}
	return nil, NodeKindUnknown
}
