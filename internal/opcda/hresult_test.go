package opcda

import (
	"fmt"
	"testing"
)

func TestHRESULTUsesCOMSuccessSemantics(t *testing.T) {
	tests := []struct {
		hr        HRESULT
		succeeded bool
		hex       string
	}{
		{SOK, true, "0x00000000"},
		{SFalse, true, "0x00000001"},
		{HRESULT(-2147467259), false, "0x80004005"},
		// The boundary itself. Zero is the first success and -1 the first
		// failure, so a comparison loosened either way shows up here.
		{HRESULT(-1), false, "0xFFFFFFFF"},
		{HRESULT(2147483647), true, "0x7FFFFFFF"},
		{HRESULT(-2147483648), false, "0x80000000"},
	}
	for _, test := range tests {
		if got := test.hr.Succeeded(); got != test.succeeded {
			t.Fatalf("Succeeded(%s) = %v, want %v", test.hex, got, test.succeeded)
		}
		// Failed is the other half of the COM pair and was never called here,
		// so nothing said the two agree. A mutation making Failed answer true
		// for S_OK survived the whole suite: every frontend classifies results
		// with it, and S_OK is the value it sees most.
		if got := test.hr.Failed(); got == test.succeeded {
			t.Fatalf("Failed(%s) = %v, which contradicts Succeeded", test.hex, got)
		}
		if got := test.hr.Hex(); got != test.hex {
			t.Fatalf("Hex() = %s, want %s", got, test.hex)
		}
	}
}

func TestAsAdapterErrorFindsWrappedError(t *testing.T) {
	want := NewAdapterError(CodeQueueFull, "queue full")
	got, ok := AsAdapterError(fmt.Errorf("wrapped: %w", want))
	if !ok || got != want {
		t.Fatalf("AsAdapterError() = %v, %v; want %v, true", got, ok, want)
	}
}
