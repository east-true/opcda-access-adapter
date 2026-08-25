package opcua

import (
	"bytes"
	"fmt"
)

// The OPC UA Connection Protocol (UACP) framing follows OPC 10000-6 clause 7.1.
// Every rule here is transcribed from that clause: an 8 byte header of a three
// byte ASCII MessageType, a one byte reserved/IsFinal code, and a UInt32
// MessageSize that includes the header itself (Table 73); Hello, Acknowledge,
// and Error bodies (Tables 74, 75, 76); and the buffer negotiation and
// validation rules in 7.1.2 and 7.1.3.

// Status codes the connection protocol reports, from the OPC Foundation
// StatusCode list.
const (
	StatusBadTcpServerTooBusy         StatusCode = 0x807D0000
	StatusBadTcpMessageTypeInvalid    StatusCode = 0x807E0000
	StatusBadTcpSecureChannelUnknown  StatusCode = 0x807F0000
	StatusBadTcpMessageTooLarge       StatusCode = 0x80800000
	StatusBadTcpNotEnoughResources    StatusCode = 0x80810000
	StatusBadTcpInternalError         StatusCode = 0x80820000
	StatusBadTcpEndpointURLInvalid    StatusCode = 0x80830000
	StatusBadProtocolVersionUnsupport StatusCode = 0x80BE0000
	StatusBadRequestTooLarge          StatusCode = 0x80B80000
	StatusBadResponseTooLarge         StatusCode = 0x80B90000
	StatusBadConnectionClosed         StatusCode = 0x80AE0000
)

// MessageType is the three byte ASCII code from OPC 10000-6 Table 73. The
// connection protocol defines HEL, ACK, ERR, and RHE; the SecureChannel layer
// defines MSG, OPN, and CLO, which this layer must accept and pass through.
type MessageType [3]byte

var (
	MessageTypeHello        = MessageType{'H', 'E', 'L'}
	MessageTypeAcknowledge  = MessageType{'A', 'C', 'K'}
	MessageTypeError        = MessageType{'E', 'R', 'R'}
	MessageTypeReverseHello = MessageType{'R', 'H', 'E'}
	MessageTypeSecure       = MessageType{'M', 'S', 'G'}
	MessageTypeOpenChannel  = MessageType{'O', 'P', 'N'}
	MessageTypeCloseChannel = MessageType{'C', 'L', 'O'}
)

func (t MessageType) String() string { return string(t[:]) }

// IsConnectionProtocol reports whether this layer owns the message body.
func (t MessageType) IsConnectionProtocol() bool {
	switch t {
	case MessageTypeHello, MessageTypeAcknowledge, MessageTypeError, MessageTypeReverseHello:
		return true
	default:
		return false
	}
}

// IsSecureChannel reports the types this layer frames but does not interpret.
func (t MessageType) IsSecureChannel() bool {
	switch t {
	case MessageTypeSecure, MessageTypeOpenChannel, MessageTypeCloseChannel:
		return true
	default:
		return false
	}
}

// Chunk codes from OPC 10000-6 Table 57. A connection protocol message always
// carries 'F'.
const (
	ChunkIntermediate byte = 'C'
	ChunkFinal        byte = 'F'
	ChunkAbort        byte = 'A'
)

// HeaderSize is the 8 bytes MessageSize includes, per Table 73.
const HeaderSize = 8

// UACP constants from OPC 10000-6 clause 7.1.
const (
	// ProtocolVersion for this version of the standard is 0.
	ProtocolVersion uint32 = 0
	// MinimumBufferSize is the floor for a non-ECC SecurityPolicy. The 1024
	// byte ECC floor is not offered because no ECC policy is implemented.
	MinimumBufferSize uint32 = 8192
	// MaxEndpointURLBytes bounds the encoded EndpointUrl and the Error Reason;
	// the clause requires both to stay under 4096 bytes.
	MaxEndpointURLBytes = 4096
	MaxErrorReasonBytes = 4096
)

// MessageHeader is OPC 10000-6 Table 73.
type MessageHeader struct {
	Type MessageType
	// Chunk is the reserved byte. For a connection protocol message the clause
	// requires 'F'; for a SecureChannel message it is the IsFinal code.
	Chunk byte
	// Size includes the 8 header bytes.
	Size uint32
}

// BodySize is the message length after the header.
func (h MessageHeader) BodySize() int { return int(h.Size) - HeaderSize }

func uacpError(status StatusCode, format string, args ...any) *CodecError {
	return &CodecError{Status: status, Message: fmt.Sprintf(format, args...)}
}

// EncodeMessageHeader writes a header for a body of the given size.
func EncodeMessageHeader(messageType MessageType, chunk byte, bodySize int, receiveBufferSize uint32) ([]byte, error) {
	if bodySize < 0 {
		return nil, uacpError(StatusBadTcpInternalError, "message body size must not be negative")
	}
	total := int64(bodySize) + HeaderSize
	if total > int64(receiveBufferSize) {
		return nil, uacpError(StatusBadTcpMessageTooLarge,
			"message of %d bytes exceeds the %d byte buffer", total, receiveBufferSize)
	}
	header := make([]byte, 0, HeaderSize)
	header = append(header, messageType[0], messageType[1], messageType[2], chunk)
	return append(header,
		byte(total), byte(total>>8), byte(total>>16), byte(total>>24),
	), nil
}

// DecodeMessageHeader validates a header against the negotiated buffer size.
// OPC 10000-6 7.1.2.2 requires the connection protocol layer to verify the
// MessageType and that MessageSize fits the negotiated ReceiveBufferSize before
// anything is passed to the SecureChannel layer.
func DecodeMessageHeader(data []byte, receiveBufferSize uint32) (MessageHeader, error) {
	if len(data) < HeaderSize {
		return MessageHeader{}, uacpError(StatusBadTcpInternalError,
			"header needs %d bytes but %d are available", HeaderSize, len(data))
	}
	header := MessageHeader{
		Type:  MessageType{data[0], data[1], data[2]},
		Chunk: data[3],
		Size:  uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24,
	}
	if !header.Type.IsConnectionProtocol() && !header.Type.IsSecureChannel() {
		return MessageHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"message type %q is not accepted", sanitiseType(header.Type))
	}
	// A size below the header is malformed; a size above the negotiated buffer
	// must be refused before any body is read.
	if header.Size < HeaderSize {
		return MessageHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"message size %d is smaller than the %d byte header", header.Size, HeaderSize)
	}
	if header.Size > receiveBufferSize {
		return MessageHeader{}, uacpError(StatusBadTcpMessageTooLarge,
			"message size %d exceeds the negotiated %d byte buffer", header.Size, receiveBufferSize)
	}
	if header.Type.IsConnectionProtocol() && header.Chunk != ChunkFinal {
		return MessageHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"connection protocol message %s must use the final chunk code", header.Type)
	}
	if header.Type.IsSecureChannel() &&
		header.Chunk != ChunkFinal && header.Chunk != ChunkIntermediate && header.Chunk != ChunkAbort {
		return MessageHeader{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"chunk code 0x%02X is not defined", header.Chunk)
	}
	return header, nil
}

// sanitiseType renders an unexpected message type without echoing raw bytes
// back into a log line.
func sanitiseType(messageType MessageType) string {
	rendered := make([]byte, 0, 3)
	for _, character := range messageType {
		if character < 0x20 || character > 0x7E {
			rendered = append(rendered, '?')
			continue
		}
		rendered = append(rendered, character)
	}
	return string(rendered)
}

// Hello is OPC 10000-6 Table 74.
type Hello struct {
	ProtocolVersion   uint32
	ReceiveBufferSize uint32
	SendBufferSize    uint32
	MaxMessageSize    uint32
	MaxChunkCount     uint32
	EndpointURL       string
}

// Acknowledge is OPC 10000-6 Table 75.
type Acknowledge struct {
	ProtocolVersion   uint32
	ReceiveBufferSize uint32
	SendBufferSize    uint32
	MaxMessageSize    uint32
	MaxChunkCount     uint32
}

// ProtocolError is OPC 10000-6 Table 76.
type ProtocolError struct {
	Error  StatusCode
	Reason string
}

// DecodeHello reads a Hello body and applies the clause's validation: a
// protocol version the server cannot serve, a buffer below the floor, or an
// over-long EndpointUrl each have their own status code.
func DecodeHello(body []byte, limits BinaryLimits) (Hello, error) {
	decoder, err := NewDecoder(body, limits)
	if err != nil {
		return Hello{}, err
	}
	var hello Hello
	for _, field := range []*uint32{
		&hello.ProtocolVersion, &hello.ReceiveBufferSize, &hello.SendBufferSize,
		&hello.MaxMessageSize, &hello.MaxChunkCount,
	} {
		value, readErr := decoder.ReadUInt32()
		if readErr != nil {
			return Hello{}, readErr
		}
		*field = value
	}
	url, isNull, err := decoder.ReadString()
	if err != nil {
		return Hello{}, err
	}
	if !isNull {
		hello.EndpointURL = url
	}
	if !decoder.Done() {
		return Hello{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"Hello carried %d unexpected trailing bytes", decoder.Remaining())
	}
	if hello.ProtocolVersion > ProtocolVersion {
		return Hello{}, uacpError(StatusBadProtocolVersionUnsupport,
			"protocol version %d is not supported", hello.ProtocolVersion)
	}
	if hello.ReceiveBufferSize < MinimumBufferSize || hello.SendBufferSize < MinimumBufferSize {
		return Hello{}, uacpError(StatusBadTcpMessageTooLarge,
			"buffer sizes must be at least %d bytes", MinimumBufferSize)
	}
	if len(hello.EndpointURL) >= MaxEndpointURLBytes {
		return Hello{}, uacpError(StatusBadTcpEndpointURLInvalid,
			"EndpointUrl of %d bytes is not under %d", len(hello.EndpointURL), MaxEndpointURLBytes)
	}
	return hello, nil
}

func EncodeHello(hello Hello, limits BinaryLimits) ([]byte, error) {
	if len(hello.EndpointURL) >= MaxEndpointURLBytes {
		return nil, uacpError(StatusBadTcpEndpointURLInvalid,
			"EndpointUrl of %d bytes is not under %d", len(hello.EndpointURL), MaxEndpointURLBytes)
	}
	encoder, err := NewEncoder(limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteUInt32(hello.ProtocolVersion)
	encoder.WriteUInt32(hello.ReceiveBufferSize)
	encoder.WriteUInt32(hello.SendBufferSize)
	encoder.WriteUInt32(hello.MaxMessageSize)
	encoder.WriteUInt32(hello.MaxChunkCount)
	encoder.WriteString(hello.EndpointURL)
	return encoder.Bytes()
}

func EncodeAcknowledge(ack Acknowledge, limits BinaryLimits) ([]byte, error) {
	encoder, err := NewEncoder(limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteUInt32(ack.ProtocolVersion)
	encoder.WriteUInt32(ack.ReceiveBufferSize)
	encoder.WriteUInt32(ack.SendBufferSize)
	encoder.WriteUInt32(ack.MaxMessageSize)
	encoder.WriteUInt32(ack.MaxChunkCount)
	return encoder.Bytes()
}

func DecodeAcknowledge(body []byte, limits BinaryLimits) (Acknowledge, error) {
	decoder, err := NewDecoder(body, limits)
	if err != nil {
		return Acknowledge{}, err
	}
	var ack Acknowledge
	for _, field := range []*uint32{
		&ack.ProtocolVersion, &ack.ReceiveBufferSize, &ack.SendBufferSize,
		&ack.MaxMessageSize, &ack.MaxChunkCount,
	} {
		value, readErr := decoder.ReadUInt32()
		if readErr != nil {
			return Acknowledge{}, readErr
		}
		*field = value
	}
	if !decoder.Done() {
		return Acknowledge{}, uacpError(StatusBadTcpMessageTypeInvalid,
			"Acknowledge carried %d unexpected trailing bytes", decoder.Remaining())
	}
	return ack, nil
}

// EncodeProtocolError truncates an over-long Reason rather than refusing to
// report the error: the clause caps Reason at 4096 bytes and lets a receiver
// ignore anything longer, and failing to send the error would be worse.
func EncodeProtocolError(protocolError ProtocolError, limits BinaryLimits) ([]byte, error) {
	reason := protocolError.Reason
	if len(reason) > MaxErrorReasonBytes {
		reason = truncateUTF8(reason, MaxErrorReasonBytes)
	}
	encoder, err := NewEncoder(limits)
	if err != nil {
		return nil, err
	}
	encoder.WriteUInt32(uint32(protocolError.Error))
	encoder.WriteString(reason)
	return encoder.Bytes()
}

func DecodeProtocolError(body []byte, limits BinaryLimits) (ProtocolError, error) {
	decoder, err := NewDecoder(body, limits)
	if err != nil {
		return ProtocolError{}, err
	}
	code, err := decoder.ReadUInt32()
	if err != nil {
		return ProtocolError{}, err
	}
	reason, isNull, err := decoder.ReadString()
	if err != nil {
		return ProtocolError{}, err
	}
	if isNull {
		reason = ""
	}
	// The clause tells a receiver to ignore a Reason longer than 4096 bytes.
	if len(reason) > MaxErrorReasonBytes {
		reason = ""
	}
	return ProtocolError{Error: StatusCode(code), Reason: reason}, nil
}

// truncateUTF8 cuts to at most limit bytes without splitting a rune.
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8Boundary(value[cut]) {
		cut--
	}
	return value[:cut]
}

func utf8Boundary(b byte) bool { return b&0xC0 != 0x80 }

// NegotiateAcknowledge applies OPC 10000-6 Table 75. The server's receive
// buffer may not exceed what the client says it will send, the server's send
// buffer may not exceed what the client can receive, and neither may fall below
// the floor. A zero MaxMessageSize or MaxChunkCount from either side means "no
// limit", so the negotiated value is the other side's limit.
func NegotiateAcknowledge(hello Hello, serverReceive, serverSend, serverMaxMessage, serverMaxChunks uint32) (Acknowledge, error) {
	if serverReceive < MinimumBufferSize || serverSend < MinimumBufferSize {
		return Acknowledge{}, uacpError(StatusBadTcpInternalError,
			"server buffer sizes must be at least %d bytes", MinimumBufferSize)
	}
	ack := Acknowledge{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: minUint32(serverReceive, hello.SendBufferSize),
		SendBufferSize:    minUint32(serverSend, hello.ReceiveBufferSize),
		MaxMessageSize:    tighterLimit(serverMaxMessage, hello.MaxMessageSize),
		MaxChunkCount:     tighterLimit(serverMaxChunks, hello.MaxChunkCount),
	}
	if ack.ReceiveBufferSize < MinimumBufferSize || ack.SendBufferSize < MinimumBufferSize {
		return Acknowledge{}, uacpError(StatusBadTcpMessageTooLarge,
			"negotiated buffer sizes fell below %d bytes", MinimumBufferSize)
	}
	return ack, nil
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// tighterLimit combines two limits where zero means "no limit".
func tighterLimit(a, b uint32) uint32 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	default:
		return minUint32(a, b)
	}
}

// ChunkAccumulator enforces the negotiated chunk and message bounds while a
// multi-chunk message is being received. OPC 10000-6 6.7.3 requires a message
// that exceeds either bound to be refused rather than buffered.
type ChunkAccumulator struct {
	maxChunks  uint32
	maxMessage uint32
	tooLarge   StatusCode

	chunks uint32
	body   bytes.Buffer
}

// NewChunkAccumulator bounds an incoming message. A zero maxChunks or
// maxMessage means the peer declared no limit, in which case the accumulator
// still refuses to grow past the other bound.
func NewChunkAccumulator(maxChunks, maxMessage uint32, tooLarge StatusCode) *ChunkAccumulator {
	return &ChunkAccumulator{maxChunks: maxChunks, maxMessage: maxMessage, tooLarge: tooLarge}
}

// Append adds one chunk body. It returns an error before copying anything that
// would breach a bound.
func (a *ChunkAccumulator) Append(chunk []byte) error {
	if a.maxChunks != 0 && a.chunks+1 > a.maxChunks {
		return uacpError(a.tooLarge, "message exceeds the negotiated %d chunk limit", a.maxChunks)
	}
	if a.maxMessage != 0 && uint64(a.body.Len())+uint64(len(chunk)) > uint64(a.maxMessage) {
		return uacpError(a.tooLarge, "message exceeds the negotiated %d byte limit", a.maxMessage)
	}
	a.chunks++
	a.body.Write(chunk)
	return nil
}

func (a *ChunkAccumulator) Chunks() uint32 { return a.chunks }
func (a *ChunkAccumulator) Body() []byte   { return a.body.Bytes() }

// Reset discards a message, which is what a receiver does when the sender
// aborts with the 'A' chunk code.
func (a *ChunkAccumulator) Reset() {
	a.chunks = 0
	a.body.Reset()
}
