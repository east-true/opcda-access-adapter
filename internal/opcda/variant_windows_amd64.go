//go:build windows && amd64

package opcda

// VARIANT is 24 bytes in the Windows 64-bit ABI: an 8-byte header followed by
// a 16-byte value union (large enough for BRECORD's two pointers).
type variant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Data      [16]byte
}
