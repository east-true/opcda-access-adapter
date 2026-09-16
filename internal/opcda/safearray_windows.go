//go:build windows

package opcda

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"unicode/utf16"
	"unsafe"
)

// safeArrayBound is SAFEARRAYBOUND: a length and the index the dimension
// starts at.
type safeArrayBound struct {
	Elements   uint32
	LowerBound int32
}

// decodeSafeArray reads a SAFEARRAY into the shape ADR-0019 carries.
//
// Elements are read with SafeArrayGetElement and an explicit index vector
// rather than by walking the data SafeArrayAccessData hands back. ADR-0019
// decision 2 records the reason: a SAFEARRAY's in-memory element order is easy
// to state backwards, and a decoder that walks raw memory bakes whichever way
// it was written into every value it ever returns -- silently, and identically
// for every test that compares the adapter against itself. An index vector
// cannot be wrong about an order it never assumes.
func decodeSafeArray(array uintptr, declared DAVarType, limits ArrayLimits) (DAArray, error) {
	if array == 0 {
		return DAArray{}, NewAdapterError(CodeInvalidValue, "source array VARIANT carries no array")
	}
	elementType := declared.Base()
	if elementType == VTVariant {
		// A SAFEARRAY of VARIANTs is a heterogeneous array. Carrying one would
		// mean an element type per element, which is not the shape §20.4
		// describes, so it is refused rather than flattened to one type.
		return DAArray{}, NewAdapterError(CodeUnsupportedVarType,
			"an array of VARIANTs has no single element VARTYPE")
	}

	dimensions, err := safeArrayDimensions(array, limits)
	if err != nil {
		return DAArray{}, err
	}
	decoded := DAArray{ElementType: elementType, Dimensions: dimensions}
	count, err := decoded.ElementCount(limits.MaxElements)
	if err != nil {
		return DAArray{}, err
	}

	decoded.Elements = make([]any, 0, count)
	indices := make([]int32, len(dimensions))
	for index := range indices {
		indices[index] = dimensions[index].LowerBound
	}
	bstrUnits := 0
	for read := 0; read < count; read++ {
		element, units, elementErr := readSafeArrayElement(array, indices, elementType, limits)
		if elementErr != nil {
			return DAArray{}, arrayElementError(read, elementErr)
		}
		bstrUnits += units
		if bstrUnits > limits.MaxArrayBSTRCodeUnits {
			return DAArray{}, NewAdapterError(CodeBSTRTooLong,
				fmt.Sprintf("source array string elements exceed the %d code unit limit",
					limits.MaxArrayBSTRCodeUnits))
		}
		decoded.Elements = append(decoded.Elements, element)
		// The last dimension varies fastest, which is the order ADR-0019
		// decision 3 publishes. Carrying it means a reader never has to know
		// what a SAFEARRAY is.
		advanceArrayIndices(indices, dimensions)
	}
	return decoded, nil
}

// advanceArrayIndices steps to the next element in the published order.
func advanceArrayIndices(indices []int32, dimensions []DADimension) {
	for position := len(indices) - 1; position >= 0; position-- {
		indices[position]++
		if indices[position] <= dimensions[position].LowerBound+int32(dimensions[position].Length)-1 {
			return
		}
		indices[position] = dimensions[position].LowerBound
	}
}

func safeArrayDimensions(array uintptr, limits ArrayLimits) ([]DADimension, error) {
	count, _, _ := procSafeArrayGetDim.Call(array)
	if count == 0 {
		return nil, NewAdapterError(CodeInvalidValue, "source array describes no dimensions")
	}
	if int(count) > limits.MaxDimensions {
		return nil, NewAdapterError(CodeArrayTooLarge,
			fmt.Sprintf("source array of %d dimensions exceeds the %d dimension limit",
				count, limits.MaxDimensions))
	}
	dimensions := make([]DADimension, 0, count)
	for dimension := uintptr(1); dimension <= count; dimension++ {
		var lower, upper int32
		result, _, _ := procSafeArrayGetLBound.Call(
			array, dimension, uintptr(unsafe.Pointer(&lower)))
		if hr := hresultFromCall(result); hr.Failed() {
			return nil, &SourceError{Operation: "SafeArrayGetLBound", HRESULT: hr}
		}
		result, _, _ = procSafeArrayGetUBound.Call(
			array, dimension, uintptr(unsafe.Pointer(&upper)))
		if hr := hresultFromCall(result); hr.Failed() {
			return nil, &SourceError{Operation: "SafeArrayGetUBound", HRESULT: hr}
		}
		if upper < lower {
			// COM expresses an empty dimension this way. It is not a shape this
			// adapter can publish, because no index addresses anything in it.
			return nil, NewAdapterError(CodeInvalidValue,
				fmt.Sprintf("source array dimension %d is empty", dimension))
		}
		length := int64(upper) - int64(lower) + 1
		if length > int64(limits.MaxElements) {
			return nil, NewAdapterError(CodeArrayTooLarge,
				fmt.Sprintf("source array dimension %d holds %d elements, past the %d limit",
					dimension, length, limits.MaxElements))
		}
		dimensions = append(dimensions, DADimension{LowerBound: lower, Length: uint32(length)})
	}
	return dimensions, nil
}

// readSafeArrayElement reads one element and reports the UTF-16 code units it
// carried, which is zero for everything but VT_BSTR.
func readSafeArrayElement(array uintptr, indices []int32, elementType DAVarType,
	limits ArrayLimits) (any, int, error) {
	// Wide enough for every element type this adapter carries, so one buffer
	// serves them all and the size never has to be derived from the type.
	var storage [16]byte
	result, _, _ := procSafeArrayGetElement.Call(
		array,
		uintptr(unsafe.Pointer(&indices[0])),
		uintptr(unsafe.Pointer(&storage[0])),
	)
	runtime.KeepAlive(indices)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, 0, &SourceError{Operation: "SafeArrayGetElement", HRESULT: hr}
	}

	data := storage[:]
	switch elementType {
	case VTI1:
		return int8(data[0]), 0, nil
	case VTUI1:
		return data[0], 0, nil
	case VTI2:
		return int16(binary.LittleEndian.Uint16(data)), 0, nil
	case VTUI2:
		return binary.LittleEndian.Uint16(data), 0, nil
	case VTI4, VTInt, VTError:
		return int32(binary.LittleEndian.Uint32(data)), 0, nil
	case VTUI4, VTUInt:
		return binary.LittleEndian.Uint32(data), 0, nil
	case VTI8:
		return int64(binary.LittleEndian.Uint64(data)), 0, nil
	case VTUI8:
		return binary.LittleEndian.Uint64(data), 0, nil
	case VTR4:
		return math.Float32frombits(binary.LittleEndian.Uint32(data)), 0, nil
	case VTR8:
		return math.Float64frombits(binary.LittleEndian.Uint64(data)), 0, nil
	case VTBool:
		return int16(binary.LittleEndian.Uint16(data)) != 0, 0, nil
	case VTBSTR:
		// SafeArrayGetElement allocates a copy of a BSTR element, and this is
		// its only owner. Freeing it here rather than at the end of the array
		// keeps one string's worth of memory in flight instead of the array's.
		bstr := variantDataPointer(data)
		defer freeBSTR(uintptr(bstr))
		text, err := decodeBSTR(bstr, limits.MaxBSTRCodeUnits)
		if err != nil {
			return nil, 0, err
		}
		return text, len(utf16.Encode([]rune(text))), nil
	default:
		return nil, 0, NewAdapterError(CodeUnsupportedVarType,
			fmt.Sprintf("unsupported array element VARTYPE %s", elementType))
	}
}

// encodeSafeArray builds a SAFEARRAY from a validated DAArray. The caller owns
// the result and destroys it through the VARIANT that carries it.
func encodeSafeArray(array DAArray) (uintptr, error) {
	bounds := make([]safeArrayBound, len(array.Dimensions))
	for index, dimension := range array.Dimensions {
		bounds[index] = safeArrayBound{
			Elements:   dimension.Length,
			LowerBound: dimension.LowerBound,
		}
	}
	created, _, _ := procSafeArrayCreate.Call(
		uintptr(array.ElementType),
		uintptr(len(bounds)),
		uintptr(unsafe.Pointer(&bounds[0])),
	)
	runtime.KeepAlive(bounds)
	if created == 0 {
		return 0, fmt.Errorf("SafeArrayCreate returned no array")
	}

	indices := make([]int32, len(array.Dimensions))
	for index := range indices {
		indices[index] = array.Dimensions[index].LowerBound
	}
	for _, element := range array.Elements {
		if err := putSafeArrayElement(created, indices, array.ElementType, element); err != nil {
			destroySafeArray(created)
			return 0, err
		}
		advanceArrayIndices(indices, array.Dimensions)
	}
	return created, nil
}

func putSafeArrayElement(array uintptr, indices []int32, elementType DAVarType, element any) error {
	var storage [16]byte
	data := storage[:]
	var allocated uintptr
	switch elementType {
	case VTI1:
		data[0] = byte(element.(int8))
	case VTUI1:
		data[0] = element.(uint8)
	case VTI2:
		binary.LittleEndian.PutUint16(data, uint16(element.(int16)))
	case VTUI2:
		binary.LittleEndian.PutUint16(data, element.(uint16))
	case VTI4, VTInt, VTError:
		binary.LittleEndian.PutUint32(data, uint32(element.(int32)))
	case VTUI4, VTUInt:
		binary.LittleEndian.PutUint32(data, element.(uint32))
	case VTI8:
		binary.LittleEndian.PutUint64(data, uint64(element.(int64)))
	case VTUI8:
		binary.LittleEndian.PutUint64(data, element.(uint64))
	case VTR4:
		binary.LittleEndian.PutUint32(data, math.Float32bits(element.(float32)))
	case VTR8:
		binary.LittleEndian.PutUint64(data, math.Float64bits(element.(float64)))
	case VTBool:
		if element.(bool) {
			binary.LittleEndian.PutUint16(data, 0xFFFF)
		}
	case VTBSTR:
		units := utf16.Encode([]rune(element.(string)))
		bstr, err := allocateBSTR(units)
		if err != nil {
			return err
		}
		// SafeArrayPutElement copies a BSTR rather than taking it, so this one
		// is freed whether the call succeeds or not.
		allocated = bstr
		putVariantPointer(data, bstr)
	default:
		return NewAdapterError(CodeUnsupportedVarType,
			fmt.Sprintf("unsupported array element VARTYPE %s", elementType))
	}
	result, _, _ := procSafeArrayPutElement.Call(
		array,
		uintptr(unsafe.Pointer(&indices[0])),
		uintptr(unsafe.Pointer(&storage[0])),
	)
	runtime.KeepAlive(indices)
	if allocated != 0 {
		freeBSTR(allocated)
	}
	if hr := hresultFromCall(result); hr.Failed() {
		return &SourceError{Operation: "SafeArrayPutElement", HRESULT: hr}
	}
	return nil
}

func destroySafeArray(array uintptr) {
	if array == 0 {
		return
	}
	_, _, _ = procSafeArrayDestroy.Call(array)
}

func freeBSTR(bstr uintptr) {
	if bstr == 0 {
		return
	}
	_, _, _ = procSysFreeString.Call(bstr)
}
