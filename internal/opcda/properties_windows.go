//go:build windows

package opcda

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var iidIOPCItemProperties = guid{
	Data1: 0x39C13A72, Data2: 0x011E, Data3: 0x11D0,
	Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
}

type iopcItemProperties struct {
	VTable *iopcItemPropertiesVTable
}

type iopcItemPropertiesVTable struct {
	iUnknownVTable
	QueryAvailableProperties uintptr
	GetItemProperties        uintptr
	LookupItemIDs            uintptr
}

func (properties *iopcItemProperties) release() uint32 {
	if properties == nil || properties.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(properties), properties.VTable.Release)
}

// queryPropertiesInterface asks the source for IOPCItemProperties. Not
// implementing it is a capability, not a failure: E_NOINTERFACE means the
// source has no properties to offer and every property path answers so.
func queryPropertiesInterface(server *iopcServer) (*iopcItemProperties, bool, error) {
	var properties *iopcItemProperties
	result, _, _ := syscall.SyscallN(
		server.VTable.QueryInterface,
		uintptr(unsafe.Pointer(server)),
		uintptr(unsafe.Pointer(&iidIOPCItemProperties)),
		uintptr(unsafe.Pointer(&properties)),
	)
	runtime.KeepAlive(server)
	hr := hresultFromCall(result)
	if hr == ENoInterface {
		return nil, false, nil
	}
	if hr.Failed() {
		return nil, false, &SourceError{Operation: "IOPCServer::QueryInterface(IOPCItemProperties)", HRESULT: hr}
	}
	if properties == nil || properties.VTable == nil {
		return nil, false, fmt.Errorf("QueryInterface(IOPCItemProperties) returned a nil interface")
	}
	return properties, true, nil
}

// queryAvailableProperties reports which properties the source offers for one
// item. It answers what the source said, in the source's order; the adapter
// adds nothing and removes nothing.
func (session *daThreadSession) queryAvailableProperties(itemID string, limits Limits) ([]AvailableProperty, error) {
	if session.properties == nil {
		return nil, NewAdapterError(CodePropertiesUnsupported, propertiesUnsupported)
	}
	wide, err := syscall.UTF16PtrFromString(itemID)
	if err != nil {
		return nil, NewAdapterError(CodeInvalidRequest, "itemId is not valid UTF-16")
	}

	var count uint32
	var identifiers *uint32
	var descriptions **uint16
	var varTypes *uint16
	result, _, _ := syscall.SyscallN(
		session.properties.VTable.QueryAvailableProperties,
		uintptr(unsafe.Pointer(session.properties)),
		uintptr(unsafe.Pointer(wide)),
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&identifiers)),
		uintptr(unsafe.Pointer(&descriptions)),
		uintptr(unsafe.Pointer(&varTypes)),
	)
	runtime.KeepAlive(session.properties)
	runtime.KeepAlive(wide)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCItemProperties::QueryAvailableProperties", HRESULT: hr}
	}
	// Every out array is the server's to allocate and ours to free, including
	// each description string inside its array.
	defer func() {
		if descriptions != nil {
			for index := uint32(0); index < count; index++ {
				pointer := *(**uint16)(unsafe.Add(unsafe.Pointer(descriptions), uintptr(index)*unsafe.Sizeof(uintptr(0))))
				coTaskMemFree(unsafe.Pointer(pointer))
			}
		}
		coTaskMemFree(unsafe.Pointer(descriptions))
		coTaskMemFree(unsafe.Pointer(identifiers))
		coTaskMemFree(unsafe.Pointer(varTypes))
	}()
	if count == 0 {
		return nil, nil
	}
	if identifiers == nil || varTypes == nil {
		return nil, fmt.Errorf("QueryAvailableProperties reported %d properties with a nil array", count)
	}
	if int(count) > limits.MaxItemProperties {
		return nil, NewAdapterError(CodeRequestLimitExceeded, "source reported more item properties than the configured limit")
	}

	available := make([]AvailableProperty, 0, count)
	for index := uint32(0); index < count; index++ {
		property := AvailableProperty{
			ID:      PropertyID(*(*uint32)(unsafe.Add(unsafe.Pointer(identifiers), uintptr(index)*unsafe.Sizeof(uint32(0))))),
			VarType: DAVarType(*(*uint16)(unsafe.Add(unsafe.Pointer(varTypes), uintptr(index)*unsafe.Sizeof(uint16(0))))),
		}
		if descriptions != nil {
			pointer := *(**uint16)(unsafe.Add(unsafe.Pointer(descriptions), uintptr(index)*unsafe.Sizeof(uintptr(0))))
			if pointer != nil {
				description, err := decodeTaskString(pointer, limits.MaxBSTRCodeUnits)
				if err != nil {
					return nil, err
				}
				property.Description = description
			}
		}
		available = append(available, property)
	}
	return available, nil
}

// getItemProperties reads property values for one item. Results match the
// requested identifiers in size and order, and a per-property HRESULT stays a
// result rather than failing the batch.
func (session *daThreadSession) getItemProperties(request ItemPropertiesRequest, limits Limits) ([]ItemPropertyValue, error) {
	if session.properties == nil {
		return nil, NewAdapterError(CodePropertiesUnsupported, propertiesUnsupported)
	}
	wide, err := syscall.UTF16PtrFromString(request.ItemID)
	if err != nil {
		return nil, NewAdapterError(CodeInvalidRequest, "itemId is not valid UTF-16")
	}
	identifiers := make([]uint32, len(request.Properties))
	for index, property := range request.Properties {
		identifiers[index] = uint32(property)
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&identifiers[0])

	var data *variant
	var errors *HRESULT
	result, _, _ := syscall.SyscallN(
		session.properties.VTable.GetItemProperties,
		uintptr(unsafe.Pointer(session.properties)),
		uintptr(unsafe.Pointer(wide)),
		uintptr(len(identifiers)),
		uintptr(unsafe.Pointer(&identifiers[0])),
		uintptr(unsafe.Pointer(&data)),
		uintptr(unsafe.Pointer(&errors)),
	)
	runtime.KeepAlive(session.properties)
	runtime.KeepAlive(wide)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCItemProperties::GetItemProperties", HRESULT: hr}
	}
	if errors == nil {
		coTaskMemFree(unsafe.Pointer(data))
		return nil, fmt.Errorf("GetItemProperties returned a nil error array")
	}
	defer coTaskMemFree(unsafe.Pointer(errors))
	if data == nil {
		return nil, fmt.Errorf("GetItemProperties returned a nil value array")
	}

	values := make([]ItemPropertyValue, len(identifiers))
	var cleanupErr error
	for index := range identifiers {
		itemHR := *(*HRESULT)(unsafe.Add(unsafe.Pointer(errors), uintptr(index)*unsafe.Sizeof(HRESULT(0))))
		value := (*variant)(unsafe.Add(unsafe.Pointer(data), uintptr(index)*unsafe.Sizeof(variant{})))
		values[index] = ItemPropertyValue{
			ID:             request.Properties[index],
			HRESULT:        itemHR,
			HRESULTPresent: true,
			OK:             itemHR.Succeeded(),
		}
		if itemHR.Succeeded() {
			varType := DAVarType(value.VT)
			values[index].VarType = varType
			values[index].VarTypePresent = true
			decoded, decodeErr := decodeVariant(value, limits.MaxBSTRCodeUnits)
			if decodeErr != nil {
				// A property the adapter cannot represent is reported as such
				// for that property alone. The rest of the batch is unaffected,
				// and the source's own HRESULT is still carried.
				values[index].OK = false
				values[index].ErrorCode = string(CodeUnsupportedVarType)
				if adapterErr, ok := AsAdapterError(decodeErr); ok {
					values[index].ErrorCode = string(adapterErr.Code)
				}
			} else {
				values[index].Value = decoded
				values[index].ValuePresent = decoded != nil
			}
		}
		// Every returned VARIANT is cleared whether or not it decoded, and
		// whether or not its HRESULT succeeded: the server allocated it.
		if err := variantClear(value); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	coTaskMemFree(unsafe.Pointer(data))
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	return values, nil
}
