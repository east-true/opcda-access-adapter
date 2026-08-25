package opcua

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// UA Binary encoding follows OPC 10000-6 clause 5.2. Every rule implemented
// here is transcribed from that clause: integers and floating-point values are
// little-endian (5.2.2.2, 5.2.2.3), Strings and ByteStrings carry an Int32
// byte length where -1 means null (5.2.2.4, 5.2.2.7), one-dimensional arrays
// carry an Int32 element count where -1 means null (5.2.5), and DateTime is a
// signed 64-bit count of 100 nanosecond intervals since 1601-01-01 UTC
// (5.2.2.5).

// Status codes for encoding and decoding failures, from the OPC Foundation
// StatusCode list.
const (
	StatusBadEncodingError          StatusCode = 0x80060000
	StatusBadDecodingError          StatusCode = 0x80070000
	StatusBadEncodingLimitsExceeded StatusCode = 0x80080000
)

// nullLength is the Int32 length that marks a null String, ByteString, or
// array. Zero length is an empty value and is distinct from null.
const nullLength int32 = -1

// minimumNestingDepth is the floor OPC 10000-6 5.1.8 places on decoders:
// "Decoders shall support at least 100 nesting levels."
const minimumNestingDepth = 100

// BinaryLimits bounds everything a peer can make the decoder allocate or
// recurse into. OPC 10000-6 5.1.8 requires a decoder to reject input beyond
// what it supports rather than attempt it.
type BinaryLimits struct {
	MaxMessageBytes    int
	MaxStringBytes     int
	MaxByteStringBytes int
	MaxArrayLength     int
	MaxNestingDepth    int
}

func DefaultBinaryLimits() BinaryLimits {
	return BinaryLimits{
		MaxMessageBytes:    2 << 20,
		MaxStringBytes:     64 << 10,
		MaxByteStringBytes: 256 << 10,
		MaxArrayLength:     4096,
		MaxNestingDepth:    minimumNestingDepth,
	}
}

func (limits BinaryLimits) validate() error {
	if limits.MaxMessageBytes <= 0 ||
		limits.MaxStringBytes <= 0 ||
		limits.MaxByteStringBytes <= 0 ||
		limits.MaxArrayLength <= 0 ||
		limits.MaxNestingDepth <= 0 {
		return fmt.Errorf("all UA binary limits must be positive")
	}
	if limits.MaxNestingDepth < minimumNestingDepth {
		return fmt.Errorf("UA binary nesting depth must be at least %d", minimumNestingDepth)
	}
	// A length field is an Int32, so no bound may exceed what one can express.
	if limits.MaxMessageBytes > math.MaxInt32 ||
		limits.MaxStringBytes > math.MaxInt32 ||
		limits.MaxByteStringBytes > math.MaxInt32 ||
		limits.MaxArrayLength > math.MaxInt32 {
		return fmt.Errorf("UA binary limits must fit in an Int32 length field")
	}
	if limits.MaxStringBytes > limits.MaxMessageBytes ||
		limits.MaxByteStringBytes > limits.MaxMessageBytes {
		return fmt.Errorf("UA binary string bounds must not exceed the message bound")
	}
	return nil
}

func (limits BinaryLimits) ValidateForConfiguration() error { return limits.validate() }

// CodecError carries the UA status code a peer should be told about. Decoding
// failures are Bad_DecodingError unless a declared length exceeded a configured
// bound, which is Bad_EncodingLimitsExceeded.
type CodecError struct {
	Status  StatusCode
	Message string
}

func (e *CodecError) Error() string { return e.Message }

func decodingError(format string, args ...any) *CodecError {
	return &CodecError{Status: StatusBadDecodingError, Message: fmt.Sprintf(format, args...)}
}

func limitsError(format string, args ...any) *CodecError {
	return &CodecError{Status: StatusBadEncodingLimitsExceeded, Message: fmt.Sprintf(format, args...)}
}

func encodingError(format string, args ...any) *CodecError {
	return &CodecError{Status: StatusBadEncodingError, Message: fmt.Sprintf(format, args...)}
}

// Guid follows OPC 10000-6 Table 2: Data1 UInt32, Data2 UInt16, Data3 UInt16,
// Data4 Byte[8]. The first three fields are little-endian like every other
// integer; Data4 is a byte sequence and keeps its order.
type Guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// Encoder writes UA Binary into a bounded buffer.
type Encoder struct {
	buffer []byte
	limits BinaryLimits
	depth  int
	err    error
}

func NewEncoder(limits BinaryLimits) (*Encoder, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &Encoder{limits: limits}, nil
}

// Err reports the first failure. Once an encoder fails it stays failed, so a
// caller may write a whole message and check once.
func (e *Encoder) Err() error { return e.err }

// Bytes returns the encoded message, or the failure that stopped it.
func (e *Encoder) Bytes() ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.buffer, nil
}

func (e *Encoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *Encoder) write(data []byte) {
	if e.err != nil {
		return
	}
	if len(e.buffer)+len(data) > e.limits.MaxMessageBytes {
		e.fail(limitsError("encoded message exceeds the %d byte limit", e.limits.MaxMessageBytes))
		return
	}
	e.buffer = append(e.buffer, data...)
}

// WriteBoolean follows OPC 10000-6 5.2.2.1: encoders shall use 1 for true.
func (e *Encoder) WriteBoolean(value bool) {
	if value {
		e.write([]byte{1})
		return
	}
	e.write([]byte{0})
}

func (e *Encoder) WriteSByte(value int8) { e.write([]byte{byte(value)}) }

// WriteByteValue and ReadByteValue carry the UA built-in type named Byte. They
// avoid the names WriteByte and ReadByte so the codec is not mistaken for an
// io.ByteWriter or io.ByteReader, whose signatures differ.
func (e *Encoder) WriteByteValue(value byte) { e.write([]byte{value}) }

func (e *Encoder) WriteUInt16(value uint16) {
	var scratch [2]byte
	binary.LittleEndian.PutUint16(scratch[:], value)
	e.write(scratch[:])
}

func (e *Encoder) WriteInt16(value int16) { e.WriteUInt16(uint16(value)) }

func (e *Encoder) WriteUInt32(value uint32) {
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], value)
	e.write(scratch[:])
}

func (e *Encoder) WriteInt32(value int32) { e.WriteUInt32(uint32(value)) }

func (e *Encoder) WriteUInt64(value uint64) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], value)
	e.write(scratch[:])
}

func (e *Encoder) WriteInt64(value int64) { e.WriteUInt64(uint64(value)) }

// WriteFloat and WriteDouble normalise NaN, which OPC 10000-6 5.2.2.3 requires
// to be encoded as an IEEE quiet NaN so the distinction between NaN variants
// cannot leak into the stream.
func (e *Encoder) WriteFloat(value float32) {
	if math.IsNaN(float64(value)) {
		e.WriteUInt32(quietNaN32)
		return
	}
	e.WriteUInt32(math.Float32bits(value))
}

func (e *Encoder) WriteDouble(value float64) {
	if math.IsNaN(value) {
		e.WriteUInt64(quietNaN64)
		return
	}
	e.WriteUInt64(math.Float64bits(value))
}

// The quiet NaN patterns named in OPC 10000-6 5.2.2.3, written here as the
// little-endian values the clause shows in stream order.
const (
	quietNaN32 uint32 = 0xFFC00000
	quietNaN64 uint64 = 0xFFF8000000000000
)

func (e *Encoder) WriteStatusCode(code StatusCode) { e.WriteUInt32(uint32(code)) }

// WriteString encodes a non-null String. A zero-length string is empty, which
// OPC 10000-6 5.1.11 distinguishes from null.
func (e *Encoder) WriteString(value string) {
	if len(value) > e.limits.MaxStringBytes {
		e.fail(limitsError("String exceeds the %d byte limit", e.limits.MaxStringBytes))
		return
	}
	e.WriteInt32(int32(len(value)))
	e.write([]byte(value))
}

func (e *Encoder) WriteNullString() { e.WriteInt32(nullLength) }

func (e *Encoder) WriteByteString(value []byte) {
	if len(value) > e.limits.MaxByteStringBytes {
		e.fail(limitsError("ByteString exceeds the %d byte limit", e.limits.MaxByteStringBytes))
		return
	}
	e.WriteInt32(int32(len(value)))
	e.write(value)
}

func (e *Encoder) WriteNullByteString() { e.WriteInt32(nullLength) }

func (e *Encoder) WriteGuid(value Guid) {
	e.WriteUInt32(value.Data1)
	e.WriteUInt16(value.Data2)
	e.WriteUInt16(value.Data3)
	e.write(value.Data4[:])
}

func (e *Encoder) WriteDateTime(value time.Time) { e.WriteInt64(EncodeDateTime(value)) }

// WriteArrayLength writes the element count that precedes a one-dimensional
// array. It is bounded so a peer cannot be told to expect more elements than
// this codec would accept.
func (e *Encoder) WriteArrayLength(length int) {
	if length < 0 {
		e.fail(encodingError("array length must not be negative; use WriteNullArray"))
		return
	}
	if length > e.limits.MaxArrayLength {
		e.fail(limitsError("array length %d exceeds the %d element limit", length, e.limits.MaxArrayLength))
		return
	}
	e.WriteInt32(int32(length))
}

func (e *Encoder) WriteNullArray() { e.WriteInt32(nullLength) }

// Enter and Leave bound recursion for nested structures, which OPC 10000-6
// 5.1.8 warns can otherwise overflow the stack within a legal message size.
func (e *Encoder) Enter() {
	if e.err != nil {
		return
	}
	if e.depth >= e.limits.MaxNestingDepth {
		e.fail(limitsError("encoding exceeds the %d level nesting limit", e.limits.MaxNestingDepth))
		return
	}
	e.depth++
}

func (e *Encoder) Leave() {
	if e.depth > 0 {
		e.depth--
	}
}

// Decoder reads UA Binary from a fixed buffer. Every read validates the bytes
// remaining before it consumes them, and every declared length is checked
// against both its configured bound and the bytes actually available, so a
// hostile length can never drive an allocation.
type Decoder struct {
	buffer []byte
	offset int
	limits BinaryLimits
	depth  int
}

func NewDecoder(data []byte, limits BinaryLimits) (*Decoder, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if len(data) > limits.MaxMessageBytes {
		return nil, limitsError("message of %d bytes exceeds the %d byte limit", len(data), limits.MaxMessageBytes)
	}
	return &Decoder{buffer: data, limits: limits}, nil
}

// Remaining reports the undecoded bytes.
func (d *Decoder) Remaining() int { return len(d.buffer) - d.offset }

// Done reports whether the whole buffer was consumed. A caller that requires a
// message to be fully consumed can reject trailing bytes explicitly.
func (d *Decoder) Done() bool { return d.Remaining() == 0 }

func (d *Decoder) take(count int) ([]byte, error) {
	if count < 0 || count > d.Remaining() {
		return nil, decodingError("read of %d bytes exceeds the %d remaining", count, d.Remaining())
	}
	start := d.offset
	d.offset += count
	return d.buffer[start:d.offset], nil
}

func (d *Decoder) ReadBoolean() (bool, error) {
	raw, err := d.take(1)
	if err != nil {
		return false, err
	}
	// OPC 10000-6 5.2.2.1: decoders shall treat any non-zero value as true.
	return raw[0] != 0, nil
}

func (d *Decoder) ReadSByte() (int8, error) {
	raw, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return int8(raw[0]), nil
}

func (d *Decoder) ReadByteValue() (byte, error) {
	raw, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (d *Decoder) ReadUInt16() (uint16, error) {
	raw, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(raw), nil
}

func (d *Decoder) ReadInt16() (int16, error) {
	value, err := d.ReadUInt16()
	return int16(value), err
}

func (d *Decoder) ReadUInt32() (uint32, error) {
	raw, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(raw), nil
}

func (d *Decoder) ReadInt32() (int32, error) {
	value, err := d.ReadUInt32()
	return int32(value), err
}

func (d *Decoder) ReadUInt64() (uint64, error) {
	raw, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(raw), nil
}

func (d *Decoder) ReadInt64() (int64, error) {
	value, err := d.ReadUInt64()
	return int64(value), err
}

func (d *Decoder) ReadFloat() (float32, error) {
	value, err := d.ReadUInt32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(value), nil
}

func (d *Decoder) ReadDouble() (float64, error) {
	value, err := d.ReadUInt64()
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(value), nil
}

func (d *Decoder) ReadStatusCode() (StatusCode, error) {
	value, err := d.ReadUInt32()
	if err != nil {
		return 0, err
	}
	return StatusCode(value), nil
}

// readLength decodes a length prefix. It rejects anything below -1, anything
// above the configured bound, and anything larger than the bytes that remain,
// so no allocation is ever sized by an unverified peer value.
func (d *Decoder) readLength(bound int, label string) (length int, isNull bool, err error) {
	raw, err := d.ReadInt32()
	if err != nil {
		return 0, false, err
	}
	if raw == nullLength {
		return 0, true, nil
	}
	if raw < 0 {
		return 0, false, decodingError("%s length %d is negative", label, raw)
	}
	if int64(raw) > int64(bound) {
		return 0, false, limitsError("%s length %d exceeds the %d limit", label, raw, bound)
	}
	return int(raw), false, nil
}

// ReadString returns the value and whether it was null. Embedded NUL bytes are
// preserved: OPC 10000-6 5.2.2.4 requires the decoder to use the length rather
// than stop at a NUL.
func (d *Decoder) ReadString() (value string, isNull bool, err error) {
	length, isNull, err := d.readLength(d.limits.MaxStringBytes, "String")
	if err != nil || isNull {
		return "", isNull, err
	}
	raw, err := d.take(length)
	if err != nil {
		return "", false, err
	}
	return string(raw), false, nil
}

// ReadByteString copies the bytes so a decoded value never aliases the caller's
// buffer.
func (d *Decoder) ReadByteString() (value []byte, isNull bool, err error) {
	length, isNull, err := d.readLength(d.limits.MaxByteStringBytes, "ByteString")
	if err != nil || isNull {
		return nil, isNull, err
	}
	raw, err := d.take(length)
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), raw...), false, nil
}

func (d *Decoder) ReadGuid() (Guid, error) {
	var value Guid
	var err error
	if value.Data1, err = d.ReadUInt32(); err != nil {
		return Guid{}, err
	}
	if value.Data2, err = d.ReadUInt16(); err != nil {
		return Guid{}, err
	}
	if value.Data3, err = d.ReadUInt16(); err != nil {
		return Guid{}, err
	}
	raw, err := d.take(8)
	if err != nil {
		return Guid{}, err
	}
	copy(value.Data4[:], raw)
	return value, nil
}

func (d *Decoder) ReadDateTime() (time.Time, error) {
	raw, err := d.ReadInt64()
	if err != nil {
		return time.Time{}, err
	}
	return DecodeDateTime(raw), nil
}

// ReadArrayLength decodes an array element count. elementBytes is the minimum
// encoded size of one element; when it is positive the declared count is also
// checked against the bytes remaining, so a peer cannot request millions of
// elements from a handful of bytes.
func (d *Decoder) ReadArrayLength(elementBytes int) (length int, isNull bool, err error) {
	length, isNull, err = d.readLength(d.limits.MaxArrayLength, "array")
	if err != nil || isNull {
		return 0, isNull, err
	}
	if elementBytes > 0 {
		if int64(length)*int64(elementBytes) > int64(d.Remaining()) {
			return 0, false, decodingError(
				"array of %d elements needs at least %d bytes but %d remain",
				length, int64(length)*int64(elementBytes), d.Remaining(),
			)
		}
	}
	return length, false, nil
}

// Enter and Leave bound decode recursion. OPC 10000-6 5.1.8 requires at least
// 100 levels and an error beyond what the decoder supports.
func (d *Decoder) Enter() error {
	if d.depth >= d.limits.MaxNestingDepth {
		return limitsError("decoding exceeds the %d level nesting limit", d.limits.MaxNestingDepth)
	}
	d.depth++
	return nil
}

func (d *Decoder) Leave() {
	if d.depth > 0 {
		d.depth--
	}
}
