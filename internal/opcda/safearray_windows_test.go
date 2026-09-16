//go:build windows

package opcda

import "testing"

// These cases run against the real oleaut32 SAFEARRAY implementation, which is
// the point of them. ADR-0019 decision 2 refuses to assume a SAFEARRAY's
// storage order; what remains to be established is that the index vector this
// adapter builds addresses the dimension it means, and that the bounds it asks
// SafeArrayCreate for are the bounds SafeArrayGetLBound reports back.
//
// Neither is a claim about how a real DA server fills an array -- that is what
// ADR-0017's vendor fixture is for -- but it is the difference between agreeing
// with Windows and agreeing with itself.

func buildSafeArray(t *testing.T, array DAArray) uintptr {
	t.Helper()
	created, err := encodeSafeArray(array)
	if err != nil {
		t.Fatalf("encodeSafeArray: %v", err)
	}
	t.Cleanup(func() { destroySafeArray(created) })
	return created
}

func decodeBuilt(t *testing.T, created uintptr, declared DAVarType, limits ArrayLimits) DAArray {
	t.Helper()
	decoded, err := decodeSafeArray(created, declared, limits)
	if err != nil {
		t.Fatalf("decodeSafeArray: %v", err)
	}
	return decoded
}

func TestASafeArrayKeepsItsShapeThroughWindows(t *testing.T) {
	limits := DefaultLimits().ArrayLimits()

	for _, testCase := range []struct {
		name  string
		array DAArray
	}{
		{
			name: "one dimension from zero",
			array: DAArray{
				ElementType: VTI4,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 3}},
				Elements:    []any{int32(10), int32(11), int32(12)},
			},
		},
		{
			// The bound a VB-era source would use. Losing it is what ADR-0019
			// decision 4 refuses, so it has to survive the round trip.
			name: "one dimension from one",
			array: DAArray{
				ElementType: VTI4,
				Dimensions:  []DADimension{{LowerBound: 1, Length: 3}},
				Elements:    []any{int32(10), int32(11), int32(12)},
			},
		},
		{
			name: "one dimension from a negative index",
			array: DAArray{
				ElementType: VTR8,
				Dimensions:  []DADimension{{LowerBound: -2, Length: 4}},
				Elements:    []any{1.5, 2.5, 3.5, 4.5},
			},
		},
		{
			// Two dimensions of different lengths, so a decoder that swapped
			// them would produce a shape that does not fit the elements.
			name: "two dimensions of different lengths",
			array: DAArray{
				ElementType: VTI2,
				Dimensions:  []DADimension{{LowerBound: 1, Length: 2}, {LowerBound: 0, Length: 3}},
				Elements:    []any{int16(1), int16(2), int16(3), int16(4), int16(5), int16(6)},
			},
		},
		{
			name: "strings",
			array: DAArray{
				ElementType: VTBSTR,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 3}},
				Elements:    []any{"a", "온도", "c"},
			},
		},
		{
			name: "booleans",
			array: DAArray{
				ElementType: VTBool,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 2}},
				Elements:    []any{true, false},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			created := buildSafeArray(t, testCase.array)
			decoded := decodeBuilt(t, created, testCase.array.ElementType|VTArray, limits)

			if decoded.ElementType != testCase.array.ElementType {
				t.Errorf("element type = %s, want %s", decoded.ElementType, testCase.array.ElementType)
			}
			if len(decoded.Dimensions) != len(testCase.array.Dimensions) {
				t.Fatalf("decoded %d dimensions, want %d",
					len(decoded.Dimensions), len(testCase.array.Dimensions))
			}
			for index, want := range testCase.array.Dimensions {
				got := decoded.Dimensions[index]
				if got != want {
					t.Errorf("dimension %d = %+v, want %+v", index, got, want)
				}
			}
			if len(decoded.Elements) != len(testCase.array.Elements) {
				t.Fatalf("decoded %d elements, want %d",
					len(decoded.Elements), len(testCase.array.Elements))
			}
			for index, want := range testCase.array.Elements {
				if decoded.Elements[index] != want {
					t.Errorf("element %d = %#v, want %#v", index, decoded.Elements[index], want)
				}
			}
		})
	}
}

// The published order is that the last dimension varies fastest. A decoder that
// read the same elements in the other order would still round trip a square
// array, so this one is not square and its elements say where they came from.
func TestASafeArrayIsReadInThePublishedOrder(t *testing.T) {
	limits := DefaultLimits().ArrayLimits()
	// Elements are encoded as row*10+column, so the value names the index it
	// was written at rather than its position in the flat list.
	const rows, columns = 2, 3
	elements := make([]any, 0, rows*columns)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			elements = append(elements, int32(row*10+column))
		}
	}
	array := DAArray{
		ElementType: VTI4,
		Dimensions:  []DADimension{{LowerBound: 0, Length: rows}, {LowerBound: 0, Length: columns}},
		Elements:    elements,
	}

	created := buildSafeArray(t, array)
	decoded := decodeBuilt(t, created, VTI4|VTArray, limits)

	for index, want := range elements {
		if decoded.Elements[index] != want {
			t.Fatalf("element %d = %#v, want %#v; the last dimension is not varying fastest",
				index, decoded.Elements[index], want)
		}
	}

	// And the elements really are at the indices the order claims: reading one
	// by hand confirms the flat position maps to the index vector it should.
	indices := []int32{1, 2}
	value, _, err := readSafeArrayElement(created, indices, VTI4, limits)
	if err != nil {
		t.Fatal(err)
	}
	if value != int32(12) {
		t.Errorf("the element at [1][2] is %#v, want 12", value)
	}
}

// A source array larger than the configured bound is refused rather than
// truncated: a client given part of an array has no way to know the rest
// existed.
func TestASafeArrayPastTheConfiguredBoundIsRefused(t *testing.T) {
	limits := DefaultLimits().ArrayLimits()
	limits.MaxElements = 3

	array := DAArray{
		ElementType: VTI4,
		Dimensions:  []DADimension{{LowerBound: 0, Length: 4}},
		Elements:    []any{int32(1), int32(2), int32(3), int32(4)},
	}
	created := buildSafeArray(t, array)

	if _, err := decodeSafeArray(created, VTI4|VTArray, limits); err == nil {
		t.Fatal("an array past the element bound was decoded")
	} else {
		assertArrayError(t, err, CodeArrayTooLarge)
	}

	// Exactly the bound is not past it.
	limits.MaxElements = 4
	decoded := decodeBuilt(t, created, VTI4|VTArray, limits)
	if len(decoded.Elements) != 4 {
		t.Errorf("an array of exactly the bound decoded %d elements", len(decoded.Elements))
	}
}

// A Write of an array builds a VARIANT that owns its SAFEARRAY, so the batch's
// VariantClear releases it along with everything else. Leaking one per written
// array is the shape of failure design.md §20.5 asks this code to be reviewed
// for.
func TestAWrittenArrayVariantOwnsItsSafeArray(t *testing.T) {
	array := DAArray{
		ElementType: VTI4,
		Dimensions:  []DADimension{{LowerBound: 0, Length: 2}},
		Elements:    []any{int32(7), int32(8)},
	}
	encoded, err := encodeWriteVariant(VTI4|VTArray, array, DefaultLimits().ArrayLimits())
	if err != nil {
		t.Fatalf("encodeWriteVariant: %v", err)
	}
	if encoded.VT != uint16(VTI4|VTArray) {
		t.Fatalf("VT = %#x, want %#x", encoded.VT, uint16(VTI4|VTArray))
	}

	decoded, err := decodeVariant(&encoded, DefaultLimits().ArrayLimits())
	if err != nil {
		t.Fatalf("decodeVariant: %v", err)
	}
	readBack, ok := decoded.(DAArray)
	if !ok {
		t.Fatalf("decoded %#v, want an array", decoded)
	}
	if len(readBack.Elements) != 2 || readBack.Elements[0] != int32(7) || readBack.Elements[1] != int32(8) {
		t.Errorf("elements = %#v", readBack.Elements)
	}

	// VariantClear releases the array the VARIANT owns. A second clear of the
	// same VARIANT is what a double free would look like, and it is safe
	// because the first one zeroed the type.
	if err := variantClear(&encoded); err != nil {
		t.Fatalf("variantClear: %v", err)
	}
	if err := variantClear(&encoded); err != nil {
		t.Fatalf("a second variantClear failed: %v", err)
	}
}
