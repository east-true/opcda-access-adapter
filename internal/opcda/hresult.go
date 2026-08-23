package opcda

import "fmt"

// HRESULT is the signed 32-bit value used by COM and OPC DA. It is kept raw so
// callers can distinguish method-level and per-item source results.
type HRESULT int32

const (
	SOK          HRESULT = 0
	SFalse       HRESULT = 1
	ENoInterface HRESULT = -2147467262 // 0x80004002
)

// Succeeded implements the COM SUCCEEDED macro semantics. Non-negative values
// include successful informational statuses such as S_FALSE.
func (hr HRESULT) Succeeded() bool {
	return hr >= 0
}

// Failed implements the COM FAILED macro semantics.
func (hr HRESULT) Failed() bool {
	return hr < 0
}

// Hex returns the raw unsigned HRESULT representation used in diagnostics and
// HTTP responses (for example, 0xC0040007).
func (hr HRESULT) Hex() string {
	return fmt.Sprintf("0x%08X", uint32(int32(hr)))
}

// HRESULTValue is the transport-neutral form of a raw HRESULT.
type HRESULTValue struct {
	Value int32  `json:"value"`
	Hex   string `json:"hex"`
}

func (hr HRESULT) Representation() HRESULTValue {
	return HRESULTValue{Value: int32(hr), Hex: hr.Hex()}
}
