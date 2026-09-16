package opcda

import (
	"math"
	"strings"
	"testing"
)

func TestValidateWriteValueRequiresExactWidths(t *testing.T) {
	tests := []struct {
		name    string
		vt      DAVarType
		value   any
		wantErr ErrorCode
	}{
		{name: "I2", vt: VTI2, value: int16(-32768)},
		{name: "I2 rejects Go int", vt: VTI2, value: int(1), wantErr: CodeInvalidValue},
		{name: "UI4", vt: VTUI4, value: uint32(math.MaxUint32)},
		{name: "I8", vt: VTI8, value: int64(math.MinInt64)},
		{name: "R4", vt: VTR4, value: float32(0.1)},
		{name: "R4 rejects R8", vt: VTR4, value: float64(0.1), wantErr: CodeInvalidValue},
		{name: "BOOL", vt: VTBool, value: true},
		// An array VARTYPE now needs an array beside it. A scalar under one is
		// a value that does not match its declared type, which is what it was
		// always closer to than "this adapter has no arrays".
		{name: "array declared but a scalar supplied", vt: VTI2 | VTArray, value: int16(1),
			wantErr: CodeInvalidValue},
		{name: "byref", vt: VTI2 | VTByRef, value: int16(1), wantErr: CodeUnsupportedVarType},
		{name: "DATE", vt: VTDate, value: float64(1), wantErr: CodeUnsupportedVarType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateWriteValue(test.vt, test.value, testArrayLimits(32))
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			adapterErr, ok := AsAdapterError(err)
			if !ok || adapterErr.Code != test.wantErr {
				t.Fatalf("error = %v, want %s", err, test.wantErr)
			}
		})
	}
}

func TestValidateWriteBSTRLimitUsesUTF16CodeUnits(t *testing.T) {
	if err := validateWriteValue(VTBSTR, "A😀", testArrayLimits(3)); err != nil {
		t.Fatal(err)
	}
	err := validateWriteValue(VTBSTR, strings.Repeat("😀", 2), testArrayLimits(3))
	adapterErr, ok := AsAdapterError(err)
	if !ok || adapterErr.Code != CodeBSTRTooLong {
		t.Fatalf("error = %v, want %s", err, CodeBSTRTooLong)
	}
}

// testArrayLimits carries one BSTR bound and the defaults for the rest, so a
// case about string length is not also a case about array shape.
func testArrayLimits(maxBSTRCodeUnits int) ArrayLimits {
	limits := DefaultLimits().ArrayLimits()
	limits.MaxBSTRCodeUnits = maxBSTRCodeUnits
	return limits
}
