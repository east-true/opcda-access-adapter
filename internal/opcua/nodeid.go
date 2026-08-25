package opcua

import (
	"bytes"
	"fmt"
	"time"
)

// NodeId, ExpandedNodeId, QualifiedName, LocalizedText, DiagnosticInfo, and
// ExtensionObject encodings follow OPC 10000-6 clauses 5.2.2.9 through
// 5.2.2.15, transcribed from Tables 16 through 25.

// NodeIDType is the identifier form of OPC 10000-6 Table 16.
type NodeIDType uint8

const (
	NodeIDTypeNumeric NodeIDType = iota
	NodeIDTypeString
	NodeIDTypeGuid
	NodeIDTypeOpaque
)

// NodeId encoding byte values from OPC 10000-6 Table 17. The low six bits
// select the format; the top two are the ExpandedNodeId flags.
const (
	nodeIDEncodingTwoByte    byte = 0x00
	nodeIDEncodingFourByte   byte = 0x01
	nodeIDEncodingNumeric    byte = 0x02
	nodeIDEncodingString     byte = 0x03
	nodeIDEncodingGuid       byte = 0x04
	nodeIDEncodingByteString byte = 0x05
	nodeIDEncodingMask       byte = 0x3F

	nodeIDFlagNamespaceURI byte = 0x80
	nodeIDFlagServerIndex  byte = 0x40
)

// NodeID is a node identifier. Exactly one identifier field is meaningful, as
// selected by Type.
type NodeID struct {
	Namespace uint16
	Type      NodeIDType
	Numeric   uint32
	// StringID is the identifier for NodeIDTypeString. It is not named String
	// so the type can keep a String method.
	StringID string
	Guid     Guid
	Opaque   []byte
}

// NumericNodeID builds the common numeric form.
func NumericNodeID(namespace uint16, identifier uint32) NodeID {
	return NodeID{Namespace: namespace, Type: NodeIDTypeNumeric, Numeric: identifier}
}

// StringNodeID builds a string node identifier. The DA frontend uses this form
// so an exact DA ItemID can be carried without reshaping it.
func StringNodeID(namespace uint16, identifier string) NodeID {
	return NodeID{Namespace: namespace, Type: NodeIDTypeString, StringID: identifier}
}

// IsNull reports the null NodeId, which OPC 10000-6 5.2.2.15 writes as i=0.
func (n NodeID) IsNull() bool {
	return n.Namespace == 0 && n.Type == NodeIDTypeNumeric && n.Numeric == 0
}

// Equal compares two node identifiers. NodeID carries a byte slice for the
// opaque form, so it is not comparable with ==.
func (n NodeID) Equal(other NodeID) bool {
	if n.Namespace != other.Namespace || n.Type != other.Type {
		return false
	}
	switch n.Type {
	case NodeIDTypeString:
		return n.StringID == other.StringID
	case NodeIDTypeGuid:
		return n.Guid == other.Guid
	case NodeIDTypeOpaque:
		return bytes.Equal(n.Opaque, other.Opaque)
	default:
		return n.Numeric == other.Numeric
	}
}

func (n NodeID) String() string {
	switch n.Type {
	case NodeIDTypeString:
		return fmt.Sprintf("ns=%d;s=%s", n.Namespace, n.StringID)
	case NodeIDTypeGuid:
		return fmt.Sprintf("ns=%d;g=%08X-%04X-%04X-%X", n.Namespace, n.Guid.Data1, n.Guid.Data2, n.Guid.Data3, n.Guid.Data4)
	case NodeIDTypeOpaque:
		return fmt.Sprintf("ns=%d;b=<%d bytes>", n.Namespace, len(n.Opaque))
	default:
		return fmt.Sprintf("ns=%d;i=%d", n.Namespace, n.Numeric)
	}
}

// WriteNodeID picks the most compact DataEncoding the value fits, which is what
// Table 17's two-byte and four-byte forms exist for.
func (e *Encoder) WriteNodeID(value NodeID) {
	e.writeNodeIDWithFlags(value, 0)
}

func (e *Encoder) writeNodeIDWithFlags(value NodeID, flags byte) {
	switch value.Type {
	case NodeIDTypeNumeric:
		switch {
		case flags == 0 && value.Namespace == 0 && value.Numeric <= 0xFF:
			e.WriteByteValue(nodeIDEncodingTwoByte)
			e.WriteByteValue(byte(value.Numeric))
		case flags == 0 && value.Namespace <= 0xFF && value.Numeric <= 0xFFFF:
			e.WriteByteValue(nodeIDEncodingFourByte)
			e.WriteByteValue(byte(value.Namespace))
			e.WriteUInt16(uint16(value.Numeric))
		default:
			e.WriteByteValue(nodeIDEncodingNumeric | flags)
			e.WriteUInt16(value.Namespace)
			e.WriteUInt32(value.Numeric)
		}
	case NodeIDTypeString:
		e.WriteByteValue(nodeIDEncodingString | flags)
		e.WriteUInt16(value.Namespace)
		e.WriteString(value.StringID)
	case NodeIDTypeGuid:
		e.WriteByteValue(nodeIDEncodingGuid | flags)
		e.WriteUInt16(value.Namespace)
		e.WriteGuid(value.Guid)
	case NodeIDTypeOpaque:
		e.WriteByteValue(nodeIDEncodingByteString | flags)
		e.WriteUInt16(value.Namespace)
		e.WriteByteString(value.Opaque)
	default:
		e.fail(encodingError("NodeId identifier type %d is not defined", value.Type))
	}
}

func (d *Decoder) readNodeIDWithFlags() (NodeID, byte, error) {
	encoding, err := d.ReadByteValue()
	if err != nil {
		return NodeID{}, 0, err
	}
	flags := encoding &^ nodeIDEncodingMask
	switch encoding & nodeIDEncodingMask {
	case nodeIDEncodingTwoByte:
		identifier, readErr := d.ReadByteValue()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		return NumericNodeID(0, uint32(identifier)), flags, nil
	case nodeIDEncodingFourByte:
		namespace, readErr := d.ReadByteValue()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		identifier, readErr := d.ReadUInt16()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		return NumericNodeID(uint16(namespace), uint32(identifier)), flags, nil
	case nodeIDEncodingNumeric:
		namespace, readErr := d.ReadUInt16()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		identifier, readErr := d.ReadUInt32()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		return NumericNodeID(namespace, identifier), flags, nil
	case nodeIDEncodingString:
		namespace, readErr := d.ReadUInt16()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		identifier, isNull, readErr := d.ReadString()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		if isNull {
			identifier = ""
		}
		return StringNodeID(namespace, identifier), flags, nil
	case nodeIDEncodingGuid:
		namespace, readErr := d.ReadUInt16()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		identifier, readErr := d.ReadGuid()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		return NodeID{Namespace: namespace, Type: NodeIDTypeGuid, Guid: identifier}, flags, nil
	case nodeIDEncodingByteString:
		namespace, readErr := d.ReadUInt16()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		identifier, isNull, readErr := d.ReadByteString()
		if readErr != nil {
			return NodeID{}, 0, readErr
		}
		if isNull {
			identifier = nil
		}
		return NodeID{Namespace: namespace, Type: NodeIDTypeOpaque, Opaque: identifier}, flags, nil
	default:
		return NodeID{}, 0, decodingError("NodeId encoding 0x%02X is not defined", encoding)
	}
}

// ReadNodeID rejects the ExpandedNodeId flags: a plain NodeId must not carry
// them, and silently ignoring them would misread the rest of the stream.
func (d *Decoder) ReadNodeID() (NodeID, error) {
	value, flags, err := d.readNodeIDWithFlags()
	if err != nil {
		return NodeID{}, err
	}
	if flags != 0 {
		return NodeID{}, decodingError("NodeId carried ExpandedNodeId flags 0x%02X", flags)
	}
	return value, nil
}

// ExpandedNodeID extends NodeId with an optional NamespaceUri and ServerIndex,
// per OPC 10000-6 5.2.2.10.
type ExpandedNodeID struct {
	NodeID          NodeID
	NamespaceURI    string
	HasNamespaceURI bool
	ServerIndex     uint32
	HasServerIndex  bool
}

// WriteExpandedNodeID follows 5.2.2.10: when the NamespaceUri is present the
// NamespaceIndex is written as 0, because the index is then to be ignored.
func (e *Encoder) WriteExpandedNodeID(value ExpandedNodeID) {
	var flags byte
	node := value.NodeID
	if value.HasNamespaceURI {
		flags |= nodeIDFlagNamespaceURI
		node.Namespace = 0
	}
	if value.HasServerIndex {
		flags |= nodeIDFlagServerIndex
	}
	e.writeNodeIDWithFlags(node, flags)
	if value.HasNamespaceURI {
		e.WriteString(value.NamespaceURI)
	}
	if value.HasServerIndex {
		e.WriteUInt32(value.ServerIndex)
	}
}

func (d *Decoder) ReadExpandedNodeID() (ExpandedNodeID, error) {
	node, flags, err := d.readNodeIDWithFlags()
	if err != nil {
		return ExpandedNodeID{}, err
	}
	value := ExpandedNodeID{NodeID: node}
	if flags&nodeIDFlagNamespaceURI != 0 {
		uri, isNull, readErr := d.ReadString()
		if readErr != nil {
			return ExpandedNodeID{}, readErr
		}
		value.HasNamespaceURI = true
		if !isNull {
			value.NamespaceURI = uri
		}
	}
	if flags&nodeIDFlagServerIndex != 0 {
		index, readErr := d.ReadUInt32()
		if readErr != nil {
			return ExpandedNodeID{}, readErr
		}
		value.HasServerIndex = true
		value.ServerIndex = index
	}
	return value, nil
}

// QualifiedName is OPC 10000-6 Table 23.
type QualifiedName struct {
	Namespace uint16
	Name      string
}

func (e *Encoder) WriteQualifiedName(value QualifiedName) {
	e.WriteUInt16(value.Namespace)
	e.WriteString(value.Name)
}

func (d *Decoder) ReadQualifiedName() (QualifiedName, error) {
	namespace, err := d.ReadUInt16()
	if err != nil {
		return QualifiedName{}, err
	}
	name, isNull, err := d.ReadString()
	if err != nil {
		return QualifiedName{}, err
	}
	if isNull {
		name = ""
	}
	return QualifiedName{Namespace: namespace, Name: name}, nil
}

// LocalizedText encoding mask bits from OPC 10000-6 Table 24.
const (
	localizedTextHasLocale byte = 0x01
	localizedTextHasText   byte = 0x02
)

// LocalizedText is Table 24. A field that is null or empty is omitted from the
// stream rather than written as an empty string.
type LocalizedText struct {
	Locale string
	Text   string
}

func (e *Encoder) WriteLocalizedText(value LocalizedText) {
	var mask byte
	if value.Locale != "" {
		mask |= localizedTextHasLocale
	}
	if value.Text != "" {
		mask |= localizedTextHasText
	}
	e.WriteByteValue(mask)
	if mask&localizedTextHasLocale != 0 {
		e.WriteString(value.Locale)
	}
	if mask&localizedTextHasText != 0 {
		e.WriteString(value.Text)
	}
}

func (d *Decoder) ReadLocalizedText() (LocalizedText, error) {
	mask, err := d.ReadByteValue()
	if err != nil {
		return LocalizedText{}, err
	}
	var value LocalizedText
	if mask&localizedTextHasLocale != 0 {
		locale, isNull, readErr := d.ReadString()
		if readErr != nil {
			return LocalizedText{}, readErr
		}
		if !isNull {
			value.Locale = locale
		}
	}
	if mask&localizedTextHasText != 0 {
		text, isNull, readErr := d.ReadString()
		if readErr != nil {
			return LocalizedText{}, readErr
		}
		if !isNull {
			value.Text = text
		}
	}
	return value, nil
}

// ExtensionObject body encodings from OPC 10000-6 Table 25.
const (
	ExtensionObjectNoBody     byte = 0x00
	ExtensionObjectByteString byte = 0x01
	ExtensionObjectXMLElement byte = 0x02
)

// ExtensionObject is Table 25. The body is kept as raw bytes: this adapter does
// not decode structures it has no schema for, and the clause allows a decoder
// that does not recognise the TypeId to treat the body as opaque.
type ExtensionObject struct {
	TypeID   NodeID
	Encoding byte
	Body     []byte
}

// NullExtensionObject is the null form the clause defines: TypeId i=0 with no
// body encoded.
func NullExtensionObject() ExtensionObject {
	return ExtensionObject{TypeID: NumericNodeID(0, 0), Encoding: ExtensionObjectNoBody}
}

func (e *Encoder) WriteExtensionObject(value ExtensionObject) {
	e.WriteNodeID(value.TypeID)
	switch value.Encoding {
	case ExtensionObjectNoBody:
		e.WriteByteValue(ExtensionObjectNoBody)
	case ExtensionObjectByteString, ExtensionObjectXMLElement:
		e.WriteByteValue(value.Encoding)
		e.WriteByteString(value.Body)
	default:
		e.fail(encodingError("ExtensionObject encoding 0x%02X is not defined", value.Encoding))
	}
}

func (d *Decoder) ReadExtensionObject() (ExtensionObject, error) {
	typeID, err := d.ReadNodeID()
	if err != nil {
		return ExtensionObject{}, err
	}
	encoding, err := d.ReadByteValue()
	if err != nil {
		return ExtensionObject{}, err
	}
	value := ExtensionObject{TypeID: typeID, Encoding: encoding}
	switch encoding {
	case ExtensionObjectNoBody:
		return value, nil
	case ExtensionObjectByteString, ExtensionObjectXMLElement:
		body, isNull, readErr := d.ReadByteString()
		if readErr != nil {
			return ExtensionObject{}, readErr
		}
		if !isNull {
			value.Body = body
		}
		return value, nil
	default:
		return ExtensionObject{}, decodingError("ExtensionObject encoding 0x%02X is not defined", encoding)
	}
}

// DiagnosticInfo encoding mask bits from OPC 10000-6 Table 22.
const (
	diagnosticHasSymbolicID    byte = 0x01
	diagnosticHasNamespace     byte = 0x02
	diagnosticHasLocalizedText byte = 0x04
	diagnosticHasLocale        byte = 0x08
	diagnosticHasAdditional    byte = 0x10
	diagnosticHasInnerStatus   byte = 0x20
	diagnosticHasInnerDiag     byte = 0x40
)

// MaxDiagnosticRecursion is the ceiling OPC 10000-6 5.2.2.12 sets: decoders
// shall support at least 4 recursion levels and are not expected to support
// more than 10.
const (
	MinDiagnosticRecursion = 4
	MaxDiagnosticRecursion = 10
)

// DiagnosticInfo is Table 22. The string fields are indexes into the response
// header's string table, where -1 means no value.
type DiagnosticInfo struct {
	SymbolicID          int32
	HasSymbolicID       bool
	NamespaceURI        int32
	HasNamespaceURI     bool
	Locale              int32
	HasLocale           bool
	LocalizedText       int32
	HasLocalizedText    bool
	AdditionalInfo      string
	HasAdditionalInfo   bool
	InnerStatusCode     StatusCode
	HasInnerStatusCode  bool
	InnerDiagnosticInfo *DiagnosticInfo
}

// The stream order of Table 22 is SymbolicId, NamespaceUri, Locale,
// LocalizedText — note that this is not the order the mask bits are listed in,
// where LocalizedText is 0x04 and Locale is 0x08. The bits select presence; the
// table rows fix the order.
func (e *Encoder) WriteDiagnosticInfo(value DiagnosticInfo) {
	e.writeDiagnosticInfo(value, 0)
}

func (e *Encoder) writeDiagnosticInfo(value DiagnosticInfo, depth int) {
	if depth > MaxDiagnosticRecursion {
		e.fail(limitsError("DiagnosticInfo exceeds the %d level recursion limit", MaxDiagnosticRecursion))
		return
	}
	var mask byte
	if value.HasSymbolicID {
		mask |= diagnosticHasSymbolicID
	}
	if value.HasNamespaceURI {
		mask |= diagnosticHasNamespace
	}
	if value.HasLocalizedText {
		mask |= diagnosticHasLocalizedText
	}
	if value.HasLocale {
		mask |= diagnosticHasLocale
	}
	if value.HasAdditionalInfo {
		mask |= diagnosticHasAdditional
	}
	if value.HasInnerStatusCode {
		mask |= diagnosticHasInnerStatus
	}
	if value.InnerDiagnosticInfo != nil {
		mask |= diagnosticHasInnerDiag
	}
	e.WriteByteValue(mask)
	if value.HasSymbolicID {
		e.WriteInt32(value.SymbolicID)
	}
	if value.HasNamespaceURI {
		e.WriteInt32(value.NamespaceURI)
	}
	if value.HasLocale {
		e.WriteInt32(value.Locale)
	}
	if value.HasLocalizedText {
		e.WriteInt32(value.LocalizedText)
	}
	if value.HasAdditionalInfo {
		e.WriteString(value.AdditionalInfo)
	}
	if value.HasInnerStatusCode {
		e.WriteStatusCode(value.InnerStatusCode)
	}
	if value.InnerDiagnosticInfo != nil {
		e.writeDiagnosticInfo(*value.InnerDiagnosticInfo, depth+1)
	}
}

// ReadDiagnosticInfo bounds recursion at MaxDiagnosticRecursion, which the
// clause requires a decoder to enforce rather than follow indefinitely.
func (d *Decoder) ReadDiagnosticInfo() (DiagnosticInfo, error) {
	return d.readDiagnosticInfo(0)
}

func (d *Decoder) readDiagnosticInfo(depth int) (DiagnosticInfo, error) {
	if depth > MaxDiagnosticRecursion {
		return DiagnosticInfo{}, limitsError(
			"DiagnosticInfo exceeds the %d level recursion limit", MaxDiagnosticRecursion)
	}
	mask, err := d.ReadByteValue()
	if err != nil {
		return DiagnosticInfo{}, err
	}
	var value DiagnosticInfo
	readIndex := func(target *int32, present *bool) error {
		index, readErr := d.ReadInt32()
		if readErr != nil {
			return readErr
		}
		*target = index
		*present = true
		return nil
	}
	if mask&diagnosticHasSymbolicID != 0 {
		if err := readIndex(&value.SymbolicID, &value.HasSymbolicID); err != nil {
			return DiagnosticInfo{}, err
		}
	}
	if mask&diagnosticHasNamespace != 0 {
		if err := readIndex(&value.NamespaceURI, &value.HasNamespaceURI); err != nil {
			return DiagnosticInfo{}, err
		}
	}
	if mask&diagnosticHasLocale != 0 {
		if err := readIndex(&value.Locale, &value.HasLocale); err != nil {
			return DiagnosticInfo{}, err
		}
	}
	if mask&diagnosticHasLocalizedText != 0 {
		if err := readIndex(&value.LocalizedText, &value.HasLocalizedText); err != nil {
			return DiagnosticInfo{}, err
		}
	}
	if mask&diagnosticHasAdditional != 0 {
		info, isNull, readErr := d.ReadString()
		if readErr != nil {
			return DiagnosticInfo{}, readErr
		}
		value.HasAdditionalInfo = true
		if !isNull {
			value.AdditionalInfo = info
		}
	}
	if mask&diagnosticHasInnerStatus != 0 {
		status, readErr := d.ReadStatusCode()
		if readErr != nil {
			return DiagnosticInfo{}, readErr
		}
		value.InnerStatusCode = status
		value.HasInnerStatusCode = true
	}
	if mask&diagnosticHasInnerDiag != 0 {
		inner, readErr := d.readDiagnosticInfo(depth + 1)
		if readErr != nil {
			return DiagnosticInfo{}, readErr
		}
		value.InnerDiagnosticInfo = &inner
	}
	return value, nil
}

// RequestHeader is OPC 10000-4 Table 171.
type RequestHeader struct {
	AuthenticationToken NodeID
	Timestamp           time.Time
	RequestHandle       uint32
	ReturnDiagnostics   uint32
	AuditEntryID        string
	TimeoutHint         uint32
	AdditionalHeader    ExtensionObject
}

// ResponseHeader is OPC 10000-4 Table 172.
type ResponseHeader struct {
	Timestamp          time.Time
	RequestHandle      uint32
	ServiceResult      StatusCode
	ServiceDiagnostics DiagnosticInfo
	StringTable        []string
	AdditionalHeader   ExtensionObject
}

func (e *Encoder) WriteRequestHeader(header RequestHeader) {
	e.WriteNodeID(header.AuthenticationToken)
	e.WriteDateTime(header.Timestamp)
	e.WriteUInt32(header.RequestHandle)
	e.WriteUInt32(header.ReturnDiagnostics)
	e.WriteString(header.AuditEntryID)
	e.WriteUInt32(header.TimeoutHint)
	e.WriteExtensionObject(header.AdditionalHeader)
}

func (d *Decoder) ReadRequestHeader() (RequestHeader, error) {
	var header RequestHeader
	var err error
	if header.AuthenticationToken, err = d.ReadNodeID(); err != nil {
		return RequestHeader{}, err
	}
	if header.Timestamp, err = d.ReadDateTime(); err != nil {
		return RequestHeader{}, err
	}
	if header.RequestHandle, err = d.ReadUInt32(); err != nil {
		return RequestHeader{}, err
	}
	if header.ReturnDiagnostics, err = d.ReadUInt32(); err != nil {
		return RequestHeader{}, err
	}
	auditEntry, isNull, err := d.ReadString()
	if err != nil {
		return RequestHeader{}, err
	}
	if !isNull {
		header.AuditEntryID = auditEntry
	}
	if header.TimeoutHint, err = d.ReadUInt32(); err != nil {
		return RequestHeader{}, err
	}
	if header.AdditionalHeader, err = d.ReadExtensionObject(); err != nil {
		return RequestHeader{}, err
	}
	return header, nil
}

func (e *Encoder) WriteResponseHeader(header ResponseHeader) {
	e.WriteDateTime(header.Timestamp)
	e.WriteUInt32(header.RequestHandle)
	e.WriteStatusCode(header.ServiceResult)
	e.WriteDiagnosticInfo(header.ServiceDiagnostics)
	if header.StringTable == nil {
		e.WriteNullArray()
	} else {
		e.WriteArrayLength(len(header.StringTable))
		for _, entry := range header.StringTable {
			e.WriteString(entry)
		}
	}
	e.WriteExtensionObject(header.AdditionalHeader)
}

func (d *Decoder) ReadResponseHeader() (ResponseHeader, error) {
	var header ResponseHeader
	var err error
	if header.Timestamp, err = d.ReadDateTime(); err != nil {
		return ResponseHeader{}, err
	}
	if header.RequestHandle, err = d.ReadUInt32(); err != nil {
		return ResponseHeader{}, err
	}
	if header.ServiceResult, err = d.ReadStatusCode(); err != nil {
		return ResponseHeader{}, err
	}
	if header.ServiceDiagnostics, err = d.ReadDiagnosticInfo(); err != nil {
		return ResponseHeader{}, err
	}
	// A String is at least its 4 byte length prefix.
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil {
		return ResponseHeader{}, err
	}
	if !isNull {
		header.StringTable = make([]string, 0, length)
		for index := 0; index < length; index++ {
			entry, entryIsNull, readErr := d.ReadString()
			if readErr != nil {
				return ResponseHeader{}, readErr
			}
			if entryIsNull {
				entry = ""
			}
			header.StringTable = append(header.StringTable, entry)
		}
	}
	if header.AdditionalHeader, err = d.ReadExtensionObject(); err != nil {
		return ResponseHeader{}, err
	}
	return header, nil
}
