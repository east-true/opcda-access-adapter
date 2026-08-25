package opcua

import (
	"testing"
	"time"
)

// FuzzDecodeUABinary drives the decoder with arbitrary bytes. A malformed
// message must always produce an error rather than a panic, an out-of-range
// read, or an allocation sized by an unverified length. OPC 10000-6 5.1.8
// requires exactly this: reject what the decoder does not support.
func FuzzDecodeUABinary(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteBoolean(true)
	seed.WriteString("水Boy")
	seed.WriteByteString([]byte{1, 2, 3})
	seed.WriteGuid(Guid{Data1: 1, Data2: 2, Data3: 3})
	seed.WriteDateTime(time.Unix(0, 0).UTC())
	seed.WriteArrayLength(2)
	seed.WriteInt32(7)
	seed.WriteInt32(8)
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	// A null length, an empty length, and lengths that must be refused.
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0xFE, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x80})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0x7F})

	f.Fuzz(func(t *testing.T, data []byte) {
		decoder, err := NewDecoder(data, limits)
		if err != nil {
			return
		}
		// Walk the buffer with every reader until one refuses. No sequence of
		// calls may panic or read past the end.
		for step := 0; decoder.Remaining() > 0; step++ {
			var readErr error
			switch step % 12 {
			case 0:
				_, readErr = decoder.ReadBoolean()
			case 1:
				_, readErr = decoder.ReadSByte()
			case 2:
				_, readErr = decoder.ReadByteValue()
			case 3:
				_, readErr = decoder.ReadInt16()
			case 4:
				_, readErr = decoder.ReadUInt32()
			case 5:
				_, readErr = decoder.ReadInt64()
			case 6:
				_, readErr = decoder.ReadDouble()
			case 7:
				_, readErr = decoder.ReadStatusCode()
			case 8:
				_, _, readErr = decoder.ReadString()
			case 9:
				_, _, readErr = decoder.ReadByteString()
			case 10:
				_, readErr = decoder.ReadGuid()
			case 11:
				var length int
				var isNull bool
				length, isNull, readErr = decoder.ReadArrayLength(4)
				if readErr == nil && !isNull && length > limits.MaxArrayLength {
					t.Fatalf("array length %d passed the %d limit", length, limits.MaxArrayLength)
				}
			}
			if readErr != nil {
				// Every refusal must carry a UA status a peer can be told.
				if _, ok := readErr.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", readErr)
				}
				return
			}
			if decoder.Remaining() < 0 {
				t.Fatalf("decoder read past the end of the buffer")
			}
		}
	})
}

// FuzzDecodeDateTime checks that no wire value produces a panic or an instant
// outside the range OPC 10000-6 5.2.2.5 defines.
func FuzzDecodeDateTime(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(DateTimeMax)
	f.Add(int64(1))
	f.Add(int64(-9223372036854775808))

	f.Fuzz(func(t *testing.T, ticks int64) {
		decoded := DecodeDateTime(ticks)
		if decoded.Before(dateTimeLowerBound) {
			t.Fatalf("ticks %d decoded before 1601: %s", ticks, decoded)
		}
		if decoded.After(dateTimeUpperBound) {
			t.Fatalf("ticks %d decoded after 9999: %s", ticks, decoded)
		}
		// Re-encoding a decoded instant must stay inside the wire range.
		if encoded := EncodeDateTime(decoded); encoded < DateTimeMin {
			t.Fatalf("re-encoding produced %d", encoded)
		}
	})
}
