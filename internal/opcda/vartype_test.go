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

func TestParseDAVarTypeRejectsFlagsAndUnknowns(t *testing.T) {
	if got, err := ParseDAVarType("VT_I2"); err != nil || got != VTI2 {
		t.Fatalf("ParseDAVarType(VT_I2) = %v, %v", got, err)
	}
	if _, err := ParseDAVarType("VT_I2|VT_ARRAY"); err == nil {
		t.Fatal("expected array type to be rejected")
	}
}
