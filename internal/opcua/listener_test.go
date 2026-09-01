package opcua

import (
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// testClient is a minimal UA-TCP client: just enough to drive the connection
// sequence of OPC 10000-6 7.1.3 against the listener over a real socket.
type testClient struct {
	t        *testing.T
	conn     net.Conn
	limits   BinaryLimits
	sequence uint32
	// responseSecurityPolicyURI is the policy the server named in its OPN
	// reply, which OPC 10000-6 6.7.7 requires to be the one the request named.
	responseSecurityPolicyURI string
	// securityPolicyURI is what the client names in its OPN. OPC 10000-6 6.7.7
	// has the receiver verify that it supports the requested policy, so a
	// client that names none, or names one the endpoint does not serve, is
	// refused. It is a field so a test can present a policy deliberately.
	securityPolicyURI string
}

// testEndpointConfig supplies the values the adapter refuses to invent: the
// endpoint URLs and the security policy URI defined by OPC 10000-7.
func testEndpointConfig() EndpointConfig {
	return EndpointConfig{
		EndpointURL:         "opc.tcp://127.0.0.1:4840",
		ApplicationURI:      "urn:example:opcda-access-adapter",
		ProductURI:          "urn:example:opcda-access-adapter",
		ApplicationName:     "OPC DA Access Adapter",
		SecurityPolicyURI:   "urn:test:security-policy:none",
		TransportProfileURI: "urn:test:transport:uatcp-uasc-uabinary",
	}
}

func testListenerConfig() ListenerConfig {
	config := DefaultListenerConfig()
	config.Endpoint = testEndpointConfig()
	config.AddressSpace = AddressSpaceConfig{
		NamespaceURI:     "urn:example:opcda-access-adapter",
		SourceFolderName: "Source",
	}
	return config
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

	policyURI := c.securityPolicyURI
	if policyURI == "" {
		policyURI = testEndpointConfig().SecurityPolicyURI
	}
	security, err := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{
		SecurityPolicyURI: policyURI,
	}, 0, c.limits)
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
	responseSecurity, used, err := DecodeAsymmetricSecurityHeader(response[4:], 4096, c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	c.responseSecurityPolicyURI = responseSecurity.SecurityPolicyURI
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

// callService sends a MSG on an open channel and returns the service TypeId and
// the decoder positioned at the response body.
func (c *testClient) callService(token ChannelSecurityToken, requestID uint32, serviceBody []byte) (uint32, *Decoder, error) {
	c.t.Helper()
	body, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	body.WriteUInt32(token.SecureChannelID)
	body.WriteUInt32(token.TokenID)
	c.sequence++
	body.WriteUInt32(c.sequence)
	body.WriteUInt32(requestID)
	body.write(serviceBody)
	encoded, err := body.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeSecure, ChunkFinal, encoded)
	return c.readServiceResponse()
}

// sendService writes a request without waiting for its response, so a test can
// have more than one outstanding at a time the way a real client does.
func (c *testClient) sendService(token ChannelSecurityToken, requestID uint32, serviceBody []byte) {
	c.t.Helper()
	body, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	body.WriteUInt32(token.SecureChannelID)
	body.WriteUInt32(token.TokenID)
	c.sequence++
	body.WriteUInt32(c.sequence)
	body.WriteUInt32(requestID)
	body.write(serviceBody)
	encoded, err := body.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(MessageTypeSecure, ChunkFinal, encoded)
}

// readServiceResponse reads one response from the socket.
func (c *testClient) readServiceResponse() (uint32, *Decoder, error) {
	c.t.Helper()
	header, response, err := c.receive()
	if err != nil {
		return 0, nil, err
	}
	if header.Type == MessageTypeError {
		protocolError, decodeErr := DecodeProtocolError(response, c.limits)
		if decodeErr != nil {
			return 0, nil, decodeErr
		}
		return 0, nil, &CodecError{Status: protocolError.Error, Message: protocolError.Reason}
	}
	if header.Type != MessageTypeSecure {
		c.t.Fatalf("response type = %s", header.Type)
	}
	// Skip the SecureChannelId, TokenId, and sequence header.
	decoder, err := NewDecoder(response[8+SequenceHeaderSize:], c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return 0, nil, err
	}
	return identifier, decoder, nil
}

func (c *testClient) getEndpoints(token ChannelSecurityToken, requestID uint32, profileURIs []string) (uint32, *Decoder, error) {
	c.t.Helper()
	encoder, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	encoder.WriteGetEndpointsRequest(GetEndpointsRequest{
		Header: RequestHeader{
			AuthenticationToken: NumericNodeID(0, 0),
			RequestHandle:       requestID,
			AdditionalHeader:    NullExtensionObject(),
		},
		EndpointURL: "opc.tcp://127.0.0.1:4840",
		ProfileURIs: profileURIs,
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	return c.callService(token, requestID, serviceBody)
}

// The full connection sequence of OPC 10000-6 7.1.3 over a real socket.
func TestListenerCompletesTheConnectionSequence(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
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
	_, address := startTestListener(t, testListenerConfig())
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
	_, address := startTestListener(t, testListenerConfig())
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
	_, address := startTestListener(t, testListenerConfig())
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

// A MSG on a channel the server does not know is refused at the transport, not
// answered as a service.
func TestListenerRefusesASecureMessageOnAnUnknownChannel(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()

	_, _, err := client.getEndpoints(ChannelSecurityToken{}, 1, nil)
	if err == nil {
		t.Fatal("a message on an unknown channel was served")
	}
	var codecErr *CodecError
	if !errors.As(err, &codecErr) || codecErr.Status != StatusBadTcpSecureChannelUnknown {
		t.Fatalf("error = %v", err)
	}
}

// GetEndpoints needs no session, so it is answered on an open channel.
func TestListenerAnswersGetEndpoints(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	identifier, decoder, err := client.getEndpoints(opened.SecurityToken, 2, nil)
	if err != nil {
		t.Fatalf("GetEndpoints: %v", err)
	}
	if identifier != GetEndpointsResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	response, err := decoder.ReadGetEndpointsResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.ServiceResult != StatusGood || response.Header.RequestHandle != 2 {
		t.Fatalf("response header = %+v", response.Header)
	}
	if len(response.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(response.Endpoints))
	}
	endpoint := response.Endpoints[0]
	expected := testEndpointConfig()
	if endpoint.EndpointURL != expected.EndpointURL ||
		endpoint.SecurityPolicyURI != expected.SecurityPolicyURI ||
		endpoint.TransportProfileURI != expected.TransportProfileURI {
		t.Fatalf("endpoint = %+v", endpoint)
	}
	if endpoint.SecurityMode != SecurityModeNone {
		t.Fatalf("security mode = %s", endpoint.SecurityMode)
	}
	// An unsecured endpoint carries no certificate and is not recommended.
	if endpoint.ServerCertificate != nil {
		t.Fatalf("server certificate = %v, want null", endpoint.ServerCertificate)
	}
	if endpoint.SecurityLevel != 0 {
		t.Fatalf("security level = %d, want 0", endpoint.SecurityLevel)
	}
	if len(endpoint.UserIdentityTokens) != 1 ||
		endpoint.UserIdentityTokens[0].TokenType != UserTokenTypeAnonymous {
		t.Fatalf("user identity tokens = %+v", endpoint.UserIdentityTokens)
	}
	if endpoint.Server.ApplicationType != ApplicationTypeServer {
		t.Fatalf("application type = %d", endpoint.Server.ApplicationType)
	}
}

// Table 5: a non-empty profile list that does not name this endpoint's
// transport profile returns nothing rather than everything.
func TestGetEndpointsHonoursTheProfileFilter(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, decoder, err := client.getEndpoints(opened.SecurityToken, 2, []string{"urn:test:transport:other"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := decoder.ReadGetEndpointsResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Endpoints) != 0 {
		t.Fatalf("a filtered request returned %d endpoints", len(response.Endpoints))
	}

	_, decoder, err = client.getEndpoints(opened.SecurityToken, 3, []string{testEndpointConfig().TransportProfileURI})
	if err != nil {
		t.Fatal(err)
	}
	response, err = decoder.ReadGetEndpointsResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Endpoints) != 1 {
		t.Fatalf("a matching filter returned %d endpoints", len(response.Endpoints))
	}
}

// A service that is not implemented is reported as a ServiceFault, keeping the
// channel open, rather than closing the connection.
func TestListenerFaultsOnAnUnimplementedService(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	// An identifier the OPC Foundation NodeIds table does not assign to any
	// service encoding. Using a real-but-unimplemented service here would make
	// this test move every time another service is added.
	const unassignedServiceEncodingID = 0x7FFFFFFF
	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteServiceTypeID(unassignedServiceEncodingID)
	serviceBody, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	identifier, decoder, err := client.callService(opened.SecurityToken, 2, serviceBody)
	if err != nil {
		t.Fatalf("the channel was closed instead of faulting: %v", err)
	}
	if identifier != ServiceFaultEncodingID {
		t.Fatalf("service = %d, want a ServiceFault", identifier)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if header.ServiceResult != StatusBadServiceUnsupported {
		t.Fatalf("service result = %s", header.ServiceResult.Hex())
	}

	// The channel survives, so a following request is still served.
	if _, _, err := client.getEndpoints(opened.SecurityToken, 3, nil); err != nil {
		t.Fatalf("the channel did not survive the fault: %v", err)
	}
}

// A connection that never sends a Hello is closed rather than held.
func TestListenerClosesAConnectionThatNeverSaysHello(t *testing.T) {
	config := testListenerConfig()
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
	config := testListenerConfig()
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

// createSession drives CreateSession over the socket.
func (c *testClient) createSession(token ChannelSecurityToken, requestID uint32, nonce []byte) (CreateSessionResponse, error) {
	c.t.Helper()
	encoder, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	encoder.WriteCreateSessionRequest(CreateSessionRequest{
		Header: RequestHeader{
			AuthenticationToken: NumericNodeID(0, 0),
			RequestHandle:       requestID,
			AdditionalHeader:    NullExtensionObject(),
		},
		SessionName:             "listener-test",
		ClientNonce:             nonce,
		RequestedSessionTimeout: 60_000,
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	identifier, decoder, err := c.callService(token, requestID, serviceBody)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	if identifier == ServiceFaultEncodingID {
		header, headerErr := decoder.ReadResponseHeader()
		if headerErr != nil {
			return CreateSessionResponse{}, headerErr
		}
		return CreateSessionResponse{}, &CodecError{Status: header.ServiceResult, Message: "service fault"}
	}
	if identifier != CreateSessionResponseEncodingID {
		c.t.Fatalf("service = %d", identifier)
	}
	return decoder.ReadCreateSessionResponse()
}

func (c *testClient) activateSession(token ChannelSecurityToken, requestID uint32, authToken NodeID, identity ExtensionObject) (ActivateSessionResponse, error) {
	c.t.Helper()
	encoder, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	encoder.WriteActivateSessionRequest(ActivateSessionRequest{
		Header: RequestHeader{
			AuthenticationToken: authToken,
			RequestHandle:       requestID,
			AdditionalHeader:    NullExtensionObject(),
		},
		UserIdentityToken: identity,
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	identifier, decoder, err := c.callService(token, requestID, serviceBody)
	if err != nil {
		return ActivateSessionResponse{}, err
	}
	if identifier == ServiceFaultEncodingID {
		header, headerErr := decoder.ReadResponseHeader()
		if headerErr != nil {
			return ActivateSessionResponse{}, headerErr
		}
		return ActivateSessionResponse{}, &CodecError{Status: header.ServiceResult, Message: "service fault"}
	}
	if identifier != ActivateSessionResponseEncodingID {
		c.t.Fatalf("service = %d", identifier)
	}
	return decoder.ReadActivateSessionResponse()
}

// The full session sequence over a real socket: Hello, OPN, CreateSession,
// ActivateSession, CloseSession.
func TestListenerCompletesTheSessionSequence(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if created.Header.ServiceResult != StatusGood || created.Header.RequestHandle != 2 {
		t.Fatalf("response header = %+v", created.Header)
	}
	if created.AuthenticationToken.Type != NodeIDTypeOpaque {
		t.Fatalf("authentication token = %+v", created.AuthenticationToken)
	}
	if created.RevisedSessionTimeout <= 0 {
		t.Fatalf("revised timeout = %v", created.RevisedSessionTimeout)
	}
	if len(created.ServerNonce) < MinNonceBytes {
		t.Fatalf("server nonce is %d bytes", len(created.ServerNonce))
	}
	// The endpoint list lets a client verify what it selected from discovery.
	if len(created.ServerEndpoints) != 1 ||
		created.ServerEndpoints[0].EndpointURL != testEndpointConfig().EndpointURL {
		t.Fatalf("server endpoints = %+v", created.ServerEndpoints)
	}
	// No signature is generated when the security mode is None.
	if len(created.ServerSignature.Signature) != 0 {
		t.Fatal("a signature was generated for an unsecured session")
	}

	activated, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject())
	if err != nil {
		t.Fatalf("ActivateSession: %v", err)
	}
	if activated.Header.ServiceResult != StatusGood || activated.Header.RequestHandle != 3 {
		t.Fatalf("activate header = %+v", activated.Header)
	}
	if len(activated.ServerNonce) < MinNonceBytes {
		t.Fatalf("activate server nonce is %d bytes", len(activated.ServerNonce))
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteCloseSessionRequest(CloseSessionRequest{
		Header: RequestHeader{
			AuthenticationToken: created.AuthenticationToken,
			RequestHandle:       4,
			AdditionalHeader:    NullExtensionObject(),
		},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	identifier, decoder, err := client.callService(opened.SecurityToken, 4, serviceBody)
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if identifier != CloseSessionResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if header.ServiceResult != StatusGood {
		t.Fatalf("close result = %s", header.ServiceResult.Hex())
	}

	// The session is gone, so activating it again faults on the same channel.
	if _, err := client.activateSession(opened.SecurityToken, 5, created.AuthenticationToken, NullExtensionObject()); err == nil {
		t.Fatal("a closed session was activated")
	}
}

// A service-level failure faults without closing the channel, so the client can
// keep using it.
func TestListenerFaultsOnAnInvalidNonceWithoutClosing(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.createSession(opened.SecurityToken, 2, []byte{1, 2, 3})
	if err == nil {
		t.Fatal("a short client nonce was accepted")
	}
	var codecErr *CodecError
	if !errors.As(err, &codecErr) || codecErr.Status != StatusBadNonceInvalid {
		t.Fatalf("error = %v", err)
	}
	// The channel survived, so a valid request still succeeds.
	if _, err := client.createSession(opened.SecurityToken, 3, testClientNonce()); err != nil {
		t.Fatalf("the channel did not survive the fault: %v", err)
	}
}

// A session cannot be used from a SecureChannel it was not created on.
func TestListenerBindsSessionsToTheirChannel(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	first, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(first.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}

	second, err := client.openChannel(0, TokenRequestIssue, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.activateSession(second.SecurityToken, 4, created.AuthenticationToken, NullExtensionObject())
	if err == nil {
		t.Fatal("a session was activated from another secure channel")
	}
	var codecErr *CodecError
	if !errors.As(err, &codecErr) || codecErr.Status != StatusBadSecureChannelIDInvalid {
		t.Fatalf("error = %v", err)
	}
}

func (c *testClient) browse(token ChannelSecurityToken, requestID uint32, authToken NodeID, node NodeID) (uint32, *Decoder, error) {
	c.t.Helper()
	encoder, err := NewEncoder(c.limits)
	if err != nil {
		c.t.Fatal(err)
	}
	encoder.WriteBrowseRequest(BrowseRequest{
		Header: RequestHeader{
			AuthenticationToken: authToken,
			RequestHandle:       requestID,
			AdditionalHeader:    NullExtensionObject(),
		},
		NodesToBrowse: []BrowseDescription{{
			NodeID:          node,
			BrowseDirection: BrowseDirectionForward,
			ResultMask:      ResultMaskAll,
		}},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		c.t.Fatal(err)
	}
	return c.callService(token, requestID, serviceBody)
}

// Browse over a real socket, after a full Hello/OPN/CreateSession/Activate.
func TestListenerServesBrowse(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Test"},
		{Kind: opcda.BrowseEntryItem, Name: "Top", ItemID: itemID("Top")},
	}); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	identifier, decoder, err := client.browse(
		opened.SecurityToken, 4, created.AuthenticationToken, listener.AddressSpace().SourceFolderID())
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if identifier != BrowseResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.ServiceResult != StatusGood || len(response.Results) != 1 {
		t.Fatalf("response = %+v", response.Header)
	}
	if len(response.Results[0].References) != 2 {
		t.Fatalf("references = %d, want 2", len(response.Results[0].References))
	}
	// The exact DA ItemID survives all the way to the client.
	found := false
	for _, reference := range response.Results[0].References {
		if reference.NodeID.NodeID.StringID == "item:Top" {
			found = true
			if reference.BrowseName.Name != "Top" {
				t.Fatalf("browse name = %q", reference.BrowseName.Name)
			}
		}
	}
	if !found {
		t.Fatal("the item node did not reach the client")
	}
}

// A session that was created but never activated cannot read the address space.
func TestListenerRefusesBrowseWithoutAnActivatedSession(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}

	identifier, decoder, err := client.browse(
		opened.SecurityToken, 3, created.AuthenticationToken, listener.AddressSpace().SourceFolderID())
	if err != nil {
		t.Fatalf("the channel closed instead of faulting: %v", err)
	}
	if identifier != ServiceFaultEncodingID {
		t.Fatalf("service = %d, want a ServiceFault", identifier)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if header.ServiceResult != StatusBadSessionNotActivated {
		t.Fatalf("service result = %s, want Bad_SessionNotActivated", header.ServiceResult.Hex())
	}

	// After activation the same browse succeeds.
	if _, err := client.activateSession(opened.SecurityToken, 4, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}
	identifier, _, err = client.browse(
		opened.SecurityToken, 5, created.AuthenticationToken, listener.AddressSpace().SourceFolderID())
	if err != nil {
		t.Fatal(err)
	}
	if identifier != BrowseResponseEncodingID {
		t.Fatalf("service = %d after activation", identifier)
	}
}

// Read over a real socket, all the way through to the DA runtime.
func TestListenerServesReadFromTheDASource(t *testing.T) {
	runtime := &stubRuntime{}
	listener, err := NewListenerWithRuntime(testListenerConfig(), runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
	}); err != nil {
		t.Fatal(err)
	}
	sourceTime := time.Date(2026, time.August, 26, 1, 2, 3, 400, time.UTC)
	varTypeI4 := opcda.VTI4
	runtime.readResults = []opcda.ReadResult{{
		ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Int32", VarType: varTypeI4, Value: int32(4242),
			QualityRaw: 0xC0, Timestamp: sourceTime, TimestampPresent: true,
		},
	}}

	client := dialTestClient(t, socket.Addr().String())
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteReadRequest(ReadRequest{
		Header: RequestHeader{
			AuthenticationToken: created.AuthenticationToken,
			RequestHandle:       4,
			AdditionalHeader:    NullExtensionObject(),
		},
		TimestampsToReturn: TimestampsBoth,
		NodesToRead:        []ReadValueID{{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue}},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	identifier, decoder, err := client.callService(opened.SecurityToken, 4, serviceBody)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if identifier != ReadResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	response, err := decoder.ReadReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d", len(response.Results))
	}
	result := response.Results[0]
	if result.Status != StatusGood {
		t.Fatalf("status = %s", result.Status.Hex())
	}
	// The DA value, its quality and its source timestamp reach the client.
	if result.Value.Type != BuiltInInt32 || result.Value.Value != int32(4242) {
		t.Fatalf("value = %+v", result.Value)
	}
	if !result.SourceTimestamp.Equal(sourceTime) {
		t.Fatalf("source timestamp = %s", result.SourceTimestamp)
	}
	// The exact ItemID was what the source was asked for.
	if len(runtime.readRequest.Items) != 1 || runtime.readRequest.Items[0] != "Test/Int32" {
		t.Fatalf("source items = %v", runtime.readRequest.Items)
	}
}

// With no DA runtime attached a Read faults rather than returning empty values.
func TestListenerReportsNoDataSource(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4),
			AccessRights:  &opcda.DAAccessRights{Raw: 1, Read: true}},
	}); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteReadRequest(ReadRequest{
		Header: RequestHeader{
			AuthenticationToken: created.AuthenticationToken,
			RequestHandle:       4,
			AdditionalHeader:    NullExtensionObject(),
		},
		NodesToRead: []ReadValueID{{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue}},
	})
	serviceBody, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	identifier, decoder, err := client.callService(opened.SecurityToken, 4, serviceBody)
	if err != nil {
		t.Fatalf("the channel closed instead of faulting: %v", err)
	}
	if identifier != ServiceFaultEncodingID {
		t.Fatalf("service = %d, want a ServiceFault", identifier)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	if header.ServiceResult != StatusBadNotConnected {
		t.Fatalf("service result = %s, want Bad_NotConnected", header.ServiceResult.Hex())
	}
}

// With a DA runtime attached the listener fills the address space from the
// source on demand, so a client browses live contents without the application
// populating anything.
func TestListenerPopulatesFromTheSourceOnDemand(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.setEntries(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Test"},
	})
	runtime.setEntries([]string{"Test"}, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Float", ItemID: itemID("Test/Float"),
			CanonicalType: varType(opcda.VTR4),
			AccessRights:  &opcda.DAAccessRights{Raw: 1, Read: true}},
	})

	listener, err := NewListenerWithRuntime(testListenerConfig(), runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	client := dialTestClient(t, socket.Addr().String())
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	// The source folder was never populated by the test; browsing it triggers
	// the DA Browse.
	_, decoder, err := client.browse(
		opened.SecurityToken, 4, created.AuthenticationToken, listener.AddressSpace().SourceFolderID())
	if err != nil {
		t.Fatal(err)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 1 {
		t.Fatalf("root references = %d", len(response.Results[0].References))
	}
	branch := response.Results[0].References[0]
	if branch.BrowseName.Name != "Test" || branch.NodeClass != NodeClassObject {
		t.Fatalf("branch = %+v", branch)
	}

	// Browsing the branch drives a second, deeper DA Browse.
	_, decoder, err = client.browse(
		opened.SecurityToken, 5, created.AuthenticationToken, branch.NodeID.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	response, err = decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 1 {
		t.Fatalf("branch references = %d", len(response.Results[0].References))
	}
	item := response.Results[0].References[0]
	if item.NodeID.NodeID.StringID != "item:Test/Float" {
		t.Fatalf("item node id = %q", item.NodeID.NodeID.StringID)
	}

	paths := runtime.browsedPaths()
	if len(paths) != 2 || len(paths[0]) != 0 || paths[1][0] != "Test" {
		t.Fatalf("the source was browsed as %v", paths)
	}
}

// The subscription services over a real socket, from the DA core through to a
// Publish response.
func TestListenerServesSubscriptions(t *testing.T) {
	runtime := &subscribingRuntime{}
	listener, err := NewListenerWithRuntime(testListenerConfig(), runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
	}); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, socket.Addr().String())
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	call := func(handle uint32, write func(*Encoder)) (uint32, *Decoder) {
		t.Helper()
		encoder, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		write(encoder)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			t.Fatal(bodyErr)
		}
		identifier, decoder, callErr := client.callService(opened.SecurityToken, handle, body)
		if callErr != nil {
			t.Fatalf("service call %d: %v", handle, callErr)
		}
		return identifier, decoder
	}

	identifier, decoder := call(4, func(e *Encoder) {
		e.WriteCreateSubscriptionRequest(CreateSubscriptionRequest{
			Header:                      requestHeaderFor(created.AuthenticationToken, 4),
			RequestedPublishingInterval: 250,
			RequestedMaxKeepAliveCount:  3,
			PublishingEnabled:           true,
		})
	})
	if identifier != CreateSubscriptionResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	subscription, err := decoder.ReadCreateSubscriptionResponse()
	if err != nil {
		t.Fatal(err)
	}
	if subscription.SubscriptionID == 0 || subscription.RevisedPublishingInterval <= 0 {
		t.Fatalf("subscription = %+v", subscription)
	}

	identifier, decoder = call(5, func(e *Encoder) {
		e.WriteCreateMonitoredItemsRequest(CreateMonitoredItemsRequest{
			Header:             requestHeaderFor(created.AuthenticationToken, 5),
			SubscriptionID:     subscription.SubscriptionID,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor: ReadValueID{
					NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode: MonitoringModeReporting,
				RequestedParameters: MonitoringParameters{
					ClientHandle: 77, Filter: NullExtensionObject()},
			}},
		})
	})
	if identifier != CreateMonitoredItemsResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	items, err := decoder.ReadCreateMonitoredItemsResponse()
	if err != nil {
		t.Fatal(err)
	}
	if items.Results[0].StatusCode != StatusGood {
		t.Fatalf("monitored item = %s", items.Results[0].StatusCode.Hex())
	}

	// A DA notification reaches the client through Publish.
	runtime.latest().push(daNotification("Test/Int32", 4242, QualityGood))
	identifier, decoder = call(6, func(e *Encoder) {
		e.WritePublishRequest(PublishRequest{
			Header: requestHeaderFor(created.AuthenticationToken, 6),
		})
	})
	if identifier != PublishResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	published, err := decoder.ReadPublishResponse()
	if err != nil {
		t.Fatal(err)
	}
	if published.SubscriptionID != subscription.SubscriptionID {
		t.Fatalf("published subscription = %d", published.SubscriptionID)
	}
	if !published.NotificationMessage.HasData ||
		len(published.NotificationMessage.Notifications) != 1 {
		t.Fatalf("notification message = %+v", published.NotificationMessage)
	}
	notification := published.NotificationMessage.Notifications[0]
	if notification.ClientHandle != 77 {
		t.Fatalf("client handle = %d", notification.ClientHandle)
	}
	if notification.Value.Value.Value != int32(4242) {
		t.Fatalf("value = %v", notification.Value.Value.Value)
	}
	if notification.Value.Status != StatusGood {
		t.Fatalf("status = %s", notification.Value.Status.Hex())
	}
}

func requestHeaderFor(authToken NodeID, handle uint32) RequestHeader {
	return RequestHeader{
		AuthenticationToken: authToken,
		RequestHandle:       handle,
		AdditionalHeader:    NullExtensionObject(),
	}
}

func TestListenerConfigValidation(t *testing.T) {
	if err := testListenerConfig().ValidateForConfiguration(); err != nil {
		t.Fatalf("test config rejected: %v", err)
	}
	// The endpoint is not defaulted: a listener cannot be built without one.
	if err := DefaultListenerConfig().ValidateForConfiguration(); err == nil {
		t.Fatal("a config with no endpoint was accepted")
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
			config := testListenerConfig()
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
	listener, _ := startTestListener(t, testListenerConfig())
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

// OPC 10000-6 6.7.7: "If the Message is the response sent to the Client, then
// the SecurityPolicy shall be the same as the one specified in the request."
// The asymmetric security header is the only place an OPN chunk carries the
// policy, so an empty one leaves a conforming client unable to tell which
// policy secured the reply — and it refuses the channel rather than guess.
//
// This went unnoticed until a third-party client was pointed at the listener,
// because this project's own decoder accepted the empty field its own encoder
// wrote. A round trip against itself cannot catch a field both sides omit.
func TestOpenSecureChannelEchoesTheRequestedSecurityPolicy(t *testing.T) {
	_, address := startTestListener(t, testListenerConfig())
	client := dialTestClient(t, address)
	client.hello()

	if _, err := client.openChannel(0, TokenRequestIssue, 1); err != nil {
		t.Fatalf("OpenSecureChannel: %v", err)
	}
	want := testEndpointConfig().SecurityPolicyURI
	if client.responseSecurityPolicyURI != want {
		t.Fatalf("response security policy = %q, want %q",
			client.responseSecurityPolicyURI, want)
	}
}

// 6.7.7 also requires the receiver to verify that it supports the requested
// policy. A client asking for one this endpoint does not serve must be told so,
// not handed an unsecured channel it never asked for.
func TestOpenSecureChannelRefusesAPolicyItDoesNotServe(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		policy string
	}{
		{"a policy the endpoint does not serve", "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256"},
		// A request naming no policy cannot be verified as supported, and the
		// reply could not name the policy the request named.
		{"no policy at all", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, address := startTestListener(t, testListenerConfig())
			client := dialTestClient(t, address)
			client.hello()
			// An empty field would otherwise fall back to the served policy.
			client.securityPolicyURI = testCase.policy
			if testCase.policy == "" {
				client.securityPolicyURI = "\x00"
			}

			_, err := client.openChannel(0, TokenRequestIssue, 1)
			if err == nil {
				t.Fatal("the channel was opened with a policy the endpoint does not serve")
			}
			if got := codecStatus(t, err); got != StatusBadSecurityPolicyRejected {
				t.Fatalf("status = %s", got.Hex())
			}
		})
	}
}

// A Publish is held until the subscription has something to say, so it must not
// occupy the connection while it waits: a client keeps a Publish outstanding
// and reads and browses on the same channel at the same time. Handling it in
// the read loop would stall everything else for the length of the wait, which
// is the whole publishing interval at best and the keep-alive interval at
// worst.
func TestAHeldPublishDoesNotBlockTheConnection(t *testing.T) {
	runtime := &subscribingRuntime{}
	listener, err := NewListenerWithRuntime(testListenerConfig(), runtime, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- listener.Serve(socket) }()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights},
	}); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, socket.Addr().String())
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.activateSession(opened.SecurityToken, 3, created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encode := func(write func(*Encoder)) []byte {
		t.Helper()
		encoder, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		write(encoder)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			t.Fatal(bodyErr)
		}
		return body
	}

	identifier, decoder, err := client.callService(opened.SecurityToken, 4, encode(func(e *Encoder) {
		e.WriteCreateSubscriptionRequest(CreateSubscriptionRequest{
			Header:                      requestHeaderFor(created.AuthenticationToken, 4),
			RequestedPublishingInterval: 250,
			RequestedMaxKeepAliveCount:  10,
			PublishingEnabled:           true,
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if identifier != CreateSubscriptionResponseEncodingID {
		t.Fatalf("service = %d", identifier)
	}
	subscription, err := decoder.ReadCreateSubscriptionResponse()
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err = client.callService(opened.SecurityToken, 5, encode(func(e *Encoder) {
		e.WriteCreateMonitoredItemsRequest(CreateMonitoredItemsRequest{
			Header:             requestHeaderFor(created.AuthenticationToken, 5),
			SubscriptionID:     subscription.SubscriptionID,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor: ReadValueID{
					NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode: MonitoringModeReporting,
				RequestedParameters: MonitoringParameters{
					ClientHandle: 77, Filter: NullExtensionObject()},
			}},
		})
	})); err != nil {
		t.Fatal(err)
	}

	// The first publishing cycle answers a keep-alive whatever the keep-alive
	// count says, because 5.14.1.1 has a new subscription tell its client it is
	// operational. That one is taken here so what follows is the held Publish
	// this test is about.
	if _, _, err = client.callService(opened.SecurityToken, 6, encode(func(e *Encoder) {
		e.WritePublishRequest(PublishRequest{
			Header: requestHeaderFor(created.AuthenticationToken, 6),
		})
	})); err != nil {
		t.Fatal(err)
	}

	// This Publish has nothing to report, so it is held. The Read that follows
	// must still be answered.
	client.sendService(opened.SecurityToken, 8, encode(func(e *Encoder) {
		e.WritePublishRequest(PublishRequest{
			Header: requestHeaderFor(created.AuthenticationToken, 8),
		})
	}))
	client.sendService(opened.SecurityToken, 7, encode(func(e *Encoder) {
		e.WriteReadRequest(ReadRequest{
			Header:             requestHeaderFor(created.AuthenticationToken, 7),
			TimestampsToReturn: TimestampsBoth,
			NodesToRead: []ReadValueID{{
				NodeID: NumericNodeID(0, NodeIDServerStatusState), AttributeID: AttributeValue}},
		})
	}))

	identifier, _, err = client.readServiceResponse()
	if err != nil {
		t.Fatalf("the Read behind a held Publish was never answered: %v", err)
	}
	if identifier != ReadResponseEncodingID {
		t.Fatalf("service = %d, want the Read answered while the Publish is held", identifier)
	}

	// Now give the subscription something to say, and the held Publish is
	// answered too.
	runtime.latest().push(daNotification("Test/Int32", 4242, QualityGood))
	identifier, decoder, err = client.readServiceResponse()
	if err != nil {
		t.Fatal(err)
	}
	if identifier != PublishResponseEncodingID {
		t.Fatalf("service = %d, want the held Publish answered", identifier)
	}
	published, err := decoder.ReadPublishResponse()
	if err != nil {
		t.Fatal(err)
	}
	if !published.NotificationMessage.HasData ||
		len(published.NotificationMessage.Notifications) != 1 {
		t.Fatalf("notification message = %+v", published.NotificationMessage)
	}
}

// The service inventory in docs/opcua-mapping.md is a claim about this switch,
// and a claim about code drifts unless something compares the two. A service
// added or removed without the document changing fails here.
func TestTheDispatchAnswersTheDocumentedServices(t *testing.T) {
	documented := map[uint32]string{
		GetEndpointsRequestEncodingID:         "GetEndpoints",
		CreateSessionRequestEncodingID:        "CreateSession",
		ActivateSessionRequestEncodingID:      "ActivateSession",
		CloseSessionRequestEncodingID:         "CloseSession",
		BrowseRequestEncodingID:               "Browse",
		BrowseNextRequestEncodingID:           "BrowseNext",
		ReadRequestEncodingID:                 "Read",
		WriteRequestEncodingID:                "Write",
		CreateSubscriptionRequestEncodingID:   "CreateSubscription",
		SetPublishingModeRequestEncodingID:    "SetPublishingMode",
		PublishRequestEncodingID:              "Publish",
		RepublishRequestEncodingID:            "Republish",
		DeleteSubscriptionsRequestEncodingID:  "DeleteSubscriptions",
		CreateMonitoredItemsRequestEncodingID: "CreateMonitoredItems",
		DeleteMonitoredItemsRequestEncodingID: "DeleteMonitoredItems",
	}

	source, err := os.ReadFile("listener.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, match := range regexp.MustCompile(
		`case ([A-Za-z]+)RequestEncodingID:`).FindAllStringSubmatch(string(source), -1) {
		found[match[1]] = true
	}
	// Publish is dispatched by the listener's own loop rather than this switch,
	// because it is held rather than answered at once.
	found["Publish"] = true

	for _, name := range documented {
		if !found[name] {
			t.Errorf("%s is documented as answered but the dispatch does not answer it", name)
		}
	}
	for name := range found {
		documentedName := false
		for _, want := range documented {
			if want == name {
				documentedName = true
				break
			}
		}
		if !documentedName {
			t.Errorf("the dispatch answers %s, which the service inventory does not list", name)
		}
	}
}
