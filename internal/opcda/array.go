package opcda

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// DADimension is one dimension of a SAFEARRAY, described the way COM describes
// it: a lower bound the source chose and a length.
//
// The lower bound is carried rather than normalised. ADR-0019 records why: two
// sources that disagree about where their arrays start would otherwise become
// indistinguishable, and a client writing back to an index it read would
// address a different element than the one it meant.
type DADimension struct {
	LowerBound int32
	Length     uint32
}

// DAArray is a SAFEARRAY value with its shape beside it. design.md §20.4
// requires five things to survive -- element VARTYPE, dimension count, lower
// bound per dimension, length per dimension, and the element values -- and
// forbids the flattening that discards the first four.
//
// Elements is flat, and that is not the flattening §20.4 rules out: it is
// ordered by a rule stated here and published alongside the shape, so a reader
// can reconstruct the array from the description alone.
//
// The order is the one ADR-0019 decision 3 fixes: the last dimension varies
// fastest, and Dimensions[i] is COM dimension i+1 as SafeArrayGetLBound and
// SafeArrayGetUBound number them.
type DAArray struct {
	// ElementType is the VARTYPE of one element, without VT_ARRAY or VT_BYREF.
	ElementType DAVarType
	Dimensions  []DADimension
	// Elements holds exactly the product of the dimension lengths, each one
	// the Go type a scalar of ElementType decodes into.
	Elements []any
}

// ArrayLimits bounds what an array may be. It is the subset of Limits the
// array code needs, so the shape of an array can be checked without a runtime.
type ArrayLimits struct {
	MaxElements      int
	MaxDimensions    int
	MaxBSTRCodeUnits int
	// MaxArrayBSTRCodeUnits bounds the UTF-16 code units all of one array's
	// BSTR elements carry together. Element count and per-element length alone
	// do not bound a string array: an array at both of those limits is two
	// orders of magnitude more memory than the same count of numbers.
	MaxArrayBSTRCodeUnits int
}

// ArrayLimits reports the array bounds these runtime limits carry.
func (limits Limits) ArrayLimits() ArrayLimits {
	return ArrayLimits{
		MaxElements:           limits.MaxArrayElements,
		MaxDimensions:         limits.MaxArrayDimensions,
		MaxBSTRCodeUnits:      limits.MaxBSTRCodeUnits,
		MaxArrayBSTRCodeUnits: limits.MaxArrayBSTRCodeUnits,
	}
}

// ElementCount reports how many elements the dimensions describe. It refuses a
// product past the supplied bound rather than computing one that wrapped: four
// dimensions of 65536 multiply to exactly 2^64, which is zero in the arithmetic
// that produced it and would agree with an array carrying nothing.
func (array DAArray) ElementCount(maximum int) (int, error) {
	if maximum <= 0 {
		return 0, NewAdapterError(CodeInvalidValue, "an array element bound must be positive")
	}
	count := uint64(1)
	for index, dimension := range array.Dimensions {
		if dimension.Length == 0 {
			return 0, NewAdapterError(CodeInvalidValue,
				fmt.Sprintf("array dimension %d has no length", index))
		}
		count *= uint64(dimension.Length)
		if count > uint64(maximum) {
			return 0, NewAdapterError(CodeArrayTooLarge,
				fmt.Sprintf("array of %d dimensions describes more than the %d element limit",
					len(array.Dimensions), maximum))
		}
	}
	return int(count), nil
}

// Validate holds an array to its own description and to the configured bounds.
// A shape that does not describe the elements beside it is not an array this
// adapter can publish: a client reading it would index into something other
// than what the source holds.
func (array DAArray) Validate(limits ArrayLimits) error {
	if limits.MaxDimensions <= 0 || limits.MaxElements <= 0 ||
		limits.MaxBSTRCodeUnits <= 0 || limits.MaxArrayBSTRCodeUnits <= 0 {
		return NewAdapterError(CodeInvalidValue, "array bounds must be positive")
	}
	if array.ElementType.IsArray() || array.ElementType.IsByRef() {
		return NewAdapterError(CodeUnsupportedVarType,
			fmt.Sprintf("an array element type may not itself be an array or byref: %s",
				array.ElementType))
	}
	if len(array.Dimensions) == 0 {
		return NewAdapterError(CodeInvalidValue, "an array describes no dimensions")
	}
	if len(array.Dimensions) > limits.MaxDimensions {
		return NewAdapterError(CodeArrayTooLarge,
			fmt.Sprintf("array of %d dimensions exceeds the %d dimension limit",
				len(array.Dimensions), limits.MaxDimensions))
	}
	count, err := array.ElementCount(limits.MaxElements)
	if err != nil {
		return err
	}
	if len(array.Elements) != count {
		return NewAdapterError(CodeInvalidValue,
			fmt.Sprintf("array carries %d elements but its dimensions describe %d",
				len(array.Elements), count))
	}

	bstrUnits := 0
	for index, element := range array.Elements {
		units, err := validateScalarValue(array.ElementType, element, limits.MaxBSTRCodeUnits)
		if err != nil {
			return arrayElementError(index, err)
		}
		bstrUnits += units
		if bstrUnits > limits.MaxArrayBSTRCodeUnits {
			return NewAdapterError(CodeBSTRTooLong,
				fmt.Sprintf("array string elements exceed the %d code unit limit",
					limits.MaxArrayBSTRCodeUnits))
		}
	}
	return nil
}

// arrayElementError says which element was wrong while keeping the reason the
// element check gave. A client told only that "an element is invalid" has to
// guess which of a thousand it was.
func arrayElementError(index int, err error) error {
	adapterErr, ok := AsAdapterError(err)
	if !ok {
		return err
	}
	return NewAdapterError(adapterErr.Code,
		fmt.Sprintf("array element %d: %s", index, adapterErr.Message))
}

// validateScalarValue enforces an exact Go representation for one VARTYPE and
// reports how many UTF-16 code units it carries, which is zero for everything
// but VT_BSTR. It never infers, widens, narrows, or coerces a value.
func validateScalarValue(varType DAVarType, value any, maxBSTRCodeUnits int) (int, error) {
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
			return 0, NewAdapterError(CodeInvalidValue, "VT_BSTR value must be valid UTF-8")
		}
		units := len(utf16.Encode([]rune(stringValue)))
		if units > maxBSTRCodeUnits {
			return 0, NewAdapterError(CodeBSTRTooLong, "VT_BSTR value exceeds configured limit")
		}
		return units, nil
	default:
		return 0, NewAdapterError(CodeUnsupportedVarType,
			fmt.Sprintf("unsupported VARTYPE %s", varType))
	}
	if !valid {
		return 0, NewAdapterError(CodeInvalidValue,
			fmt.Sprintf("value does not exactly match %s", varType))
	}
	return 0, nil
}
