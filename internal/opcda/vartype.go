package opcda

import (
	"fmt"
	"strings"
)

// DAVarType is the unmodified COM VARTYPE bitfield received from or supplied
// to OPC DA. The scalar base type must never be inferred from a Go value.
type DAVarType uint16

const (
	VTEmpty   DAVarType = 0
	VTNull    DAVarType = 1
	VTI2      DAVarType = 2
	VTI4      DAVarType = 3
	VTR4      DAVarType = 4
	VTR8      DAVarType = 5
	VTCY      DAVarType = 6
	VTDate    DAVarType = 7
	VTBSTR    DAVarType = 8
	VTError   DAVarType = 10
	VTBool    DAVarType = 11
	VTVariant DAVarType = 12
	VTDecimal DAVarType = 14
	VTI1      DAVarType = 16
	VTUI1     DAVarType = 17
	VTUI2     DAVarType = 18
	VTUI4     DAVarType = 19
	VTI8      DAVarType = 20
	VTUI8     DAVarType = 21
	VTInt     DAVarType = 22
	VTUInt    DAVarType = 23

	VTArray    DAVarType = 0x2000
	VTByRef    DAVarType = 0x4000
	VTTypeMask DAVarType = 0x0FFF
)

func (vt DAVarType) Base() DAVarType {
	return vt & VTTypeMask
}

func (vt DAVarType) IsArray() bool {
	return vt&VTArray != 0
}

func (vt DAVarType) IsByRef() bool {
	return vt&VTByRef != 0
}

// DecodesAs reports the VARTYPE whose Go representation this one is decoded
// into. A COM VARIANT carries several types that are the same value in the same
// storage: VT_INT and VT_ERROR are read as VT_I4's int32, and VT_UINT as
// VT_UI4's uint32. The decoder therefore hands all of them up as int32 or
// uint32, and every layer above sees only that.
//
// Stating the normalisation here, once, is what stops layers disagreeing about
// it. The UA frontend answers two questions about a value — what type its node
// declares, and what type its Variant carries — and it used to derive them
// separately: the declared type from the raw VARTYPE through OPC 10000-8
// Table A.2, which has no row for VT_INT, and the Variant from the decoded Go
// value, which is plainly an int32. A VT_INT item was therefore delivered as an
// Int32 by a node that declared itself the abstract base type.
//
// Everything else maps to itself, so composing with this is a no-op for the
// types Table A.2 does cover.
func (vt DAVarType) DecodesAs() DAVarType {
	switch vt.Base() {
	case VTInt, VTError:
		return VTI4
	case VTUInt:
		return VTUI4
	default:
		return vt.Base()
	}
}

func (vt DAVarType) Name() string {
	if name, ok := varTypeNames[vt.Base()]; ok {
		return name
	}
	return fmt.Sprintf("VT_UNKNOWN_%d", uint16(vt.Base()))
}

func (vt DAVarType) String() string {
	name := vt.Name()
	if vt.IsArray() {
		name += "|VT_ARRAY"
	}
	if vt.IsByRef() {
		name += "|VT_BYREF"
	}
	return name
}

var varTypeNames = map[DAVarType]string{
	VTEmpty:   "VT_EMPTY",
	VTNull:    "VT_NULL",
	VTI2:      "VT_I2",
	VTI4:      "VT_I4",
	VTR4:      "VT_R4",
	VTR8:      "VT_R8",
	VTCY:      "VT_CY",
	VTDate:    "VT_DATE",
	VTBSTR:    "VT_BSTR",
	VTError:   "VT_ERROR",
	VTBool:    "VT_BOOL",
	VTVariant: "VT_VARIANT",
	VTDecimal: "VT_DECIMAL",
	VTI1:      "VT_I1",
	VTUI1:     "VT_UI1",
	VTUI2:     "VT_UI2",
	VTUI4:     "VT_UI4",
	VTI8:      "VT_I8",
	VTUI8:     "VT_UI8",
	VTInt:     "VT_INT",
	VTUInt:    "VT_UINT",
}

var varTypesByName = func() map[string]DAVarType {
	values := make(map[string]DAVarType, len(varTypeNames))
	for vt, name := range varTypeNames {
		values[name] = vt
	}
	return values
}()

// ParseDAVarType accepts a symbolic VARTYPE, optionally with the VT_ARRAY
// suffix String writes. Parsing what String produces is what lets a client send
// back the type it was told an item has.
//
// A byref suffix parses too, and is refused later by the value rules rather
// than here: "byref values are unsupported" tells an operator something, and
// "unknown VARTYPE" sends them looking for a spelling mistake that is not
// there.
func ParseDAVarType(name string) (DAVarType, error) {
	base := name
	var flags DAVarType
	for {
		remaining, suffix, found := cutLastVarTypeSuffix(base)
		if !found {
			break
		}
		switch suffix {
		case "VT_ARRAY":
			flags |= VTArray
		case "VT_BYREF":
			flags |= VTByRef
		default:
			return 0, fmt.Errorf("unknown VARTYPE %q", name)
		}
		base = remaining
	}
	vt, ok := varTypesByName[base]
	if !ok {
		return 0, fmt.Errorf("unknown VARTYPE %q", name)
	}
	return vt | flags, nil
}

// cutLastVarTypeSuffix splits one trailing "|VT_..." flag off a VARTYPE name.
func cutLastVarTypeSuffix(name string) (string, string, bool) {
	index := strings.LastIndex(name, "|")
	if index < 0 {
		return name, "", false
	}
	return name[:index], name[index+1:], true
}

type DAVarTypeInfo struct {
	Code  uint16 `json:"code"`
	Name  string `json:"name"`
	Array bool   `json:"array,omitempty"`
	ByRef bool   `json:"byRef,omitempty"`
}

func (vt DAVarType) Information() DAVarTypeInfo {
	return DAVarTypeInfo{
		Code:  uint16(vt),
		Name:  vt.Name(),
		Array: vt.IsArray(),
		ByRef: vt.IsByRef(),
	}
}
