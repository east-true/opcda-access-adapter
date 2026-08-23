//go:build windows

package opcda

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestEncodeWriteVariantPreservesScalarWidths(t *testing.T) {
	tests := []struct {
		name  string
		vt    DAVarType
		value any
		bits  uint64
	}{
		{name: "I2", vt: VTI2, value: int16(-32768), bits: 0x8000},
		{name: "UI4", vt: VTUI4, value: uint32(math.MaxUint32), bits: math.MaxUint32},
		{name: "I8", vt: VTI8, value: int64(math.MinInt64), bits: 0x8000000000000000},
		{name: "R4", vt: VTR4, value: float32(1.25), bits: uint64(math.Float32bits(1.25))},
		{name: "BOOL true", vt: VTBool, value: true, bits: 0xFFFF},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := encodeWriteVariant(test.vt, test.value, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if value.VT != uint16(test.vt) {
				t.Fatalf("VT = %d, want %d", value.VT, test.vt)
			}
			var got uint64
			if test.vt == VTI8 {
				got = binary.LittleEndian.Uint64(value.Data[:])
			} else if test.vt == VTUI4 || test.vt == VTR4 {
				got = uint64(binary.LittleEndian.Uint32(value.Data[:]))
			} else {
				got = uint64(binary.LittleEndian.Uint16(value.Data[:]))
			}
			if got != test.bits {
				t.Fatalf("bits = %#x, want %#x", got, test.bits)
			}
			if err := variantClear(&value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEncodeWriteBSTRPreservesEmbeddedNULAndClearsOwnership(t *testing.T) {
	value, err := encodeWriteVariant(VTBSTR, "A\x00😀", 16)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeVariant(&value, 16)
	if err != nil || decoded != "A\x00😀" {
		t.Fatalf("decode = %q, %v", decoded, err)
	}
	if err := variantClear(&value); err != nil {
		t.Fatal(err)
	}
	if value.VT != uint16(VTEmpty) {
		t.Fatalf("VariantClear left VT = %d", value.VT)
	}
}
