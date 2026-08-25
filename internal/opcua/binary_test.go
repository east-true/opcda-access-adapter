package opcua

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func newTestEncoder(t *testing.T, limits BinaryLimits) *Encoder {
	t.Helper()
	encoder, err := NewEncoder(limits)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	return encoder
}

func newTestDecoder(t *testing.T, data []byte, limits BinaryLimits) *Decoder {
	t.Helper()
	decoder, err := NewDecoder(data, limits)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	return decoder
}

func codecStatus(t *testing.T, err error) StatusCode {
	t.Helper()
	var codecErr *CodecError
	if !errors.As(err, &codecErr) {
		t.Fatalf("error %v is not a CodecError", err)
	}
	return codecErr.Status
}

// OPC 10000-6 5.2.2.2 and 5.2.2.3: every integer and floating-point value is
// little-endian.
func TestScalarsAreLittleEndian(t *testing.T) {
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteUInt32(1_000_000_000) // 0x3B9ACA00, the clause's own example
	encoder.WriteFloat(-6.5)           // 0xC0D00000, the clause's own example
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0xCA, 0x9A, 0x3B, 0x00, 0x00, 0xD0, 0xC0}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded % X, want % X", encoded, want)
	}
}

func TestScalarRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteBoolean(true)
	encoder.WriteSByte(math.MinInt8)
	encoder.WriteByteValue(0xFF)
	encoder.WriteInt16(math.MinInt16)
	encoder.WriteUInt16(math.MaxUint16)
	encoder.WriteInt32(math.MinInt32)
	encoder.WriteUInt32(math.MaxUint32)
	encoder.WriteInt64(math.MinInt64)
	encoder.WriteUInt64(math.MaxUint64)
	encoder.WriteDouble(math.Inf(-1))
	encoder.WriteStatusCode(StatusBadOutOfService)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	if value, err := decoder.ReadBoolean(); err != nil || !value {
		t.Fatalf("Boolean = %t, %v", value, err)
	}
	if value, err := decoder.ReadSByte(); err != nil || value != math.MinInt8 {
		t.Fatalf("SByte = %d, %v", value, err)
	}
	if value, err := decoder.ReadByteValue(); err != nil || value != 0xFF {
		t.Fatalf("Byte = %d, %v", value, err)
	}
	if value, err := decoder.ReadInt16(); err != nil || value != math.MinInt16 {
		t.Fatalf("Int16 = %d, %v", value, err)
	}
	if value, err := decoder.ReadUInt16(); err != nil || value != math.MaxUint16 {
		t.Fatalf("UInt16 = %d, %v", value, err)
	}
	if value, err := decoder.ReadInt32(); err != nil || value != math.MinInt32 {
		t.Fatalf("Int32 = %d, %v", value, err)
	}
	if value, err := decoder.ReadUInt32(); err != nil || value != math.MaxUint32 {
		t.Fatalf("UInt32 = %d, %v", value, err)
	}
	if value, err := decoder.ReadInt64(); err != nil || value != math.MinInt64 {
		t.Fatalf("Int64 = %d, %v", value, err)
	}
	if value, err := decoder.ReadUInt64(); err != nil || value != math.MaxUint64 {
		t.Fatalf("UInt64 = %d, %v", value, err)
	}
	if value, err := decoder.ReadDouble(); err != nil || !math.IsInf(value, -1) {
		t.Fatalf("Double = %v, %v", value, err)
	}
	if value, err := decoder.ReadStatusCode(); err != nil || value != StatusBadOutOfService {
		t.Fatalf("StatusCode = %s, %v", value.Hex(), err)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left undecoded", decoder.Remaining())
	}
}

// OPC 10000-6 5.2.2.1: encoders shall write 1 for true, decoders shall treat
// any non-zero value as true.
func TestBooleanEncodesOneAndDecodesAnyNonZero(t *testing.T) {
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteBoolean(true)
	encoded, _ := encoder.Bytes()
	if encoded[0] != 1 {
		t.Fatalf("true encoded as %d, want 1", encoded[0])
	}
	for _, raw := range []byte{1, 2, 0x80, 0xFF} {
		decoder := newTestDecoder(t, []byte{raw}, DefaultBinaryLimits())
		if value, err := decoder.ReadBoolean(); err != nil || !value {
			t.Fatalf("0x%02X decoded as %t, %v", raw, value, err)
		}
	}
	decoder := newTestDecoder(t, []byte{0}, DefaultBinaryLimits())
	if value, err := decoder.ReadBoolean(); err != nil || value {
		t.Fatalf("0x00 decoded as %t, %v", value, err)
	}
}

// OPC 10000-6 5.2.2.3: NaN shall be encoded as an IEEE quiet NaN, so no NaN
// variant can leak into the stream.
func TestNaNIsNormalised(t *testing.T) {
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteFloat(math.Float32frombits(0x7F800001)) // a signalling NaN
	encoder.WriteDouble(math.Float64frombits(0x7FF0000000000001))
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x00, 0x00, 0xC0, 0xFF,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0xFF,
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded % X, want % X", encoded, want)
	}
	decoder := newTestDecoder(t, encoded, DefaultBinaryLimits())
	single, err := decoder.ReadFloat()
	if err != nil || !math.IsNaN(float64(single)) {
		t.Fatalf("Float = %v, %v", single, err)
	}
	double, err := decoder.ReadDouble()
	if err != nil || !math.IsNaN(double) {
		t.Fatalf("Double = %v, %v", double, err)
	}
}

// OPC 10000-6 5.1.11: a zero length is an empty value, distinct from null.
func TestStringAndByteStringDistinguishNullFromEmpty(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteNullString()
	encoder.WriteString("")
	encoder.WriteString("水Boy")
	encoder.WriteNullByteString()
	encoder.WriteByteString([]byte{})
	encoder.WriteByteString([]byte{1, 2, 3})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	if value, isNull, err := decoder.ReadString(); err != nil || !isNull || value != "" {
		t.Fatalf("null String = %q null=%t %v", value, isNull, err)
	}
	if value, isNull, err := decoder.ReadString(); err != nil || isNull || value != "" {
		t.Fatalf("empty String = %q null=%t %v", value, isNull, err)
	}
	if value, isNull, err := decoder.ReadString(); err != nil || isNull || value != "水Boy" {
		t.Fatalf("String = %q null=%t %v", value, isNull, err)
	}
	if value, isNull, err := decoder.ReadByteString(); err != nil || !isNull || value != nil {
		t.Fatalf("null ByteString = %v null=%t %v", value, isNull, err)
	}
	if value, isNull, err := decoder.ReadByteString(); err != nil || isNull || len(value) != 0 {
		t.Fatalf("empty ByteString = %v null=%t %v", value, isNull, err)
	}
	if value, isNull, err := decoder.ReadByteString(); err != nil || isNull || !bytes.Equal(value, []byte{1, 2, 3}) {
		t.Fatalf("ByteString = %v null=%t %v", value, isNull, err)
	}
}

// OPC 10000-6 5.2.2.4: a decoder shall use the length, not stop at a NUL.
func TestStringPreservesEmbeddedNul(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteString("a\x00b")
	encoded, _ := encoder.Bytes()
	decoder := newTestDecoder(t, encoded, limits)
	value, isNull, err := decoder.ReadString()
	if err != nil || isNull || value != "a\x00b" {
		t.Fatalf("String = %q null=%t %v", value, isNull, err)
	}
}

// A decoded ByteString must not alias the caller's buffer.
func TestByteStringIsCopied(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteByteString([]byte{9, 8, 7})
	encoded, _ := encoder.Bytes()
	decoder := newTestDecoder(t, encoded, limits)
	value, _, err := decoder.ReadByteString()
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)-1] = 0
	if !bytes.Equal(value, []byte{9, 8, 7}) {
		t.Fatalf("decoded ByteString aliased the source buffer: %v", value)
	}
}

// OPC 10000-6 Table 2: Data1/Data2/Data3 are integers and therefore
// little-endian; Data4 is a byte sequence and keeps its order.
func TestGuidFieldOrder(t *testing.T) {
	limits := DefaultBinaryLimits()
	value := Guid{
		Data1: 0x72962B91, Data2: 0xFA75, Data3: 0x4AE6,
		Data4: [8]byte{0x8D, 0x28, 0xB4, 0x04, 0xDC, 0x7D, 0xAF, 0x63},
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteGuid(value)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x91, 0x2B, 0x96, 0x72,
		0x75, 0xFA,
		0xE6, 0x4A,
		0x8D, 0x28, 0xB4, 0x04, 0xDC, 0x7D, 0xAF, 0x63,
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encoded % X, want % X", encoded, want)
	}
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadGuid()
	if err != nil || decoded != value {
		t.Fatalf("Guid = %+v, %v", decoded, err)
	}
}

func TestDateTimeFollowsTheSaturationRules(t *testing.T) {
	// 1601-01-01 is tick zero.
	if got := EncodeDateTime(time.Date(1601, time.January, 1, 0, 0, 0, 0, time.UTC)); got != DateTimeMin {
		t.Fatalf("epoch encoded as %d, want %d", got, DateTimeMin)
	}
	// Anything earlier saturates rather than going negative.
	if got := EncodeDateTime(time.Date(1500, time.January, 1, 0, 0, 0, 0, time.UTC)); got != DateTimeMin {
		t.Fatalf("pre-epoch encoded as %d, want %d", got, DateTimeMin)
	}
	// Anything at or beyond 9999-12-31 saturates to the Int64 maximum.
	if got := EncodeDateTime(time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)); got != DateTimeMax {
		t.Fatalf("post-range encoded as %d, want %d", got, DateTimeMax)
	}
	// The reserved values decode to the platform bounds.
	if got := DecodeDateTime(DateTimeMin).Year(); got != 1601 {
		t.Fatalf("DateTimeMin decoded to year %d", got)
	}
	if got := DecodeDateTime(DateTimeMax).Year(); got != 9999 {
		t.Fatalf("DateTimeMax decoded to year %d", got)
	}
	// A negative wire value is earlier than the epoch and decodes as the
	// earliest instant rather than wrapping.
	if got := DecodeDateTime(-1).Year(); got != 1601 {
		t.Fatalf("negative ticks decoded to year %d", got)
	}
}

func TestDateTimeRoundTripKeeps100NanosecondResolution(t *testing.T) {
	limits := DefaultBinaryLimits()
	value := time.Date(2026, time.August, 25, 1, 2, 3, 400, time.UTC)
	encoder := newTestEncoder(t, limits)
	encoder.WriteDateTime(value)
	encoded, _ := encoder.Bytes()
	decoder := newTestDecoder(t, encoded, limits)
	decoded, err := decoder.ReadDateTime()
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(value) {
		t.Fatalf("DateTime round-tripped to %s, want %s", decoded, value)
	}
}

func TestArrayLengthDistinguishesNullFromEmpty(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteNullArray()
	encoder.WriteArrayLength(0)
	encoder.WriteArrayLength(2)
	encoder.WriteInt32(7)
	encoder.WriteInt32(8)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	if length, isNull, err := decoder.ReadArrayLength(4); err != nil || !isNull || length != 0 {
		t.Fatalf("null array = %d null=%t %v", length, isNull, err)
	}
	if length, isNull, err := decoder.ReadArrayLength(4); err != nil || isNull || length != 0 {
		t.Fatalf("empty array = %d null=%t %v", length, isNull, err)
	}
	length, isNull, err := decoder.ReadArrayLength(4)
	if err != nil || isNull || length != 2 {
		t.Fatalf("array = %d null=%t %v", length, isNull, err)
	}
}

// A declared element count must be checked against the bytes that remain, so a
// hostile length cannot drive a large allocation from a tiny message.
func TestArrayLengthIsCheckedAgainstRemainingBytes(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteArrayLength(1000)
	encoder.WriteInt32(1)
	encoded, _ := encoder.Bytes()

	decoder := newTestDecoder(t, encoded, limits)
	_, _, err := decoder.ReadArrayLength(4)
	if err == nil {
		t.Fatal("an array length larger than the remaining bytes was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadDecodingError {
		t.Fatalf("status = %s, want Bad_DecodingError", got.Hex())
	}
}

func TestDecoderRejectsOverlongDeclaredLengths(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxStringBytes = 8
	limits.MaxByteStringBytes = 8
	limits.MaxArrayLength = 4

	cases := []struct {
		name   string
		encode func(*Encoder)
		read   func(*Decoder) error
	}{
		{"String", func(e *Encoder) { e.WriteInt32(9) }, func(d *Decoder) error {
			_, _, err := d.ReadString()
			return err
		}},
		{"ByteString", func(e *Encoder) { e.WriteInt32(9) }, func(d *Decoder) error {
			_, _, err := d.ReadByteString()
			return err
		}},
		{"array", func(e *Encoder) { e.WriteInt32(5) }, func(d *Decoder) error {
			_, _, err := d.ReadArrayLength(0)
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoder := newTestEncoder(t, DefaultBinaryLimits())
			testCase.encode(encoder)
			encoded, _ := encoder.Bytes()
			decoder := newTestDecoder(t, encoded, limits)
			err := testCase.read(decoder)
			if err == nil {
				t.Fatal("an over-limit length was accepted")
			}
			if got := codecStatus(t, err); got != StatusBadEncodingLimitsExceeded {
				t.Fatalf("status = %s, want Bad_EncodingLimitsExceeded", got.Hex())
			}
		})
	}
}

// Any negative length other than -1 is malformed, including the Int32 minimum,
// whose negation overflows.
func TestDecoderRejectsNegativeLengths(t *testing.T) {
	limits := DefaultBinaryLimits()
	for _, length := range []int32{-2, -1000, math.MinInt32} {
		encoder := newTestEncoder(t, limits)
		encoder.WriteInt32(length)
		encoded, _ := encoder.Bytes()
		decoder := newTestDecoder(t, encoded, limits)
		if _, _, err := decoder.ReadString(); err == nil {
			t.Fatalf("length %d was accepted", length)
		} else if got := codecStatus(t, err); got != StatusBadDecodingError {
			t.Fatalf("length %d status = %s", length, got.Hex())
		}
	}
}

func TestDecoderRejectsTruncatedInput(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteString("hello")
	encoded, _ := encoder.Bytes()

	for cut := 1; cut < len(encoded); cut++ {
		decoder := newTestDecoder(t, encoded[:cut], limits)
		if _, _, err := decoder.ReadString(); err == nil {
			t.Fatalf("a String truncated to %d bytes decoded", cut)
		}
	}
	// An empty buffer must fail every scalar read rather than return a zero.
	decoder := newTestDecoder(t, nil, limits)
	if _, err := decoder.ReadUInt64(); err == nil {
		t.Fatal("an empty buffer produced a UInt64")
	}
}

func TestEncoderEnforcesItsBounds(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxMessageBytes = 16
	limits.MaxStringBytes = 8
	limits.MaxByteStringBytes = 8
	limits.MaxArrayLength = 4

	encoder := newTestEncoder(t, limits)
	encoder.WriteString(strings.Repeat("x", 9))
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("an over-limit String was encoded")
	} else if got := codecStatus(t, err); got != StatusBadEncodingLimitsExceeded {
		t.Fatalf("status = %s", got.Hex())
	}

	encoder = newTestEncoder(t, limits)
	encoder.WriteArrayLength(5)
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("an over-limit array length was encoded")
	}

	encoder = newTestEncoder(t, limits)
	encoder.WriteArrayLength(-1)
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("a negative array length was encoded")
	} else if got := codecStatus(t, err); got != StatusBadEncodingError {
		t.Fatalf("status = %s, want Bad_EncodingError", got.Hex())
	}

	// The message bound applies to the total, not to any single field.
	encoder = newTestEncoder(t, limits)
	for count := 0; count < 5; count++ {
		encoder.WriteInt32(int32(count))
	}
	if _, err := encoder.Bytes(); err == nil {
		t.Fatal("a message beyond the byte bound was encoded")
	}
}

// The first failure sticks, so a caller may write a whole message and check
// once without a later write masking an earlier error.
func TestEncoderKeepsTheFirstFailure(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxArrayLength = 1
	encoder := newTestEncoder(t, limits)
	encoder.WriteArrayLength(-1)
	encoder.WriteArrayLength(2)
	encoder.WriteInt32(0)
	if got := codecStatus(t, encoder.Err()); got != StatusBadEncodingError {
		t.Fatalf("status = %s, want the first failure", got.Hex())
	}
}

// OPC 10000-6 5.1.8 requires at least 100 nesting levels and an error beyond
// what the decoder supports.
func TestNestingDepthIsBounded(t *testing.T) {
	limits := DefaultBinaryLimits()
	decoder := newTestDecoder(t, nil, limits)
	for level := 0; level < limits.MaxNestingDepth; level++ {
		if err := decoder.Enter(); err != nil {
			t.Fatalf("level %d rejected: %v", level, err)
		}
	}
	err := decoder.Enter()
	if err == nil {
		t.Fatal("nesting past the limit was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadEncodingLimitsExceeded {
		t.Fatalf("status = %s", got.Hex())
	}
	decoder.Leave()
	if err := decoder.Enter(); err != nil {
		t.Fatalf("leaving a level did not free it: %v", err)
	}

	encoder := newTestEncoder(t, limits)
	for level := 0; level < limits.MaxNestingDepth; level++ {
		encoder.Enter()
	}
	encoder.Enter()
	if encoder.Err() == nil {
		t.Fatal("encoder nesting past the limit was accepted")
	}
}

func TestBinaryLimitsValidation(t *testing.T) {
	if err := DefaultBinaryLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
	for name, mutate := range map[string]func(*BinaryLimits){
		"zero message bound":     func(l *BinaryLimits) { l.MaxMessageBytes = 0 },
		"zero string bound":      func(l *BinaryLimits) { l.MaxStringBytes = 0 },
		"zero byte string bound": func(l *BinaryLimits) { l.MaxByteStringBytes = 0 },
		"zero array bound":       func(l *BinaryLimits) { l.MaxArrayLength = 0 },
		"below the depth floor":  func(l *BinaryLimits) { l.MaxNestingDepth = minimumNestingDepth - 1 },
		"string beyond message":  func(l *BinaryLimits) { l.MaxStringBytes = l.MaxMessageBytes + 1 },
		"bytes beyond message":   func(l *BinaryLimits) { l.MaxByteStringBytes = l.MaxMessageBytes + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultBinaryLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatalf("limits %+v were accepted", limits)
			}
		})
	}

	// A length field is an Int32, so a bound wider than one can express must be
	// refused. The case exists only where int is wider than Int32; on a 32-bit
	// platform such a bound is not representable in the first place.
	if int64(math.MaxInt) > int64(math.MaxInt32) {
		t.Run("array beyond Int32", func(t *testing.T) {
			limits := DefaultBinaryLimits()
			// Built at runtime: as a constant expression the conversion would
			// not compile on a 32-bit platform.
			var beyondInt32 int64 = math.MaxInt32
			beyondInt32++
			limits.MaxArrayLength = int(beyondInt32)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("an array bound wider than Int32 was accepted")
			}
		})
	}
}

func TestDecoderRejectsAnOversizedMessage(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxMessageBytes = 16
	limits.MaxStringBytes = 8
	limits.MaxByteStringBytes = 8
	if _, err := NewDecoder(make([]byte, 17), limits); err == nil {
		t.Fatal("an oversized message was accepted")
	} else if got := codecStatus(t, err); got != StatusBadEncodingLimitsExceeded {
		t.Fatalf("status = %s", got.Hex())
	}
}
