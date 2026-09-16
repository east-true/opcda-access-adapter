package opcua

import (
	"bytes"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A UA Variant carries an array as the array bit, a length prefix and an
// optional ArrayDimensions field of lengths (OPC 10000-6 Tables 25 and 26).
// There is no lower bound anywhere in it, so an array that does not start at
// zero has no lossless UA representation -- and ADR-0019 decision 7 refuses it
// rather than re-basing it.
//
// The refusal is the part worth pinning. A UA client cannot tell a re-based
// array from one that was always zero-based, which would leave this adapter
// the only party knowing the value it published is not the value the source
// holds.

func daArray(elementType opcda.DAVarType, dimensions []opcda.DADimension, elements ...any) opcda.DAArray {
	return opcda.DAArray{ElementType: elementType, Dimensions: dimensions, Elements: elements}
}

func TestAnArrayThatDoesNotStartAtZeroIsRefusedRatherThanRebased(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		dimensions []opcda.DADimension
		status     StatusCode
	}{
		{
			name:       "one dimension from zero",
			dimensions: []opcda.DADimension{{LowerBound: 0, Length: 2}},
			status:     StatusGood,
		},
		{
			name:       "two dimensions from zero",
			dimensions: []opcda.DADimension{{LowerBound: 0, Length: 1}, {LowerBound: 0, Length: 2}},
			status:     StatusGood,
		},
		{
			// The bound a VB-era source would use.
			name:       "one dimension from one",
			dimensions: []opcda.DADimension{{LowerBound: 1, Length: 2}},
			status:     StatusBadNotSupported,
		},
		{
			name:       "one dimension from a negative index",
			dimensions: []opcda.DADimension{{LowerBound: -1, Length: 2}},
			status:     StatusBadNotSupported,
		},
		{
			// One dimension is enough to make the whole value unrepresentable:
			// publishing the others and silently re-basing this one would be
			// the same loss in a smaller place.
			name:       "one of two dimensions not from zero",
			dimensions: []opcda.DADimension{{LowerBound: 0, Length: 1}, {LowerBound: 3, Length: 2}},
			status:     StatusBadNotSupported,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			array := daArray(opcda.VTI4, testCase.dimensions, int32(1), int32(2))
			_, status := variantForDAValue(opcda.DAValue{Value: array})
			if status != testCase.status {
				t.Errorf("status = %s, want %s", status.Hex(), testCase.status.Hex())
			}
		})
	}
}

// Every element type the DA layer carries reaches a built-in type of its own
// width. A width widened or narrowed on the way out would be a value the
// source did not report.
func TestEveryArrayElementTypeKeepsItsWidth(t *testing.T) {
	for _, testCase := range []struct {
		elementType opcda.DAVarType
		elements    []any
		builtIn     BuiltInTypeID
	}{
		{opcda.VTBool, []any{true, false}, BuiltInBoolean},
		{opcda.VTI1, []any{int8(1), int8(2)}, BuiltInSByte},
		{opcda.VTUI1, []any{byte(1), byte(2)}, BuiltInByte},
		{opcda.VTI2, []any{int16(1), int16(2)}, BuiltInInt16},
		{opcda.VTUI2, []any{uint16(1), uint16(2)}, BuiltInUInt16},
		{opcda.VTI4, []any{int32(1), int32(2)}, BuiltInInt32},
		{opcda.VTUI4, []any{uint32(1), uint32(2)}, BuiltInUInt32},
		{opcda.VTI8, []any{int64(1), int64(2)}, BuiltInInt64},
		{opcda.VTUI8, []any{uint64(1), uint64(2)}, BuiltInUInt64},
		{opcda.VTR4, []any{float32(1), float32(2)}, BuiltInFloat},
		{opcda.VTR8, []any{1.0, 2.0}, BuiltInDouble},
		{opcda.VTBSTR, []any{"a", "b"}, BuiltInString},
	} {
		t.Run(testCase.elementType.String(), func(t *testing.T) {
			array := daArray(testCase.elementType,
				[]opcda.DADimension{{Length: 2}}, testCase.elements...)
			variant, status := variantForDAValue(opcda.DAValue{Value: array})
			if status != StatusGood {
				t.Fatalf("status = %s", status.Hex())
			}
			if variant.Type != testCase.builtIn {
				t.Errorf("built-in type = %d, want %d", variant.Type, testCase.builtIn)
			}
			if !variant.IsArray {
				t.Error("the Variant was not marked as an array")
			}
		})
	}

	// An element whose Go type is not the one its VARTYPE produces is refused
	// rather than coerced.
	mismatched := daArray(opcda.VTI4, []opcda.DADimension{{Length: 2}}, int32(1), int16(2))
	if _, status := variantForDAValue(opcda.DAValue{Value: mismatched}); status != StatusBadTypeMismatch {
		t.Errorf("a mismatched element gave %s, want Bad_TypeMismatch", status.Hex())
	}
	// And an element type the mapping has no built-in for.
	unmapped := daArray(opcda.VTDate, []opcda.DADimension{{Length: 1}}, 1.0)
	if _, status := variantForDAValue(opcda.DAValue{Value: unmapped}); status != StatusBadTypeMismatch {
		t.Errorf("an unmapped element type gave %s, want Bad_TypeMismatch", status.Hex())
	}
}

// Table 25 puts the array bit in the encoding mask beside the type id and a
// length prefix. Table 26 adds the dimensions field, which is written only
// when there is more than one dimension: a single dimension is already the
// length prefix, and repeating it would be a second statement of the same fact
// for a decoder to disagree with.
func TestAnEncodedArrayVariantCarriesWhatTablesTwentyFiveAndSixRequire(t *testing.T) {
	encode := func(t *testing.T, variant Variant) []byte {
		t.Helper()
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		encoder.WriteVariant(variant)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatalf("encoding failed: %v", err)
		}
		return encoded
	}

	oneDimension, status := variantForDAValue(opcda.DAValue{
		Value: daArray(opcda.VTI4, []opcda.DADimension{{Length: 3}},
			int32(1), int32(2), int32(3)),
	})
	if status != StatusGood {
		t.Fatal(status.Hex())
	}
	encoded := encode(t, oneDimension)
	if encoded[0]&variantArrayValues == 0 {
		t.Errorf("the array bit is not set: mask %#02x", encoded[0])
	}
	if encoded[0]&variantArrayDimensions != 0 {
		t.Errorf("a one-dimensional array wrote a dimensions field: mask %#02x", encoded[0])
	}

	twoDimensions, status := variantForDAValue(opcda.DAValue{
		Value: daArray(opcda.VTI4, []opcda.DADimension{{Length: 3}, {Length: 2}},
			int32(1), int32(2), int32(3), int32(4), int32(5), int32(6)),
	})
	if status != StatusGood {
		t.Fatal(status.Hex())
	}
	multi := encode(t, twoDimensions)
	if multi[0]&variantArrayDimensions == 0 {
		t.Errorf("a two-dimensional array wrote no dimensions field: mask %#02x", multi[0])
	}

	// The two encodings differ only from the mask onward, so the second really
	// carries something the first does not.
	if bytes.Equal(encoded, multi) {
		t.Error("a one and a two dimensional array encoded identically")
	}

	// A decoder held to Table 26 accepts what this encoder writes. The decoder
	// is the one that enforces "all dimensions specified, each greater than
	// zero, product consistent with the array length", so a stream it refuses
	// is one this encoder should not have produced.
	decoder, err := NewDecoder(multi, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.ReadVariant(); err != nil {
		t.Errorf("the decoder refused what the encoder wrote: %v", err)
	}
}

// A Variant declaring an array of one type and carrying a slice of another is a
// programming error that would produce a stream no client can decode, so the
// encoder fails rather than writing it.
func TestAnArrayVariantMustCarryTheSliceItDeclares(t *testing.T) {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteVariant(Variant{Type: BuiltInInt32, IsArray: true, Value: []string{"a"}})
	if _, err := encoder.Bytes(); err == nil {
		t.Error("a Variant declaring Int32 and carrying strings was encoded")
	}
}
