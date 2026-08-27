//go:build windows

package opcua

import (
	"testing"

	"golang.org/x/sys/windows"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// OPC 10000-8 Tables A.4 and A.5 name four Windows COM codes alongside the OPC
// ones. Their values are declared in mapping.go, which builds on every platform,
// so they cannot be taken from golang.org/x/sys/windows directly. They are
// checked against it here instead, on the one platform where that package
// builds, so the declared value is tied to the Windows SDK headers it was
// transcribed from rather than to somebody's recollection.
func TestWindowsErrorCodesMatchTheSDK(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		declared opcda.HRESULT
		sdk      windows.Handle
	}{
		{"E_OUTOFMEMORY", EOutOfMemory, windows.E_OUTOFMEMORY},
		{"E_ACCESSDENIED", EAccessDenied, windows.E_ACCESSDENIED},
		{"DISP_E_TYPEMISMATCH", DispETypeMismatch, windows.DISP_E_TYPEMISMATCH},
		{"DISP_E_OVERFLOW", DispEOverflow, windows.DISP_E_OVERFLOW},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := uint32(int32(testCase.declared)); got != uint32(testCase.sdk) {
				t.Fatalf("declared 0x%08X, SDK 0x%08X", got, uint32(testCase.sdk))
			}
		})
	}
}
