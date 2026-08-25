package opcua

import (
	"bytes"
	"testing"
	"time"
)

func roundTripNodeID(t *testing.T, value NodeID) (NodeID, []byte) {
	t.Helper()
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteNodeID(value)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, DefaultBinaryLimits())
	decoded, err := decoder.ReadNodeID()
	if err != nil {
		t.Fatal(err)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left after the NodeId", decoder.Remaining())
	}
	return decoded, encoded
}

// OPC 10000-6 Tables 17 to 20: the compact forms exist to be used, and the
// clause's own worked examples fix their bytes.
func TestNodeIDCompactEncodings(t *testing.T) {
	// Table 19: Two Byte, namespace 0, identifier 72.
	decoded, encoded := roundTripNodeID(t, NumericNodeID(0, 72))
	if !bytes.Equal(encoded, []byte{0x00, 72}) {
		t.Fatalf("two byte NodeId = % X", encoded)
	}
	if !decoded.Equal(NumericNodeID(0, 72)) {
		t.Fatalf("decoded = %s", decoded)
	}

	// Table 20: Four Byte, namespace 5, identifier 1025.
	decoded, encoded = roundTripNodeID(t, NumericNodeID(5, 1025))
	if !bytes.Equal(encoded, []byte{0x01, 5, 0x01, 0x04}) {
		t.Fatalf("four byte NodeId = % X", encoded)
	}
	if !decoded.Equal(NumericNodeID(5, 1025)) {
		t.Fatalf("decoded = %s", decoded)
	}

	// Beyond the compact ranges the standard numeric form is used.
	_, encoded = roundTripNodeID(t, NumericNodeID(300, 70000))
	if encoded[0] != nodeIDEncodingNumeric {
		t.Fatalf("wide NodeId used encoding 0x%02X", encoded[0])
	}
	// A namespace that does not fit a byte forces the standard form even for a
	// small identifier.
	_, encoded = roundTripNodeID(t, NumericNodeID(256, 1))
	if encoded[0] != nodeIDEncodingNumeric {
		t.Fatalf("namespace 256 used encoding 0x%02X", encoded[0])
	}
}

func TestNodeIDIdentifierTypes(t *testing.T) {
	guid := Guid{Data1: 1, Data2: 2, Data3: 3, Data4: [8]byte{4, 5, 6, 7, 8, 9, 10, 11}}
	cases := []NodeID{
		StringNodeID(1, "Hot水"),
		// The DA frontend carries an exact ItemID, delimiters and all.
		StringNodeID(2, "Test/Float"),
		{Namespace: 3, Type: NodeIDTypeGuid, Guid: guid},
		{Namespace: 4, Type: NodeIDTypeOpaque, Opaque: []byte{9, 8, 7}},
	}
	for _, value := range cases {
		t.Run(value.String(), func(t *testing.T) {
			decoded, _ := roundTripNodeID(t, value)
			if decoded.Namespace != value.Namespace || decoded.Type != value.Type {
				t.Fatalf("decoded = %+v", decoded)
			}
			switch value.Type {
			case NodeIDTypeString:
				if decoded.StringID != value.StringID {
					t.Fatalf("string identifier = %q", decoded.StringID)
				}
			case NodeIDTypeGuid:
				if decoded.Guid != value.Guid {
					t.Fatalf("guid identifier = %+v", decoded.Guid)
				}
			case NodeIDTypeOpaque:
				if !bytes.Equal(decoded.Opaque, value.Opaque) {
					t.Fatalf("opaque identifier = %v", decoded.Opaque)
				}
			}
		})
	}
}

func TestNullNodeID(t *testing.T) {
	if !NumericNodeID(0, 0).IsNull() {
		t.Fatal("i=0 was not recognised as null")
	}
	if NumericNodeID(0, 1).IsNull() || NumericNodeID(1, 0).IsNull() {
		t.Fatal("a non-null NodeId was recognised as null")
	}
}

// A plain NodeId must not carry the ExpandedNodeId flags; ignoring them would
// leave the rest of the stream misaligned.
func TestNodeIDRejectsExpandedFlagsAndUnknownEncodings(t *testing.T) {
	limits := DefaultBinaryLimits()
	for _, encoding := range []byte{
		nodeIDEncodingTwoByte | nodeIDFlagNamespaceURI,
		nodeIDEncodingTwoByte | nodeIDFlagServerIndex,
	} {
		decoder := newTestDecoder(t, []byte{encoding, 1, 0, 0, 0, 0}, limits)
		if _, err := decoder.ReadNodeID(); err == nil {
			t.Fatalf("encoding 0x%02X was accepted as a plain NodeId", encoding)
		}
	}
	decoder := newTestDecoder(t, []byte{0x06, 0, 0}, limits)
	if _, err := decoder.ReadNodeID(); err == nil {
		t.Fatal("an undefined NodeId encoding was accepted")
	}
}

// OPC 10000-6 5.2.2.10: when the NamespaceUri is present the NamespaceIndex is
// written as 0 because the index is then to be ignored.
func TestExpandedNodeIDWritesNamespaceIndexZeroWithAURI(t *testing.T) {
	limits := DefaultBinaryLimits()
	value := ExpandedNodeID{
		NodeID:          StringNodeID(7, "tag"),
		NamespaceURI:    "urn:example",
		HasNamespaceURI: true,
		ServerIndex:     3,
		HasServerIndex:  true,
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteExpandedNodeID(value)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0]&nodeIDFlagNamespaceURI == 0 || encoded[0]&nodeIDFlagServerIndex == 0 {
		t.Fatalf("flags missing from 0x%02X", encoded[0])
	}
	// Namespace index bytes follow the encoding byte.
	if encoded[1] != 0 || encoded[2] != 0 {
		t.Fatalf("namespace index was not written as 0: % X", encoded[1:3])
	}

	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadExpandedNodeID()
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HasNamespaceURI || decoded.NamespaceURI != "urn:example" {
		t.Fatalf("namespace uri = %+v", decoded)
	}
	if !decoded.HasServerIndex || decoded.ServerIndex != 3 {
		t.Fatalf("server index = %+v", decoded)
	}
	if decoded.NodeID.StringID != "tag" || decoded.NodeID.Namespace != 0 {
		t.Fatalf("inner NodeId = %+v", decoded.NodeID)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left", decoder.Remaining())
	}
}

func TestExpandedNodeIDWithoutOptionalFields(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteExpandedNodeID(ExpandedNodeID{NodeID: NumericNodeID(0, 5)})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte{0x00, 5}) {
		t.Fatalf("encoded % X", encoded)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadExpandedNodeID()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.HasNamespaceURI || decoded.HasServerIndex {
		t.Fatalf("optional fields appeared: %+v", decoded)
	}
}

func TestQualifiedNameAndLocalizedText(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteQualifiedName(QualifiedName{Namespace: 2, Name: "Test"})
	// Table 24: an omitted field is absent from the stream, not an empty string.
	encoder.WriteLocalizedText(LocalizedText{Text: "value"})
	encoder.WriteLocalizedText(LocalizedText{})
	encoder.WriteLocalizedText(LocalizedText{Locale: "en", Text: "value"})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	name, err := decoder.ReadQualifiedName()
	if err != nil || name.Namespace != 2 || name.Name != "Test" {
		t.Fatalf("qualified name = %+v, %v", name, err)
	}
	textOnly, err := decoder.ReadLocalizedText()
	if err != nil || textOnly.Locale != "" || textOnly.Text != "value" {
		t.Fatalf("text only = %+v, %v", textOnly, err)
	}
	empty, err := decoder.ReadLocalizedText()
	if err != nil || empty.Locale != "" || empty.Text != "" {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	both, err := decoder.ReadLocalizedText()
	if err != nil || both.Locale != "en" || both.Text != "value" {
		t.Fatalf("both = %+v, %v", both, err)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left", decoder.Remaining())
	}
}

// OPC 10000-6 5.2.2.15: a null ExtensionObject is TypeId i=0 with no body.
func TestExtensionObjectNullAndBodies(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteExtensionObject(NullExtensionObject())
	encoder.WriteExtensionObject(ExtensionObject{
		TypeID: NumericNodeID(0, 321), Encoding: ExtensionObjectByteString, Body: []byte{1, 2, 3},
	})
	encoder.WriteExtensionObject(ExtensionObject{
		TypeID: NumericNodeID(0, 322), Encoding: ExtensionObjectXMLElement, Body: []byte("<a/>"),
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded[:3], []byte{0x00, 0x00, ExtensionObjectNoBody}) {
		t.Fatalf("null ExtensionObject = % X", encoded[:3])
	}

	decoder := newTestDecoder(t, encoded, limits)
	null, err := decoder.ReadExtensionObject()
	if err != nil || !null.TypeID.IsNull() || null.Encoding != ExtensionObjectNoBody || null.Body != nil {
		t.Fatalf("null = %+v, %v", null, err)
	}
	binaryBody, err := decoder.ReadExtensionObject()
	if err != nil || !bytes.Equal(binaryBody.Body, []byte{1, 2, 3}) {
		t.Fatalf("binary body = %+v, %v", binaryBody, err)
	}
	xmlBody, err := decoder.ReadExtensionObject()
	if err != nil || string(xmlBody.Body) != "<a/>" {
		t.Fatalf("xml body = %+v, %v", xmlBody, err)
	}
}

func TestExtensionObjectRejectsUndefinedEncoding(t *testing.T) {
	limits := DefaultBinaryLimits()
	decoder := newTestDecoder(t, []byte{0x00, 0x00, 0x03}, limits)
	if _, err := decoder.ReadExtensionObject(); err == nil {
		t.Fatal("an undefined ExtensionObject encoding was accepted")
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteExtensionObject(ExtensionObject{TypeID: NumericNodeID(0, 0), Encoding: 0x03})
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("an undefined ExtensionObject encoding was written")
	}
}

// The stream order of Table 22 is SymbolicId, NamespaceUri, Locale,
// LocalizedText, which is not the order the mask bits are listed in: the
// LocalizedText bit is 0x04 and the Locale bit is 0x08.
func TestDiagnosticInfoFieldOrderIsNotMaskOrder(t *testing.T) {
	limits := DefaultBinaryLimits()
	value := DiagnosticInfo{
		Locale: 11, HasLocale: true,
		LocalizedText: 22, HasLocalizedText: true,
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteDiagnosticInfo(value)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != diagnosticHasLocale|diagnosticHasLocalizedText {
		t.Fatalf("mask = 0x%02X", encoded[0])
	}
	// Locale is written first even though its bit is the higher one.
	if encoded[1] != 11 {
		t.Fatalf("first encoded index = %d, want the Locale", encoded[1])
	}
	if encoded[5] != 22 {
		t.Fatalf("second encoded index = %d, want the LocalizedText", encoded[5])
	}

	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadDiagnosticInfo()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Locale != 11 || decoded.LocalizedText != 22 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestDiagnosticInfoAllFieldsRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	inner := DiagnosticInfo{SymbolicID: -1, HasSymbolicID: true}
	value := DiagnosticInfo{
		SymbolicID: 1, HasSymbolicID: true,
		NamespaceURI: 2, HasNamespaceURI: true,
		Locale: 3, HasLocale: true,
		LocalizedText: 4, HasLocalizedText: true,
		AdditionalInfo: "detail", HasAdditionalInfo: true,
		InnerStatusCode: StatusBadOutOfService, HasInnerStatusCode: true,
		InnerDiagnosticInfo: &inner,
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteDiagnosticInfo(value)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadDiagnosticInfo()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SymbolicID != 1 || decoded.NamespaceURI != 2 || decoded.Locale != 3 ||
		decoded.LocalizedText != 4 || decoded.AdditionalInfo != "detail" ||
		decoded.InnerStatusCode != StatusBadOutOfService {
		t.Fatalf("decoded = %+v", decoded)
	}
	// An index of -1 means the string table has no value, and must survive.
	if decoded.InnerDiagnosticInfo == nil || decoded.InnerDiagnosticInfo.SymbolicID != -1 {
		t.Fatalf("inner = %+v", decoded.InnerDiagnosticInfo)
	}
}

// OPC 10000-6 5.2.2.12: decoders shall support at least 4 recursion levels and
// are not expected to support more than 10, and shall report an error beyond
// what they support.
func TestDiagnosticInfoRecursionIsBounded(t *testing.T) {
	limits := DefaultBinaryLimits()
	if MaxDiagnosticRecursion < MinDiagnosticRecursion || MaxDiagnosticRecursion > 10 {
		t.Fatalf("recursion bound %d is outside the clause's range", MaxDiagnosticRecursion)
	}

	build := func(depth int) DiagnosticInfo {
		node := DiagnosticInfo{SymbolicID: int32(depth), HasSymbolicID: true}
		for level := depth - 1; level >= 0; level-- {
			inner := node
			node = DiagnosticInfo{SymbolicID: int32(level), HasSymbolicID: true, InnerDiagnosticInfo: &inner}
		}
		return node
	}

	// The supported depth encodes and decodes.
	encoder := newTestEncoder(t, limits)
	encoder.WriteDiagnosticInfo(build(MaxDiagnosticRecursion))
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("the supported recursion depth was refused: %v", err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadDiagnosticInfo(); err != nil {
		t.Fatalf("the supported recursion depth failed to decode: %v", err)
	}

	// One level deeper is refused rather than followed.
	encoder = newTestEncoder(t, limits)
	encoder.WriteDiagnosticInfo(build(MaxDiagnosticRecursion + 1))
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("recursion past the limit was encoded")
	}

	// A peer can still send one, so the decoder must refuse it too. Each level
	// is a mask byte with only the inner-diagnostic bit set.
	deep := bytes.Repeat([]byte{diagnosticHasInnerDiag}, MaxDiagnosticRecursion+2)
	deep = append(deep, 0x00)
	decoder = newTestDecoder(t, deep, limits)
	if _, err := decoder.ReadDiagnosticInfo(); err == nil {
		t.Fatal("recursion past the limit was decoded")
	} else if got := codecStatus(t, err); got != StatusBadEncodingLimitsExceeded {
		t.Fatalf("status = %s, want Bad_EncodingLimitsExceeded", got.Hex())
	}
}

func TestRequestAndResponseHeaderRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	timestamp := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)

	request := RequestHeader{
		AuthenticationToken: NumericNodeID(1, 42),
		Timestamp:           timestamp,
		RequestHandle:       7,
		ReturnDiagnostics:   0x3F,
		AuditEntryID:        "audit-1",
		TimeoutHint:         30_000,
		AdditionalHeader:    NullExtensionObject(),
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteRequestHeader(request)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decodedRequest, err := decoder.ReadRequestHeader()
	if err != nil {
		t.Fatal(err)
	}
	if !decodedRequest.AuthenticationToken.Equal(request.AuthenticationToken) ||
		!decodedRequest.Timestamp.Equal(timestamp) ||
		decodedRequest.RequestHandle != 7 || decodedRequest.ReturnDiagnostics != 0x3F ||
		decodedRequest.AuditEntryID != "audit-1" || decodedRequest.TimeoutHint != 30_000 {
		t.Fatalf("request header = %+v", decodedRequest)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left after the request header", decoder.Remaining())
	}

	response := ResponseHeader{
		Timestamp:        timestamp,
		RequestHandle:    7,
		ServiceResult:    StatusBadNodeIdUnknown,
		StringTable:      []string{"a", "b"},
		AdditionalHeader: NullExtensionObject(),
	}
	encoder = newTestEncoder(t, limits)
	encoder.WriteResponseHeader(response)
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	decodedResponse, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if !decodedResponse.Timestamp.Equal(timestamp) || decodedResponse.RequestHandle != 7 ||
		decodedResponse.ServiceResult != StatusBadNodeIdUnknown ||
		len(decodedResponse.StringTable) != 2 || decodedResponse.StringTable[1] != "b" {
		t.Fatalf("response header = %+v", decodedResponse)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left after the response header", decoder.Remaining())
	}
}

// A null string table is distinct from an empty one.
func TestResponseHeaderNullStringTable(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteResponseHeader(ResponseHeader{AdditionalHeader: NullExtensionObject()})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.StringTable != nil {
		t.Fatalf("string table = %v, want nil", decoded.StringTable)
	}
}

func TestHeadersRejectTruncatedInput(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteRequestHeader(RequestHeader{
		AuthenticationToken: NumericNodeID(0, 1), AdditionalHeader: NullExtensionObject(),
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut < len(encoded); cut++ {
		decoder := newTestDecoder(t, encoded[:cut], limits)
		if _, err := decoder.ReadRequestHeader(); err == nil {
			t.Fatalf("a request header truncated to %d bytes was accepted", cut)
		}
	}
}
