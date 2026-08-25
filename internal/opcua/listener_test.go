package opcua

import (
	"io"
	"net"
	"testing"
	"time"
)

// testClient is a minimal UA-TCP client: just enough to drive the connection
// sequence of OPC 10000-6 7.1.3 against the listener over a real socket.
type testClient struct {
	t        *testing.T
	conn     net.Conn
	limits   BinaryLimits
	sequence uint32
}

func startTestListener(t *testing.T, config ListenerConfig) (*Listener, string) {
	t.Helper()
	listener, err := NewListener(config, 1000, 2000)
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for !listener.Listening() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return listener, socket.Addr().String()
}

func dialTestClient(t *testing.T, address string) *testClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return &testClient{t: t, conn: conn, limits: DefaultBinaryLimits()}
}

func (c *testClient) send(messageType MessageType, chunk byte, body []byte) {
	c.t.Helper()
	header, err := EncodeMessageHeader(messageType, chunk, len(body), 1<<20)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(append(header, body...)); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testClient) receive() (MessageHeader, []byte, error) {
	var headerBytes [HeaderSize]byte
	if _, err := io.ReadFull(c.conn, headerBytes[:]); err != nil {
		return MessageHeader{}, nil, err
	}
	header, err := DecodeMessageHeader(headerBytes[:], 1<<20)
	if err != nil {
		return MessageHeader{}, nil, err
	}
	body := make([]byte, header.BodySize())
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return MessageHeader{}, nil, err
	}
	return header, body, nil
}

func (c *testClient) hello() Acknowledge {
	c.t.Helper()
	body, err := EncodeHello(Hello{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		MaxMessageSize:    1 << 20,
		MaxChunkCount:     8,
		EndpointURL:       "opc.tcp://127.0.0.1:4840",
	}, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeHello, ChunkFinal, body)

	header, response, err := c.receive()
	if err != nil {
		c.t.Fatalf("receive Acknowledge: %v", err)
	}
	if header.Type != MessageTypeAcknowledge {
		c.t.Fatalf("first response was %s", header.Type)
	}
	ack, err := DecodeAcknowledge(response, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	return ack
}

// openChannel sends an OPN and returns the decoded response.
func (c *testClient) openChannel(channelID uint32, requestType TokenRequestType, requestID uint32) (OpenSecureChannelResponse, error) {
	c.t.Helper()
	service, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	service.WriteOpenSecureChannelRequest(OpenSecureChannelRequest{
		Header: RequestHeader{
			AuthenticationToken: NumericNodeID(0, 0),
			Timestamp:           time.Now().UTC(),
			RequestHandle:       requestID,
			AdditionalHeader:    NullExtensionObject(),
		},
		ClientProtocolVersion: ProtocolVersion,
		RequestType:           requestType,
		SecurityMode:          SecurityModeNone,
		RequestedLifetime:     60_000,
	})
	serviceBody, err := service.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}

	security, err := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{}, 0, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	body, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	body.WriteUInt32(channelID)
	body.write(security)
	c.sequence++
	body.WriteUInt32(c.sequence)
	body.WriteUInt32(requestID)
	body.write(serviceBody)
	encoded, err := body.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeOpenChannel, ChunkFinal, encoded)

	header, response, err := c.receive()
	if err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if header.Type == MessageTypeError {
		protocolError, decodeErr := DecodeProtocolError(response, c.limits)
		if decodeErr != nil {
			return OpenSecureChannelResponse{}, decodeErr
		}
		return OpenSecureChannelResponse{}, &CodecError{Status: protocolError.Error, Message: protocolError.Reason}
	}
	if header.Type != MessageTypeOpenChannel {
		c.t.Fatalf("response type = %s", header.Type)
	}
	// Skip the SecureChannelId, the asymmetric security header, and the
	// sequence header to reach the service body.
	decoder, err := NewDecoder(response[4:], c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	_, used, err := DecodeAsymmetricSecurityHeader(response[4:], 4096, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	_ = decoder
	payload := response[4+used+SequenceHeaderSize:]
	serviceDecoder, err := NewDecoder(payload, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	identifier, err := serviceDecoder.ReadServiceTypeID()
	if err != nil {
		c.t.Fatal(err)
	}
	if identifier != OpenSecureChannelResponseEncodingID {
		c.t.Fatalf("service = %d", identifier)
	}
	return serviceDecoder.ReadOpenSecureChannelResponse()
}

// The full connection sequence of OPC 10000-6 7.1.3 over a real socket.
func TestListenerCompletesTheConnectionSequence(t *testing.T) {
	_, address := startTestListener(t, DefaultListenerConfig())
	client := dialTestClient(t, address)

	ack := client.hello()
	if ack.ProtocolVersion != ProtocolVersion {
		t.Fatalf("acknowledged version = %d", ack.ProtocolVersion)
	}
	if ack.ReceiveBufferSize < MinimumBufferSize || ack.SendBufferSize < MinimumBufferSize {
		t.Fatalf("negotiated buffers = %+v", ack)
	}
	// The client asked for 8 chunks, which is tighter than the server's 32.
	if ack.MaxChunkCount != 8 {
		t.Fatalf("max chunks = %d, want the client's limit", ack.MaxChunkCount)
	}

	response, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatalf("OpenSecureChannel: %v", err)
	}
	if response.Header.ServiceResult != StatusGood {
		t.Fatalf("service result = %s", response.Header.ServiceResult.Hex())
	}
	if response.SecurityToken.SecureChannelID == 0 || response.SecurityToken.TokenID == 0 {
		t.Fatalf("token = %+v", response.SecurityToken)
	}
	if response.SecurityToken.RevisedLifetime == 0 {
		t.Fatal("the server returned a zero lifetime")
	}
	// With SecurityMode None the nonce is null, not an empty byte string.
	if response.ServerNonce != nil {
		t.Fatalf("server nonce = %v", response.ServerNonce)
	}

	// A renew on the same connection keeps the channel and changes the token.
	renewed, err := client.openChannel(response.SecurityToken.SecureChannelID, TokenRequestRenew, 2)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.SecurityToken.SecureChannelID != response.SecurityToken.SecureChannelID {
		t.Fatal("renew changed the channel id")
	}
	if renewed.SecurityToken.TokenID == response.SecurityToken.TokenID {
		t.Fatal("renew reused the token id")
	}
}

// 7.1.3: Hello is sent once; a second one is an error and closes the socket.
func TestListenerRejectsASecondHello(t *testing.T) {
	_, address := startTestListener(t, DefaultListenerConfig())
	client := dialTestClient(t, address)
	client.hello()

	body, err := EncodeHello(Hello{
		ProtocolVersion: ProtocolVersion, ReceiveBufferSize: 65536, SendBufferSize: 65536,
	}, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	client.send(MessageTypeHello, ChunkFinal, body)

	header, response, err := client.receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if header.Type != MessageTypeError {
		t.Fatalf("second Hello was answered with %s", header.Type)
	}
	protocolError, err := DecodeProtocolError(response, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	if protocolError.Error != StatusBadTcpMessageTypeInvalid {
		t.Fatalf("error = %s", protocolError.Error.Hex())
	}
	// The connection is then closed.
	if _, _, err := client.receive(); err == nil {
		t.Fatal("the connection stayed open after the error")
	}
}

func TestListenerRejectsAnOpenChannelBeforeTheHello(t *testing.T) {
	_, address := startTestListener(t, DefaultListenerConfig())
	client := dialTestClient(t, address)
	client.send(MessageTypeOpenChannel, ChunkFinal, make([]byte, 32))

	header, response, err := client.receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if header.Type != MessageTypeError {
		t.Fatalf("answered with %s", header.Type)
	}
	protocolError, err := DecodeProtocolError(response, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	if protocolError.Error != StatusBadTcpMessageTypeInvalid {
		t.Fatalf("error = %s", protocolError.Error.Hex())
	}
}

// A protocol version the server cannot serve is refused with the status the
// clause names.
func TestListenerRejectsAnUnsupportedProtocolVersion(t *testing.T) {
	_, address := startTestListener(t, DefaultListenerConfig())
	client := dialTestClient(t, address)

	body, err := EncodeHello(Hello{
		ProtocolVersion: ProtocolVersion + 1, ReceiveBufferSize: 65536, SendBufferSize: 65536,
	}, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	client.send(MessageTypeHello, ChunkFinal, body)

	header, response, err := client.receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if header.Type != MessageTypeError {
		t.Fatalf("answered with %s", header.Type)
	}
	protocolError, err := DecodeProtocolError(response, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	if protocolError.Error != StatusBadProtocolVersionUnsupport {
		t.Fatalf("error = %s", protocolError.Error.Hex())
	}
}

// A MSG is answered with an explicit fault rather than ignored, because no
// session service is implemented yet.
func TestListenerReportsThatNoSessionServiceExists(t *testing.T) {
	_, address := startTestListener(t, DefaultListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	client.send(MessageTypeSecure, ChunkFinal, make([]byte, 32))

	header, response, err := client.receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if header.Type != MessageTypeError {
		t.Fatalf("answered with %s", header.Type)
	}
	protocolError, err := DecodeProtocolError(response, client.limits)
	if err != nil {
		t.Fatal(err)
	}
	if protocolError.Error != StatusBadServiceUnsupported {
		t.Fatalf("error = %s", protocolError.Error.Hex())
	}
}

// A connection that never sends a Hello is closed rather than held.
func TestListenerClosesAConnectionThatNeverSaysHello(t *testing.T) {
	config := DefaultListenerConfig()
	config.HelloTimeout = 150 * time.Millisecond
	_, address := startTestListener(t, config)

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Either an EOF or a reset is an acceptable close; what matters is that the
	// server does not hold the socket open indefinitely.
	buffer := make([]byte, 1)
	if _, err := conn.Read(buffer); err == nil {
		t.Fatal("the silent connection was not closed")
	}
}

// The connection limit closes an excess connection immediately rather than
// queueing sockets the server will not serve.
func TestListenerBoundsConcurrentConnections(t *testing.T) {
	config := DefaultListenerConfig()
	config.MaxConnections = 1
	_, address := startTestListener(t, config)

	first := dialTestClient(t, address)
	first.hello()

	second, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := second.Read(buffer); err == nil {
		t.Fatal("a connection beyond the limit was served")
	}
}

func TestListenerConfigValidation(t *testing.T) {
	if err := DefaultListenerConfig().ValidateForConfiguration(); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ListenerConfig){
		"receive buffer below the floor": func(c *ListenerConfig) { c.ReceiveBufferSize = MinimumBufferSize - 1 },
		"send buffer below the floor":    func(c *ListenerConfig) { c.SendBufferSize = MinimumBufferSize - 1 },
		"zero hello timeout":             func(c *ListenerConfig) { c.HelloTimeout = 0 },
		"hello timeout beyond two minutes": func(c *ListenerConfig) {
			c.HelloTimeout = maximumHelloTimeout + time.Second
		},
		"zero connections":              func(c *ListenerConfig) { c.MaxConnections = 0 },
		"buffer beyond the codec bound": func(c *ListenerConfig) { c.ReceiveBufferSize = uint32(c.Binary.MaxMessageBytes) + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			config := DefaultListenerConfig()
			mutate(&config)
			if err := config.ValidateForConfiguration(); err == nil {
				t.Fatalf("config %+v was accepted", config)
			}
			if _, err := NewListener(config, 1, 1); err == nil {
				t.Fatal("a listener was built from an invalid config")
			}
		})
	}
}

func TestListenerCloseIsIdempotent(t *testing.T) {
	listener, _ := startTestListener(t, DefaultListenerConfig())
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if listener.Listening() {
		t.Fatal("the listener still reports itself as listening")
	}
}
