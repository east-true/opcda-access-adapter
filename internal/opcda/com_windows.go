//go:build windows

package opcda

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// These declarations follow the Microsoft Win32 API and the OPC Foundation
// opcda.idl definitions. COM pointers are owned by the locked DA OS thread.
const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	clsctxInprocServer      = 0x1
	clsctxLocalServer       = 0x4
	infiniteWait            = 0xFFFFFFFF
	qsAllInput              = 0x04FF
	mwmoInputAvailable      = 0x0004
	pmRemove                = 0x0001
	waitObject0             = 0
	waitFailed              = 0xFFFFFFFF
)

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	oleaut32 = syscall.NewLazyDLL("oleaut32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCoInitializeEx    = ole32.NewProc("CoInitializeEx")
	procCoUninitialize    = ole32.NewProc("CoUninitialize")
	procCLSIDFromProgID   = ole32.NewProc("CLSIDFromProgID")
	procCLSIDFromString   = ole32.NewProc("CLSIDFromString")
	procCoCreateInstance  = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree     = ole32.NewProc("CoTaskMemFree")
	procVariantClear      = oleaut32.NewProc("VariantClear")
	procSysAllocStringLen = oleaut32.NewProc("SysAllocStringLen")
	procSysStringLen      = oleaut32.NewProc("SysStringLen")

	procCreateEventW = kernel32.NewProc("CreateEventW")
	procSetEvent     = kernel32.NewProc("SetEvent")
	procResetEvent   = kernel32.NewProc("ResetEvent")
	procCloseHandle  = kernel32.NewProc("CloseHandle")

	procMsgWaitForMultipleObjectsEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procPeekMessageW                = user32.NewProc("PeekMessageW")
	procTranslateMessage            = user32.NewProc("TranslateMessage")
	procDispatchMessageW            = user32.NewProc("DispatchMessageW")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// IID_IOPCServer is defined by the OPC Foundation DA IDL.
var iidIOPCServer = guid{
	Data1: 0x39C13A4D,
	Data2: 0x011E,
	Data3: 0x11D0,
	Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
}

func (g guid) String() string {
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3],
		g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

type iUnknownVTable struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

func releaseCOM(object unsafe.Pointer, release uintptr) uint32 {
	if object == nil || release == 0 {
		return 0
	}
	result, _, _ := syscall.SyscallN(
		release,
		uintptr(object),
	)
	runtime.KeepAlive(object)
	return uint32(result)
}

type comCallError struct {
	Operation string
	HRESULT   HRESULT
}

func (e *comCallError) Error() string {
	return fmt.Sprintf("%s failed: %s", e.Operation, e.HRESULT.Hex())
}

func hresultFromCall(result uintptr) HRESULT {
	return HRESULT(int32(uint32(result)))
}

func coInitializeSTA() (bool, error) {
	result, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	hr := hresultFromCall(result)
	if hr.Failed() {
		return false, &comCallError{Operation: "CoInitializeEx", HRESULT: hr}
	}
	// S_OK and S_FALSE both require a balancing CoUninitialize call.
	return true, nil
}

func coUninitialize() {
	procCoUninitialize.Call()
}

func resolveSourceCLSID(source SourceConfig) (guid, error) {
	if source.ProgID != "" && source.CLSID != "" {
		return guid{}, fmt.Errorf("configure exactly one source ProgID or CLSID")
	}
	var (
		value string
		proc  *syscall.LazyProc
		name  string
	)
	if source.ProgID != "" {
		value, proc, name = source.ProgID, procCLSIDFromProgID, "CLSIDFromProgID"
	} else if source.CLSID != "" {
		value, proc, name = source.CLSID, procCLSIDFromString, "CLSIDFromString"
	} else {
		return guid{}, fmt.Errorf("source is not configured")
	}

	wide, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return guid{}, fmt.Errorf("%s contains an embedded NUL", name)
	}
	var clsid guid
	result, _, _ := proc.Call(
		uintptr(unsafe.Pointer(wide)),
		uintptr(unsafe.Pointer(&clsid)),
	)
	runtime.KeepAlive(wide)
	if hr := hresultFromCall(result); hr.Failed() {
		return guid{}, &comCallError{Operation: name, HRESULT: hr}
	}
	return clsid, nil
}

func coCreateOPCServer(clsid *guid) (*iopcServer, error) {
	var server *iopcServer
	result, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)),
		0,
		clsctxInprocServer|clsctxLocalServer,
		uintptr(unsafe.Pointer(&iidIOPCServer)),
		uintptr(unsafe.Pointer(&server)),
	)
	runtime.KeepAlive(clsid)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &comCallError{Operation: "CoCreateInstance(IOPCServer)", HRESULT: hr}
	}
	if server == nil {
		return nil, fmt.Errorf("CoCreateInstance(IOPCServer) returned a nil interface")
	}
	return server, nil
}

func coTaskMemFree(pointer unsafe.Pointer) {
	if pointer != nil {
		procCoTaskMemFree.Call(uintptr(pointer))
	}
}

func variantClear(value *variant) error {
	result, _, _ := procVariantClear.Call(uintptr(unsafe.Pointer(value)))
	runtime.KeepAlive(value)
	if hr := hresultFromCall(result); hr.Failed() {
		return &comCallError{Operation: "VariantClear", HRESULT: hr}
	}
	return nil
}

func bstrLength(value unsafe.Pointer) uint32 {
	length, _, _ := procSysStringLen.Call(uintptr(value))
	return uint32(length)
}

func allocateBSTR(units []uint16) (uintptr, error) {
	var pointer uintptr
	if len(units) == 0 {
		pointer, _, _ = procSysAllocStringLen.Call(0, 0)
	} else {
		pointer, _, _ = procSysAllocStringLen.Call(
			uintptr(unsafe.Pointer(&units[0])), uintptr(len(units)),
		)
		runtime.KeepAlive(units)
	}
	if pointer == 0 {
		return 0, fmt.Errorf("SysAllocStringLen failed")
	}
	return pointer, nil
}

type windowsHandle uintptr

func createWakeEvent() (windowsHandle, error) {
	handle, _, callErr := procCreateEventW.Call(0, 1, 0, 0)
	if handle == 0 {
		return 0, fmt.Errorf("CreateEventW: %w", callErr)
	}
	return windowsHandle(handle), nil
}

func (handle windowsHandle) signal() error {
	result, _, callErr := procSetEvent.Call(uintptr(handle))
	if result == 0 {
		return fmt.Errorf("SetEvent: %w", callErr)
	}
	return nil
}

func (handle windowsHandle) reset() error {
	result, _, callErr := procResetEvent.Call(uintptr(handle))
	if result == 0 {
		return fmt.Errorf("ResetEvent: %w", callErr)
	}
	return nil
}

func (handle windowsHandle) close() {
	if handle != 0 {
		procCloseHandle.Call(uintptr(handle))
	}
}

type windowPoint struct {
	X int32
	Y int32
}

type windowMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   windowPoint
	Private uint32
}

func waitForDAWork(handle windowsHandle) error {
	waitHandle := uintptr(handle)
	result, _, callErr := procMsgWaitForMultipleObjectsEx.Call(
		1,
		uintptr(unsafe.Pointer(&waitHandle)),
		infiniteWait,
		qsAllInput,
		mwmoInputAvailable,
	)
	switch uint32(result) {
	case waitObject0:
		return handle.reset()
	case waitObject0 + 1:
		pumpWindowMessages()
		return nil
	case waitFailed:
		return fmt.Errorf("MsgWaitForMultipleObjectsEx: %w", callErr)
	default:
		return fmt.Errorf("MsgWaitForMultipleObjectsEx returned 0x%08X", uint32(result))
	}
}

func pumpWindowMessages() {
	var message windowMessage
	for {
		available, _, _ := procPeekMessageW.Call(
			uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove,
		)
		if available == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}
