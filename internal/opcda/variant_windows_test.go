//go:build windows

package opcda

import (
	"encoding/binary"
	"math"
	"testing"
	"unsafe"
)

func TestWindowsABILayouts(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if got, want := unsafe.Offsetof(iopcServerVTable{}.AddGroup), 3*pointerSize; got != want {
		t.Fatalf("IOPCServer::AddGroup offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(iopcItemMgtVTable{}.AddItems), 3*pointerSize; got != want {
		t.Fatalf("IOPCItemMgt::AddItems offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(iopcSyncIOVTable{}.Read), 3*pointerSize; got != want {
		t.Fatalf("IOPCSyncIO::Read offset = %d, want %d", got, want)
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		assertSize(t, "VARIANT", unsafe.Sizeof(variant{}), 16)
		assertSize(t, "OPCITEMDEF", unsafe.Sizeof(opcItemDef{}), 28)
		assertSize(t, "OPCITEMRESULT", unsafe.Sizeof(opcItemResult{}), 20)
		assertSize(t, "OPCITEMSTATE", unsafe.Sizeof(opcItemState{}), 32)
	} else {
		assertSize(t, "VARIANT", unsafe.Sizeof(variant{}), 24)
		assertSize(t, "OPCITEMDEF", unsafe.Sizeof(opcItemDef{}), 48)
		assertSize(t, "OPCITEMRESULT", unsafe.Sizeof(opcItemResult{}), 24)
		assertSize(t, "OPCITEMSTATE", unsafe.Sizeof(opcItemState{}), 40)
	}
}

func TestVariantScalarWidths(t *testing.T) {
	tests := []struct {
		name  string
		value variant
		want  any
	}{
		{name: "I1", value: variantWith(VTI1, []byte{0x80}), want: int8(-128)},
		{name: "UI1", value: variantWith(VTUI1, []byte{0xFF}), want: uint8(255)},
		{name: "I2", value: variantWith(VTI2, []byte{0x00, 0x80}), want: int16(-32768)},
		{name: "UI2", value: variantWith(VTUI2, []byte{0xFF, 0xFF}), want: uint16(65535)},
		{name: "I4", value: variantWith(VTI4, []byte{0x00, 0x00, 0x00, 0x80}), want: int32(-2147483648)},
		{name: "UI4", value: variantWith(VTUI4, []byte{0xFF, 0xFF, 0xFF, 0xFF}), want: uint32(4294967295)},
		{name: "I8", value: variantWith(VTI8, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80}), want: int64(-9223372036854775808)},
		{name: "UI8", value: variantWith(VTUI8, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}), want: uint64(18446744073709551615)},
		{name: "BOOL true", value: variantWith(VTBool, []byte{0xFF, 0xFF}), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeVariant(&test.value, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("value = %#v (%T), want %#v (%T)", got, got, test.want, test.want)
			}
		})
	}
}

func TestVariantFloatWidthsAndSpecialValues(t *testing.T) {
	r4 := variant{VT: uint16(VTR4)}
	binary.LittleEndian.PutUint32(r4.Data[:], math.Float32bits(float32(1.25)))
	if got, err := decodeVariant(&r4, 1024); err != nil || got != float32(1.25) {
		t.Fatalf("R4 = %#v, %v", got, err)
	}

	r8 := variant{VT: uint16(VTR8)}
	binary.LittleEndian.PutUint64(r8.Data[:], math.Float64bits(math.Inf(1)))
	got, err := decodeVariant(&r8, 1024)
	if err != nil || !math.IsInf(got.(float64), 1) {
		t.Fatalf("R8 = %#v, %v", got, err)
	}
}

func TestBSTRPreservesEmbeddedNULAndIsCleared(t *testing.T) {
	bstr, err := allocateBSTR([]uint16{'A', 0, 'B'})
	if err != nil {
		t.Fatal(err)
	}
	value := variant{VT: uint16(VTBSTR)}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		binary.LittleEndian.PutUint32(value.Data[:], uint32(bstr))
	} else {
		binary.LittleEndian.PutUint64(value.Data[:], uint64(bstr))
	}
	got, err := decodeVariant(&value, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "A\x00B" {
		t.Fatalf("BSTR = %q", got)
	}
	if err := variantClear(&value); err != nil {
		t.Fatal(err)
	}
	if value.VT != uint16(VTEmpty) {
		t.Fatalf("VariantClear left vt = %d", value.VT)
	}
}

func TestFiletimePresenceAndUnixConversion(t *testing.T) {
	if _, present := (filetime{}).toTime(); present {
		t.Fatal("zero FILETIME must remain timestamp-absent")
	}
	const unixEpochTicks = uint64(116444736000000000)
	ticks := unixEpochTicks
	value := filetime{LowDateTime: uint32(ticks), HighDateTime: uint32(ticks >> 32)}
	got, present := value.toTime()
	if !present || got.Unix() != 0 || got.Nanosecond() != 0 {
		t.Fatalf("FILETIME = %s, present %v", got, present)
	}
}

func variantWith(varType DAVarType, bytes []byte) variant {
	value := variant{VT: uint16(varType)}
	copy(value.Data[:], bytes)
	return value
}

func assertSize(t *testing.T, name string, got, want uintptr) {
	t.Helper()
	if got != want {
		t.Fatalf("%s size = %d, want %d", name, got, want)
	}
}
