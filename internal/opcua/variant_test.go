package opcua

import (
	"bytes"
	"math"
	"testing"
	"time"
)

// Built-in type ids come from OPC 10000-6 Table 1.
func TestBuiltInTypeIDs(t *testing.T) {
	cases := map[BuiltInTypeID]byte{
		BuiltInNull: 0, BuiltInBoolean: 1, BuiltInSByte: 2, BuiltInByte: 3,
		BuiltInInt16: 4, BuiltInUInt16: 5, BuiltInInt32: 6, BuiltInUInt32: 7,
		BuiltInInt64: 8, BuiltInUInt64: 9, BuiltInFloat: 10, BuiltInDouble: 11,
		BuiltInString: 12, BuiltInDateTime: 13, BuiltInGuid: 14,
		BuiltInByteString: 15, BuiltInXMLElement: 16, BuiltInNodeID: 17,
		BuiltInExpandedNodeID: 18, BuiltInStatusCode: 19, BuiltInQualifiedName: 20,
		BuiltInLocalizedText: 21, BuiltInExtensionObject: 22, BuiltInDataValue: 23,
		BuiltInVariant: 24, BuiltInDiagnosticInfo: 25,
	}
	for id, want := range cases {
		if byte(id) != want {
			t.Fatalf("built-in id %d, want %d", byte(id), want)
		}
	}
}

func TestVariantScalarRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	timestamp := time.Date(2026, time.August, 26, 1, 2, 3, 400, time.UTC)
	cases := []Variant{
		{Type: BuiltInBoolean, Value: true},
		{Type: BuiltInSByte, Value: int8(-5)},
		{Type: BuiltInByte, Value: byte(200)},
		{Type: BuiltInInt16, Value: int16(-300)},
		{Type: BuiltInUInt16, Value: uint16(60000)},
		{Type: BuiltInInt32, Value: int32(-70000)},
		{Type: BuiltInUInt32, Value: uint32(4000000000)},
		{Type: BuiltInInt64, Value: int64(-5000000000)},
		{Type: BuiltInUInt64, Value: uint64(18000000000000000000)},
		{Type: BuiltInFloat, Value: float32(1.5)},
		{Type: BuiltInDouble, Value: float64(2.25)},
		{Type: BuiltInString, Value: "水Boy"},
		{Type: BuiltInDateTime, Value: timestamp},
		{Type: BuiltInStatusCode, Value: StatusBadOutOfService},
	}
	for _, value := range cases {
		encoder := newTestEncoder(t, limits)
		encoder.WriteVariant(value)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatalf("%d: %v", value.Type, err)
		}
		// Table 26: the low six bits carry the built-in type id.
		if encoded[0]&variantTypeMask != byte(value.Type) {
			t.Fatalf("mask = 0x%02X for type %d", encoded[0], value.Type)
		}
		decoder := newTestDecoder(t, encoded, limits)
		decoded, err := decoder.ReadVariant()
		if err != nil {
			t.Fatalf("%d: %v", value.Type, err)
		}
		if decoded.Type != value.Type {
			t.Fatalf("type = %d, want %d", decoded.Type, value.Type)
		}
		if value.Type == BuiltInDateTime {
			if !decoded.Value.(time.Time).Equal(timestamp) {
				t.Fatalf("timestamp = %v", decoded.Value)
			}
			continue
		}
		if decoded.Value != value.Value {
			t.Fatalf("value = %v, want %v", decoded.Value, value.Value)
		}
	}
}

// Table 26: a mask of 0 is a null Variant and no other field follows.
func TestNullVariantEncodesAsASingleZeroByte(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteVariant(NullVariant())
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte{0}) {
		t.Fatalf("null variant = % X", encoded)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadVariant()
	if err != nil || !decoded.IsNull() {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
}

// A Go value that does not match the declared built-in type is refused rather
// than coerced, because coercion would produce a stream a client cannot decode.
func TestVariantRefusesMismatchedGoTypes(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteVariant(Variant{Type: BuiltInInt32, Value: "not an int32"})
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("a mismatched value was encoded")
	}

	// Table 26: encoders shall not use the unassigned ids.
	encoder = newTestEncoder(t, limits)
	encoder.WriteVariant(Variant{Type: BuiltInTypeID(28), Value: []byte{1}})
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("an unassigned built-in id was encoded")
	}

	// This adapter produces scalars only.
	encoder = newTestEncoder(t, limits)
	encoder.WriteVariant(Variant{Type: BuiltInInt32, IsArray: true})
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("an array variant was encoded")
	}
}

// Table 26: decoders shall accept the unassigned ids and treat the value as a
// ByteString.
func TestVariantDecodesUnassignedIDsAsByteStrings(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteByteValue(28)
	encoder.WriteByteString([]byte{1, 2, 3})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadVariant()
	if err != nil {
		t.Fatalf("an unassigned id was refused: %v", err)
	}
	if decoded.Type != BuiltInTypeID(28) {
		t.Fatalf("type = %d", decoded.Type)
	}
	if !bytes.Equal(decoded.Value.([]byte), []byte{1, 2, 3}) {
		t.Fatalf("value = %v", decoded.Value)
	}

	// Beyond the reserved range is malformed.
	decoder = newTestDecoder(t, []byte{40}, limits)
	if _, err := decoder.ReadVariant(); err == nil {
		t.Fatal("a built-in id beyond the reserved range was accepted")
	}
}

// Table 26: a Variant's value shall not itself be a Variant.
func TestVariantRefusesANestedVariant(t *testing.T) {
	limits := DefaultBinaryLimits()
	decoder := newTestDecoder(t, []byte{byte(BuiltInVariant), 0}, limits)
	if _, err := decoder.ReadVariant(); err == nil {
		t.Fatal("a nested variant was accepted")
	}
}

// An array Variant is reported and skipped; the declared count is still bounded
// so a hostile length cannot drive an allocation.
func TestVariantArrayIsBoundedAndSkipped(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteByteValue(byte(BuiltInInt32) | variantArrayValues)
	encoder.WriteArrayLength(2)
	encoder.WriteInt32(1)
	encoder.WriteInt32(2)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadVariant()
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.IsArray || decoded.Type != BuiltInInt32 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left after the array", decoder.Remaining())
	}

	// A length far beyond the bytes present is refused.
	encoder = newTestEncoder(t, limits)
	encoder.WriteByteValue(byte(BuiltInInt32) | variantArrayValues)
	encoder.WriteInt32(1_000_000)
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadVariant(); err == nil {
		t.Fatal("an oversized array length was accepted")
	}
}

// Table 27: a field is present only when it carries information, so a Good
// status and an absent timestamp are omitted rather than written as zeros.
func TestDataValueOmitsDefaultFields(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteDataValue(DataValue{Value: NullVariant(), Status: StatusGood})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte{0}) {
		t.Fatalf("an empty DataValue encoded to % X", encoded)
	}

	value := DataValue{
		Value:           Variant{Type: BuiltInInt32, Value: int32(7)},
		Status:          StatusUncertain,
		SourceTimestamp: time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC),
	}
	encoder = newTestEncoder(t, limits)
	encoder.WriteDataValue(value)
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0]&dataValueHasValue == 0 || encoded[0]&dataValueHasStatus == 0 ||
		encoded[0]&dataValueHasSourceTimestamp == 0 {
		t.Fatalf("mask = 0x%02X", encoded[0])
	}
	if encoded[0]&dataValueHasServerTimestamp != 0 {
		t.Fatal("an absent server timestamp was written")
	}

	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadDataValue()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Status != StatusUncertain || decoded.Value.Value != int32(7) {
		t.Fatalf("decoded = %+v", decoded)
	}
	if !decoded.SourceTimestamp.Equal(value.SourceTimestamp) {
		t.Fatalf("source timestamp = %s", decoded.SourceTimestamp)
	}
	if !decoded.ServerTimestamp.IsZero() {
		t.Fatal("an absent server timestamp was decoded as present")
	}
}

// Table 27: a decoder shall treat picoseconds of 10000 or more as 9999.
func TestDataValuePicosecondsAreClamped(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteByteValue(dataValueHasSourceTimestamp | dataValueHasSourcePicoseconds)
	encoder.WriteDateTime(time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC))
	encoder.WriteUInt16(50000)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadDataValue()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SourcePicoseconds != maxPicoseconds {
		t.Fatalf("picoseconds = %d, want %d", decoded.SourcePicoseconds, maxPicoseconds)
	}
}

// variantBytes builds a Variant by hand, so a decoder can be given shapes this
// encoder would never produce.
func variantBytes(t *testing.T, build func(e *Encoder)) []byte {
	t.Helper()
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	build(encoder)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decodeVariantBytes(t *testing.T, encoded []byte) (Variant, error) {
	t.Helper()
	decoder, err := NewDecoder(encoded, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	return decoder.ReadVariant()
}

// OPC 10000-6 Table 26 gives the decoder two rules about ArrayDimensions: "all
// dimensions shall be specified and shall be greater than zero", and "if
// ArrayDimensions are inconsistent with the ArrayLength then the decoder shall
// stop and raise a Bad_DecodingError".
func TestVariantArrayDimensionsAreValidated(t *testing.T) {
	arrayOfInt32 := variantArrayValues | variantArrayDimensions | byte(BuiltInInt32)

	for _, testCase := range []struct {
		name       string
		length     int32
		dimensions []int32
		wantErr    bool
	}{
		{"consistent", 6, []int32{2, 3}, false},
		{"a dimension of zero", 6, []int32{2, 3, 0}, true},
		{"a negative dimension", 6, []int32{2, -3}, true},
		{"too few elements for the dimensions", 5, []int32{2, 3}, true},
		{"too many elements for the dimensions", 8, []int32{2, 3}, true},
		{"no dimensions at all", 0, []int32{}, true},
		{"a dimension that would overflow the product", 6,
			[]int32{math.MaxInt32, math.MaxInt32, math.MaxInt32}, true},
		// Negative dimensions whose product happens to match the element
		// count. Only the per-dimension rule refuses these; the consistency
		// check alone would let them through.
		{"negative dimensions that multiply to the right count", 6, []int32{-2, -3}, true},
		// Four dimensions of 65536 multiply to exactly 2^64, which wraps to
		// zero in an Int64 and would agree with an array declaring no
		// elements. Only bailing as soon as the product passes the count
		// keeps the arithmetic honest.
		{"dimensions that wrap to the element count", 0, []int32{65536, 65536, 65536, 65536}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			encoded := variantBytes(t, func(e *Encoder) {
				e.WriteByteValue(arrayOfInt32)
				e.WriteInt32(testCase.length)
				for i := int32(0); i < testCase.length; i++ {
					e.WriteInt32(i)
				}
				e.WriteInt32(int32(len(testCase.dimensions)))
				for _, dimension := range testCase.dimensions {
					e.WriteInt32(dimension)
				}
			})
			_, err := decodeVariantBytes(t, encoded)
			if testCase.wantErr && err == nil {
				t.Fatal("the Variant was accepted")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("the Variant was refused: %v", err)
			}
		})
	}
}

// Table 26 gives the ArrayDimensions field only to arrays. Reading the flag and
// not the field would leave its bytes for the next field to consume as its own,
// so the whole message after it decodes as something else entirely.
func TestAScalarVariantMayNotCarryArrayDimensions(t *testing.T) {
	encoded := variantBytes(t, func(e *Encoder) {
		e.WriteByteValue(variantArrayDimensions | byte(BuiltInInt32))
		e.WriteInt32(7)
		// What a desynchronised decoder would go on to read as a new field.
		e.WriteInt32(1)
		e.WriteInt32(1)
	})
	if _, err := decodeVariantBytes(t, encoded); err == nil {
		t.Fatal("a scalar Variant with the ArrayDimensions flag was accepted")
	}
}
