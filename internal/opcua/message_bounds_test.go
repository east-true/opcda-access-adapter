package opcua

import (
	"bytes"
	"strings"
	"testing"
)

// Three bounds a client's message is held to, each unpinned at the value that
// decides it. The suite refused something clearly outside each one, so nothing
// said a message of exactly the permitted shape is accepted -- and every one of
// these is a number the protocol itself names, so refusing it would refuse a
// client that read the specification correctly.

// The negotiated buffers may not fall below the minimum the protocol sets.
// Exactly the minimum is not below it: a client that asks for precisely that
// has asked for the smallest buffer the protocol defines, which is a legal
// request rather than a failed negotiation.
func TestNegotiatedBuffersMayBeExactlyTheMinimum(t *testing.T) {
	hello := func(buffer uint32) Hello {
		return Hello{
			ProtocolVersion:   ProtocolVersion,
			ReceiveBufferSize: buffer,
			SendBufferSize:    buffer,
			MaxMessageSize:    0,
			MaxChunkCount:     0,
			EndpointURL:       "opc.tcp://127.0.0.1:4840",
		}
	}

	ack, err := NegotiateAcknowledge(hello(MinimumBufferSize), 65536, 65536, 1<<20, 32)
	if err != nil {
		t.Fatalf("a Hello asking for exactly the minimum buffer was refused: %v", err)
	}
	if ack.ReceiveBufferSize != MinimumBufferSize || ack.SendBufferSize != MinimumBufferSize {
		t.Errorf("the acknowledged buffers are %d and %d, want %d",
			ack.ReceiveBufferSize, ack.SendBufferSize, MinimumBufferSize)
	}

	if _, err := NegotiateAcknowledge(hello(MinimumBufferSize-1), 65536, 65536, 1<<20, 32); err == nil {
		t.Error("a Hello one byte below the minimum buffer was accepted")
	}
}

// The SecurityPolicyUri has a documented byte bound, and a policy URI of
// exactly that length is one a client may send. The real ones are far shorter,
// which is exactly why nothing had ever sat on the boundary.
func TestASecurityPolicyURIMayBeExactlyItsBound(t *testing.T) {
	header := func(length int) AsymmetricSecurityHeader {
		return AsymmetricSecurityHeader{SecurityPolicyURI: strings.Repeat("p", length)}
	}
	if _, err := EncodeAsymmetricSecurityHeader(
		header(MaxSecurityPolicyURIBytes), 4096, DefaultBinaryLimits()); err != nil {
		t.Errorf("a policy URI of exactly %d bytes was refused: %v", MaxSecurityPolicyURIBytes, err)
	}
	if _, err := EncodeAsymmetricSecurityHeader(
		header(MaxSecurityPolicyURIBytes+1), 4096, DefaultBinaryLimits()); err == nil {
		t.Errorf("a policy URI of %d bytes was accepted", MaxSecurityPolicyURIBytes+1)
	}
}

// A MessageSecurityMode outside the enumeration is refused rather than reduced
// to Invalid, so a malformed field can never look like a deliberate choice.
// Invalid is itself a defined value, and the range that refuses the undefined
// ones must not refuse it: a client that sends the enumeration's own zero has
// sent something the enumeration names.
func TestEveryDefinedSecurityModeIsInsideTheRangeThatRefusesTheRest(t *testing.T) {
	request := func(mode int32) []byte {
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		encoder.WriteRequestHeader(RequestHeader{AdditionalHeader: NullExtensionObject()})
		encoder.WriteUInt32(ProtocolVersion)
		encoder.WriteInt32(int32(TokenRequestIssue))
		encoder.WriteInt32(mode)
		encoder.WriteNullByteString()
		encoder.WriteUInt32(60000)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	decode := func(mode int32) error {
		decoder, err := NewDecoder(request(mode), DefaultBinaryLimits())
		if err != nil {
			return err
		}
		_, err = decoder.ReadOpenSecureChannelRequest()
		return err
	}

	for _, mode := range []SecurityMode{
		SecurityModeInvalid, SecurityModeNone, SecurityModeSign, SecurityModeSignAndEncrypt,
	} {
		if err := decode(int32(mode)); err != nil {
			t.Errorf("MessageSecurityMode %d is defined and was refused: %v", mode, err)
		}
	}
	for _, mode := range []int32{-1, int32(SecurityModeSignAndEncrypt) + 1} {
		if err := decode(mode); err == nil {
			t.Errorf("MessageSecurityMode %d is not defined and was accepted", mode)
		}
	}
}

// A third place writes a certificate, and it makes the same null-versus-empty
// choice the endpoint and session-response encoders do: a client that has no
// certificate writes a null ByteString, and one that has a certificate writes
// the certificate. Inverting the check swaps the two, so a real certificate
// would be replaced by a null and never reach the peer.
func TestACreateSessionRequestWithoutACertificateWritesANullOne(t *testing.T) {
	encode := func(certificate []byte) []byte {
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		encoder.WriteCreateSessionRequest(CreateSessionRequest{
			Header:                  RequestHeader{AdditionalHeader: NullExtensionObject()},
			ClientDescription:       ApplicationDescription{ApplicationURI: "urn:client"},
			EndpointURL:             "opc.tcp://127.0.0.1:4840",
			SessionName:             "session",
			ClientNonce:             []byte("0123456789abcdef0123456789abcdef"),
			ClientCertificate:       certificate,
			RequestedSessionTimeout: 60000,
		})
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatalf("encoding a CreateSession request failed: %v", err)
		}
		return encoded
	}

	certificate := []byte{0x30, 0x82, 0x03, 0x0c, 0xfe, 0xed, 0xfa, 0xce}
	absent := encode(nil)
	if !bytes.Contains(absent, nullByteStringMarker()) {
		t.Errorf("a request with no certificate carries no null ByteString: %x", absent)
	}
	present := encode(certificate)
	if !bytes.Contains(present, certificate) {
		t.Errorf("a request's certificate is not in what it encodes to: %x", present)
	}
	if bytes.Equal(absent, present) {
		t.Error("a request encodes the same with and without a certificate")
	}
}

// A Variant's ArrayDimensions field is written only when there is more than one
// dimension to describe. A single dimension is already the length prefix, and
// writing it twice gives a decoder a second statement of the same fact to
// disagree with -- Table 26 has it stop on exactly that disagreement.
func TestASingleDimensionIsNotRepeatedInArrayDimensions(t *testing.T) {
	encode := func(t *testing.T, variant Variant) []byte {
		t.Helper()
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		encoder.WriteVariant(variant)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatalf("encoding failed: %v", err)
		}
		return encoded
	}

	elements := []int32{1, 2, 3}
	withoutDimensions := encode(t, Variant{Type: BuiltInInt32, IsArray: true, Value: elements})
	// One dimension named explicitly is the same array, and encodes the same
	// way: the field is not written for it.
	withOne := encode(t, Variant{
		Type: BuiltInInt32, IsArray: true, Value: elements, ArrayDimensions: []int32{3},
	})
	if !bytes.Equal(withoutDimensions, withOne) {
		t.Errorf("naming the single dimension changed the encoding:\n %x\n %x",
			withoutDimensions, withOne)
	}
	if withoutDimensions[0]&variantArrayDimensions != 0 {
		t.Errorf("a one-dimensional array set the dimensions bit: mask %#02x", withoutDimensions[0])
	}
}
