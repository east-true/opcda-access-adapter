package opcua

import (
	"bytes"
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
