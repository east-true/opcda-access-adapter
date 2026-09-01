package opcua

import (
	"errors"
	"fmt"
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
		ApplicationURI:   "urn:example:opcda-access-adapter:server",
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

// trySend writes a framed message and reports a write failure instead of
// failing the test, so a caller can observe the server closing the connection.
func (c *testClient) trySend(messageType MessageType, chunk byte, body []byte) error {
	c.t.Helper()
	header, err := EncodeMessageHeader(messageType, chunk, len(body), 1<<20)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(header, body...))
	return err
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

func (c *testClient) hello() Acknowledge { return c.helloWithMaxMessage(1 << 20) }

// helloWithMaxMessage names the largest response this client will accept, which
// OPC 10000-6 Table 74 makes a limit the server has to answer within.
func (c *testClient) helloWithMaxMessage(maxMessage uint32) Acknowledge {
	c.t.Helper()
	body, err := EncodeHello(Hello{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		MaxMessageSize:    maxMessage,
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
	// Table 75: the Acknowledge announces what this server accepts in a
	// request, which is its own bound and not the client's response bound.
	if ack.MaxChunkCount != testListenerConfig().MaxChunkCount {
		t.Fatalf("max chunks = %d, want the server's request limit", ack.MaxChunkCount)
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
	if len(response.Results[0].References) != 3 {
		t.Fatalf("references = %d, want two children and a type definition", len(response.Results[0].References))
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
	// The branch, and the folder's own HasTypeDefinition after it.
	if len(response.Results[0].References) != 2 {
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
	// The item, and the branch's own HasTypeDefinition after it.
	if len(response.Results[0].References) != 2 {
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

// The listener is what fills ServerCapabilities from the configuration its
// services are built from. A limit published here that the service does not
// enforce would be a promise nothing keeps, so the two are compared through a
// real listener rather than through a hand-built address space.
func TestTheListenerPublishesTheLimitsItEnforces(t *testing.T) {
	config := testListenerConfig()
	// Values distinct from the defaults, so a hard-coded number cannot pass.
	config.Browse.MaxNodesPerBrowse = 37
	config.Browse.MaxContinuationPoints = 9
	config.DataAccess.MaxNodesPerRead = 41
	config.DataAccess.MaxNodesPerWrite = 13
	config.Subscriptions.MinPublishingInterval = 250 * time.Millisecond

	listener, err := NewListenerWithRuntime(config, &fuzzRuntime{}, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	space := listener.AddressSpace()

	value := func(identifier uint32) any {
		t.Helper()
		node, ok := space.Node(NumericNodeID(0, identifier))
		if !ok {
			t.Fatalf("node %d is not published", identifier)
		}
		return node.LocalValue(time.Now().UTC()).Value
	}
	for _, testCase := range []struct {
		name       string
		identifier uint32
		want       any
	}{
		{"MaxNodesPerBrowse", NodeIDOperationLimitsMaxNodesPerBrowse, uint32(37)},
		{"MaxBrowseContinuationPoints", NodeIDServerCapabilitiesMaxBrowseCP, uint16(9)},
		{"MaxNodesPerRead", NodeIDOperationLimitsMaxNodesPerRead, uint32(41)},
		{"MaxNodesPerWrite", NodeIDOperationLimitsMaxNodesPerWrite, uint32(13)},
		{"MinSupportedSampleRate", NodeIDServerCapabilitiesMinSampleRate, float64(250)},
	} {
		if got := value(testCase.identifier); got != testCase.want {
			t.Errorf("%s publishes %#v, the listener was configured with %#v",
				testCase.name, got, testCase.want)
		}
	}
}

// OPC 10000-6 Table 74: MaxMessageSize is "the maximum size for any response
// Message", and "if MessageChunks have not been sent, the Server shall return
// an Error Message with a Bad_ResponseTooLarge error if a response Message
// exceeds this value". The value the server acknowledges is a promise, not a
// note -- a client sizes its own buffers on it.
func TestAResponseLargerThanTheClientAcceptedIsRefused(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	entries := []opcda.BrowseEntry{}
	for index := 0; index < 200; index++ {
		name := fmt.Sprintf("Item%03d", index)
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	if err := listener.AddressSpace().PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, address)
	// Small enough that a browse of two hundred items cannot fit, and well
	// above the 8192 byte floor a Hello's buffers have to clear.
	const accepted = 2048
	// The Acknowledge does not echo this back -- Table 75 gives its
	// MaxMessageSize the opposite meaning, the largest request the server
	// accepts. What matters is that the server holds its responses to what the
	// Hello asked for.
	client.helloWithMaxMessage(accepted)

	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteBrowseRequest(BrowseRequest{
		Header:        requestHeaderFor(created.AuthenticationToken, 4),
		NodesToBrowse: []BrowseDescription{browseAll(listener.AddressSpace().SourceFolderID())},
	})
	request, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	client.sendService(opened.SecurityToken, 4, request)

	// The answer is a protocol level Error Message, not a service fault: 7.1.5
	// has the server send one and close the connection.
	_, _, err = client.readServiceResponse()
	if err == nil {
		t.Fatal("a response larger than the client accepted was sent")
	}
	var codecErr *CodecError
	if !errors.As(err, &codecErr) || codecErr.Status != StatusBadResponseTooLarge {
		t.Fatalf("error = %v, want Bad_ResponseTooLarge", err)
	}
}

// Table 74: "a value of zero indicates that the Client has no limit". The same
// browse that was refused above is answered when the client asks for no bound,
// because what remains is the server's own.
func TestAClientWithNoLimitGetsTheWholeResponse(t *testing.T) {
	clientWithNoLimit(t)
}

func clientWithNoLimit(t *testing.T) {
	t.Helper()
	listener, address := startTestListener(t, testListenerConfig())
	entries := []opcda.BrowseEntry{}
	for index := 0; index < 200; index++ {
		name := fmt.Sprintf("Item%03d", index)
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	if err := listener.AddressSpace().PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	client := dialTestClient(t, address)
	// Zero is the client declining to bound its responses at all.
	client.helloWithMaxMessage(0)

	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.createSession(opened.SecurityToken, 2, testClientNonce())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteBrowseRequest(BrowseRequest{
		Header:        requestHeaderFor(created.AuthenticationToken, 4),
		NodesToBrowse: []BrowseDescription{browseAll(listener.AddressSpace().SourceFolderID())},
	})
	request, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	client.sendService(opened.SecurityToken, 4, request)

	identifier, decoder, err := client.readServiceResponse()
	if err != nil {
		t.Fatalf("a client that named no limit was refused: %v", err)
	}
	if identifier != BrowseResponseEncodingID {
		t.Fatalf("service = %d, want a browse response", identifier)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	// The whole answer arrived: two hundred items and the folder's own
	// HasTypeDefinition.
	if len(response.Results[0].References) != 201 {
		t.Fatalf("references = %d, want all of them", len(response.Results[0].References))
	}
}

// sendServiceChunked splits one service body across chunks, the way a client
// with a small negotiated buffer has to. Each chunk carries its own security
// and sequence header, which OPC 10000-6 6.7.2.4 increments per chunk.
func (c *testClient) sendServiceChunked(token ChannelSecurityToken, requestID uint32,
	serviceBody []byte, pieces int) {
	c.t.Helper()
	size := (len(serviceBody) + pieces - 1) / pieces
	for offset := 0; offset < len(serviceBody); offset += size {
		end := offset + size
		if end > len(serviceBody) {
			end = len(serviceBody)
		}
		chunk := ChunkIntermediate
		if end == len(serviceBody) {
			chunk = ChunkFinal
		}
		body, err := NewEncoder(c.limits)
		if err != nil {
			c.t.Fatal(err)
		}
		body.WriteUInt32(token.SecureChannelID)
		body.WriteUInt32(token.TokenID)
		c.sequence++
		body.WriteUInt32(c.sequence)
		body.WriteUInt32(requestID)
		body.write(serviceBody[offset:end])
		encoded, encodeErr := body.Bytes()
		if encodeErr != nil {
			c.t.Fatal(encodeErr)
		}
		c.send(MessageTypeSecure, chunk, encoded)
	}
}

// OPC 10000-6 6.7.3: a message may arrive in several chunks, sent sequentially,
// and the receiver reassembles them. A client that negotiated the 8192 byte
// minimum has to chunk a request this server would otherwise never see whole --
// the ChunkAccumulator that does the reassembly existed and was tested, and
// nothing called it.
func TestARequestArrivingInChunksIsReassembled(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
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
	if _, err = client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	encoder, err := NewEncoder(client.limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteBrowseRequest(BrowseRequest{
		Header:        requestHeaderFor(created.AuthenticationToken, 4),
		NodesToBrowse: []BrowseDescription{browseAll(listener.AddressSpace().SourceFolderID())},
	})
	request, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	// Twelve chunks: more than the eight this client's Hello declared, and
	// fewer than the thirty-two the server's Acknowledge did. Table 74's
	// MaxChunkCount bounds responses and Table 75's bounds requests, so a
	// server that bounded this request by the client's number would refuse a
	// request the client was entitled to send.
	if testListenerConfig().MaxChunkCount <= 8 {
		t.Fatalf("this test needs a server chunk limit above the client's 8, got %d",
			testListenerConfig().MaxChunkCount)
	}
	client.sendServiceChunked(opened.SecurityToken, 4, request, 12)

	identifier, decoder, err := client.readServiceResponse()
	if err != nil {
		t.Fatalf("a chunked request was refused: %v", err)
	}
	if identifier != BrowseResponseEncodingID {
		t.Fatalf("service = %d, want a browse response", identifier)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("browse = %s", response.Results[0].StatusCode.Hex())
	}
	// The item and the folder's own HasTypeDefinition.
	if len(response.Results[0].References) != 2 {
		t.Fatalf("references = %d", len(response.Results[0].References))
	}
}

// OPC 10000-6 6.7.3: on an abort chunk the receiver "shall ignore the Message
// but shall not close the SecureChannel". Dropping the connection instead would
// turn a sender's recoverable encoding failure into a reconnect.
func TestAnAbortedMessageIsDiscardedAndTheChannelSurvives(t *testing.T) {
	listener, address := startTestListener(t, testListenerConfig())
	if err := listener.AddressSpace().PopulateBranch(nil, []opcda.BrowseEntry{
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
	if _, err = client.activateSession(opened.SecurityToken, 3,
		created.AuthenticationToken, NullExtensionObject()); err != nil {
		t.Fatal(err)
	}

	browse := func(requestID uint32) []byte {
		t.Helper()
		encoder, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		encoder.WriteBrowseRequest(BrowseRequest{
			Header:        requestHeaderFor(created.AuthenticationToken, requestID),
			NodesToBrowse: []BrowseDescription{browseAll(listener.AddressSpace().SourceFolderID())},
		})
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			t.Fatal(bodyErr)
		}
		return body
	}

	// Half a request, then an abort. Nothing is answered and nothing is kept.
	abandoned := browse(4)
	sendChunk := func(chunk byte, payload []byte) {
		t.Helper()
		body, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		body.WriteUInt32(opened.SecurityToken.SecureChannelID)
		body.WriteUInt32(opened.SecurityToken.TokenID)
		client.sequence++
		body.WriteUInt32(client.sequence)
		body.WriteUInt32(4)
		body.write(payload)
		encoded, bytesErr := body.Bytes()
		if bytesErr != nil {
			t.Fatal(bytesErr)
		}
		client.send(MessageTypeSecure, chunk, encoded)
	}
	sendChunk(ChunkIntermediate, abandoned[:len(abandoned)/2])
	sendChunk(ChunkAbort, nil)

	// The channel is still usable, and the abandoned half did not become part
	// of the next request.
	client.sendService(opened.SecurityToken, 5, browse(5))
	identifier, decoder, err := client.readServiceResponse()
	if err != nil {
		t.Fatalf("the channel did not survive the abort: %v", err)
	}
	if identifier != BrowseResponseEncodingID {
		t.Fatalf("service = %d, want the browse that followed the abort", identifier)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("browse after abort = %s", response.Results[0].StatusCode.Hex())
	}
}

// A server may announce no request bound, which Table 75 allows with a zero
// MaxMessageSize. That cannot mean an unbounded buffer: with reassembly in
// place, a peer would otherwise decide how much memory this process spends, one
// intermediate chunk at a time and never sending a final one.
func TestAnUnboundedServerStillRefusesAnEndlessRequest(t *testing.T) {
	config := testListenerConfig()
	// Neither bound announced, which is the configuration that has nothing
	// else to stop the buffer growing.
	config.MaxMessageSize = 0
	config.MaxChunkCount = 0
	listener, address := startTestListener(t, config)
	_ = listener

	client := dialTestClient(t, address)
	client.hello()
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Intermediate chunks, never a final one. The binary message bound is the
	// ceiling, so this ends in a refusal rather than in memory.
	filler := make([]byte, 32*1024)
	ceiling := config.Binary.MaxMessageBytes
	for sent := 0; sent <= ceiling+len(filler); sent += len(filler) {
		body, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		body.WriteUInt32(opened.SecurityToken.SecureChannelID)
		body.WriteUInt32(opened.SecurityToken.TokenID)
		client.sequence++
		body.WriteUInt32(client.sequence)
		body.WriteUInt32(9)
		body.write(filler)
		encoded, bytesErr := body.Bytes()
		if bytesErr != nil {
			t.Fatal(bytesErr)
		}
		if err := client.trySend(MessageTypeSecure, ChunkIntermediate, encoded); err != nil {
			// The server closed the connection on us, which is the refusal
			// arriving as a broken pipe.
			return
		}
	}

	// Every write succeeded, so the server has to have answered with an error
	// rather than buffered it all. A read that merely times out is not that --
	// it is what an unbounded server would do, so it fails here.
	_ = client.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.readServiceResponse()
	var codecErr *CodecError
	if !errors.As(err, &codecErr) {
		t.Fatalf("the server neither refused nor closed the connection: %v", err)
	}
	if codecErr.Status != StatusBadRequestTooLarge {
		t.Fatalf("error = %s, want Bad_RequestTooLarge", codecErr.Status.Hex())
	}
}

// A server may announce a request bound larger than it could ever decode. The
// accumulator holds it to the smaller of the two, so a message that could only
// end in a decoding failure is refused as too large instead of being buffered
// to the announced size first.
func TestAnAnnouncedBoundIsHeldToWhatCanBeDecoded(t *testing.T) {
	config := testListenerConfig()
	// Announce four times what the binary layer will decode.
	config.MaxMessageSize = uint32(config.Binary.MaxMessageBytes) * 4
	config.MaxChunkCount = 0
	listener, address := startTestListener(t, config)
	_ = listener

	client := dialTestClient(t, address)
	ack := client.hello()
	if ack.MaxMessageSize != config.MaxMessageSize {
		t.Fatalf("the server announced %d, want its configured %d",
			ack.MaxMessageSize, config.MaxMessageSize)
	}
	opened, err := client.openChannel(0, TokenRequestIssue, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Push past what can be decoded but stay under what was announced. The
	// chunks stay well inside the negotiated receive buffer, so what refuses
	// them is the accumulated size and not the size of any one chunk.
	filler := make([]byte, 32*1024)
	ceiling := config.Binary.MaxMessageBytes
	for sent := 0; sent <= ceiling+len(filler); sent += len(filler) {
		body, encodeErr := NewEncoder(client.limits)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		body.WriteUInt32(opened.SecurityToken.SecureChannelID)
		body.WriteUInt32(opened.SecurityToken.TokenID)
		client.sequence++
		body.WriteUInt32(client.sequence)
		body.WriteUInt32(9)
		body.write(filler)
		encoded, bytesErr := body.Bytes()
		if bytesErr != nil {
			t.Fatal(bytesErr)
		}
		if err := client.trySend(MessageTypeSecure, ChunkIntermediate, encoded); err != nil {
			return
		}
	}

	_ = client.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = client.readServiceResponse()
	var codecErr *CodecError
	if !errors.As(err, &codecErr) {
		t.Fatalf("a request past the decodable size was buffered: %v", err)
	}
	if codecErr.Status != StatusBadRequestTooLarge {
		t.Fatalf("error = %s, want Bad_RequestTooLarge", codecErr.Status.Hex())
	}
}
