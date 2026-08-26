package opcua

import (
	"bytes"
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

// FuzzDecodeStructuredTypes drives NodeId, ExtensionObject, DiagnosticInfo and
// the service headers with arbitrary bytes. Recursion and every declared length
// must stay bounded no matter what a peer sends.
func FuzzDecodeStructuredTypes(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteNodeID(StringNodeID(2, "Test/Float"))
	seed.WriteExtensionObject(NullExtensionObject())
	seed.WriteDiagnosticInfo(DiagnosticInfo{SymbolicID: -1, HasSymbolicID: true})
	seed.WriteRequestHeader(RequestHeader{
		AuthenticationToken: NumericNodeID(0, 1), AdditionalHeader: NullExtensionObject(),
	})
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{0x03, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// A chain of DiagnosticInfo masks that ask for unbounded recursion.
	f.Add(bytes.Repeat([]byte{0x40}, 64))
	f.Add([]byte{0x05, 0, 0, 0xFF, 0xFF, 0xFF, 0x7F})

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, read := range []func(*Decoder) error{
			func(d *Decoder) error { _, err := d.ReadNodeID(); return err },
			func(d *Decoder) error { _, err := d.ReadExpandedNodeID(); return err },
			func(d *Decoder) error { _, err := d.ReadQualifiedName(); return err },
			func(d *Decoder) error { _, err := d.ReadLocalizedText(); return err },
			func(d *Decoder) error { _, err := d.ReadExtensionObject(); return err },
			func(d *Decoder) error { _, err := d.ReadDiagnosticInfo(); return err },
			func(d *Decoder) error { _, err := d.ReadRequestHeader(); return err },
			func(d *Decoder) error { _, err := d.ReadResponseHeader(); return err },
			func(d *Decoder) error { _, err := d.ReadApplicationDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadUserTokenPolicy(); return err },
			func(d *Decoder) error { _, err := d.ReadEndpointDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadGetEndpointsRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadGetEndpointsResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadSignatureData(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSessionResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadActivateSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadActivateSessionResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadCloseSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadViewDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadReferenceDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseResult(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseNextRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseNextResponse(); return err },
		} {
			decoder, err := NewDecoder(data, limits)
			if err != nil {
				return
			}
			if err := read(decoder); err != nil {
				if _, ok := err.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", err)
				}
				continue
			}
			if decoder.Remaining() < 0 {
				t.Fatal("a reader consumed past the end of the buffer")
			}
		}
	})
}

// FuzzDecodeSecureChannelService drives the service bodies with arbitrary
// bytes. An undefined enumeration value must be refused rather than reduced to
// a neighbouring one, and no length may be honoured beyond the bytes present.
func FuzzDecodeSecureChannelService(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteOpenSecureChannelRequest(OpenSecureChannelRequest{
		Header:       RequestHeader{AuthenticationToken: NumericNodeID(0, 0), AdditionalHeader: NullExtensionObject()},
		RequestType:  TokenRequestIssue,
		SecurityMode: SecurityModeNone,
	})
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte{0x01, 0x00, 0xBE, 0x01})
	f.Add(bytes.Repeat([]byte{0xFF}, 48))

	f.Fuzz(func(t *testing.T, data []byte) {
		decoder, err := NewDecoder(data, limits)
		if err != nil {
			return
		}
		identifier, err := decoder.ReadServiceTypeID()
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("TypeId error %v is not a CodecError", err)
			}
			return
		}
		switch identifier {
		case OpenSecureChannelRequestEncodingID:
			request, readErr := decoder.ReadOpenSecureChannelRequest()
			if readErr != nil {
				if _, ok := readErr.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", readErr)
				}
				return
			}
			switch request.RequestType {
			case TokenRequestIssue, TokenRequestRenew:
			default:
				t.Fatalf("an undefined request type %d was accepted", int32(request.RequestType))
			}
			if request.SecurityMode > SecurityModeSignAndEncrypt {
				t.Fatalf("an undefined security mode %d was accepted", uint32(request.SecurityMode))
			}
		case OpenSecureChannelResponseEncodingID:
			_, _ = decoder.ReadOpenSecureChannelResponse()
		case CloseSecureChannelRequestEncodingID:
			_, _ = decoder.ReadCloseSecureChannelRequest()
		default:
			_, _ = decoder.ReadResponseHeader()
		}
		if decoder.Remaining() < 0 {
			t.Fatal("a reader consumed past the end of the buffer")
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
