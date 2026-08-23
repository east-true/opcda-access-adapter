//go:build windows

package opcda

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

func (session *daThreadSession) writeValues(items []WriteItem, maxBSTRCodeUnits int) (resultsOut []WriteResult, errOut error) {
	itemIDs := make([]DAItemID, len(items))
	for index := range items {
		itemIDs[index] = items[index].ItemID
	}
	registrations, registered, registrationResults, err := session.resolveRegistrations(itemIDs)
	if err != nil {
		return nil, err
	}
	results := make([]WriteResult, len(items))
	for index := range items {
		results[index] = WriteResult{
			ItemID:         items[index].ItemID,
			HRESULT:        registrationResults[index].HRESULT,
			HRESULTPresent: registrationResults[index].HRESULTPresent,
			ErrorCode:      registrationResults[index].ErrorCode,
		}
	}

	handles := make([]uint32, 0, len(items))
	positions := make([]int, 0, len(items))
	values := make([]variant, 0, len(items))
	for index := range items {
		if !registered[index] {
			continue
		}
		if registrations[index].CanonicalType != items[index].VarType {
			results[index].ErrorCode = string(CodeTypeMismatch)
			continue
		}
		encoded, encodeErr := encodeWriteVariant(items[index].VarType, items[index].Value, maxBSTRCodeUnits)
		if encodeErr != nil {
			if clearErr := clearWriteVariants(values); clearErr != nil {
				return nil, fmt.Errorf("encode Write value: %v; clear prior values: %w", encodeErr, clearErr)
			}
			return nil, encodeErr
		}
		handles = append(handles, registrations[index].ServerHandle)
		positions = append(positions, index)
		values = append(values, encoded)
	}
	if len(handles) == 0 {
		return results, nil
	}
	defer func() {
		if clearErr := clearWriteVariants(values); clearErr != nil && errOut == nil {
			resultsOut = nil
			errOut = fmt.Errorf("clear IOPCSyncIO::Write values: %w", clearErr)
		}
	}()

	var errorPointer unsafe.Pointer
	methodResult, _, _ := syscall.SyscallN(
		session.syncIO.VTable.Write,
		uintptr(unsafe.Pointer(session.syncIO)),
		uintptr(len(handles)),
		uintptr(unsafe.Pointer(&handles[0])),
		uintptr(unsafe.Pointer(&values[0])),
		uintptr(unsafe.Pointer(&errorPointer)),
	)
	runtime.KeepAlive(session.syncIO)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(values)
	defer coTaskMemFree(errorPointer)
	if hr := hresultFromCall(methodResult); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCSyncIO::Write", HRESULT: hr}
	}
	if errorPointer == nil {
		return nil, fmt.Errorf("IOPCSyncIO::Write returned a nil error array")
	}

	itemErrors := unsafe.Slice((*int32)(errorPointer), len(handles))
	for writeIndex, resultIndex := range positions {
		results[resultIndex].HRESULT = HRESULT(itemErrors[writeIndex])
		results[resultIndex].HRESULTPresent = true
	}
	return results, nil
}

func encodeWriteVariant(varType DAVarType, value any, maxBSTRCodeUnits int) (variant, error) {
	if err := validateWriteValue(varType, value, maxBSTRCodeUnits); err != nil {
		return variant{}, err
	}
	encoded := variant{VT: uint16(varType)}
	switch varType.Base() {
	case VTEmpty, VTNull:
	case VTI1:
		encoded.Data[0] = byte(value.(int8))
	case VTUI1:
		encoded.Data[0] = value.(uint8)
	case VTI2:
		binary.LittleEndian.PutUint16(encoded.Data[:], uint16(value.(int16)))
	case VTUI2:
		binary.LittleEndian.PutUint16(encoded.Data[:], value.(uint16))
	case VTI4, VTInt, VTError:
		binary.LittleEndian.PutUint32(encoded.Data[:], uint32(value.(int32)))
	case VTUI4, VTUInt:
		binary.LittleEndian.PutUint32(encoded.Data[:], value.(uint32))
	case VTI8:
		binary.LittleEndian.PutUint64(encoded.Data[:], uint64(value.(int64)))
	case VTUI8:
		binary.LittleEndian.PutUint64(encoded.Data[:], value.(uint64))
	case VTR4:
		binary.LittleEndian.PutUint32(encoded.Data[:], math.Float32bits(value.(float32)))
	case VTR8:
		binary.LittleEndian.PutUint64(encoded.Data[:], math.Float64bits(value.(float64)))
	case VTBool:
		if value.(bool) {
			binary.LittleEndian.PutUint16(encoded.Data[:], 0xFFFF)
		}
	case VTBSTR:
		units := utf16.Encode([]rune(value.(string)))
		pointer, err := allocateBSTR(units)
		if err != nil {
			return variant{}, err
		}
		putVariantPointer(encoded.Data[:], pointer)
	default:
		return variant{}, NewAdapterError(CodeUnsupportedVarType, fmt.Sprintf("unsupported Write VARTYPE %s", varType))
	}
	return encoded, nil
}

func putVariantPointer(data []byte, pointer uintptr) {
	if unsafe.Sizeof(pointer) == 8 {
		binary.LittleEndian.PutUint64(data, uint64(pointer))
	} else {
		binary.LittleEndian.PutUint32(data, uint32(pointer))
	}
}

func clearWriteVariants(values []variant) error {
	var firstErr error
	for index := range values {
		if err := variantClear(&values[index]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
