package opcua

import (
	"bytes"
	"testing"
)

// A ByteString that is absent and one that is present but empty are different
// values on the wire: the first is encoded as a length of -1 and the second as
// a length of zero. The adapter publishes no certificate, so every place it
// writes one has to choose the null form -- and the code says so in a comment
// at each site.
//
// Three of those checks survived the sweep, and what inverting one does is
// worse than writing the wrong empty: it swaps the two arms, so a server that
// does have a certificate writes a null in its place and the certificate never
// reaches the client at all. The cases below assert both directions, because
// only the pair distinguishes a correct choice from a swapped one.

func nullByteStringMarker() []byte {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		panic(err)
	}
	encoder.WriteNullByteString()
	encoded, err := encoder.Bytes()
	if err != nil {
		panic(err)
	}
	return encoded
}

func emptyByteStringMarker() []byte {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		panic(err)
	}
	encoder.WriteByteString([]byte{})
	encoded, err := encoder.Bytes()
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestAnAbsentByteStringIsNotAnEmptyOne(t *testing.T) {
	null, empty := nullByteStringMarker(), emptyByteStringMarker()
	if bytes.Equal(null, empty) {
		t.Fatalf("a null ByteString and an empty one encode alike as %x, so nothing below "+
			"can tell the two apart", null)
	}
	if len(null) != 4 || len(empty) != 4 {
		t.Fatalf("a bare length is %d and %d bytes, not the four the encoding defines",
			len(null), len(empty))
	}
}

func encodeEndpoint(t *testing.T, certificate []byte) []byte {
	t.Helper()
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteEndpointDescription(EndpointDescription{
		EndpointURL:         "opc.tcp://127.0.0.1:4840",
		ServerCertificate:   certificate,
		SecurityMode:        SecurityModeNone,
		SecurityPolicyURI:   "urn:test:security-policy:none",
		TransportProfileURI: "urn:test:transport:uatcp-uasc-uabinary",
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("encoding an endpoint description failed: %v", err)
	}
	return encoded
}

func TestAnEndpointWithoutACertificateWritesANullOne(t *testing.T) {
	certificate := []byte{0x30, 0x82, 0x01, 0x0a, 0xde, 0xad, 0xbe, 0xef}

	absent := encodeEndpoint(t, nil)
	if !bytes.Contains(absent, nullByteStringMarker()) {
		t.Errorf("an endpoint with no certificate carries no null ByteString: %x", absent)
	}

	// The other direction. A certificate that is present must reach the client
	// rather than being replaced by the null the absent case writes.
	present := encodeEndpoint(t, certificate)
	if !bytes.Contains(present, certificate) {
		t.Errorf("an endpoint's certificate is not in what it encodes to: %x", present)
	}
	if bytes.Equal(absent, present) {
		t.Error("an endpoint encodes the same with and without a certificate")
	}

	// An empty but non-nil certificate is the same absence, and must be
	// written the same way: a source that hands over a zero-length slice has
	// no certificate either.
	if empty := encodeEndpoint(t, []byte{}); !bytes.Equal(empty, absent) {
		t.Errorf("an empty certificate encoded as %x, not as the absent form %x", empty, absent)
	}
}

func encodeCreateSessionResponse(t *testing.T, certificate []byte) []byte {
	t.Helper()
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteCreateSessionResponse(CreateSessionResponse{
		SessionID:             NodeID{Namespace: 1, Type: NodeIDTypeString, StringID: "session"},
		AuthenticationToken:   NodeID{Namespace: 1, Type: NodeIDTypeString, StringID: "token"},
		RevisedSessionTimeout: 60000,
		ServerNonce:           []byte("0123456789abcdef0123456789abcdef"),
		ServerCertificate:     certificate,
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("encoding a CreateSession response failed: %v", err)
	}
	return encoded
}

func TestASessionWithoutACertificateWritesANullOne(t *testing.T) {
	certificate := []byte{0x30, 0x82, 0x02, 0x0b, 0xca, 0xfe, 0xba, 0xbe}

	absent := encodeCreateSessionResponse(t, nil)
	if !bytes.Contains(absent, nullByteStringMarker()) {
		t.Errorf("a session response with no certificate carries no null ByteString: %x", absent)
	}
	present := encodeCreateSessionResponse(t, certificate)
	if !bytes.Contains(present, certificate) {
		t.Errorf("a session response's certificate is not in what it encodes to: %x", present)
	}
	if bytes.Equal(absent, present) {
		t.Error("a session response encodes the same with and without a certificate")
	}
}

// A null ByteString inside a Variant decodes to nothing rather than to an
// empty slice, and the two are told apart by the same length. Reading it as an
// empty slice would hand a client a value that is present and blank where the
// source sent none at all.
func TestANullByteStringVariantDecodesToNothing(t *testing.T) {
	decodeVariantBytes := func(t *testing.T, write func(*Encoder)) (any, error) {
		t.Helper()
		encoder, err := NewEncoder(DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		write(encoder)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		decoder, err := NewDecoder(encoded, DefaultBinaryLimits())
		if err != nil {
			t.Fatal(err)
		}
		return decoder.skipBuiltIn(BuiltInByteString)
	}

	nothing, err := decodeVariantBytes(t, func(e *Encoder) { e.WriteNullByteString() })
	if err != nil {
		t.Fatalf("a null ByteString was refused: %v", err)
	}
	if nothing != nil {
		t.Errorf("a null ByteString decoded as %#v, want nothing", nothing)
	}

	value, err := decodeVariantBytes(t, func(e *Encoder) { e.WriteByteString([]byte{1, 2, 3}) })
	if err != nil {
		t.Fatalf("a present ByteString was refused: %v", err)
	}
	decoded, ok := value.([]byte)
	if !ok || !bytes.Equal(decoded, []byte{1, 2, 3}) {
		t.Errorf("a present ByteString decoded as %#v", value)
	}
}
