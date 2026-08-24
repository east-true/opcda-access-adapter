//go:build windows

package opcda

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// These GUIDs and vtable layouts are defined by the Windows SDK ComCat.idl
// and the OPC Foundation DA IDL. The category manager is requested with
// CLSCTX_INPROC_SERVER only; no detected vendor class is activated.
var (
	clsidStandardComponentCategoriesManager = guid{
		Data1: 0x0002E005,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidICatInformation = guid{
		Data1: 0x0002E013,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	catidOPCDAServer20 = guid{
		Data1: 0x63D5F432,
		Data2: 0xCFE4,
		Data3: 0x11D1,
		Data4: [8]byte{0xB2, 0xC8, 0x00, 0x60, 0x08, 0x3B, 0xA1, 0xFB},
	}
)

type iCatInformation struct {
	VTable *iCatInformationVTable
}

type iCatInformationVTable struct {
	iUnknownVTable
	EnumCategories                uintptr
	GetCategoryDesc               uintptr
	EnumClassesOfCategories       uintptr
	IsClassOfCategories           uintptr
	EnumImplCategoriesOfClass     uintptr
	EnumRequiredCategoriesOfClass uintptr
}

func (information *iCatInformation) release() uint32 {
	if information == nil || information.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(information), information.VTable.Release)
}

type iEnumGUID struct {
	VTable *iEnumGUIDVTable
}

type iEnumGUIDVTable struct {
	iUnknownVTable
	Next  uintptr
	Skip  uintptr
	Reset uintptr
	Clone uintptr
}

func (enumerator *iEnumGUID) release() uint32 {
	if enumerator == nil || enumerator.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(enumerator), enumerator.VTable.Release)
}

type localDetectionThreadResult struct {
	servers []DetectedLocalServer
	err     error
}

func detectLocalServers(ctx context.Context, limits LocalDetectionLimits) ([]DetectedLocalServer, error) {
	result := make(chan localDetectionThreadResult, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		initialized, err := coInitializeSTA()
		if err != nil {
			result <- localDetectionThreadResult{err: err}
			return
		}
		if initialized {
			defer coUninitialize()
		}
		servers, err := enumerateLocalDA20Registrations(ctx, limits)
		result <- localDetectionThreadResult{servers: servers, err: err}
	}()

	select {
	case <-ctx.Done():
		// COM calls already executing on the owning thread are not forcefully
		// cancelled. The buffered result lets that thread finish and clean up.
		return nil, ctx.Err()
	case detected := <-result:
		return detected.servers, detected.err
	}
}

func enumerateLocalDA20Registrations(ctx context.Context, limits LocalDetectionLimits) ([]DetectedLocalServer, error) {
	information, err := createCategoryInformation()
	if err != nil {
		return nil, err
	}
	defer information.release()

	var enumerator *iEnumGUID
	callResult, _, _ := syscall.SyscallN(
		information.VTable.EnumClassesOfCategories,
		uintptr(unsafe.Pointer(information)),
		1,
		uintptr(unsafe.Pointer(&catidOPCDAServer20)),
		0,
		0,
		uintptr(unsafe.Pointer(&enumerator)),
	)
	runtime.KeepAlive(information)
	hr := hresultFromCall(callResult)
	if hr.Failed() {
		return nil, &comCallError{Operation: "ICatInformation::EnumClassesOfCategories(OPC_DA_20)", HRESULT: hr}
	}
	if enumerator == nil {
		return nil, fmt.Errorf("ICatInformation::EnumClassesOfCategories returned a nil enumerator")
	}
	defer enumerator.release()

	servers := make([]DetectedLocalServer, 0, min(limits.MaxServers, 32))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var (
			clsid   guid
			fetched uint32
		)
		nextResult, _, _ := syscall.SyscallN(
			enumerator.VTable.Next,
			uintptr(unsafe.Pointer(enumerator)),
			1,
			uintptr(unsafe.Pointer(&clsid)),
			uintptr(unsafe.Pointer(&fetched)),
		)
		runtime.KeepAlive(enumerator)
		nextHR := hresultFromCall(nextResult)
		if nextHR.Failed() {
			return nil, &comCallError{Operation: "IEnumGUID::Next(OPC_DA_20)", HRESULT: nextHR}
		}
		if fetched == 0 {
			return servers, nil
		}
		if fetched != 1 {
			return nil, fmt.Errorf("IEnumGUID::Next returned invalid fetched count %d", fetched)
		}
		if len(servers) == limits.MaxServers {
			return nil, NewAdapterError(CodeDetectionResultLimitExceeded, "local OPC DA detection result limit exceeded")
		}
		progID, err := registeredProgID(clsid, limits.MaxProgIDCodeUnits)
		if err != nil {
			return nil, err
		}
		servers = append(servers, DetectedLocalServer{CLSID: clsid.String(), ProgID: progID})
		if nextHR == SFalse {
			return servers, nil
		}
	}
}

func createCategoryInformation() (*iCatInformation, error) {
	var information *iCatInformation
	result, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidStandardComponentCategoriesManager)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidICatInformation)),
		uintptr(unsafe.Pointer(&information)),
	)
	runtime.KeepAlive(&clsidStandardComponentCategoriesManager)
	runtime.KeepAlive(&iidICatInformation)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &comCallError{Operation: "CoCreateInstance(ICatInformation)", HRESULT: hr}
	}
	if information == nil || information.VTable == nil {
		return nil, fmt.Errorf("CoCreateInstance(ICatInformation) returned a nil interface")
	}
	return information, nil
}

func registeredProgID(clsid guid, maximumCodeUnits int) (string, error) {
	var pointer *uint16
	result, _, _ := procProgIDFromCLSID.Call(
		uintptr(unsafe.Pointer(&clsid)),
		uintptr(unsafe.Pointer(&pointer)),
	)
	runtime.KeepAlive(&clsid)
	hr := hresultFromCall(result)
	if hr.Failed() {
		// A stale or incomplete component-category entry remains useful by
		// exact CLSID. Do not infer or synthesize a ProgID.
		return "", nil
	}
	if pointer == nil {
		return "", fmt.Errorf("ProgIDFromCLSID returned a nil ProgID")
	}
	defer coTaskMemFree(unsafe.Pointer(pointer))
	return decodeTaskString(pointer, maximumCodeUnits)
}
