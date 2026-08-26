package opcua

import "time"

// Variant and DataValue follow OPC 10000-6 Tables 26, 27 and 1.

// BuiltInTypeID values from OPC 10000-6 Table 1.
type BuiltInTypeID byte

const (
	BuiltInNull            BuiltInTypeID = 0
	BuiltInBoolean         BuiltInTypeID = 1
	BuiltInSByte           BuiltInTypeID = 2
	BuiltInByte            BuiltInTypeID = 3
	BuiltInInt16           BuiltInTypeID = 4
	BuiltInUInt16          BuiltInTypeID = 5
	BuiltInInt32           BuiltInTypeID = 6
	BuiltInUInt32          BuiltInTypeID = 7
	BuiltInInt64           BuiltInTypeID = 8
	BuiltInUInt64          BuiltInTypeID = 9
	BuiltInFloat           BuiltInTypeID = 10
	BuiltInDouble          BuiltInTypeID = 11
	BuiltInString          BuiltInTypeID = 12
	BuiltInDateTime        BuiltInTypeID = 13
	BuiltInGuid            BuiltInTypeID = 14
	BuiltInByteString      BuiltInTypeID = 15
	BuiltInXMLElement      BuiltInTypeID = 16
	BuiltInNodeID          BuiltInTypeID = 17
	BuiltInExpandedNodeID  BuiltInTypeID = 18
	BuiltInStatusCode      BuiltInTypeID = 19
	BuiltInQualifiedName   BuiltInTypeID = 20
	BuiltInLocalizedText   BuiltInTypeID = 21
	BuiltInExtensionObject BuiltInTypeID = 22
	BuiltInDataValue       BuiltInTypeID = 23
	BuiltInVariant         BuiltInTypeID = 24
	BuiltInDiagnosticInfo  BuiltInTypeID = 25
	// Table 26: ids 26 to 31 are unassigned. A decoder shall accept them and
	// treat the value as a ByteString; an encoder shall not use them.
	builtInMaxAssigned BuiltInTypeID = 25
	builtInMaxReserved BuiltInTypeID = 31
)

// Variant encoding mask bits from OPC 10000-6 Table 26.
const (
	variantTypeMask        byte = 0x3F
	variantArrayDimensions byte = 0x40
	variantArrayValues     byte = 0x80
)

// Variant is a union of the built-in types. This adapter produces scalars only,
// because the DA core decodes no arrays, so an array Variant is decoded but
// never constructed.
type Variant struct {
	Type BuiltInTypeID
	// Value holds the scalar. It is nil for a null Variant.
	Value any
	// IsArray reports that the stream carried an array. The elements are not
	// decoded, since nothing in this adapter consumes them.
	IsArray bool
}

// NullVariant is the value a DataValue carries when there is nothing to report.
func NullVariant() Variant { return Variant{Type: BuiltInNull} }

func (v Variant) IsNull() bool { return v.Type == BuiltInNull }

func (e *Encoder) WriteVariant(value Variant) {
	if value.IsArray {
		e.writeVariantArray(value)
		return
	}
	if value.Type == BuiltInNull {
		e.WriteByteValue(0)
		return
	}
	if value.Type > builtInMaxAssigned {
		// Table 26: encoders shall not use the unassigned ids.
		e.fail(encodingError("built-in type id %d is not assigned", value.Type))
		return
	}
	e.WriteByteValue(byte(value.Type))
	e.writeVariantScalar(value)
}

func (e *Encoder) writeVariantScalar(value Variant) {
	switch value.Type {
	case BuiltInBoolean:
		writeVariantValue(e, value, e.WriteBoolean)
	case BuiltInSByte:
		writeVariantValue(e, value, e.WriteSByte)
	case BuiltInByte:
		writeVariantValue(e, value, e.WriteByteValue)
	case BuiltInInt16:
		writeVariantValue(e, value, e.WriteInt16)
	case BuiltInUInt16:
		writeVariantValue(e, value, e.WriteUInt16)
	case BuiltInInt32:
		writeVariantValue(e, value, e.WriteInt32)
	case BuiltInUInt32:
		writeVariantValue(e, value, e.WriteUInt32)
	case BuiltInInt64:
		writeVariantValue(e, value, e.WriteInt64)
	case BuiltInUInt64:
		writeVariantValue(e, value, e.WriteUInt64)
	case BuiltInFloat:
		writeVariantValue(e, value, e.WriteFloat)
	case BuiltInDouble:
		writeVariantValue(e, value, e.WriteDouble)
	case BuiltInString:
		writeVariantValue(e, value, e.WriteString)
	case BuiltInDateTime:
		writeVariantValue(e, value, e.WriteDateTime)
	case BuiltInGuid:
		writeVariantValue(e, value, e.WriteGuid)
	case BuiltInByteString:
		writeVariantValue(e, value, e.WriteByteString)
	case BuiltInStatusCode:
		writeVariantValue(e, value, e.WriteStatusCode)
	case BuiltInNodeID:
		writeVariantValue(e, value, e.WriteNodeID)
	case BuiltInQualifiedName:
		writeVariantValue(e, value, e.WriteQualifiedName)
	case BuiltInLocalizedText:
		writeVariantValue(e, value, e.WriteLocalizedText)
	case BuiltInExtensionObject:
		// A structure reaches a client inside a Variant as an ExtensionObject.
		// The only structures this adapter produces are its own ServerStatus
		// and BuildInfo; a DA process value is a scalar and never a structure.
		writeVariantValue(e, value, e.WriteExtensionObject)
	default:
		e.fail(encodingError("this adapter does not encode built-in type %d", value.Type))
	}
}

// writeVariantArray writes a one dimensional array Variant. The only arrays
// this adapter produces are the address space's own standard properties, so
// String is the only element type encoded; a DA process value is always a
// scalar, because the DA core decodes no VT_ARRAY variant. An unsupported
// element type fails loudly rather than being written as something it is not.
func (e *Encoder) writeVariantArray(value Variant) {
	elements, ok := value.Value.([]string)
	if !ok || value.Type != BuiltInString {
		e.fail(encodingError("this adapter encodes only String array Variants"))
		return
	}
	// Table 25: the array bit is set in the encoding mask alongside the type
	// id, and the elements follow a length prefix. No ArrayDimensions are
	// written, because the array is one dimensional.
	e.WriteByteValue(byte(value.Type) | variantArrayValues)
	e.WriteInt32(int32(len(elements)))
	for _, element := range elements {
		e.WriteString(element)
	}
}

// writeVariantValue refuses a value whose Go type does not match the declared
// built-in type, rather than coercing it. A mismatch is a programming error
// that would otherwise produce a stream a client cannot decode.
func writeVariantValue[T any](e *Encoder, value Variant, write func(T)) {
	typed, ok := value.Value.(T)
	if !ok {
		e.fail(encodingError("variant value does not match built-in type %d", value.Type))
		return
	}
	write(typed)
}

// ReadVariant decodes a Variant. An array is reported but its elements are
// skipped, because nothing in this adapter consumes them; the decoder still
// validates the declared length so a hostile count cannot drive an allocation.
func (d *Decoder) ReadVariant() (Variant, error) {
	mask, err := d.ReadByteValue()
	if err != nil {
		return Variant{}, err
	}
	typeID := BuiltInTypeID(mask & variantTypeMask)
	if typeID == BuiltInNull {
		return NullVariant(), nil
	}
	if typeID > builtInMaxReserved {
		return Variant{}, decodingError("built-in type id %d is out of range", typeID)
	}

	if mask&variantArrayValues != 0 {
		// The element size is unknown for variable-length types, so the count
		// is bounded by the configured array limit and by the bytes remaining
		// through the one-byte floor.
		length, isNull, lengthErr := d.ReadArrayLength(1)
		if lengthErr != nil {
			return Variant{}, lengthErr
		}
		for index := 0; index < length && !isNull; index++ {
			if _, skipErr := d.skipBuiltIn(typeID); skipErr != nil {
				return Variant{}, skipErr
			}
		}
		if mask&variantArrayDimensions != 0 {
			dimensions, dimensionsNull, dimensionsErr := d.ReadArrayLength(4)
			if dimensionsErr != nil {
				return Variant{}, dimensionsErr
			}
			for index := 0; index < dimensions && !dimensionsNull; index++ {
				if _, readErr := d.ReadInt32(); readErr != nil {
					return Variant{}, readErr
				}
			}
		}
		return Variant{Type: typeID, IsArray: true}, nil
	}

	value, err := d.skipBuiltIn(typeID)
	if err != nil {
		return Variant{}, err
	}
	return Variant{Type: typeID, Value: value}, nil
}

// skipBuiltIn decodes one built-in value, returning it where this adapter has a
// representation and nil where it only needs to advance past it.
func (d *Decoder) skipBuiltIn(typeID BuiltInTypeID) (any, error) {
	switch typeID {
	case BuiltInBoolean:
		return d.ReadBoolean()
	case BuiltInSByte:
		return d.ReadSByte()
	case BuiltInByte:
		return d.ReadByteValue()
	case BuiltInInt16:
		return d.ReadInt16()
	case BuiltInUInt16:
		return d.ReadUInt16()
	case BuiltInInt32:
		return d.ReadInt32()
	case BuiltInUInt32:
		return d.ReadUInt32()
	case BuiltInInt64:
		return d.ReadInt64()
	case BuiltInUInt64:
		return d.ReadUInt64()
	case BuiltInFloat:
		return d.ReadFloat()
	case BuiltInDouble:
		return d.ReadDouble()
	case BuiltInString, BuiltInXMLElement:
		value, isNull, err := d.ReadString()
		if err != nil || isNull {
			return nil, err
		}
		return value, nil
	case BuiltInDateTime:
		return d.ReadDateTime()
	case BuiltInGuid:
		return d.ReadGuid()
	case BuiltInByteString:
		value, isNull, err := d.ReadByteString()
		if err != nil || isNull {
			return nil, err
		}
		return value, nil
	case BuiltInStatusCode:
		return d.ReadStatusCode()
	case BuiltInNodeID:
		return d.ReadNodeID()
	case BuiltInExpandedNodeID:
		return d.ReadExpandedNodeID()
	case BuiltInQualifiedName:
		return d.ReadQualifiedName()
	case BuiltInLocalizedText:
		return d.ReadLocalizedText()
	case BuiltInExtensionObject:
		return d.ReadExtensionObject()
	case BuiltInDataValue:
		return d.ReadDataValue()
	case BuiltInDiagnosticInfo:
		return d.ReadDiagnosticInfo()
	case BuiltInVariant:
		// Table 26: a Variant's value shall not itself be a Variant.
		return nil, decodingError("a Variant may not contain a Variant")
	default:
		// Table 26: unassigned ids carry a ByteString.
		value, isNull, err := d.ReadByteString()
		if err != nil || isNull {
			return nil, err
		}
		return value, nil
	}
}

// DataValue encoding mask bits from OPC 10000-6 Table 27.
const (
	dataValueHasValue             byte = 0x01
	dataValueHasStatus            byte = 0x02
	dataValueHasSourceTimestamp   byte = 0x04
	dataValueHasServerTimestamp   byte = 0x08
	dataValueHasSourcePicoseconds byte = 0x10
	dataValueHasServerPicoseconds byte = 0x20
)

// maxPicoseconds is the ceiling Table 27 sets: a decoder shall treat values of
// 10000 or more as 9999.
const maxPicoseconds uint16 = 9999

// DataValue is OPC 10000-4 Table 131.
type DataValue struct {
	Value             Variant
	Status            StatusCode
	SourceTimestamp   time.Time
	SourcePicoseconds uint16
	ServerTimestamp   time.Time
	ServerPicoseconds uint16
}

// A field is written only when it carries information, which is what Table 27's
// mask is for: a Good status and an absent timestamp are omitted rather than
// written as zeros.
func (e *Encoder) WriteDataValue(value DataValue) {
	var mask byte
	if !value.Value.IsNull() {
		mask |= dataValueHasValue
	}
	if value.Status != StatusGood {
		mask |= dataValueHasStatus
	}
	if !value.SourceTimestamp.IsZero() {
		mask |= dataValueHasSourceTimestamp
		if value.SourcePicoseconds != 0 {
			mask |= dataValueHasSourcePicoseconds
		}
	}
	if !value.ServerTimestamp.IsZero() {
		mask |= dataValueHasServerTimestamp
		if value.ServerPicoseconds != 0 {
			mask |= dataValueHasServerPicoseconds
		}
	}
	e.WriteByteValue(mask)
	if mask&dataValueHasValue != 0 {
		e.WriteVariant(value.Value)
	}
	if mask&dataValueHasStatus != 0 {
		e.WriteStatusCode(value.Status)
	}
	if mask&dataValueHasSourceTimestamp != 0 {
		e.WriteDateTime(value.SourceTimestamp)
	}
	if mask&dataValueHasSourcePicoseconds != 0 {
		e.WriteUInt16(value.SourcePicoseconds)
	}
	if mask&dataValueHasServerTimestamp != 0 {
		e.WriteDateTime(value.ServerTimestamp)
	}
	if mask&dataValueHasServerPicoseconds != 0 {
		e.WriteUInt16(value.ServerPicoseconds)
	}
}

func (d *Decoder) ReadDataValue() (DataValue, error) {
	mask, err := d.ReadByteValue()
	if err != nil {
		return DataValue{}, err
	}
	value := DataValue{Value: NullVariant(), Status: StatusGood}
	if mask&dataValueHasValue != 0 {
		if value.Value, err = d.ReadVariant(); err != nil {
			return DataValue{}, err
		}
	}
	if mask&dataValueHasStatus != 0 {
		if value.Status, err = d.ReadStatusCode(); err != nil {
			return DataValue{}, err
		}
	}
	if mask&dataValueHasSourceTimestamp != 0 {
		if value.SourceTimestamp, err = d.ReadDateTime(); err != nil {
			return DataValue{}, err
		}
	}
	if mask&dataValueHasSourcePicoseconds != 0 {
		picoseconds, readErr := d.ReadUInt16()
		if readErr != nil {
			return DataValue{}, readErr
		}
		value.SourcePicoseconds = clampPicoseconds(picoseconds)
	}
	if mask&dataValueHasServerTimestamp != 0 {
		if value.ServerTimestamp, err = d.ReadDateTime(); err != nil {
			return DataValue{}, err
		}
	}
	if mask&dataValueHasServerPicoseconds != 0 {
		picoseconds, readErr := d.ReadUInt16()
		if readErr != nil {
			return DataValue{}, readErr
		}
		value.ServerPicoseconds = clampPicoseconds(picoseconds)
	}
	return value, nil
}

func clampPicoseconds(value uint16) uint16 {
	if value > maxPicoseconds {
		return maxPicoseconds
	}
	return value
}
