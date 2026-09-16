package grpcfrontend

import (
	"context"
	"math"
	"testing"

	"google.golang.org/protobuf/proto"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// An array travels in a field of its own beside the scalar one. ADR-0019
// decision 6 adds the field rather than replacing the scalar in a oneof:
// renumbering or re-typing a field clients already decode would break every
// existing one to add a feature none of them asked for.
//
// Exactly one of the two is set, and the result's data_type already carries an
// array bit, so a client that only understands scalars can tell an array apart
// from a value that was not carried.

func readArrayResult(t *testing.T, array opcda.DAArray) *opcdav1.DAReadResult {
	t.Helper()
	varType := array.ElementType | opcda.VTArray
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return []opcda.ReadResult{{
			ItemID: "Test/Array", VarType: &varType,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{
				ItemID: "Test/Array", VarType: varType, Value: array,
				QualityRaw: 0x00C0, HRESULT: opcda.SOK,
			},
		}}, nil
	}}
	server := New(runtime, Config{MaxReadItems: 4, MaxItemIDBytes: 64})
	response, err := server.Read(context.Background(), &opcdav1.DAReadRequest{
		Items: []*opcdav1.DAReadItem{{ItemId: "Test/Array"}},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("the response carried %d results", len(response.Results))
	}
	return response.Results[0]
}

func TestAReadArrayOverGRPCCarriesItsShape(t *testing.T) {
	result := readArrayResult(t, opcda.DAArray{
		ElementType: opcda.VTI4,
		Dimensions: []opcda.DADimension{
			{LowerBound: 1, Length: 3}, {LowerBound: 0, Length: 2},
		},
		Elements: []any{int32(10), int32(11), int32(12), int32(13), int32(14), int32(15)},
	})

	if !result.Ok {
		t.Fatalf("an array read was not ok: %s", result.ErrorCode)
	}
	// The scalar field stays empty, and the array bit on the data type is what
	// tells a scalar-only client which field carries the value.
	if result.Value != nil {
		t.Errorf("an array result also set the scalar value field: %#v", result.Value)
	}
	if !result.DataType.GetArray() {
		t.Error("an array result did not set the array bit on its data type")
	}

	array := result.GetArrayValue()
	if array == nil {
		t.Fatal("an array result carried no array value")
	}
	if array.GetElementDataType().GetRaw() != uint32(opcda.VTI4) {
		t.Errorf("element type = %#v", array.GetElementDataType())
	}
	if array.GetElementDataType().GetArray() {
		t.Error("the element type was published as an array")
	}
	dimensions := array.GetDimensions()
	if len(dimensions) != 2 {
		t.Fatalf("dimensions = %#v", dimensions)
	}
	if dimensions[0].GetLowerBound() != 1 || dimensions[0].GetLength() != 3 {
		t.Errorf("the first dimension = %+v", dimensions[0])
	}
	if dimensions[1].GetLowerBound() != 0 || dimensions[1].GetLength() != 2 {
		t.Errorf("the second dimension = %+v", dimensions[1])
	}
	elements := array.GetElements()
	if len(elements) != 6 {
		t.Fatalf("%d elements", len(elements))
	}
	for index, want := range []int32{10, 11, 12, 13, 14, 15} {
		if got := elements[index].GetI4Value(); got != want {
			t.Errorf("element %d = %d, want %d", index, got, want)
		}
	}
}

// The lower bound is the source's, never normalised. A client writing back to
// an index it read must reach the element it read.
func TestAReadArrayOverGRPCKeepsTheLowerBoundTheSourceGave(t *testing.T) {
	for _, lowerBound := range []int32{-3, 0, 1, 7} {
		result := readArrayResult(t, opcda.DAArray{
			ElementType: opcda.VTI2,
			Dimensions:  []opcda.DADimension{{LowerBound: lowerBound, Length: 2}},
			Elements:    []any{int16(1), int16(2)},
		})
		got := result.GetArrayValue().GetDimensions()[0].GetLowerBound()
		if got != lowerBound {
			t.Errorf("a lower bound of %d was published as %d", lowerBound, got)
		}
	}
}

// Protobuf carries a non-finite float natively, so an array element needs no
// spelling of its own here -- unlike JSON, where a scalar names one through a
// sibling field an element does not have.
func TestANonFiniteArrayElementNeedsNoEncodingOverGRPC(t *testing.T) {
	result := readArrayResult(t, opcda.DAArray{
		ElementType: opcda.VTR8,
		Dimensions:  []opcda.DADimension{{Length: 3}},
		Elements:    []any{1.5, math.Inf(-1), math.NaN()},
	})
	elements := result.GetArrayValue().GetElements()
	if elements[0].GetR8Value() != 1.5 {
		t.Errorf("element 0 = %v", elements[0].GetR8Value())
	}
	if !math.IsInf(elements[1].GetR8Value(), -1) {
		t.Errorf("element 1 = %v, want -Inf", elements[1].GetR8Value())
	}
	if !math.IsNaN(elements[2].GetR8Value()) {
		t.Errorf("element 2 = %v, want NaN", elements[2].GetR8Value())
	}
}

func writeArrayItem(elementType opcda.DAVarType, array *opcdav1.DAArrayValue) *opcdav1.DAWriteItem {
	varType := elementType | opcda.VTArray
	return &opcdav1.DAWriteItem{
		ItemId:     "Test/Array",
		DataType:   &opcdav1.DAVarType{Raw: uint32(varType), Name: varType.String(), Array: true},
		ArrayValue: array,
	}
}

func i4Elements(values ...int32) []*opcdav1.DAScalarValue {
	elements := make([]*opcdav1.DAScalarValue, len(values))
	for index, value := range values {
		elements[index] = &opcdav1.DAScalarValue{
			Value: &opcdav1.DAScalarValue_I4Value{I4Value: value},
		}
	}
	return elements
}

// A Write supplies the same description a Read publishes, so a client can send
// back what it was given.
func TestAWrittenArrayOverGRPCReachesTheSourceAsDescribed(t *testing.T) {
	var written []opcda.WriteItem
	runtime := &testRuntime{
		status: opcda.RuntimeStatus{WriteEnabled: true},
		write: func(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
			written = items
			return []opcda.WriteResult{
				{ItemID: "Test/Array", HRESULT: opcda.SOK, HRESULTPresent: true},
			}, nil
		},
	}
	server := New(runtime, Config{MaxWriteItems: 4, MaxItemIDBytes: 64})
	_, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{
		Items: []*opcdav1.DAWriteItem{writeArrayItem(opcda.VTI4, &opcdav1.DAArrayValue{
			ElementDataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI4)},
			Dimensions: []*opcdav1.DADimension{
				{LowerBound: 1, Length: 2}, {LowerBound: 0, Length: 2},
			},
			Elements: i4Elements(1, 2, 3, 4),
		})},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("the source received %d items", len(written))
	}
	if written[0].VarType != opcda.VTI4|opcda.VTArray {
		t.Errorf("the source was asked for %s", written[0].VarType)
	}
	array, ok := written[0].Value.(opcda.DAArray)
	if !ok {
		t.Fatalf("the source received %#v", written[0].Value)
	}
	want := []opcda.DADimension{{LowerBound: 1, Length: 2}, {LowerBound: 0, Length: 2}}
	for index, dimension := range want {
		if array.Dimensions[index] != dimension {
			t.Errorf("dimension %d = %+v, want %+v", index, array.Dimensions[index], dimension)
		}
	}
	for index, element := range []any{int32(1), int32(2), int32(3), int32(4)} {
		if array.Elements[index] != element {
			t.Errorf("element %d = %#v", index, array.Elements[index])
		}
	}
}

// The two value fields name different shapes, and a request that sets the
// wrong one -- or both -- has not said what it wants written.
func TestAWriteMustUseTheValueFieldItsVarTypeNames(t *testing.T) {
	shape := &opcdav1.DAArrayValue{
		Dimensions: []*opcdav1.DADimension{{Length: 2}},
		Elements:   i4Elements(1, 2),
	}
	scalar := &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}}

	if _, err := decodeWriteItemValue(opcda.VTI4|opcda.VTArray,
		writeArrayItem(opcda.VTI4, shape)); err != nil {
		t.Fatalf("an array under an array VARTYPE was refused: %v", err)
	}

	both := writeArrayItem(opcda.VTI4, shape)
	both.Value = scalar
	if _, err := decodeWriteItemValue(opcda.VTI4|opcda.VTArray, both); err == nil {
		t.Error("an item carrying both a scalar and an array was accepted")
	}

	scalarUnderArray := writeArrayItem(opcda.VTI4, nil)
	scalarUnderArray.Value = scalar
	if _, err := decodeWriteItemValue(opcda.VTI4|opcda.VTArray, scalarUnderArray); err == nil {
		t.Error("an array VARTYPE carrying a scalar was accepted")
	}

	arrayUnderScalar := &opcdav1.DAWriteItem{
		ItemId:     "Test/Array",
		DataType:   &opcdav1.DAVarType{Raw: uint32(opcda.VTI4)},
		ArrayValue: shape,
	}
	if _, err := decodeWriteItemValue(opcda.VTI4, arrayUnderScalar); err == nil {
		t.Error("a scalar VARTYPE carrying an array was accepted")
	}

	// And an array VARTYPE with neither is a Write that named nothing.
	if _, err := decodeWriteItemValue(opcda.VTI4|opcda.VTArray,
		writeArrayItem(opcda.VTI4, nil)); err == nil {
		t.Error("an array VARTYPE carrying no value at all was accepted")
	}
}

// A message whose elements and dimensions do not describe each other is
// refused rather than reshaped: the shape is what the client asked the source
// to write, and guessing would write somewhere it did not name.
func TestAWrittenArrayOverGRPCMustAgreeWithItself(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		array *opcdav1.DAArrayValue
	}{
		{"fewer elements than the shape describes", &opcdav1.DAArrayValue{
			Dimensions: []*opcdav1.DADimension{{Length: 3}}, Elements: i4Elements(1, 2)}},
		{"more elements than the shape describes", &opcdav1.DAArrayValue{
			Dimensions: []*opcdav1.DADimension{{Length: 1}}, Elements: i4Elements(1, 2)}},
		{"no dimensions at all", &opcdav1.DAArrayValue{Elements: i4Elements(1)}},
		{"a dimension of no length", &opcdav1.DAArrayValue{
			Dimensions: []*opcdav1.DADimension{{Length: 0}}, Elements: i4Elements(1)}},
		{"an element type that disagrees with the declared one", &opcdav1.DAArrayValue{
			ElementDataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI2)},
			Dimensions:      []*opcdav1.DADimension{{Length: 1}}, Elements: i4Elements(1)}},
		{"an element whose field is not the declared type", &opcdav1.DAArrayValue{
			Dimensions: []*opcdav1.DADimension{{Length: 1}},
			Elements: []*opcdav1.DAScalarValue{{
				Value: &opcdav1.DAScalarValue_BstrValue{BstrValue: "x"},
			}}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := decodeArray(opcda.VTI4|opcda.VTArray, testCase.array); err == nil {
				t.Error("a contradictory array was accepted")
			}
		})
	}
}

// The array encoder fills its elements from backing slices rather than
// allocating a message and a wrapper each, which is what turns a
// thousand-element array from two thousand allocations into a fixed few. It is
// a second encoder for values the scalar one already knows how to write, and
// two encoders that drift are two wire formats -- so it is held to answering
// exactly what encodeScalar answers for the same element.
func TestTheArrayEncoderWritesWhatTheScalarEncoderWrites(t *testing.T) {
	for _, testCase := range []struct {
		elementType opcda.DAVarType
		elements    []any
	}{
		{opcda.VTEmpty, []any{nil, nil}},
		{opcda.VTNull, []any{nil}},
		{opcda.VTI1, []any{int8(-128), int8(0), int8(127)}},
		{opcda.VTUI1, []any{uint8(0), uint8(255)}},
		{opcda.VTI2, []any{int16(-32768), int16(32767)}},
		{opcda.VTUI2, []any{uint16(0), uint16(65535)}},
		{opcda.VTI4, []any{int32(-2147483648), int32(2147483647)}},
		{opcda.VTUI4, []any{uint32(0), uint32(4294967295)}},
		{opcda.VTI8, []any{int64(-9223372036854775808), int64(9223372036854775807)}},
		{opcda.VTUI8, []any{uint64(0), uint64(18446744073709551615)}},
		{opcda.VTR4, []any{float32(1.5), float32(math.Inf(-1)), float32(math.NaN())}},
		{opcda.VTR8, []any{1.5, math.Inf(1), math.NaN()}},
		{opcda.VTBool, []any{true, false}},
		{opcda.VTBSTR, []any{"", "a", "온도", "😀"}},
		{opcda.VTError, []any{int32(-2147024891)}},
		{opcda.VTInt, []any{int32(7)}},
		{opcda.VTUInt, []any{uint32(7)}},
	} {
		t.Run(testCase.elementType.String(), func(t *testing.T) {
			array := opcda.DAArray{
				ElementType: testCase.elementType,
				Dimensions:  []opcda.DADimension{{Length: uint32(len(testCase.elements))}},
				Elements:    testCase.elements,
			}
			encoded, err := encodeArray(array)
			if err != nil {
				t.Fatalf("encodeArray: %v", err)
			}
			if len(encoded.GetElements()) != len(testCase.elements) {
				t.Fatalf("%d elements encoded, want %d",
					len(encoded.GetElements()), len(testCase.elements))
			}
			for index, element := range testCase.elements {
				want, err := encodeScalar(testCase.elementType, element)
				if err != nil {
					t.Fatalf("encodeScalar(%#v): %v", element, err)
				}
				if !proto.Equal(encoded.GetElements()[index], want) {
					t.Errorf("element %d encoded as %v, the scalar encoder writes %v",
						index, encoded.GetElements()[index], want)
				}
			}
		})
	}
}

// The two also have to refuse the same things. An element of the wrong Go type
// is the case that matters: the batch encoder asserts a type per element, and
// one that let a mismatch through would write a zero where a value belongs.
func TestTheArrayEncoderRefusesWhatTheScalarEncoderRefuses(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		elementType opcda.DAVarType
		elements    []any
	}{
		{"an int16 where an int32 belongs", opcda.VTI4, []any{int32(1), int16(2)}},
		{"a float64 where a float32 belongs", opcda.VTR4, []any{float32(1), 2.0}},
		{"a string where a number belongs", opcda.VTI4, []any{"1"}},
		{"nothing where a number belongs", opcda.VTI4, []any{nil}},
		{"a number where nothing belongs", opcda.VTEmpty, []any{int32(1)}},
		{"invalid UTF-8 in a string", opcda.VTBSTR, []any{string([]byte{0xff})}},
		{"an element type the encoder has no field for", opcda.VTDate, []any{1.0}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			array := opcda.DAArray{
				ElementType: testCase.elementType,
				Dimensions:  []opcda.DADimension{{Length: uint32(len(testCase.elements))}},
				Elements:    testCase.elements,
			}
			if _, err := encodeArray(array); err == nil {
				t.Error("the array encoder accepted it")
			}
			// The scalar encoder refuses at least one of the same elements,
			// which is what makes this the same rule rather than a new one.
			refused := false
			for _, element := range testCase.elements {
				if _, err := encodeScalar(testCase.elementType, element); err != nil {
					refused = true
				}
			}
			if !refused {
				t.Error("the scalar encoder accepted every element, so the two disagree")
			}
		})
	}
}
