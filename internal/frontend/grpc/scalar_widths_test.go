package grpcfrontend

import (
	"math"
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A Write carries its value in a protobuf field wider than the VARTYPE it
// declares: an I1 travels in a sint32. decodeWriteValue is what stops a value
// that does not fit the declared type from reaching the DA server, and the
// exact COM width is this project's central promise about a Write.
//
// The mutation sweep found the range checks unpinned at both ends. Loosening
// either comparison by one survived for every narrow type, as did turning the
// `!ok ||` into `!ok &&`. What existed tested one value one past one limit --
// math.MaxInt16 + 1 is refused -- and never that math.MaxInt16 itself is
// accepted, so nothing said the adapter can write the largest value the type
// holds.
//
// Both ends matter and in opposite ways. A bound loosened by one lets a value
// through that overflows when it is narrowed to the COM type; tightened by
// one, the adapter refuses a value the source would have taken.

type scalarWidth struct {
	name    string
	varType opcda.DAVarType
	// at the limits, which must be accepted, and just outside, which must not.
	low, high           *opcdav1.DAScalarValue
	belowLow, aboveHigh *opcdav1.DAScalarValue
	// a value in a field belonging to a different type.
	wrongField *opcdav1.DAScalarValue
}

func signed(v int32) *opcdav1.DAScalarValue {
	return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I1Value{I1Value: v}}
}

func scalarWidths() []scalarWidth {
	i1 := func(v int32) *opcdav1.DAScalarValue {
		return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I1Value{I1Value: v}}
	}
	ui1 := func(v uint32) *opcdav1.DAScalarValue {
		return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_Ui1Value{Ui1Value: v}}
	}
	i2 := func(v int32) *opcdav1.DAScalarValue {
		return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: v}}
	}
	ui2 := func(v uint32) *opcdav1.DAScalarValue {
		return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_Ui2Value{Ui2Value: v}}
	}
	return []scalarWidth{
		{
			name: "VT_I1", varType: opcda.VTI1,
			low: i1(math.MinInt8), high: i1(math.MaxInt8),
			belowLow: i1(math.MinInt8 - 1), aboveHigh: i1(math.MaxInt8 + 1),
			wrongField: ui1(1),
		},
		{
			name: "VT_UI1", varType: opcda.VTUI1,
			low: ui1(0), high: ui1(math.MaxUint8),
			aboveHigh: ui1(math.MaxUint8 + 1),
			// An unsigned field cannot carry a value below nought, so there is
			// no "below the low bound" case to write: the type is the bound.
			wrongField: i1(1),
		},
		{
			name: "VT_I2", varType: opcda.VTI2,
			low: i2(math.MinInt16), high: i2(math.MaxInt16),
			belowLow: i2(math.MinInt16 - 1), aboveHigh: i2(math.MaxInt16 + 1),
			wrongField: ui2(1),
		},
		{
			name: "VT_UI2", varType: opcda.VTUI2,
			low: ui2(0), high: ui2(math.MaxUint16),
			aboveHigh:  ui2(math.MaxUint16 + 1),
			wrongField: i2(1),
		},
	}
}

func TestAWriteAcceptsTheWholeWidthOfItsTypeAndNothingMore(t *testing.T) {
	for _, width := range scalarWidths() {
		t.Run(width.name, func(t *testing.T) {
			for _, accepted := range []struct {
				what  string
				value *opcdav1.DAScalarValue
			}{{"the lowest value the type holds", width.low}, {"the highest value the type holds", width.high}} {
				if _, err := decodeWriteValue(width.varType, accepted.value); err != nil {
					t.Errorf("%s was refused: %v", accepted.what, err)
				}
			}
			for _, refused := range []struct {
				what  string
				value *opcdav1.DAScalarValue
			}{
				{"one below the lowest", width.belowLow},
				{"one above the highest", width.aboveHigh},
				{"a value in another type's field", width.wrongField},
			} {
				if refused.value == nil {
					continue // not expressible for this type
				}
				if _, err := decodeWriteValue(width.varType, refused.value); err == nil {
					t.Errorf("%s was accepted", refused.what)
				}
			}
		})
	}
}

// The value a Write carries has to survive the narrowing with its bits intact,
// not merely be accepted. A bound checked but a conversion that wrapped would
// send the source a different number than the client asked for, which is the
// failure this whole path exists to prevent.
func TestAWriteNarrowsToTheDeclaredTypeWithoutChangingTheValue(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		varType opcda.DAVarType
		value   *opcdav1.DAScalarValue
		want    any
	}{
		{"I1 lowest", opcda.VTI1, signed(math.MinInt8), int8(math.MinInt8)},
		{"I1 highest", opcda.VTI1, signed(math.MaxInt8), int8(math.MaxInt8)},
		{"UI1 highest", opcda.VTUI1,
			&opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_Ui1Value{Ui1Value: math.MaxUint8}},
			uint8(math.MaxUint8)},
		{"I2 lowest", opcda.VTI2,
			&opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: math.MinInt16}},
			int16(math.MinInt16)},
		{"I2 highest", opcda.VTI2,
			&opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: math.MaxInt16}},
			int16(math.MaxInt16)},
		{"UI2 highest", opcda.VTUI2,
			&opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_Ui2Value{Ui2Value: math.MaxUint16}},
			uint16(math.MaxUint16)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := decodeWriteValue(testCase.varType, testCase.value)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got != testCase.want {
				t.Errorf("decoded %#v, want %#v", got, testCase.want)
			}
		})
	}
}
