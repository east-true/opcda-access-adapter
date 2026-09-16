package opcda

import "fmt"

// validateWriteValue enforces an exact Go representation for the explicitly
// supplied VARTYPE. It never infers, widens, narrows, or coerces a value.
//
// An array is held to its own description as well as to its element type:
// ADR-0019 carries the shape beside the elements, so a Write that disagrees
// with the shape it supplied is refused rather than reshaped.
func validateWriteValue(varType DAVarType, value any, limits ArrayLimits) error {
	if varType.IsByRef() {
		return NewAdapterError(CodeUnsupportedVarType, "byref Write values are unsupported")
	}
	if varType.IsArray() {
		array, ok := value.(DAArray)
		if !ok {
			return NewAdapterError(CodeInvalidValue,
				fmt.Sprintf("a %s Write value must carry an array and its shape", varType))
		}
		if array.ElementType != varType.Base() {
			return NewAdapterError(CodeTypeMismatch,
				fmt.Sprintf("array element type %s does not match the declared %s",
					array.ElementType, varType))
		}
		return array.Validate(limits)
	}
	if _, err := validateScalarValue(varType, value, limits.MaxBSTRCodeUnits); err != nil {
		return err
	}
	return nil
}
