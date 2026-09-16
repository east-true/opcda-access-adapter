package opcda

import "testing"

func TestDAVarTypePreservesFlagsAndBaseType(t *testing.T) {
	vt := VTI2 | VTArray | VTByRef
	if got, want := vt.Base(), VTI2; got != want {
		t.Fatalf("base = %d, want %d", got, want)
	}
	if !vt.IsArray() || !vt.IsByRef() {
		t.Fatal("expected array and byref flags")
	}
	if got, want := vt.String(), "VT_I2|VT_ARRAY|VT_BYREF"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Parse accepts what String writes, which is what lets a client send back the
// type it was told an item has. The flags are carried rather than refused
// here; byref is refused by the value rules, where the message can say so.
func TestParseDAVarTypeReadsWhatStringWrites(t *testing.T) {
	for _, varType := range []DAVarType{
		VTI2, VTBSTR, VTR8,
		VTI2 | VTArray,
		VTBSTR | VTArray,
		VTI2 | VTByRef,
		VTI2 | VTArray | VTByRef,
	} {
		t.Run(varType.String(), func(t *testing.T) {
			got, err := ParseDAVarType(varType.String())
			if err != nil {
				t.Fatalf("ParseDAVarType(%q) = %v", varType.String(), err)
			}
			if got != varType {
				t.Errorf("ParseDAVarType(%q) = %s", varType.String(), got)
			}
		})
	}
	for _, name := range []string{
		"", "VT_NOPE", "VT_I2|", "|VT_ARRAY", "VT_I2|VT_NOPE", "vt_i2", "VT_I2|VT_ARRAY|",
	} {
		if _, err := ParseDAVarType(name); err == nil {
			t.Errorf("ParseDAVarType(%q) was accepted", name)
		}
	}
}

// The normalisation DecodesAs states must be the one the decoder performs, or
// stating it is worse than not stating it. decodeVariant reads VT_INT and
// VT_ERROR out of the same storage as VT_I4 and VT_UINT out of the same as
// VT_UI4, and everything else out of its own.
func TestDecodesAsMatchesTheTypesTheDecoderGroups(t *testing.T) {
	for _, testCase := range []struct {
		varType DAVarType
		want    DAVarType
	}{
		{VTInt, VTI4},
		{VTError, VTI4},
		{VTUInt, VTUI4},
		// Everything the table covers maps to itself, so composing is a no-op.
		{VTI1, VTI1}, {VTUI1, VTUI1}, {VTI2, VTI2}, {VTUI2, VTUI2},
		{VTI4, VTI4}, {VTUI4, VTUI4}, {VTI8, VTI8}, {VTUI8, VTUI8},
		{VTR4, VTR4}, {VTR8, VTR8}, {VTBool, VTBool}, {VTBSTR, VTBSTR},
		{VTEmpty, VTEmpty}, {VTNull, VTNull},
		// A type the decoder refuses is left alone rather than normalised onto
		// something it is not.
		{VTDate, VTDate}, {VTDecimal, VTDecimal}, {VTCY, VTCY},
	} {
		t.Run(testCase.varType.Name(), func(t *testing.T) {
			if got := testCase.varType.DecodesAs(); got != testCase.want {
				t.Fatalf("DecodesAs = %s, want %s", got, testCase.want)
			}
		})
	}

	// The array and byref bits are not part of the question; the base type is.
	if got := (VTI4 | VTArray).DecodesAs(); got != VTI4 {
		t.Fatalf("DecodesAs on an array = %s, want the base type", got)
	}
}
