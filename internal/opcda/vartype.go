package opcda

import "fmt"

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

// ParseDAVarType accepts only a symbolic scalar VARTYPE. Array and byref
// request types are intentionally excluded from v0 typed Write.
func ParseDAVarType(name string) (DAVarType, error) {
	if vt, ok := varTypesByName[name]; ok {
		return vt, nil
	}
	return 0, fmt.Errorf("unknown VARTYPE %q", name)
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
