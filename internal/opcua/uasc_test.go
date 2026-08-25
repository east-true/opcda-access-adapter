package opcua

import (
	"bytes"
	"strings"
	"testing"
)

// OPC 10000-6 Table 57: a 12 byte header whose MessageSize includes itself,
// followed by the SecureChannelId.
func TestSecureConversationHeaderLayout(t *testing.T) {
	header := SecureConversationHeader{
		Type: MessageTypeSecure, Chunk: ChunkFinal, SecureChannelID: 0x01020304,
	}
	encoded, err := EncodeSecureConversationHeader(header, 4, testBuffer)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'M', 'S', 'G', 'F', 16, 0, 0, 0, 0x04, 0x03, 0x02, 0x01}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("header % X, want % X", encoded, want)
	}
	decoded, err := DecodeSecureConversationHeader(encoded, testBuffer)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != MessageTypeSecure || decoded.Chunk != ChunkFinal ||
		decoded.Size != 16 || decoded.SecureChannelID != 0x01020304 {
		t.Fatalf("header = %+v", decoded)
	}
}

// Table 57: the intermediate and abort codes are only meaningful for MSG.
func TestSecureConversationHeaderChunkCodes(t *testing.T) {
	for _, chunk := range []byte{ChunkIntermediate, ChunkAbort} {
		_, err := EncodeSecureConversationHeader(
			SecureConversationHeader{Type: MessageTypeOpenChannel, Chunk: chunk}, 0, testBuffer)
		if err == nil {
			t.Fatalf("OPN accepted chunk code %c", chunk)
		}
		if _, err := EncodeSecureConversationHeader(
			SecureConversationHeader{Type: MessageTypeSecure, Chunk: chunk}, 0, testBuffer); err != nil {
			t.Fatalf("MSG rejected chunk code %c: %v", chunk, err)
		}
	}
	if _, err := EncodeSecureConversationHeader(
		SecureConversationHeader{Type: MessageTypeHello, Chunk: ChunkFinal}, 0, testBuffer); err == nil {
		t.Fatal("a connection protocol type was framed as a secure conversation message")
	}
}

func TestSecureConversationHeaderRejectsMalformed(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		buffer uint32
		status StatusCode
	}{
		{"connection protocol type", []byte{'H', 'E', 'L', 'F', 12, 0, 0, 0, 0, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
		{"size below the 12 byte header", []byte{'M', 'S', 'G', 'F', 11, 0, 0, 0, 0, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
		{"beyond the negotiated buffer", []byte{'M', 'S', 'G', 'F', 0, 0, 1, 0, 0, 0, 0, 0}, 8192, StatusBadTcpMessageTooLarge},
		{"truncated", []byte{'M', 'S', 'G', 'F', 12, 0, 0, 0, 0}, testBuffer, StatusBadTcpInternalError},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeSecureConversationHeader(testCase.data, testCase.buffer)
			if err == nil {
				t.Fatal("malformed header accepted")
			}
			if got := codecStatus(t, err); got != testCase.status {
				t.Fatalf("status = %s, want %s", got.Hex(), testCase.status.Hex())
			}
		})
	}
}

func TestAsymmetricSecurityHeaderRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	header := AsymmetricSecurityHeader{
		SecurityPolicyURI:             "urn:example:policy",
		SenderCertificate:             bytes.Repeat([]byte{0xAB}, 64),
		ReceiverCertificateThumbprint: bytes.Repeat([]byte{0xCD}, CertificateThumbprintBytes),
	}
	encoded, err := EncodeAsymmetricSecurityHeader(header, 4096, limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, consumed, err := DecodeAsymmetricSecurityHeader(encoded, 4096, limits)
	if err != nil {
		t.Fatal(err)
	}
	if consumed != len(encoded) {
		t.Fatalf("consumed %d of %d bytes", consumed, len(encoded))
	}
	if decoded.SecurityPolicyURI != header.SecurityPolicyURI ||
		!bytes.Equal(decoded.SenderCertificate, header.SenderCertificate) ||
		!bytes.Equal(decoded.ReceiverCertificateThumbprint, header.ReceiverCertificateThumbprint) {
		t.Fatalf("header = %+v", decoded)
	}
}

// Table 58 allows 0 or -1 for an unspecified field, which is the shape a
// SecurityPolicy with no signing or encryption produces.
func TestAsymmetricSecurityHeaderAllowsAbsentFields(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoded, err := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{}, 4096, limits)
	if err != nil {
		t.Fatal(err)
	}
	// Three length fields, all -1.
	if len(encoded) != 12 {
		t.Fatalf("absent header encoded to %d bytes", len(encoded))
	}
	decoded, _, err := DecodeAsymmetricSecurityHeader(encoded, 4096, limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SecurityPolicyURI != "" || decoded.SenderCertificate != nil ||
		decoded.ReceiverCertificateThumbprint != nil {
		t.Fatalf("header = %+v", decoded)
	}

	// A zero length means the same thing as -1.
	zeroed, err := NewEncoder(limits)
	if err != nil {
		t.Fatal(err)
	}
	zeroed.WriteInt32(0)
	zeroed.WriteInt32(0)
	zeroed.WriteInt32(0)
	body, err := zeroed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeAsymmetricSecurityHeader(body, 4096, limits); err != nil {
		t.Fatalf("zero lengths were rejected: %v", err)
	}
}

// "The receiver shall close the communication channel if any of the fields in
// the security header have invalid lengths."
func TestAsymmetricSecurityHeaderRejectsInvalidLengths(t *testing.T) {
	limits := DefaultBinaryLimits()
	cases := []struct {
		name    string
		lengths []int32
		extra   int
	}{
		{"negative other than -1", []int32{-2}, 0},
		{"policy URI beyond 255", []int32{MaxSecurityPolicyURIBytes + 1}, MaxSecurityPolicyURIBytes + 1},
		{"length beyond the body", []int32{64}, 8},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoder, err := NewEncoder(limits)
			if err != nil {
				t.Fatal(err)
			}
			for _, length := range testCase.lengths {
				encoder.WriteInt32(length)
			}
			for count := 0; count < testCase.extra; count++ {
				encoder.WriteByteValue(0)
			}
			body, err := encoder.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = DecodeAsymmetricSecurityHeader(body, 4096, limits)
			if err == nil {
				t.Fatal("an invalid length was accepted")
			}
			if got := codecStatus(t, err); got != StatusBadSecurityChecksFailed {
				t.Fatalf("status = %s, want Bad_SecurityChecksFailed", got.Hex())
			}
		})
	}
}

// Table 58 fixes the thumbprint at 20 bytes whenever it is present.
func TestThumbprintMustBeTwentyBytes(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder, err := NewEncoder(limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteInt32(nullLength)
	encoder.WriteInt32(nullLength)
	encoder.WriteInt32(8)
	for count := 0; count < 8; count++ {
		encoder.WriteByteValue(0)
	}
	body, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeAsymmetricSecurityHeader(body, 4096, limits); err == nil {
		t.Fatal("an 8 byte thumbprint was accepted")
	}

	if _, err := EncodeAsymmetricSecurityHeader(
		AsymmetricSecurityHeader{ReceiverCertificateThumbprint: make([]byte, 8)}, 4096, limits); err == nil {
		t.Fatal("an 8 byte thumbprint was encoded")
	}
}

func TestSenderCertificateBoundIsEnforced(t *testing.T) {
	limits := DefaultBinaryLimits()
	header := AsymmetricSecurityHeader{SenderCertificate: bytes.Repeat([]byte{1}, 65)}
	if _, err := EncodeAsymmetricSecurityHeader(header, 64, limits); err == nil {
		t.Fatal("an over-limit SenderCertificate was encoded")
	}
	encoded, err := EncodeAsymmetricSecurityHeader(header, 128, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeAsymmetricSecurityHeader(encoded, 64, limits); err == nil {
		t.Fatal("an over-limit SenderCertificate was decoded")
	}
}

// The formula in OPC 10000-6 6.7.2.3, with the optional terms absent.
func TestMaxSenderCertificateSize(t *testing.T) {
	const chunk = 8192
	uri := len("urn:example:policy")
	got := MaxSenderCertificateSize(chunk, uri, 0, 0)
	want := chunk - (SecureConversationHeaderSize + 4 + 4 + 4 + CertificateThumbprintBytes + SequenceHeaderSize + 1) - uri
	if got != want {
		t.Fatalf("size = %d, want %d", got, want)
	}
	// Padding costs its own size byte as well as the padding itself.
	if signed := MaxSenderCertificateSize(chunk, uri, 16, 256); signed != want-16-256-1 {
		t.Fatalf("signed size = %d, want %d", signed, want-16-256-1)
	}
	// A chunk too small for the fixed overhead yields zero, never a negative.
	if got := MaxSenderCertificateSize(8, 0, 0, 0); got != 0 {
		t.Fatalf("tiny chunk gave %d", got)
	}
}

func TestSequenceHeaderRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoded, err := EncodeSequenceHeader(SequenceHeader{SequenceNumber: 51, RequestID: 7}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != SequenceHeaderSize {
		t.Fatalf("sequence header is %d bytes, want %d", len(encoded), SequenceHeaderSize)
	}
	decoded, err := DecodeSequenceHeader(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SequenceNumber != 51 || decoded.RequestID != 7 {
		t.Fatalf("sequence header = %+v", decoded)
	}
	if _, err := DecodeSequenceHeader(encoded[:5], limits); err == nil {
		t.Fatal("a truncated sequence header was accepted")
	}
}

// OPC 10000-6 6.7.2.4: incremented by exactly one for each chunk.
func TestSequenceValidatorRequiresExactIncrements(t *testing.T) {
	validator := NewSequenceValidator(SequenceNumberingLegacy)
	if err := validator.Accept(1); err != nil {
		t.Fatal(err)
	}
	if err := validator.Accept(2); err != nil {
		t.Fatal(err)
	}
	if err := validator.Accept(4); err == nil {
		t.Fatal("a skipped sequence number was accepted")
	}
	if err := validator.Accept(2); err == nil {
		t.Fatal("a repeated sequence number was accepted")
	}
}

func TestSequenceValidatorWrapRules(t *testing.T) {
	// Legacy: wrap only above UInt32.MaxValue - 1024, and only below 1024.
	legacy := NewSequenceValidator(SequenceNumberingLegacy)
	if err := legacy.Accept(legacySequenceWrapFloor + 1); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Accept(1024); err == nil {
		t.Fatal("legacy wrap accepted a value that is not below 1024")
	}
	if err := legacy.Accept(5); err != nil {
		t.Fatalf("legacy wrap below 1024 rejected: %v", err)
	}

	early := NewSequenceValidator(SequenceNumberingLegacy)
	if err := early.Accept(100); err != nil {
		t.Fatal(err)
	}
	if err := early.Accept(3); err == nil {
		t.Fatal("a wrap below the floor was accepted")
	}

	// Zero-based: start at 0, wrap only at UInt32.MaxValue, back to 0.
	zeroBased := NewSequenceValidator(SequenceNumberingZeroBased)
	if err := zeroBased.Accept(1); err == nil {
		t.Fatal("zero-based numbering accepted a non-zero start")
	}
	zeroBased = NewSequenceValidator(SequenceNumberingZeroBased)
	if err := zeroBased.Accept(0); err != nil {
		t.Fatal(err)
	}
	if err := zeroBased.Accept(1); err != nil {
		t.Fatal(err)
	}
	atMax := NewSequenceValidator(SequenceNumberingZeroBased)
	if err := atMax.Accept(0); err != nil {
		t.Fatal(err)
	}
	atMax.last = ^uint32(0)
	if err := atMax.Accept(0); err != nil {
		t.Fatalf("zero-based wrap rejected: %v", err)
	}
}

func TestSequenceValidatorNextFollowsItsOwnRules(t *testing.T) {
	legacy := NewSequenceValidator(SequenceNumberingLegacy)
	if got := legacy.Next(); got != 1 {
		t.Fatalf("first legacy number = %d, want 1", got)
	}
	if got := legacy.Next(); got != 2 {
		t.Fatalf("second legacy number = %d", got)
	}
	legacy.last = legacySequenceWrapFloor + 1
	if got := legacy.Next(); got != 0 {
		t.Fatalf("legacy wrap produced %d, want 0", got)
	}

	zeroBased := NewSequenceValidator(SequenceNumberingZeroBased)
	if got := zeroBased.Next(); got != 0 {
		t.Fatalf("first zero-based number = %d, want 0", got)
	}
	zeroBased.last = ^uint32(0)
	if got := zeroBased.Next(); got != 0 {
		t.Fatalf("zero-based wrap produced %d, want 0", got)
	}
}

// Only None is implemented; a mode the adapter cannot provide must be refused
// rather than accepted and then left unprotected.
func TestOnlyTheNoneSecurityModeIsAccepted(t *testing.T) {
	if err := RequireSupportedSecurityMode(SecurityModeNone); err != nil {
		t.Fatalf("None rejected: %v", err)
	}
	for _, mode := range []SecurityMode{SecurityModeSign, SecurityModeSignAndEncrypt} {
		err := RequireSupportedSecurityMode(mode)
		if err == nil {
			t.Fatalf("%s was accepted", mode)
		}
		if got := codecStatus(t, err); got != StatusBadSecurityModeRejected {
			t.Fatalf("status = %s, want Bad_SecurityModeRejected", got.Hex())
		}
		if !strings.Contains(err.Error(), mode.String()) {
			t.Fatalf("error did not name the mode: %v", err)
		}
	}
}
