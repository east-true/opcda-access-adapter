package opcua

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// The UA-TCP listener implements the connection sequence of OPC 10000-6 7.1.3:
// the client's first message is a Hello, the server answers with an Acknowledge
// that completes buffer negotiation, and the client then sends
// OpenSecureChannel.
//
// This is the transport for the SecurityPolicy None path only. Per ADR-0016 it
// exists for local interoperability work and is never described as production
// ready.

// ListenerConfig bounds everything a peer can consume before it has proved
// anything about itself.
type ListenerConfig struct {
	// ReceiveBufferSize and SendBufferSize are this server's side of the buffer
	// negotiation, and the receive buffer also bounds a pre-negotiation header.
	ReceiveBufferSize uint32
	SendBufferSize    uint32
	MaxMessageSize    uint32
	MaxChunkCount     uint32

	// HelloTimeout closes a connection that never sends a Hello. OPC 10000-6
	// 7.1.3 requires this to be configurable with a default of at most two
	// minutes.
	HelloTimeout time.Duration
	// ReadTimeout and WriteTimeout bound a single message exchange.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	MaxConnections int
	Binary         BinaryLimits
	Channels       ChannelLimits
	Sessions       SessionLimits
	Browse         BrowseLimits
	// AddressSpace describes the namespace and folder the DA source appears
	// under.
	AddressSpace AddressSpaceConfig
	// Endpoint is the single endpoint this listener publishes through
	// GetEndpoints.
	Endpoint EndpointConfig
}

func DefaultListenerConfig() ListenerConfig {
	return ListenerConfig{
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		MaxMessageSize:    1 << 20,
		MaxChunkCount:     32,
		HelloTimeout:      30 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      30 * time.Second,
		MaxConnections:    16,
		Binary:            DefaultBinaryLimits(),
		Channels:          DefaultChannelLimits(),
		Sessions:          DefaultSessionLimits(),
		Browse:            DefaultBrowseLimits(),
		// The endpoint has no default: its URLs identify a deployment and its
		// security policy URI is defined by OPC 10000-7, so both are supplied
		// rather than assumed.
	}
}

// maximumHelloTimeout is the ceiling OPC 10000-6 7.1.3 places on the wait for a
// Hello.
const maximumHelloTimeout = 2 * time.Minute

func (config ListenerConfig) validate() error {
	if config.ReceiveBufferSize < MinimumBufferSize || config.SendBufferSize < MinimumBufferSize {
		return fmt.Errorf("UA-TCP buffer sizes must be at least %d bytes", MinimumBufferSize)
	}
	if config.HelloTimeout <= 0 || config.ReadTimeout <= 0 || config.WriteTimeout <= 0 {
		return fmt.Errorf("UA-TCP timeouts must be positive")
	}
	if config.HelloTimeout > maximumHelloTimeout {
		return fmt.Errorf("the wait for a Hello must not exceed %s", maximumHelloTimeout)
	}
	if config.MaxConnections <= 0 {
		return fmt.Errorf("UA-TCP connection limit must be positive")
	}
	if uint64(config.ReceiveBufferSize) > uint64(config.Binary.MaxMessageBytes) ||
		uint64(config.SendBufferSize) > uint64(config.Binary.MaxMessageBytes) {
		return fmt.Errorf("UA-TCP buffer sizes must not exceed the binary message bound")
	}
	if err := config.Binary.validate(); err != nil {
		return err
	}
	if err := config.Channels.validate(); err != nil {
		return err
	}
	if err := config.Sessions.validate(); err != nil {
		return err
	}
	if err := config.Browse.validate(); err != nil {
		return err
	}
	if err := config.AddressSpace.validate(); err != nil {
		return err
	}
	return config.Endpoint.validate()
}

func (config ListenerConfig) ValidateForConfiguration() error { return config.validate() }

// Listener serves UA-TCP connections. It owns one channel registry, so the
// SecureChannels it issues are scoped to this listener.
type Listener struct {
	config    ListenerConfig
	registry  *ChannelRegistry
	endpoints *EndpointService
	sessions  *SessionRegistry
	space     *AddressSpace
	browse    *BrowseService

	listening atomic.Bool
	slots     chan struct{}

	mu       sync.Mutex
	closed   bool
	conns    map[net.Conn]struct{}
	listener net.Listener
}

func NewListener(config ListenerConfig, channelIDSeed, tokenIDSeed uint32) (*Listener, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	registry, err := NewChannelRegistry(config.Channels, channelIDSeed, tokenIDSeed)
	if err != nil {
		return nil, err
	}
	endpoints, err := NewEndpointService(config.Endpoint)
	if err != nil {
		return nil, err
	}
	sessions, err := NewSessionRegistry(config.Sessions)
	if err != nil {
		return nil, err
	}
	space, err := NewAddressSpace(config.AddressSpace)
	if err != nil {
		return nil, err
	}
	browse, err := NewBrowseService(space, config.Browse)
	if err != nil {
		return nil, err
	}
	return &Listener{
		config:    config,
		registry:  registry,
		endpoints: endpoints,
		sessions:  sessions,
		space:     space,
		browse:    browse,
		slots:     make(chan struct{}, config.MaxConnections),
		conns:     make(map[net.Conn]struct{}),
	}, nil
}

func (l *Listener) Listening() bool { return l.listening.Load() }

// Serve accepts connections until the listener is closed.
func (l *Listener) Serve(listener net.Listener) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	l.listener = listener
	l.mu.Unlock()

	l.listening.Store(true)
	defer l.listening.Store(false)

	var active sync.WaitGroup
	defer active.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		// A connection beyond the limit is closed immediately rather than
		// queued, so a peer cannot make the server hold sockets it will not
		// serve.
		select {
		case l.slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		if !l.track(conn) {
			<-l.slots
			_ = conn.Close()
			continue
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-l.slots }()
			defer l.untrack(conn)
			l.serveConnection(conn)
		}()
	}
}

func (l *Listener) track(conn net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[conn] = struct{}{}
	return true
}

func (l *Listener) untrack(conn net.Conn) {
	l.mu.Lock()
	delete(l.conns, conn)
	l.mu.Unlock()
	_ = conn.Close()
}

// Close stops accepting and drops every live connection.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	// Marked here rather than only in Serve's deferred call: once Close returns
	// the listener is no longer accepting, and a caller must not observe it as
	// listening while the accept goroutine is still unwinding.
	l.listening.Store(false)
	listener := l.listener
	conns := make([]net.Conn, 0, len(l.conns))
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	return nil
}

// connectionState is one connection's negotiated state.
type connectionState struct {
	negotiated  bool
	receiveSize uint32
	sendSize    uint32
	service     *ChannelService
	// Each side of a channel assigns its own sequence numbers, so the received
	// and sent series are tracked separately rather than sharing one counter.
	receiveSequence *SequenceValidator
	sendSequence    *SequenceValidator
	channelID       uint32
}

func (l *Listener) serveConnection(conn net.Conn) {
	state := &connectionState{
		// Before negotiation the server's own receive buffer bounds a header.
		receiveSize: l.config.ReceiveBufferSize,
		sendSize:    l.config.SendBufferSize,
	}
	// 7.1.3: close a connection that never sends a Hello.
	deadline := l.config.HelloTimeout
	for {
		if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
			return
		}
		header, body, err := readMessage(conn, state.receiveSize)
		if err != nil {
			l.writeProtocolError(conn, err)
			return
		}
		if err := l.handleMessage(conn, state, header, body); err != nil {
			l.writeProtocolError(conn, err)
			return
		}
		deadline = l.config.ReadTimeout
	}
}

// readMessage reads one framed message. The header is validated against the
// negotiated buffer before the body is read, as OPC 10000-6 7.1.2.2 requires,
// so an oversized declaration never causes an allocation.
func readMessage(conn net.Conn, receiveBufferSize uint32) (MessageHeader, []byte, error) {
	var headerBytes [HeaderSize]byte
	if _, err := io.ReadFull(conn, headerBytes[:]); err != nil {
		return MessageHeader{}, nil, err
	}
	header, err := DecodeMessageHeader(headerBytes[:], receiveBufferSize)
	if err != nil {
		return MessageHeader{}, nil, err
	}
	body := make([]byte, header.BodySize())
	if _, err := io.ReadFull(conn, body); err != nil {
		return MessageHeader{}, nil, err
	}
	return header, body, nil
}

func (l *Listener) handleMessage(conn net.Conn, state *connectionState, header MessageHeader, body []byte) error {
	switch header.Type {
	case MessageTypeHello:
		return l.handleHello(conn, state, body)
	case MessageTypeOpenChannel:
		return l.handleOpenChannel(conn, state, body)
	case MessageTypeCloseChannel:
		return l.handleCloseChannel(conn, state, body)
	case MessageTypeSecure:
		return l.handleSecureMessage(conn, state, body)
	default:
		return uacpError(StatusBadTcpMessageTypeInvalid, "message type %s is not accepted here", header.Type)
	}
}

func (l *Listener) handleHello(conn net.Conn, state *connectionState, body []byte) error {
	// 7.1.3: Hello is sent once; a second one is an error and closes the
	// connection.
	if state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "a second Hello was received")
	}
	hello, err := DecodeHello(body, l.config.Binary)
	if err != nil {
		return err
	}
	ack, err := NegotiateAcknowledge(hello,
		l.config.ReceiveBufferSize, l.config.SendBufferSize,
		l.config.MaxMessageSize, l.config.MaxChunkCount)
	if err != nil {
		return err
	}
	encoded, err := EncodeAcknowledge(ack, l.config.Binary)
	if err != nil {
		return err
	}
	if err := l.writeMessage(conn, MessageTypeAcknowledge, ChunkFinal, encoded); err != nil {
		return err
	}
	state.negotiated = true
	state.receiveSize = ack.ReceiveBufferSize
	state.sendSize = ack.SendBufferSize
	state.service = NewChannelService(l.registry, hello.ProtocolVersion)
	// The sequence rule set is a property of the SecurityPolicy. Only the
	// legacy rules are exercised here; see docs/opcua-mapping.md for why the
	// per-policy flag is not bound.
	state.receiveSequence = NewSequenceValidator(SequenceNumberingLegacy)
	state.sendSequence = NewSequenceValidator(SequenceNumberingLegacy)
	return nil
}

func (l *Listener) handleOpenChannel(conn net.Conn, state *connectionState, body []byte) error {
	if !state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "OpenSecureChannel arrived before the Hello")
	}
	channelID, _, sequence, payload, err := l.splitSecureMessage(state, body, true)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(payload, l.config.Binary)
	if err != nil {
		return err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return err
	}
	if identifier != OpenSecureChannelRequestEncodingID {
		return uacpError(StatusBadServiceUnsupported, "service %d is not an OpenSecureChannel request", identifier)
	}
	request, err := decoder.ReadOpenSecureChannelRequest()
	if err != nil {
		return err
	}
	response, err := state.service.OpenSecureChannel(request, channelID, time.Now().UTC())
	if err != nil {
		return err
	}
	state.channelID = response.SecurityToken.SecureChannelID

	encoder, err := NewEncoder(l.config.Binary)
	if err != nil {
		return err
	}
	encoder.WriteOpenSecureChannelResponse(response)
	serviceBody, err := encoder.Bytes()
	if err != nil {
		return err
	}
	return l.writeSecureMessage(conn, state, MessageTypeOpenChannel,
		response.SecurityToken, sequence.RequestID, serviceBody, true)
}

func (l *Listener) handleCloseChannel(conn net.Conn, state *connectionState, body []byte) error {
	if !state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "CloseSecureChannel arrived before the Hello")
	}
	channelID, _, _, payload, err := l.splitSecureMessage(state, body, false)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(payload, l.config.Binary)
	if err != nil {
		return err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return err
	}
	if identifier != CloseSecureChannelRequestEncodingID {
		return uacpError(StatusBadServiceUnsupported, "service %d is not a CloseSecureChannel request", identifier)
	}
	request, err := decoder.ReadCloseSecureChannelRequest()
	if err != nil {
		return err
	}
	if _, err := state.service.CloseSecureChannel(request, channelID, time.Now().UTC()); err != nil {
		return err
	}
	// The clause has the socket closed after a CloseSecureChannel; returning an
	// io.EOF-shaped signal ends the loop without writing an error message.
	return errConnectionFinished
}

var errConnectionFinished = errors.New("secure channel closed by the client")

// splitSecureMessage reads the 12 byte secure conversation header, the security
// header, and the sequence header, and returns the remaining service payload.
// The message header itself has already been consumed by readMessage, so the
// body here starts at the SecureChannelId.
func (l *Listener) splitSecureMessage(state *connectionState, body []byte, asymmetric bool) (uint32, ChannelSecurityToken, SequenceHeader, []byte, error) {
	var token ChannelSecurityToken
	decoder, err := NewDecoder(body, l.config.Binary)
	if err != nil {
		return 0, token, SequenceHeader{}, nil, err
	}
	channelID, err := decoder.ReadUInt32()
	if err != nil {
		return 0, token, SequenceHeader{}, nil, err
	}
	consumed := 4
	if asymmetric {
		maxCertificate := MaxSenderCertificateSize(int(state.receiveSize), MaxSecurityPolicyURIBytes, 0, 0)
		security, used, securityErr := DecodeAsymmetricSecurityHeader(body[consumed:], maxCertificate, l.config.Binary)
		if securityErr != nil {
			return 0, token, SequenceHeader{}, nil, securityErr
		}
		// Only the None policy is served, so a request that presents a
		// certificate is refused rather than silently treated as unsecured.
		if len(security.SenderCertificate) != 0 || len(security.ReceiverCertificateThumbprint) != 0 {
			return 0, token, SequenceHeader{}, nil, uacpError(StatusBadSecurityPolicyRejected,
				"only an unsecured channel is served by this listener")
		}
		consumed += used
	} else {
		tokenID, tokenErr := decoder.ReadUInt32()
		if tokenErr != nil {
			return 0, token, SequenceHeader{}, nil, tokenErr
		}
		channel, acceptErr := l.registry.Accept(channelID, tokenID, time.Now().UTC())
		if acceptErr != nil {
			return 0, token, SequenceHeader{}, nil, acceptErr
		}
		token = channel.Token()
		consumed += 4
	}
	if len(body) < consumed+SequenceHeaderSize {
		return 0, token, SequenceHeader{}, nil, decodingError("message is too short for a sequence header")
	}
	sequence, err := DecodeSequenceHeader(body[consumed:consumed+SequenceHeaderSize], l.config.Binary)
	if err != nil {
		return 0, token, SequenceHeader{}, nil, err
	}
	if err := state.receiveSequence.Accept(sequence.SequenceNumber); err != nil {
		return 0, token, SequenceHeader{}, nil, err
	}
	return channelID, token, sequence, body[consumed+SequenceHeaderSize:], nil
}

// handleSecureMessage dispatches a MSG. GetEndpoints needs no session, which
// OPC 10000-4 Table 5 states explicitly, so it can be answered here; every
// other service is reported as unsupported through a ServiceFault rather than
// closing the connection, because the channel itself is still healthy.
func (l *Listener) handleSecureMessage(conn net.Conn, state *connectionState, body []byte) error {
	if !state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "a secure message arrived before the Hello")
	}
	channelID, token, sequence, payload, err := l.splitSecureMessage(state, body, false)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(payload, l.config.Binary)
	if err != nil {
		return err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return err
	}

	// A decoding failure means the stream cannot be trusted and closes the
	// connection. A service failure is reported as a ServiceFault, which leaves
	// the channel open because the channel itself is healthy.
	serviceBody, requestHandle, serviceErr, fatal := l.dispatchService(channelID, identifier, decoder)
	if fatal != nil {
		return fatal
	}
	if serviceErr != nil {
		status := StatusBadInternalError
		var codecErr *CodecError
		if errors.As(serviceErr, &codecErr) {
			status = codecErr.Status
		}
		encoder, encodeErr := NewEncoder(l.config.Binary)
		if encodeErr != nil {
			return encodeErr
		}
		encoder.WriteServiceFault(NewServiceFault(requestHandle, status, time.Now().UTC()))
		if serviceBody, encodeErr = encoder.Bytes(); encodeErr != nil {
			return encodeErr
		}
	}
	return l.writeSecureMessage(conn, state, MessageTypeSecure, token, sequence.RequestID, serviceBody, false)
}

// requireActivatedSession resolves the session a request claims and refuses one
// that was created but never activated. OPC 10000-4 defines
// Bad_SessionNotActivated for exactly that state, so a client cannot skip
// ActivateSession and still read the address space.
func (l *Listener) requireActivatedSession(header RequestHeader, channelID uint32, now time.Time) error {
	session, err := l.sessions.Lookup(header.AuthenticationToken, channelID, now)
	if err != nil {
		return err
	}
	if !session.Activated {
		return uacpError(StatusBadSessionNotActivated, "the session has not been activated")
	}
	return nil
}

// AddressSpace exposes the served address space so the owning application can
// populate it from the DA source.
func (l *Listener) AddressSpace() *AddressSpace { return l.space }

// StatusBadInternalError is from the OPC Foundation StatusCode list.
const StatusBadInternalError StatusCode = 0x80020000

// dispatchService decodes and runs one service. It reports the encoded response,
// the request handle to echo in a fault, a service-level failure, and a fatal
// error that must close the connection.
func (l *Listener) dispatchService(channelID uint32, identifier uint32, decoder *Decoder) ([]byte, uint32, error, error) {
	now := time.Now().UTC()
	encoder, err := NewEncoder(l.config.Binary)
	if err != nil {
		return nil, 0, nil, err
	}
	switch identifier {
	case GetEndpointsRequestEncodingID:
		request, requestErr := decoder.ReadGetEndpointsRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		encoder.WriteGetEndpointsResponse(l.endpoints.GetEndpoints(request, now))

	case CreateSessionRequestEncodingID:
		request, requestErr := decoder.ReadCreateSessionRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, serverNonce, createErr := l.sessions.Create(channelID, request, now)
		if createErr != nil {
			return nil, request.Header.RequestHandle, createErr, nil
		}
		encoder.WriteCreateSessionResponse(CreateSessionResponse{
			Header: ResponseHeader{
				Timestamp: now, RequestHandle: request.Header.RequestHandle,
				ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
			},
			SessionID:             session.ID,
			AuthenticationToken:   session.AuthenticationToken,
			RevisedSessionTimeout: session.Timeout,
			ServerNonce:           serverNonce,
			ServerEndpoints:       []EndpointDescription{l.endpoints.Endpoint()},
			// No signature is generated when the security mode is None.
			ServerSignature:       SignatureData{},
			MaxRequestMessageSize: l.config.MaxMessageSize,
		})

	case ActivateSessionRequestEncodingID:
		request, requestErr := decoder.ReadActivateSessionRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, lookupErr := l.sessions.Lookup(request.Header.AuthenticationToken, channelID, now)
		if lookupErr != nil {
			return nil, request.Header.RequestHandle, lookupErr, nil
		}
		serverNonce, activateErr := l.sessions.Activate(session, request, l.config.Endpoint.AnonymousPolicyID, now)
		if activateErr != nil {
			return nil, request.Header.RequestHandle, activateErr, nil
		}
		encoder.WriteActivateSessionResponse(ActivateSessionResponse{
			Header: ResponseHeader{
				Timestamp: now, RequestHandle: request.Header.RequestHandle,
				ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
			},
			ServerNonce: serverNonce,
			Results:     []StatusCode{},
			Diagnostics: []DiagnosticInfo{},
		})

	case CloseSessionRequestEncodingID:
		request, requestErr := decoder.ReadCloseSessionRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		if closeErr := l.sessions.Close(request.Header.AuthenticationToken, channelID, now); closeErr != nil {
			return nil, request.Header.RequestHandle, closeErr, nil
		}
		encoder.WriteCloseSessionResponse(CloseSessionResponse{Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		}})

	case BrowseRequestEncodingID:
		request, requestErr := decoder.ReadBrowseRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		if sessionErr := l.requireActivatedSession(request.Header, channelID, now); sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		response, browseErr := l.browse.Browse(request, now)
		if browseErr != nil {
			return nil, request.Header.RequestHandle, browseErr, nil
		}
		encoder.WriteBrowseResponse(response)

	case BrowseNextRequestEncodingID:
		request, requestErr := decoder.ReadBrowseNextRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		if sessionErr := l.requireActivatedSession(request.Header, channelID, now); sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		response, browseErr := l.browse.BrowseNext(request, now)
		if browseErr != nil {
			return nil, request.Header.RequestHandle, browseErr, nil
		}
		encoder.WriteBrowseNextResponse(response)

	default:
		// The request handle cannot be trusted from an unparsed body, so the
		// fault echoes zero rather than a guessed value.
		encoder.WriteServiceFault(NewServiceFault(0, StatusBadServiceUnsupported, now))
	}
	body, err := encoder.Bytes()
	return body, 0, nil, err
}

// writeSecureMessage frames a single-chunk response. Multi-chunk responses are
// not produced yet; a body that does not fit the negotiated buffer is reported
// rather than silently truncated.
func (l *Listener) writeSecureMessage(conn net.Conn, state *connectionState, messageType MessageType, token ChannelSecurityToken, requestID uint32, serviceBody []byte, asymmetric bool) error {
	encoder, err := NewEncoder(l.config.Binary)
	if err != nil {
		return err
	}
	// The SecureChannelId is part of the 12 byte header of Table 57, so it is
	// not repeated here; this body starts at the security header.
	if asymmetric {
		security, securityErr := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{}, 0, l.config.Binary)
		if securityErr != nil {
			return securityErr
		}
		encoder.write(security)
	} else {
		encoder.WriteUInt32(token.TokenID)
	}
	encoder.WriteUInt32(state.sendSequence.Next())
	encoder.WriteUInt32(requestID)
	encoder.write(serviceBody)
	body, err := encoder.Bytes()
	if err != nil {
		return err
	}

	header, err := EncodeSecureConversationHeader(SecureConversationHeader{
		Type: messageType, Chunk: ChunkFinal, SecureChannelID: token.SecureChannelID,
	}, len(body), state.sendSize)
	if err != nil {
		return err
	}
	return l.write(conn, append(header, body...))
}

func (l *Listener) writeMessage(conn net.Conn, messageType MessageType, chunk byte, body []byte) error {
	header, err := EncodeMessageHeader(messageType, chunk, len(body), l.config.SendBufferSize)
	if err != nil {
		return err
	}
	return l.write(conn, append(header, body...))
}

func (l *Listener) write(conn net.Conn, data []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(l.config.WriteTimeout)); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

// writeProtocolError reports a failure as an Error message when the failure has
// a UA status to report, then lets the caller close the socket. A transport
// failure carries nothing useful to the peer and is not answered.
func (l *Listener) writeProtocolError(conn net.Conn, cause error) {
	if cause == nil || errors.Is(cause, errConnectionFinished) {
		return
	}
	var codecErr *CodecError
	if !errors.As(cause, &codecErr) {
		return
	}
	encoded, err := EncodeProtocolError(ProtocolError{
		Error: codecErr.Status, Reason: codecErr.Message,
	}, l.config.Binary)
	if err != nil {
		return
	}
	_ = l.writeMessage(conn, MessageTypeError, ChunkFinal, encoded)
}

// ExpireStaleChannels reclaims channels whose tokens have all expired, and
// sessions that have gone quiet past their revised timeout. A server calls it
// periodically; it is exposed rather than run on an internal timer so the
// owning application keeps control of its own scheduling.
func (l *Listener) ExpireStaleChannels(now time.Time) int {
	return l.registry.ExpireStale(now) +
		l.sessions.ExpireStale(now) +
		l.browse.ExpireContinuationPoints(now)
}

// Shutdown closes the listener and waits for the context.
func (l *Listener) Shutdown(ctx context.Context) error {
	if err := l.Close(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
