//go:build windows

package opcda

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	opcDataSourceCache  = 1
	opcDataSourceDevice = 2
	opcAccessRightRead  = 1
	opcAccessRightWrite = 2
)

var (
	iidIOPCItemMgt = guid{
		Data1: 0x39C13A54, Data2: 0x011E, Data3: 0x11D0,
		Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
	iidIOPCSyncIO = guid{
		Data1: 0x39C13A52, Data2: 0x011E, Data3: 0x11D0,
		Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
)

type iopcServer struct {
	VTable *iopcServerVTable
}

type iopcServerVTable struct {
	iUnknownVTable
	AddGroup              uintptr
	GetErrorString        uintptr
	GetGroupByName        uintptr
	GetStatus             uintptr
	RemoveGroup           uintptr
	CreateGroupEnumerator uintptr
}

func (server *iopcServer) release() uint32 {
	if server == nil || server.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(server), server.VTable.Release)
}

type iopcItemMgt struct {
	VTable *iopcItemMgtVTable
}

type iopcItemMgtVTable struct {
	iUnknownVTable
	AddItems         uintptr
	ValidateItems    uintptr
	RemoveItems      uintptr
	SetActiveState   uintptr
	SetClientHandles uintptr
	SetDatatypes     uintptr
	CreateEnumerator uintptr
}

func (items *iopcItemMgt) release() uint32 {
	if items == nil || items.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(items), items.VTable.Release)
}

type iopcSyncIO struct {
	VTable *iopcSyncIOVTable
}

type iopcSyncIOVTable struct {
	iUnknownVTable
	Read  uintptr
	Write uintptr
}

func (syncIO *iopcSyncIO) release() uint32 {
	if syncIO == nil || syncIO.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(syncIO), syncIO.VTable.Release)
}

type opcItemDef struct {
	AccessPath        *uint16
	ItemID            *uint16
	Active            int32
	ClientHandle      uint32
	BlobSize          uint32
	Blob              unsafe.Pointer
	RequestedDataType uint16
	Reserved          uint16
}

type opcItemResult struct {
	ServerHandle      uint32
	CanonicalDataType uint16
	Reserved          uint16
	AccessRights      uint32
	BlobSize          uint32
	Blob              unsafe.Pointer
}

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

type opcItemState struct {
	ClientHandle uint32
	Timestamp    filetime
	Quality      uint16
	Reserved     uint16
	Value        variant
}

func addDAGroup(server *iopcServer) (uint32, *iopcItemMgt, error) {
	groupName, err := syscall.UTF16PtrFromString("opcda-access-adapter-v0")
	if err != nil {
		return 0, nil, err
	}
	var (
		serverHandle uint32
		revisedRate  uint32
		items        *iopcItemMgt
	)
	result, _, _ := syscall.SyscallN(
		server.VTable.AddGroup,
		uintptr(unsafe.Pointer(server)),
		uintptr(unsafe.Pointer(groupName)),
		0,
		1000,
		1,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&serverHandle)),
		uintptr(unsafe.Pointer(&revisedRate)),
		uintptr(unsafe.Pointer(&iidIOPCItemMgt)),
		uintptr(unsafe.Pointer(&items)),
	)
	runtime.KeepAlive(server)
	runtime.KeepAlive(groupName)
	if hr := hresultFromCall(result); hr.Failed() {
		return 0, nil, &SourceError{Operation: "IOPCServer::AddGroup", HRESULT: hr}
	}
	if items == nil {
		return 0, nil, fmt.Errorf("IOPCServer::AddGroup returned a nil IOPCItemMgt")
	}
	return serverHandle, items, nil
}

func removeDAGroup(server *iopcServer, serverHandle uint32) error {
	if server == nil {
		return nil
	}
	result, _, _ := syscall.SyscallN(
		server.VTable.RemoveGroup,
		uintptr(unsafe.Pointer(server)),
		uintptr(serverHandle),
		0,
	)
	runtime.KeepAlive(server)
	if hr := hresultFromCall(result); hr.Failed() {
		return &SourceError{Operation: "IOPCServer::RemoveGroup", HRESULT: hr}
	}
	return nil
}

func querySyncIO(items *iopcItemMgt) (*iopcSyncIO, error) {
	var syncIO *iopcSyncIO
	result, _, _ := syscall.SyscallN(
		items.VTable.QueryInterface,
		uintptr(unsafe.Pointer(items)),
		uintptr(unsafe.Pointer(&iidIOPCSyncIO)),
		uintptr(unsafe.Pointer(&syncIO)),
	)
	runtime.KeepAlive(items)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCItemMgt::QueryInterface(IOPCSyncIO)", HRESULT: hr}
	}
	if syncIO == nil {
		return nil, fmt.Errorf("QueryInterface(IOPCSyncIO) returned a nil interface")
	}
	return syncIO, nil
}

type registrationAttempt struct {
	registration itemRegistration
	ok           bool
	hresult      HRESULT
	hresultSet   bool
	errorCode    string
}

func (session *daThreadSession) resolveRegistrations(itemIDs []DAItemID) ([]itemRegistration, []bool, []ReadResult, error) {
	registrations := make([]itemRegistration, len(itemIDs))
	registered := make([]bool, len(itemIDs))
	results := make([]ReadResult, len(itemIDs))
	pendingIndices := make(map[DAItemID][]int)
	pendingOrder := make([]DAItemID, 0, len(itemIDs))

	for index, itemID := range itemIDs {
		results[index].ItemID = itemID
		if registration, ok := session.registrations.get(itemID); ok {
			registrations[index] = registration
			registered[index] = true
			continue
		}
		if _, exists := pendingIndices[itemID]; !exists {
			pendingOrder = append(pendingOrder, itemID)
		}
		pendingIndices[itemID] = append(pendingIndices[itemID], index)
	}

	available := session.registrations.remaining()
	if len(pendingOrder) > available {
		for _, itemID := range pendingOrder[available:] {
			for _, index := range pendingIndices[itemID] {
				results[index].ErrorCode = string(CodeRegisteredItemLimit)
			}
			delete(pendingIndices, itemID)
		}
		pendingOrder = pendingOrder[:available]
	}
	if len(pendingOrder) == 0 {
		return registrations, registered, results, nil
	}

	attempts, err := session.addItems(pendingOrder)
	if err != nil {
		return nil, nil, nil, err
	}
	for itemIndex, itemID := range pendingOrder {
		attempt := attempts[itemIndex]
		for _, resultIndex := range pendingIndices[itemID] {
			results[resultIndex].HRESULT = attempt.hresult
			results[resultIndex].HRESULTPresent = attempt.hresultSet
			results[resultIndex].ErrorCode = attempt.errorCode
			if attempt.ok {
				registrations[resultIndex] = attempt.registration
				registered[resultIndex] = true
			}
		}
	}
	return registrations, registered, results, nil
}

func (session *daThreadSession) addItems(itemIDs []DAItemID) ([]registrationAttempt, error) {
	definitions := make([]opcItemDef, len(itemIDs))
	wideItemIDs := make([][]uint16, len(itemIDs))
	for index, itemID := range itemIDs {
		wide, err := syscall.UTF16FromString(string(itemID))
		if err != nil {
			return nil, fmt.Errorf("item ID contains an embedded NUL")
		}
		wideItemIDs[index] = wide
		session.nextClientHandle++
		definitions[index] = opcItemDef{
			ItemID:            &wideItemIDs[index][0],
			ClientHandle:      session.nextClientHandle,
			RequestedDataType: uint16(VTEmpty),
		}
	}

	var resultPointer, errorPointer unsafe.Pointer
	result, _, _ := syscall.SyscallN(
		session.itemMgt.VTable.AddItems,
		uintptr(unsafe.Pointer(session.itemMgt)),
		uintptr(len(definitions)),
		uintptr(unsafe.Pointer(&definitions[0])),
		uintptr(unsafe.Pointer(&resultPointer)),
		uintptr(unsafe.Pointer(&errorPointer)),
	)
	runtime.KeepAlive(session.itemMgt)
	runtime.KeepAlive(definitions)
	runtime.KeepAlive(wideItemIDs)
	defer coTaskMemFree(resultPointer)
	defer coTaskMemFree(errorPointer)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCItemMgt::AddItems", HRESULT: hr}
	}
	if resultPointer == nil || errorPointer == nil {
		return nil, fmt.Errorf("IOPCItemMgt::AddItems returned nil result arrays")
	}

	addResults := unsafe.Slice((*opcItemResult)(resultPointer), len(itemIDs))
	itemErrors := unsafe.Slice((*int32)(errorPointer), len(itemIDs))
	attempts := make([]registrationAttempt, len(itemIDs))
	for index, itemID := range itemIDs {
		if addResults[index].Blob != nil {
			coTaskMemFree(addResults[index].Blob)
		}
		itemHR := HRESULT(itemErrors[index])
		attempts[index] = registrationAttempt{hresult: itemHR, hresultSet: true}
		if itemHR.Failed() {
			continue
		}
		rights := DAAccessRights{
			Raw:   addResults[index].AccessRights,
			Read:  addResults[index].AccessRights&opcAccessRightRead != 0,
			Write: addResults[index].AccessRights&opcAccessRightWrite != 0,
		}
		registration := itemRegistration{
			ItemID:        itemID,
			ServerHandle:  addResults[index].ServerHandle,
			CanonicalType: DAVarType(addResults[index].CanonicalDataType),
			AccessRights:  rights,
			Generation:    session.generation,
		}
		if !session.registrations.put(registration) {
			attempts[index].errorCode = string(CodeRegisteredItemLimit)
			continue
		}
		attempts[index].registration = registration
		attempts[index].ok = true
	}
	return attempts, nil
}

func (session *daThreadSession) readDevice(itemIDs []DAItemID, maxBSTRCodeUnits int) ([]ReadResult, error) {
	registrations, registered, results, err := session.resolveRegistrations(itemIDs)
	if err != nil {
		return nil, err
	}
	handles := make([]uint32, 0, len(itemIDs))
	positions := make([]int, 0, len(itemIDs))
	for index := range itemIDs {
		if registered[index] {
			handles = append(handles, registrations[index].ServerHandle)
			positions = append(positions, index)
		}
	}
	if len(handles) == 0 {
		return results, nil
	}

	var statePointer, errorPointer unsafe.Pointer
	methodResult, _, _ := syscall.SyscallN(
		session.syncIO.VTable.Read,
		uintptr(unsafe.Pointer(session.syncIO)),
		opcDataSourceDevice,
		uintptr(len(handles)),
		uintptr(unsafe.Pointer(&handles[0])),
		uintptr(unsafe.Pointer(&statePointer)),
		uintptr(unsafe.Pointer(&errorPointer)),
	)
	runtime.KeepAlive(session.syncIO)
	runtime.KeepAlive(handles)
	defer coTaskMemFree(statePointer)
	defer coTaskMemFree(errorPointer)
	if hr := hresultFromCall(methodResult); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCSyncIO::Read", HRESULT: hr}
	}
	if statePointer == nil || errorPointer == nil {
		return nil, fmt.Errorf("IOPCSyncIO::Read returned nil result arrays")
	}

	states := unsafe.Slice((*opcItemState)(statePointer), len(handles))
	itemErrors := unsafe.Slice((*int32)(errorPointer), len(handles))
	var cleanupErr error
	for readIndex, resultIndex := range positions {
		itemHR := HRESULT(itemErrors[readIndex])
		results[resultIndex].HRESULT = itemHR
		results[resultIndex].HRESULTPresent = true
		valueType := DAVarType(states[readIndex].Value.VT)
		canonicalType := registrations[resultIndex].CanonicalType
		rights := registrations[resultIndex].AccessRights
		results[resultIndex].VarType = &valueType
		results[resultIndex].CanonicalType = &canonicalType
		results[resultIndex].AccessRights = &rights
		if itemHR.Succeeded() {
			value, decodeErr := decodeVariant(&states[readIndex].Value, maxBSTRCodeUnits)
			if decodeErr != nil {
				if adapterErr, ok := AsAdapterError(decodeErr); ok {
					results[resultIndex].ErrorCode = string(adapterErr.Code)
				} else {
					results[resultIndex].ErrorCode = string(CodeInvalidValue)
				}
			} else {
				timestamp, present := states[readIndex].Timestamp.toTime()
				results[resultIndex].Value = &DAValue{
					ItemID:           itemIDs[resultIndex],
					VarType:          valueType,
					Value:            value,
					QualityRaw:       states[readIndex].Quality,
					Timestamp:        timestamp,
					TimestampPresent: present,
					HRESULT:          itemHR,
					AccessRights:     &rights,
				}
			}
		}
		if err := variantClear(&states[readIndex].Value); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	if cleanupErr != nil {
		return nil, fmt.Errorf("clear IOPCSyncIO::Read values: %w", cleanupErr)
	}
	return results, nil
}

func decodeVariant(value *variant, maxBSTRCodeUnits int) (any, error) {
	varType := DAVarType(value.VT)
	if varType.IsArray() || varType.IsByRef() {
		return nil, NewAdapterError(CodeUnsupportedVarType, "array and byref VARIANT values are unsupported")
	}
	data := value.Data[:]
	switch varType.Base() {
	case VTEmpty, VTNull:
		return nil, nil
	case VTI1:
		return int8(data[0]), nil
	case VTUI1:
		return data[0], nil
	case VTI2:
		return int16(binary.LittleEndian.Uint16(data)), nil
	case VTUI2:
		return binary.LittleEndian.Uint16(data), nil
	case VTI4, VTInt, VTError:
		return int32(binary.LittleEndian.Uint32(data)), nil
	case VTUI4, VTUInt:
		return binary.LittleEndian.Uint32(data), nil
	case VTI8:
		return int64(binary.LittleEndian.Uint64(data)), nil
	case VTUI8:
		return binary.LittleEndian.Uint64(data), nil
	case VTR4:
		return math.Float32frombits(binary.LittleEndian.Uint32(data)), nil
	case VTR8:
		return math.Float64frombits(binary.LittleEndian.Uint64(data)), nil
	case VTBool:
		return int16(binary.LittleEndian.Uint16(data)) != 0, nil
	case VTBSTR:
		return decodeBSTR(variantDataPointer(data), maxBSTRCodeUnits)
	default:
		return nil, NewAdapterError(CodeUnsupportedVarType, fmt.Sprintf("unsupported VARTYPE %s", varType))
	}
}

func variantDataPointer(data []byte) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&data[0]))
}

func decodeBSTR(pointer unsafe.Pointer, maxCodeUnits int) (string, error) {
	if pointer == nil {
		return "", nil
	}
	length := bstrLength(pointer)
	if uint64(length) > uint64(maxCodeUnits) {
		return "", NewAdapterError(CodeBSTRTooLong, "source BSTR exceeds configured limit")
	}
	units := unsafe.Slice((*uint16)(pointer), int(length))
	if !wellFormedUTF16(units) {
		return "", NewAdapterError(CodeInvalidValue, "source BSTR contains invalid UTF-16")
	}
	return string(utf16.Decode(units)), nil
}

func wellFormedUTF16(units []uint16) bool {
	for index := 0; index < len(units); index++ {
		switch {
		case 0xD800 <= units[index] && units[index] <= 0xDBFF:
			if index+1 >= len(units) || units[index+1] < 0xDC00 || units[index+1] > 0xDFFF {
				return false
			}
			index++
		case 0xDC00 <= units[index] && units[index] <= 0xDFFF:
			return false
		}
	}
	return true
}

func (value filetime) toTime() (time.Time, bool) {
	ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	if ticks == 0 {
		return time.Time{}, false
	}
	const ticksPerSecond = uint64(10_000_000)
	const windowsToUnixSeconds = int64(11_644_473_600)
	seconds := int64(ticks/ticksPerSecond) - windowsToUnixSeconds
	nanoseconds := int64(ticks%ticksPerSecond) * 100
	return time.Unix(seconds, nanoseconds).UTC(), true
}
