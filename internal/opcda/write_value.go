package opcda

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// validateWriteValue enforces an exact Go representation for the explicitly
// supplied VARTYPE. It never infers, widens, narrows, or coerces a value.
func validateWriteValue(varType DAVarType, value any, maxBSTRCodeUnits int) error {
	if varType.IsArray() || varType.IsByRef() {
		return NewAdapterError(CodeUnsupportedVarType, "array and byref Write values are unsupported")
	}
	valid := false
	switch varType.Base() {
	case VTEmpty, VTNull:
		valid = value == nil
	case VTI1:
		_, valid = value.(int8)
	case VTUI1:
		_, valid = value.(uint8)
	case VTI2:
		_, valid = value.(int16)
	case VTUI2:
		_, valid = value.(uint16)
	case VTI4, VTInt, VTError:
		_, valid = value.(int32)
	case VTUI4, VTUInt:
		_, valid = value.(uint32)
	case VTI8:
		_, valid = value.(int64)
	case VTUI8:
		_, valid = value.(uint64)
	case VTR4:
		_, valid = value.(float32)
	case VTR8:
		_, valid = value.(float64)
	case VTBool:
		_, valid = value.(bool)
	case VTBSTR:
		stringValue, ok := value.(string)
		if !ok {
			break
		}
		if !utf8.ValidString(stringValue) {
			return NewAdapterError(CodeInvalidValue, "VT_BSTR Write value must be valid UTF-8")
		}
		if len(utf16.Encode([]rune(stringValue))) > maxBSTRCodeUnits {
			return NewAdapterError(CodeBSTRTooLong, "VT_BSTR Write value exceeds configured limit")
		}
		valid = true
	default:
		return NewAdapterError(CodeUnsupportedVarType, fmt.Sprintf("unsupported Write VARTYPE %s", varType))
	}
	if !valid {
		return NewAdapterError(CodeInvalidValue, fmt.Sprintf("Write value does not exactly match %s", varType))
	}
	return nil
}
