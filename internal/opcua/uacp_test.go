package opcua

import (
	"bytes"
	"strings"
	"testing"
)

const testBuffer = uint32(65536)

// OPC 10000-6 Table 73: MessageSize includes the 8 header bytes and is a
// little-endian UInt32.
func TestMessageHeaderLayout(t *testing.T) {
	header, err := EncodeMessageHeader(MessageTypeHello, ChunkFinal, 4, testBuffer)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'H', 'E', 'L', 'F', 12, 0, 0, 0}
	if !bytes.Equal(header, want) {
		t.Fatalf("header % X, want % X", header, want)
	}
	decoded, err := DecodeMessageHeader(header, testBuffer)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != MessageTypeHello || decoded.Chunk != ChunkFinal || decoded.Size != 12 {
		t.Fatalf("header = %+v", decoded)
	}
	if decoded.BodySize() != 4 {
		t.Fatalf("body size = %d, want 4", decoded.BodySize())
	}
}

func TestMessageHeaderRejectsUnknownAndMalformed(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		buffer uint32
		status StatusCode
	}{
		{"short header", []byte{'H', 'E', 'L'}, testBuffer, StatusBadTcpInternalError},
		{"unknown type", []byte{'X', 'X', 'X', 'F', 8, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
		{"size below header", []byte{'H', 'E', 'L', 'F', 7, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
		// 7.1.2.2: the size must be checked against the negotiated buffer
		// before any body is read.
		{"size beyond buffer", []byte{'M', 'S', 'G', 'F', 0, 0, 1, 0}, 8192, StatusBadTcpMessageTooLarge},
		{"non-final connection message", []byte{'H', 'E', 'L', 'C', 8, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
		{"undefined chunk code", []byte{'M', 'S', 'G', 'Z', 8, 0, 0, 0}, testBuffer, StatusBadTcpMessageTypeInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeMessageHeader(testCase.data, testCase.buffer)
			if err == nil {
				t.Fatal("malformed header accepted")
			}
			if got := codecStatus(t, err); got != testCase.status {
				t.Fatalf("status = %s, want %s", got.Hex(), testCase.status.Hex())
			}
		})
	}
}

// The SecureChannel types must be framed and passed through, with all three
// chunk codes accepted.
func TestMessageHeaderPassesSecureChannelTypes(t *testing.T) {
	for _, messageType := range []MessageType{MessageTypeSecure, MessageTypeOpenChannel, MessageTypeCloseChannel} {
		for _, chunk := range []byte{ChunkFinal, ChunkIntermediate, ChunkAbort} {
			data := []byte{messageType[0], messageType[1], messageType[2], chunk, 8, 0, 0, 0}
			header, err := DecodeMessageHeader(data, testBuffer)
			if err != nil {
				t.Fatalf("%s/%c rejected: %v", messageType, chunk, err)
			}
			if !header.Type.IsSecureChannel() || header.Type.IsConnectionProtocol() {
				t.Fatalf("%s was classified wrongly", messageType)
			}
		}
	}
}

func TestEncodeMessageHeaderRefusesAnOversizedMessage(t *testing.T) {
	_, err := EncodeMessageHeader(MessageTypeSecure, ChunkFinal, int(testBuffer), testBuffer)
	if err == nil {
		t.Fatal("a message beyond the buffer was framed")
	}
	if got := codecStatus(t, err); got != StatusBadTcpMessageTooLarge {
		t.Fatalf("status = %s", got.Hex())
	}
}

func helloFixture() Hello {
	return Hello{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		MaxMessageSize:    1 << 20,
		MaxChunkCount:     32,
		EndpointURL:       "opc.tcp://127.0.0.1:4840",
	}
}

func TestHelloRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoded, err := EncodeHello(helloFixture(), limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeHello(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != helloFixture() {
		t.Fatalf("Hello = %+v", decoded)
	}
}

func TestHelloValidation(t *testing.T) {
	limits := DefaultBinaryLimits()
	cases := []struct {
		name   string
		mutate func(*Hello)
		status StatusCode
	}{
		// The clause rejects a version the server cannot serve.
		{"future protocol version", func(h *Hello) { h.ProtocolVersion = ProtocolVersion + 1 }, StatusBadProtocolVersionUnsupport},
		{"receive buffer below the floor", func(h *Hello) { h.ReceiveBufferSize = MinimumBufferSize - 1 }, StatusBadTcpMessageTooLarge},
		{"send buffer below the floor", func(h *Hello) { h.SendBufferSize = MinimumBufferSize - 1 }, StatusBadTcpMessageTooLarge},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			hello := helloFixture()
			testCase.mutate(&hello)
			encoded, err := EncodeHello(hello, limits)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeHello(encoded, limits)
			if err == nil {
				t.Fatal("an invalid Hello was accepted")
			}
			if got := codecStatus(t, err); got != testCase.status {
				t.Fatalf("status = %s, want %s", got.Hex(), testCase.status.Hex())
			}
		})
	}
}

// The clause requires the encoded EndpointUrl to stay under 4096 bytes.
func TestHelloRejectsAnOverlongEndpointURL(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxStringBytes = MaxEndpointURLBytes + 16
	hello := helloFixture()
	hello.EndpointURL = strings.Repeat("u", MaxEndpointURLBytes)

	if _, err := EncodeHello(hello, limits); err == nil {
		t.Fatal("an over-long EndpointUrl was encoded")
	} else if got := codecStatus(t, err); got != StatusBadTcpEndpointURLInvalid {
		t.Fatalf("status = %s", got.Hex())
	}

	// A peer can still send one, so the decoder must refuse it too.
	encoder, err := NewEncoder(limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteUInt32(hello.ProtocolVersion)
	encoder.WriteUInt32(hello.ReceiveBufferSize)
	encoder.WriteUInt32(hello.SendBufferSize)
	encoder.WriteUInt32(hello.MaxMessageSize)
	encoder.WriteUInt32(hello.MaxChunkCount)
	encoder.WriteString(hello.EndpointURL)
	body, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHello(body, limits); err == nil {
		t.Fatal("an over-long EndpointUrl was decoded")
	} else if got := codecStatus(t, err); got != StatusBadTcpEndpointURLInvalid {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestHelloAndAcknowledgeRejectTrailingBytes(t *testing.T) {
	limits := DefaultBinaryLimits()
	hello, err := EncodeHello(helloFixture(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHello(append(hello, 0), limits); err == nil {
		t.Fatal("a Hello with trailing bytes was accepted")
	}

	ack, err := EncodeAcknowledge(Acknowledge{ProtocolVersion: ProtocolVersion,
		ReceiveBufferSize: 65536, SendBufferSize: 65536}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAcknowledge(append(ack, 0), limits); err == nil {
		t.Fatal("an Acknowledge with trailing bytes was accepted")
	}
}

func TestHelloRejectsTruncatedBodies(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoded, err := EncodeHello(helloFixture(), limits)
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut < len(encoded); cut++ {
		if _, err := DecodeHello(encoded[:cut], limits); err == nil {
			t.Fatalf("a Hello truncated to %d bytes was accepted", cut)
		}
	}
}

// OPC 10000-6 Table 75: the server's receive buffer may not exceed what the
// client will send, and its send buffer may not exceed what the client can
// receive.
func TestNegotiationClampsBuffersToThePeer(t *testing.T) {
	hello := helloFixture()
	hello.SendBufferSize = 16384
	hello.ReceiveBufferSize = 32768

	ack, err := NegotiateAcknowledge(hello, 65536, 65536, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ack.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %d", ack.ProtocolVersion)
	}
	if ack.ReceiveBufferSize != 16384 {
		t.Fatalf("receive buffer = %d, want the client's send buffer", ack.ReceiveBufferSize)
	}
	if ack.SendBufferSize != 32768 {
		t.Fatalf("send buffer = %d, want the client's receive buffer", ack.SendBufferSize)
	}
}

// A zero MaxMessageSize or MaxChunkCount means "no limit", so the other side's
// limit is the one that applies.
// OPC 10000-6 Tables 74 and 75 give MaxMessageSize and MaxChunkCount opposite
// meanings in each direction. The Hello's are the largest response the client
// will accept; the Acknowledge's are "the maximum size for any request Message"
// and "the maximum number of chunks in any request Message" -- what this server
// will accept. So neither is a negotiation between the two, and tightening one
// by the other would tell a client that asking for small responses had shrunk
// what it may send.
//
// The buffer sizes are the negotiated pair, and Table 75 says how: each "shall
// not be larger than" its counterpart in the Hello.
func TestTheAcknowledgeAnnouncesWhatTheServerAccepts(t *testing.T) {
	hello := helloFixture()
	hello.MaxMessageSize = 4096
	hello.MaxChunkCount = 8

	ack, err := NegotiateAcknowledge(hello, 65536, 65536, 1<<20, 32)
	if err != nil {
		t.Fatal(err)
	}
	// The server's own request bounds, untouched by the client's response
	// bounds even though the client's are tighter.
	if ack.MaxMessageSize != 1<<20 {
		t.Fatalf("max message = %d, want the server's request limit", ack.MaxMessageSize)
	}
	if ack.MaxChunkCount != 32 {
		t.Fatalf("max chunks = %d, want the server's request limit", ack.MaxChunkCount)
	}
	// The buffers are negotiated: neither exceeds what the other side asked
	// for.
	if ack.ReceiveBufferSize > hello.SendBufferSize {
		t.Fatalf("receive buffer %d exceeds the client's send buffer %d",
			ack.ReceiveBufferSize, hello.SendBufferSize)
	}
	if ack.SendBufferSize > hello.ReceiveBufferSize {
		t.Fatalf("send buffer %d exceeds the client's receive buffer %d",
			ack.SendBufferSize, hello.ReceiveBufferSize)
	}

	// A server that imposes no request bound announces none, whatever the
	// client said about responses.
	ack, err = NegotiateAcknowledge(hello, 65536, 65536, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ack.MaxMessageSize != 0 || ack.MaxChunkCount != 0 {
		t.Fatalf("max message = %d, max chunks = %d, want no request limit",
			ack.MaxMessageSize, ack.MaxChunkCount)
	}
}

func TestNegotiationRefusesBuffersBelowTheFloor(t *testing.T) {
	hello := helloFixture()
	if _, err := NegotiateAcknowledge(hello, MinimumBufferSize-1, 65536, 0, 0); err == nil {
		t.Fatal("a server receive buffer below the floor was accepted")
	}
	if _, err := NegotiateAcknowledge(hello, 65536, MinimumBufferSize-1, 0, 0); err == nil {
		t.Fatal("a server send buffer below the floor was accepted")
	}
}

func TestProtocolErrorRoundTripAndReasonBound(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxStringBytes = MaxErrorReasonBytes + 64

	encoded, err := EncodeProtocolError(ProtocolError{
		Error: StatusBadTcpEndpointURLInvalid, Reason: "unknown endpoint",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProtocolError(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Error != StatusBadTcpEndpointURLInvalid || decoded.Reason != "unknown endpoint" {
		t.Fatalf("error = %+v", decoded)
	}

	// An over-long Reason is truncated rather than suppressing the error, and
	// truncation never splits a rune.
	long := strings.Repeat("가", MaxErrorReasonBytes)
	encoded, err = EncodeProtocolError(ProtocolError{Error: StatusBadTcpInternalError, Reason: long}, limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeProtocolError(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Error != StatusBadTcpInternalError {
		t.Fatalf("truncation lost the error code: %s", decoded.Error.Hex())
	}
	if len(decoded.Reason) > MaxErrorReasonBytes {
		t.Fatalf("reason kept %d bytes", len(decoded.Reason))
	}
	if !isValidUTF8(decoded.Reason) {
		t.Fatal("truncation split a rune")
	}
}

func isValidUTF8(value string) bool {
	for _, character := range value {
		if character == '�' {
			return false
		}
	}
	return true
}

// OPC 10000-6 6.7.3: a message beyond the negotiated chunk or size bound is
// refused rather than buffered.
func TestChunkAccumulatorEnforcesNegotiatedBounds(t *testing.T) {
	accumulator := NewChunkAccumulator(2, 16, StatusBadRequestTooLarge)
	if err := accumulator.Append([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Append([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if accumulator.Chunks() != 2 || !bytes.Equal(accumulator.Body(), []byte("12345678abcdefgh")) {
		t.Fatalf("chunks = %d body = %q", accumulator.Chunks(), accumulator.Body())
	}
	err := accumulator.Append([]byte("x"))
	if err == nil {
		t.Fatal("a third chunk passed a two chunk limit")
	}
	if got := codecStatus(t, err); got != StatusBadRequestTooLarge {
		t.Fatalf("status = %s", got.Hex())
	}

	// The size bound is checked before anything is copied.
	sized := NewChunkAccumulator(0, 8, StatusBadResponseTooLarge)
	if err := sized.Append([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if err := sized.Append([]byte("9")); err == nil {
		t.Fatal("a chunk past the byte bound was appended")
	}
	if sized.Body() == nil || len(sized.Body()) != 8 {
		t.Fatalf("a refused chunk changed the body: %q", sized.Body())
	}

	// Zero on both bounds means the peer declared no limit.
	unlimited := NewChunkAccumulator(0, 0, StatusBadRequestTooLarge)
	for count := 0; count < 64; count++ {
		if err := unlimited.Append([]byte("x")); err != nil {
			t.Fatalf("chunk %d refused with no declared limit: %v", count, err)
		}
	}

	// An abort discards the message.
	accumulator.Reset()
	if accumulator.Chunks() != 0 || len(accumulator.Body()) != 0 {
		t.Fatal("reset did not discard the message")
	}
}
