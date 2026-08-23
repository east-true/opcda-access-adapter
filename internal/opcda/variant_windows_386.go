//go:build windows && 386

package opcda

// VARIANT is 16 bytes in the Windows 32-bit ABI: an 8-byte header followed by
// an 8-byte value union.
type variant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Data      [8]byte
}
