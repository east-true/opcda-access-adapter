package opcua

import "fmt"

// OPC UA Secure Conversation (UASC) framing follows OPC 10000-6 clause 6.7.
// This slice implements the framing and its length rules. It deliberately does
// not bind the SecurityPolicy URI string or the per-policy
// LegacySequenceNumbers flag: both belong to a profile, and OPC 10000-7 governs
// profiles without listing them -- its clause 1 puts them in an online
// database. With no pinned document to check against, they are supplied by the
// caller rather than guessed.
// See ADR-0016 for why an unverified constant is not added from recollection.

// SecureConversationHeaderSize is the 12 bytes of OPC 10000-6 Table 57: a three
// byte MessageType, the IsFinal code, a UInt32 MessageSize that includes the
// header, and a UInt32 SecureChannelId.
const SecureConversationHeaderSize = 12

// SequenceHeaderSize is the 8 bytes of Table 60.
const SequenceHeaderSize = 8

// Length rules from OPC 10000-6 Table 58.
const (
	MaxSecurityPolicyURIBytes = 255
	// A thumbprint is the SHA1 hash of the DER encoded certificate.
	CertificateThumbprintBytes = 20
)

// SecureConversationHeader is OPC 10000-6 Table 57.
type SecureConversationHeader struct {
	Type            MessageType
	Chunk           byte
	Size            uint32
	SecureChannelID uint32
}

// EncodeSecureConversationHeader frames a chunk. bodySize covers everything
// after this 12 byte header, and the encoded MessageSize includes the header
// as Table 57 requires.
func EncodeSecureConversationHeader(header SecureConversationHeader, bodySize int, sendBufferSize uint32) ([]byte, error) {
	if !header.Type.IsSecureChannel() {
		return nil, uacpError(StatusBadTcpMessageTypeInvalid,
			"%s is not a secure conversation message type", header.Type)
	}
	switch header.Chunk {
	case ChunkFinal, ChunkIntermediate, ChunkAbort:
	default:
		return nil, uacpError(StatusBadTcpMessageTypeInvalid, "chunk code 0x%02X is not defined", header.Chunk)
	}
	// Table 57: the abort and intermediate codes are only meaningful for MSG.
	if header.Type != MessageTypeSecure && header.Chunk != ChunkFinal {
		return nil, uacpError(StatusBadTcpMessageTypeInvalid,
			"%s must use the final chunk code", header.Type)
	}
	if bodySize < 0 {
		return nil, uacpError(StatusBadTcpInternalError, "body size must not be negative")
	}
	total := int64(bodySize) + SecureConversationHeaderSize
	if total > int64(sendBufferSize) {
		return nil, uacpError(StatusBadTcpMessageTooLarge,
			"chunk of %d bytes exceeds the %d byte buffer", total, sendBufferSize)
	}
	encoded := []byte{header.Type[0], header.Type[1], header.Type[2], header.Chunk}
	encoded = append(encoded, byte(total), byte(total>>8), byte(total>>16), byte(total>>24))
	return append(encoded,
		byte(header.SecureChannelID), byte(header.SecureChannelID>>8),
		byte(header.SecureChannelID>>16), byte(header.SecureChannelID>>24),
	), nil
}

// DecodeSecureConversationHeader reuses the connection protocol validation for
// the first eight bytes, which Table 57 makes deliberately identical, then
// reads the SecureChannelId.
func DecodeSecureConversationHeader(data []byte, receiveBufferSize uint32) (SecureConversationHeader, error) {
	base, err := DecodeMessageHeader(data, receiveBufferSize)
	if err != nil {
		return SecureConversationHeader{}, err
	}
	if !base.Type.IsSecureChannel() {
		return SecureConversationHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"%s is not a secure conversation message type", base.Type)
	}
	if base.Size < SecureConversationHeaderSize {
		return SecureConversationHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"message size %d is smaller than the %d byte header", base.Size, SecureConversationHeaderSize)
	}
	if len(data) < SecureConversationHeaderSize {
		return SecureConversationHeader{}, uacpError(StatusBadTcpInternalError,
			"header needs %d bytes but %d are available", SecureConversationHeaderSize, len(data))
	}
	return SecureConversationHeader{
		Type:  base.Type,
		Chunk: base.Chunk,
		Size:  base.Size,
		SecureChannelID: uint32(data[8]) | uint32(data[9])<<8 |
			uint32(data[10])<<16 | uint32(data[11])<<24,
	}, nil
}

// AsymmetricSecurityHeader is OPC 10000-6 Table 58, used by OPN chunks. Each
// field is present only when its length says so; the clause allows 0 or -1 for
// an unspecified value and calls any other negative length invalid.
type AsymmetricSecurityHeader struct {
	SecurityPolicyURI             string
	SenderCertificate             []byte
	ReceiverCertificateThumbprint []byte
}

// SymmetricSecurityHeader is the TokenId that precedes MSG and CLO chunks.
type SymmetricSecurityHeader struct {
	TokenID uint32
}

// SequenceHeader is OPC 10000-6 Table 60.
type SequenceHeader struct {
	SequenceNumber uint32
	RequestID      uint32
}

// securityHeaderLength decodes one of Table 58's length fields. The clause is
// explicit that 0 and -1 both mean "not specified" and that any other negative
// value is invalid, and that the receiver shall close the channel when a length
// is invalid.
func securityHeaderLength(decoder *Decoder, bound int, label string) (int, error) {
	raw, err := decoder.ReadInt32()
	if err != nil {
		return 0, err
	}
	if raw == 0 || raw == nullLength {
		return 0, nil
	}
	if raw < 0 {
		return 0, uacpError(StatusBadSecurityChecksFailed,
			"%s length %d is invalid", label, raw)
	}
	if int64(raw) > int64(bound) {
		return 0, uacpError(StatusBadSecurityChecksFailed,
			"%s length %d exceeds the %d byte limit", label, raw, bound)
	}
	if int64(raw) > int64(decoder.Remaining()) {
		return 0, uacpError(StatusBadSecurityChecksFailed,
			"%s length %d exceeds the %d bytes remaining", label, raw, decoder.Remaining())
	}
	return int(raw), nil
}

// DecodeAsymmetricSecurityHeader reads Table 58 and returns how many bytes it
// consumed, so the caller can locate the sequence header that follows.
func DecodeAsymmetricSecurityHeader(body []byte, maxSenderCertificateBytes int, limits BinaryLimits) (AsymmetricSecurityHeader, int, error) {
	decoder, err := NewDecoder(body, limits)
	if err != nil {
		return AsymmetricSecurityHeader{}, 0, err
	}
	var header AsymmetricSecurityHeader

	uriLength, err := securityHeaderLength(decoder, MaxSecurityPolicyURIBytes, "SecurityPolicyUri")
	if err != nil {
		return AsymmetricSecurityHeader{}, 0, err
	}
	if uriLength > 0 {
		raw, takeErr := decoder.take(uriLength)
		if takeErr != nil {
			return AsymmetricSecurityHeader{}, 0, takeErr
		}
		// Table 58: a UTF-8 string without a null terminator.
		header.SecurityPolicyURI = string(raw)
	}

	certificateLength, err := securityHeaderLength(decoder, maxSenderCertificateBytes, "SenderCertificate")
	if err != nil {
		return AsymmetricSecurityHeader{}, 0, err
	}
	if certificateLength > 0 {
		raw, takeErr := decoder.take(certificateLength)
		if takeErr != nil {
			return AsymmetricSecurityHeader{}, 0, takeErr
		}
		header.SenderCertificate = append([]byte(nil), raw...)
	}

	thumbprintLength, err := securityHeaderLength(decoder, CertificateThumbprintBytes, "ReceiverCertificateThumbprint")
	if err != nil {
		return AsymmetricSecurityHeader{}, 0, err
	}
	if thumbprintLength > 0 {
		// Table 58 fixes the thumbprint at 20 bytes whenever it is present.
		if thumbprintLength != CertificateThumbprintBytes {
			return AsymmetricSecurityHeader{}, 0, uacpError(StatusBadSecurityChecksFailed,
				"ReceiverCertificateThumbprint length %d is not %d", thumbprintLength, CertificateThumbprintBytes)
		}
		raw, takeErr := decoder.take(thumbprintLength)
		if takeErr != nil {
			return AsymmetricSecurityHeader{}, 0, takeErr
		}
		header.ReceiverCertificateThumbprint = append([]byte(nil), raw...)
	}
	return header, len(body) - decoder.Remaining(), nil
}

// EncodeAsymmetricSecurityHeader writes Table 58. An absent field is written
// with a length of -1, which the clause accepts for "not specified".
func EncodeAsymmetricSecurityHeader(header AsymmetricSecurityHeader, maxSenderCertificateBytes int, limits BinaryLimits) ([]byte, error) {
	if len(header.SecurityPolicyURI) > MaxSecurityPolicyURIBytes {
		return nil, uacpError(StatusBadSecurityChecksFailed,
			"SecurityPolicyUri of %d bytes exceeds %d", len(header.SecurityPolicyURI), MaxSecurityPolicyURIBytes)
	}
	if len(header.SenderCertificate) > maxSenderCertificateBytes {
		return nil, uacpError(StatusBadSecurityChecksFailed,
			"SenderCertificate of %d bytes exceeds %d", len(header.SenderCertificate), maxSenderCertificateBytes)
	}
	if len(header.ReceiverCertificateThumbprint) != 0 &&
		len(header.ReceiverCertificateThumbprint) != CertificateThumbprintBytes {
		return nil, uacpError(StatusBadSecurityChecksFailed,
			"ReceiverCertificateThumbprint of %d bytes is not %d",
			len(header.ReceiverCertificateThumbprint), CertificateThumbprintBytes)
	}
	encoder, err := NewEncoder(limits)
	if err != nil {
		return nil, err
	}
	writeOptional := func(value []byte) {
		if len(value) == 0 {
			encoder.WriteInt32(nullLength)
			return
		}
		encoder.WriteInt32(int32(len(value)))
		encoder.write(value)
	}
	writeOptional([]byte(header.SecurityPolicyURI))
	writeOptional(header.SenderCertificate)
	writeOptional(header.ReceiverCertificateThumbprint)
	return encoder.Bytes()
}

// MaxSenderCertificateSize is the formula in OPC 10000-6 6.7.2.3. The optional
// terms are parameters rather than assumptions so a signed policy can supply
// them later; with no signing and no padding they are zero.
func MaxSenderCertificateSize(chunkSize int, securityPolicyURIBytes, paddingBytes, signatureBytes int) int {
	const fixedOverhead = SecureConversationHeaderSize + // header
		4 + // SecurityPolicyUriLength
		4 + // SenderCertificateLength
		4 + // ReceiverCertificateThumbprintLength
		CertificateThumbprintBytes +
		SequenceHeaderSize +
		1 // minimum body size
	available := chunkSize - fixedOverhead - securityPolicyURIBytes - paddingBytes - signatureBytes
	if paddingBytes > 0 {
		available-- // the PaddingSize byte itself
	}
	if available < 0 {
		return 0
	}
	return available
}

func EncodeSequenceHeader(header SequenceHeader, limits BinaryLimits) ([]byte, error) {
	encoder, err := NewEncoder(limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteUInt32(header.SequenceNumber)
	encoder.WriteUInt32(header.RequestID)
	return encoder.Bytes()
}

func DecodeSequenceHeader(body []byte, limits BinaryLimits) (SequenceHeader, error) {
	decoder, err := NewDecoder(body, limits)
	if err != nil {
		return SequenceHeader{}, err
	}
	number, err := decoder.ReadUInt32()
	if err != nil {
		return SequenceHeader{}, err
	}
	requestID, err := decoder.ReadUInt32()
	if err != nil {
		return SequenceHeader{}, err
	}
	return SequenceHeader{SequenceNumber: number, RequestID: requestID}, nil
}

// SequenceNumbering selects which rule set of OPC 10000-6 6.7.2.4 applies. The
// value is a property of the SecurityPolicy, which OPC 10000-7 leaves to the
// profile database rather than listing, so it is supplied by the caller rather
// than assumed here.
type SequenceNumbering int

const (
	// SequenceNumberingLegacy is LegacySequenceNumbers TRUE: the number shall
	// not wrap until it is greater than UInt32.MaxValue - 1024, and the first
	// number after the wrap shall be less than 1024.
	SequenceNumberingLegacy SequenceNumbering = iota
	// SequenceNumberingZeroBased is LegacySequenceNumbers FALSE: the number
	// starts at 0, wraps at UInt32.MaxValue, and restarts at 0.
	SequenceNumberingZeroBased
)

const legacySequenceWrapFloor uint32 = 4_294_966_271 // UInt32.MaxValue - 1024

// SequenceValidator enforces 6.7.2.4 for received chunks: the number is
// incremented by exactly one per chunk, and a wrap is only accepted where the
// selected rule set allows one.
type SequenceValidator struct {
	numbering SequenceNumbering
	started   bool
	last      uint32
}

func NewSequenceValidator(numbering SequenceNumbering) *SequenceValidator {
	return &SequenceValidator{numbering: numbering}
}

// Accept validates the next received sequence number.
func (v *SequenceValidator) Accept(number uint32) error {
	if !v.started {
		if v.numbering == SequenceNumberingZeroBased && number != 0 {
			return uacpError(StatusBadSecurityChecksFailed,
				"zero-based sequence numbering must start at 0, not %d", number)
		}
		v.started = true
		v.last = number
		return nil
	}
	if number == v.last+1 && number != 0 {
		v.last = number
		return nil
	}
	if v.isValidWrap(number) {
		v.last = number
		return nil
	}
	return uacpError(StatusBadSecurityChecksFailed,
		"sequence number %d does not follow %d", number, v.last)
}

func (v *SequenceValidator) isValidWrap(number uint32) bool {
	switch v.numbering {
	case SequenceNumberingZeroBased:
		// Wrap only at the maximum, and only back to 0.
		return v.last == ^uint32(0) && number == 0
	default:
		// Wrap only above the floor, and only to a value below 1024.
		return v.last > legacySequenceWrapFloor && number < 1024
	}
}

// Next produces the sender's next sequence number under the same rules.
func (v *SequenceValidator) Next() uint32 {
	if !v.started {
		v.started = true
		if v.numbering == SequenceNumberingZeroBased {
			v.last = 0
		} else {
			v.last = 1
		}
		return v.last
	}
	switch v.numbering {
	case SequenceNumberingZeroBased:
		if v.last == ^uint32(0) {
			v.last = 0
			return v.last
		}
	default:
		if v.last > legacySequenceWrapFloor {
			v.last = 0
			return v.last
		}
	}
	v.last++
	return v.last
}

// SecurityMode is MessageSecurityMode. The values are the wire values of
// OPC 10000-4 Table 139, not an arbitrary ordering: Invalid is deliberately 0
// so that an unset field can never be mistaken for a deliberate choice of no
// security.
//
// Only None is implemented, and it is for local interoperability testing:
// ADR-0016 forbids describing it as production ready.
type SecurityMode uint32

const (
	SecurityModeInvalid        SecurityMode = 0
	SecurityModeNone           SecurityMode = 1
	SecurityModeSign           SecurityMode = 2
	SecurityModeSignAndEncrypt SecurityMode = 3
)

func (m SecurityMode) String() string {
	switch m {
	case SecurityModeInvalid:
		return "Invalid"
	case SecurityModeNone:
		return "None"
	case SecurityModeSign:
		return "Sign"
	case SecurityModeSignAndEncrypt:
		return "SignAndEncrypt"
	default:
		return fmt.Sprintf("Unknown(%d)", uint32(m))
	}
}

// RequireSupportedSecurityMode refuses any mode this adapter cannot actually
// provide, rather than accepting a channel it would then fail to protect.
// Table 139 states that Invalid "will always be rejected".
func RequireSupportedSecurityMode(mode SecurityMode) error {
	if mode == SecurityModeNone {
		return nil
	}
	return uacpError(StatusBadSecurityModeRejected,
		"security mode %s is not implemented", mode)
}

// Security status codes, from the OPC Foundation StatusCode list.
const (
	// StatusBadSecurityChecksFailed is what a receiver reports when a security
	// header is malformed.
	StatusBadSecurityChecksFailed   StatusCode = 0x80130000
	StatusBadSecurityModeRejected   StatusCode = 0x80540000
	StatusBadSecurityPolicyRejected StatusCode = 0x80550000
)
