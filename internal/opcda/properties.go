package opcda

// OPC DA item properties, the source of OPC 10000-8 Table A.1.
//
// A property is metadata about an item -- its engineering units, its range,
// the labels for a discrete item's two states -- rather than its value. The
// adapter reads them through IOPCItemProperties, which is optional: a server
// may not implement it, and one that does need not offer every property for
// every item.
//
// Two of these identifiers name the item's own value and quality. The adapter
// never reads them through this path. Value and Quality belong to the Read and
// Subscribe paths, which carry the timestamp and the raw quality alongside;
// fetching them here would produce a second, poorer answer to a question that
// already has one.

// PropertyID is an OPC DA item property identifier. The standard identifiers
// are declared in opcda.idl, which scripts/spec-check/check.py verifies these
// against.
type PropertyID uint32

const (
	PropertyDataType     PropertyID = 1
	PropertyValue        PropertyID = 2
	PropertyQuality      PropertyID = 3
	PropertyTimestamp    PropertyID = 4
	PropertyAccessRights PropertyID = 5
	PropertyScanRate     PropertyID = 6
	PropertyEUType       PropertyID = 7
	PropertyEUInfo       PropertyID = 8

	PropertyEUUnits     PropertyID = 100
	PropertyDescription PropertyID = 101
	PropertyHighEU      PropertyID = 102
	PropertyLowEU       PropertyID = 103
	PropertyHighIR      PropertyID = 104
	PropertyLowIR       PropertyID = 105
	PropertyCloseLabel  PropertyID = 106
	PropertyOpenLabel   PropertyID = 107
)

// EUType is OPCEUTYPE, the value of the EU Type property. Annex A.3.1.3 chooses
// an item's UA VariableType partly from it, so it is read as a value rather
// than only discovered as a property.
type EUType int32

const (
	EUTypeNoEnum     EUType = 0
	EUTypeAnalog     EUType = 1
	EUTypeEnumerated EUType = 2
)

// AvailableProperty is one property a source reports for an item, as
// IOPCItemProperties::QueryAvailableProperties reported it. The description is
// the server's own text and is passed through unchanged.
type AvailableProperty struct {
	ID          PropertyID
	Description string
	VarType     DAVarType

	// ItemID is the property's own ItemID when the source gives it one, from
	// IOPCItemProperties::LookupItemIDs. A property with one is a DA item in
	// its own right, which OPC 10000-8 A.3.1.4 is what makes it writable; a
	// property without one only describes its item.
	ItemID        DAItemID
	ItemIDPresent bool
}

// ItemPropertiesRequest asks for the value of specific properties of one item.
type ItemPropertiesRequest struct {
	ItemID     string
	Properties []PropertyID
}

// ItemPropertyValue is one property value exactly as the source reported it.
// A per-property HRESULT is a result, not a transport failure: a source may
// offer a property for one item and refuse it for another.
type ItemPropertyValue struct {
	ID             PropertyID
	OK             bool
	HRESULT        HRESULT
	HRESULTPresent bool
	VarType        DAVarType
	VarTypePresent bool
	Value          any
	ValuePresent   bool
	ErrorCode      string
}

// propertiesUnsupported is what every path answers when the source does not
// implement IOPCItemProperties. It is a capability, not a failure: the source
// is working correctly and simply does not offer properties.
const propertiesUnsupported = "IOPC ItemProperties is not implemented by this source"

// PropertyItemID reports whether one property of an item is also a DA item in
// its own right, and under which ItemID.
//
// OPC 10000-8 A.3.1.4 makes a property writable exactly when it has one, which
// is what IOPCItemProperties::LookupItemIDs answers. A property without one
// describes its item and is nothing a client can write to.
type PropertyItemID struct {
	ID      PropertyID
	ItemID  DAItemID
	Present bool
}
