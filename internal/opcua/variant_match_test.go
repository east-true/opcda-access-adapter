package opcua

import (
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A UA Write is strictly typed: the variant a client sends has to be the built-in
// type the item's VARTYPE maps to, and nothing is coerced. variantMatchesVarType
// is where that is decided, and it is a switch of fourteen equalities.
//
// The sweep found several of them removable -- turning `variant.Type ==
// BuiltInBoolean` into `!=` survived, and the same for Byte. What that means is
// that no test had ever offered a Boolean-declared Write carrying something
// that was not a Boolean, so nothing said the check rejected anything.
//
// The table below pairs each VARTYPE with the built-in it demands and with a
// different one, so every arm is exercised both ways. The mismatching type is
// chosen to be adjacent in width where possible, because a check that compared
// sizes rather than types would still pass a deliberately absurd mismatch.

func TestAWriteVariantMustBeExactlyTheTypeItsVarTypeDemands(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		varType  opcda.DAVarType
		matching BuiltInTypeID
		other    BuiltInTypeID
	}{
		{"VT_BOOL", opcda.VTBool, BuiltInBoolean, BuiltInSByte},
		{"VT_I1", opcda.VTI1, BuiltInSByte, BuiltInByte},
		{"VT_UI1", opcda.VTUI1, BuiltInByte, BuiltInSByte},
		{"VT_I2", opcda.VTI2, BuiltInInt16, BuiltInUInt16},
		{"VT_UI2", opcda.VTUI2, BuiltInUInt16, BuiltInInt16},
		{"VT_I4", opcda.VTI4, BuiltInInt32, BuiltInUInt32},
		{"VT_UI4", opcda.VTUI4, BuiltInUInt32, BuiltInInt32},
		{"VT_I8", opcda.VTI8, BuiltInInt64, BuiltInUInt64},
		{"VT_UI8", opcda.VTUI8, BuiltInUInt64, BuiltInInt64},
		{"VT_R4", opcda.VTR4, BuiltInFloat, BuiltInDouble},
		{"VT_R8", opcda.VTR8, BuiltInDouble, BuiltInFloat},
		{"VT_BSTR", opcda.VTBSTR, BuiltInString, BuiltInByteString},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if !variantMatchesVarType(Variant{Type: testCase.matching}, testCase.varType) {
				t.Errorf("%s refused its own built-in type %v", testCase.name, testCase.matching)
			}
			if variantMatchesVarType(Variant{Type: testCase.other}, testCase.varType) {
				t.Errorf("%s accepted %v, which is a different type of a comparable width",
					testCase.name, testCase.other)
			}
		})
	}
}

// A VARTYPE with no UA mapping matches nothing, which is INV-10 again: an
// unmapped type fails rather than being coerced into whatever is nearest.
//
// VT_ERROR is deliberately not in this list, and finding out why is what this
// sweep turned up. DataTypeFor composes with DAVarType.DecodesAs, which reports
// the type the DA core actually produced -- and the core reads VT_INT and
// VT_ERROR out of the same storage as VT_I4. So they answer Int32, which is the
// value that was really decoded rather than a borrowed row. VT_CY has no row
// and no normalisation, so it is the one that still has no answer.
func TestAnUnmappedVarTypeMatchesNoVariant(t *testing.T) {
	for _, varType := range []opcda.DAVarType{opcda.VTCY, opcda.VTVariant, opcda.VTArray | opcda.VTI4} {
		for _, builtIn := range []BuiltInTypeID{BuiltInInt32, BuiltInString, BuiltInDouble, BuiltInBoolean} {
			if variantMatchesVarType(Variant{Type: builtIn}, varType) {
				t.Errorf("unmapped VARTYPE 0x%04X matched %v", uint16(varType), builtIn)
			}
		}
	}
}

// The three VARTYPEs the DA core decodes into another type's storage report
// that type rather than failing. #61 made this so, and it is the opposite of
// what ADR-0016 originally decided, so it is worth a test that says which
// behaviour is current.
func TestTheDecodedWidthIsWhatIsReported(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		varType opcda.DAVarType
		builtIn BuiltInTypeID
	}{
		{"VT_INT is decoded as an int32", opcda.VTInt, BuiltInInt32},
		{"VT_ERROR is decoded as an int32", opcda.VTError, BuiltInInt32},
		{"VT_UINT is decoded as a uint32", opcda.VTUInt, BuiltInUInt32},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if !variantMatchesVarType(Variant{Type: testCase.builtIn}, testCase.varType) {
				t.Errorf("%s did not match %v", testCase.name, testCase.builtIn)
			}
			// It reports the decoded width and not some other one: the
			// normalisation is to the storage the core used, not to anything
			// that merely looks close.
			if variantMatchesVarType(Variant{Type: BuiltInInt16}, testCase.varType) {
				t.Errorf("%s matched Int16", testCase.name)
			}
		})
	}
}
