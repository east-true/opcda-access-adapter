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
	}
	for _, test := range tests {
		if got := test.hr.Succeeded(); got != test.succeeded {
			t.Fatalf("Succeeded(%s) = %v, want %v", test.hex, got, test.succeeded)
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
