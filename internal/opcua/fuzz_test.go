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

// FuzzDecodeUACP drives the connection protocol framing with arbitrary bytes.
// A header must be validated against the negotiated buffer before any body is
// read, and no message body may panic or over-read.
func FuzzDecodeUACP(f *testing.F) {
	limits := DefaultBinaryLimits()

	hello, err := EncodeHello(Hello{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: MinimumBufferSize,
		SendBufferSize:    MinimumBufferSize,
		EndpointURL:       "opc.tcp://127.0.0.1:4840",
	}, limits)
	if err != nil {
		f.Fatal(err)
	}
	header, err := EncodeMessageHeader(MessageTypeHello, ChunkFinal, len(hello), 65536)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(header, hello...))
	f.Add([]byte{})
	f.Add([]byte{'H', 'E', 'L', 'F', 8, 0, 0, 0})
	f.Add([]byte{'M', 'S', 'G', 'C', 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{'E', 'R', 'R', 'F', 8, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		const negotiated = uint32(65536)
		decoded, err := DecodeMessageHeader(data, negotiated)
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("header error %v is not a CodecError", err)
			}
			return
		}
		if decoded.Size > negotiated || decoded.Size < HeaderSize {
			t.Fatalf("an out-of-range message size passed validation: %d", decoded.Size)
		}
		if decoded.BodySize() < 0 {
			t.Fatalf("negative body size %d", decoded.BodySize())
		}
		if len(data) < int(decoded.Size) {
			return
		}
		body := data[HeaderSize:decoded.Size]
		switch decoded.Type {
		case MessageTypeHello:
			_, _ = DecodeHello(body, limits)
		case MessageTypeAcknowledge:
			_, _ = DecodeAcknowledge(body, limits)
		case MessageTypeError:
			_, _ = DecodeProtocolError(body, limits)
		}
	})
}

// FuzzDecodeUASC drives the secure conversation framing with arbitrary bytes.
// A malformed security header must be refused rather than trusted, and no
// declared length may be honoured beyond the bytes present.
func FuzzDecodeUASC(f *testing.F) {
	limits := DefaultBinaryLimits()

	header, err := EncodeSecureConversationHeader(SecureConversationHeader{
		Type: MessageTypeOpenChannel, Chunk: ChunkFinal, SecureChannelID: 1,
	}, 12, 65536)
	if err != nil {
		f.Fatal(err)
	}
	security, err := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{}, 4096, limits)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(header, security...))
	f.Add([]byte{})
	f.Add([]byte{'M', 'S', 'G', 'F', 12, 0, 0, 0, 1, 0, 0, 0})
	f.Add([]byte{'O', 'P', 'N', 'F', 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0})
	f.Add([]byte{'C', 'L', 'O', 'A', 12, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		const negotiated = uint32(65536)
		decoded, err := DecodeSecureConversationHeader(data, negotiated)
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("header error %v is not a CodecError", err)
			}
			return
		}
		if decoded.Size < SecureConversationHeaderSize || decoded.Size > negotiated {
			t.Fatalf("an out-of-range message size passed validation: %d", decoded.Size)
		}
		if len(data) < int(decoded.Size) {
			return
		}
		body := data[SecureConversationHeaderSize:decoded.Size]
		if decoded.Type == MessageTypeOpenChannel {
			security, consumed, securityErr := DecodeAsymmetricSecurityHeader(body, 4096, limits)
			if securityErr != nil {
				if _, ok := securityErr.(*CodecError); !ok {
					t.Fatalf("security header error %v is not a CodecError", securityErr)
				}
				return
			}
			if consumed < 0 || consumed > len(body) {
				t.Fatalf("security header consumed %d of %d bytes", consumed, len(body))
			}
			if len(security.SecurityPolicyURI) > MaxSecurityPolicyURIBytes {
				t.Fatalf("a policy URI of %d bytes passed the limit", len(security.SecurityPolicyURI))
			}
			if n := len(security.ReceiverCertificateThumbprint); n != 0 && n != CertificateThumbprintBytes {
				t.Fatalf("a thumbprint of %d bytes passed validation", n)
			}
			body = body[consumed:]
		} else {
			_, _ = DecodeSequenceHeader(body, limits)
			return
		}
		_, _ = DecodeSequenceHeader(body, limits)
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
