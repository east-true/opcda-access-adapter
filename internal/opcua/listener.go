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

	"github.com/east-true/opcda-access-adapter/internal/opcda"
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
	DataAccess     DataAccessLimits
	Population     PopulationLimits
	Subscriptions  SubscriptionLimits
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
		DataAccess:        DefaultDataAccessLimits(),
		Population:        DefaultPopulationLimits(),
		Subscriptions:     DefaultSubscriptionLimits(),
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
	if err := config.DataAccess.validate(); err != nil {
		return err
	}
	if err := config.Population.validate(); err != nil {
		return err
	}
	if err := config.Subscriptions.validate(); err != nil {
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
// Listener holds the services a server shares across every connection.
//
// # Concurrency
//
// The listener serves each connection on its own goroutine, and the owning
// application calls ExpireStaleChannels and InvalidateAddressSpace from
// another. So every field below is reached from several goroutines at once,
// and the rule for this package follows from that:
//
//   - A service that holds mutable state is responsible for its own
//     synchronisation. It does not assume a caller serialises access, because
//     no caller does.
//   - A service that is immutable after construction needs nothing, and
//     EndpointService and DataAccessService are exactly that.
//   - A service never hands out a pointer to state it owns. Callers get value
//     snapshots, so there is no way to read a field while another connection
//     writes it, and no way to hold a stale object across two calls.
//
// The rule is not new; six of these services already followed it. Two — the
// channel and session registries — carried a comment asserting a
// single-goroutine owner that the listener has never provided, and nothing
// checked, so two clients connecting at the same time faulted the process with
// a Go runtime "concurrent map read and map write". concurrency_test.go now
// exercises the rule, so a service that opts out of it fails there.
type Listener struct {
	config    ListenerConfig
	registry  *ChannelRegistry
	endpoints *EndpointService
	sessions  *SessionRegistry
	space     *AddressSpace
	browse    *BrowseService
	data      *DataAccessService
	populator *Populator
	subs      *SubscriptionService

	listening atomic.Bool
	slots     chan struct{}

	// active counts every goroutine the listener starts — connection loops and
	// the goroutines that hold a Publish — so Shutdown can wait for all of
	// them. It lives here rather than inside Serve because a Publish outlives
	// the read loop that accepted it, and a WaitGroup local to Serve could
	// only ever see the read loops.
	active sync.WaitGroup
	// drained is closed when Serve has returned and every goroutine it started
	// has finished. It is what lets Shutdown honour its context instead of
	// pretending to.
	drained chan struct{}

	drainedOnce sync.Once

	mu       sync.Mutex
	closed   bool
	conns    map[net.Conn]struct{}
	listener net.Listener
}

// NewListener builds a listener with no DA runtime, so Read and Write report
// that no source is available. NewListenerWithRuntime attaches one.
func NewListener(config ListenerConfig, channelIDSeed, tokenIDSeed uint32) (*Listener, error) {
	return NewListenerWithRuntime(config, nil, channelIDSeed, tokenIDSeed)
}

// NewListenerWithRuntime attaches the DA runtime that answers Read and Write.
func NewListenerWithRuntime(config ListenerConfig, runtime opcda.Runtime, channelIDSeed, tokenIDSeed uint32) (*Listener, error) {
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
	// The address space's ServerArray names this server, and the endpoint is
	// where that URI is configured.
	addressSpaceConfig := config.AddressSpace
	addressSpaceConfig.ApplicationURI = config.Endpoint.ApplicationURI
	if addressSpaceConfig.ProductURI == "" {
		addressSpaceConfig.ProductURI = config.Endpoint.ProductURI
	}
	if addressSpaceConfig.ProductName == "" {
		addressSpaceConfig.ProductName = config.Endpoint.ApplicationName
	}
	space, err := NewAddressSpace(addressSpaceConfig)
	if err != nil {
		return nil, err
	}
	browse, err := NewBrowseService(space, config.Browse)
	if err != nil {
		return nil, err
	}
	var data *DataAccessService
	var populator *Populator
	if runtime != nil {
		if data, err = NewDataAccessService(space, runtime, config.DataAccess); err != nil {
			return nil, err
		}
		if populator, err = NewPopulator(space, runtime, config.Population); err != nil {
			return nil, err
		}
		browse.AttachPopulator(populator)
	}
	var subs *SubscriptionService
	if runtime != nil {
		// Table 82 advises that subscription ids start from a random value, so
		// a restart does not reuse identifiers a client still holds.
		if subs, err = NewSubscriptionService(space, runtime, config.Subscriptions, channelIDSeed); err != nil {
			return nil, err
		}
	}
	listener := &Listener{
		config:    config,
		registry:  registry,
		endpoints: endpoints,
		sessions:  sessions,
		space:     space,
		browse:    browse,
		data:      data,
		populator: populator,
		subs:      subs,
		slots:     make(chan struct{}, config.MaxConnections),
		conns:     make(map[net.Conn]struct{}),
		drained:   make(chan struct{}),
	}
	if subs != nil {
		// A session's subscriptions hold DA groups open on the source, so
		// ending a session must release them — whichever route ended it. This
		// is registered once rather than repeated at each call site, because a
		// call site that forgets is a leak on a real DA server.
		sessions.OnSessionEnd(func(session SessionInfo) {
			subs.ReleaseSession(context.Background(), session.Key())
			// 7.9: a session's continuation points do not outlive it.
			browse.ReleaseSession(session.Key())
		})
	}
	return listener, nil
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

	// Nothing is served after this returns, so anything still running is
	// waited for and the completion signal is raised for Shutdown. The order
	// inside matters: everything an observer of the signal may check is
	// settled before the signal is raised, so waiting on it is enough.
	defer func() {
		l.active.Wait()
		l.listening.Store(false)
		l.drainedOnce.Do(func() { close(l.drained) })
	}()
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
		l.active.Add(1)
		go func() {
			defer l.active.Done()
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

	// writeMu serialises writes to the socket. A held Publish is answered from
	// its own goroutine, so a response can now be written while the read loop
	// is answering something else, and two interleaved chunks would corrupt
	// the stream. The send sequence number is assigned under the same lock so
	// the numbers a client sees stay in the order they were written.
	writeMu sync.Mutex
	// done is closed when the connection is finished, releasing any Publish
	// still waiting for something to report.
	done chan struct{}
	// publishing counts the Publish requests being held for this connection.
	publishing atomic.Int32
}

func (l *Listener) serveConnection(conn net.Conn) {
	state := &connectionState{
		// Before negotiation the server's own receive buffer bounds a header.
		receiveSize: l.config.ReceiveBufferSize,
		sendSize:    l.config.SendBufferSize,
		done:        make(chan struct{}),
	}
	defer close(state.done)
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
		if state.publishing.Load() > 0 {
			// The connection is idle because the server owes this client a
			// Publish response, not because the client has gone away. Closing
			// it here would break the one exchange a subscription depends on,
			// so the read deadline waits for the keep-alive that is already
			// bounded by the subscription's own interval.
			deadline = l.publishIdleTimeout()
		}
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
	parts, err := l.splitSecureMessage(state, body, true)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(parts.Payload, l.config.Binary)
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
	response, err := state.service.OpenSecureChannel(request, parts.ChannelID, time.Now().UTC())
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
		response.SecurityToken, parts.Sequence.RequestID, serviceBody, parts.SecurityPolicyURI, true)
}

func (l *Listener) handleCloseChannel(conn net.Conn, state *connectionState, body []byte) error {
	if !state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "CloseSecureChannel arrived before the Hello")
	}
	parts, err := l.splitSecureMessage(state, body, false)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(parts.Payload, l.config.Binary)
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
	if _, err := state.service.CloseSecureChannel(request, parts.ChannelID, time.Now().UTC()); err != nil {
		return err
	}
	// The clause has the socket closed after a CloseSecureChannel; returning an
	// io.EOF-shaped signal ends the loop without writing an error message.
	return errConnectionFinished
}

var errConnectionFinished = errors.New("secure channel closed by the client")

// secureMessageParts is what one secure conversation chunk carries ahead of its
// service body.
type secureMessageParts struct {
	ChannelID uint32
	Token     ChannelSecurityToken
	Sequence  SequenceHeader
	Payload   []byte
	// SecurityPolicyURI is the policy the sender named in an OPN chunk's
	// asymmetric security header. OPC 10000-6 6.7.7 requires the response to
	// name the same policy the request did, so it has to survive as far as the
	// reply. It is empty for a symmetric chunk, which carries a TokenId
	// instead.
	SecurityPolicyURI string
}

// splitSecureMessage reads the 12 byte secure conversation header, the security
// header, and the sequence header, and returns the remaining service payload.
// The message header itself has already been consumed by readMessage, so the
// body here starts at the SecureChannelId.
func (l *Listener) splitSecureMessage(state *connectionState, body []byte, asymmetric bool) (secureMessageParts, error) {
	var parts secureMessageParts
	decoder, err := NewDecoder(body, l.config.Binary)
	if err != nil {
		return parts, err
	}
	channelID, err := decoder.ReadUInt32()
	if err != nil {
		return parts, err
	}
	parts.ChannelID = channelID
	consumed := 4
	if asymmetric {
		maxCertificate := MaxSenderCertificateSize(int(state.receiveSize), MaxSecurityPolicyURIBytes, 0, 0)
		security, used, securityErr := DecodeAsymmetricSecurityHeader(body[consumed:], maxCertificate, l.config.Binary)
		if securityErr != nil {
			return parts, securityErr
		}
		// Only the None policy is served, so a request that presents a
		// certificate is refused rather than silently treated as unsecured.
		if len(security.SenderCertificate) != 0 || len(security.ReceiverCertificateThumbprint) != 0 {
			return parts, uacpError(StatusBadSecurityPolicyRejected,
				"only an unsecured channel is served by this listener")
		}
		// OPC 10000-6 6.7.7 requires the receiver to verify that it supports
		// the requested SecurityPolicy. Without this check a client that asked
		// for a signed and encrypted policy would be handed an unsecured
		// channel and never told, which is the one outcome the clause exists
		// to prevent. Since exactly one policy is served, comparing against it
		// also satisfies the clause's rule that a renew must carry the policy
		// the channel was created with.
		//
		// A request that names no policy is refused too: an unnamed policy
		// cannot be verified as supported, and 6.7.7 requires the response to
		// carry the same policy the request did, which cannot be answered when
		// the request carried none.
		if security.SecurityPolicyURI != l.config.Endpoint.SecurityPolicyURI {
			return parts, uacpError(StatusBadSecurityPolicyRejected,
				"the requested security policy is not served by this endpoint")
		}
		parts.SecurityPolicyURI = security.SecurityPolicyURI
		consumed += used
	} else {
		tokenID, tokenErr := decoder.ReadUInt32()
		if tokenErr != nil {
			return parts, tokenErr
		}
		channel, acceptErr := l.registry.Accept(channelID, tokenID, time.Now().UTC())
		if acceptErr != nil {
			return parts, acceptErr
		}
		parts.Token = channel.Token
		consumed += 4
	}
	if len(body) < consumed+SequenceHeaderSize {
		return parts, decodingError("message is too short for a sequence header")
	}
	sequence, err := DecodeSequenceHeader(body[consumed:consumed+SequenceHeaderSize], l.config.Binary)
	if err != nil {
		return parts, err
	}
	if err := state.receiveSequence.Accept(sequence.SequenceNumber); err != nil {
		return parts, err
	}
	parts.Sequence = sequence
	parts.Payload = body[consumed+SequenceHeaderSize:]
	return parts, nil
}

// handleSecureMessage dispatches a MSG. GetEndpoints needs no session, which
// OPC 10000-4 Table 5 states explicitly, so it can be answered here; every
// other service is reported as unsupported through a ServiceFault rather than
// closing the connection, because the channel itself is still healthy.
func (l *Listener) handleSecureMessage(conn net.Conn, state *connectionState, body []byte) error {
	if !state.negotiated {
		return uacpError(StatusBadTcpMessageTypeInvalid, "a secure message arrived before the Hello")
	}
	parts, err := l.splitSecureMessage(state, body, false)
	if err != nil {
		return err
	}
	decoder, err := NewDecoder(parts.Payload, l.config.Binary)
	if err != nil {
		return err
	}
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		return err
	}

	// A Publish is held until the subscription has something to report, so it
	// is answered from its own goroutine. Handling it in the read loop would
	// stop this connection from serving anything else for as long as the wait
	// lasts, and a client keeps a Publish outstanding while it reads and
	// browses on the same channel.
	if identifier == PublishRequestEncodingID {
		return l.handlePublish(conn, state, parts, decoder)
	}

	// A decoding failure means the stream cannot be trusted and closes the
	// connection. A service failure is reported as a ServiceFault, which leaves
	// the channel open because the channel itself is healthy.
	serviceBody, requestHandle, serviceErr, fatal := l.dispatchService(parts.ChannelID, identifier, decoder)
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
	return l.writeSecureMessage(conn, state, MessageTypeSecure, parts.Token, parts.Sequence.RequestID, serviceBody, "", false)
}

// requireActivatedSession resolves the session a request claims and refuses one
// that was created but never activated. OPC 10000-4 defines
// Bad_SessionNotActivated for exactly that state, so a client cannot skip
// ActivateSession and still read the address space.
func (l *Listener) requireActivatedSession(header RequestHeader, channelID uint32, now time.Time) error {
	_, err := l.activatedSession(header, channelID, now)
	return err
}

// sessionSecurity describes the security of the channel a request arrived on.
// CreateSession records it and ActivateSession compares against it, so the two
// cannot disagree about what a channel offered.
func (l *Listener) sessionSecurity(channelID uint32) (SessionSecurity, error) {
	channel, err := l.registry.Lookup(channelID)
	if err != nil {
		return SessionSecurity{}, err
	}
	return SessionSecurity{
		Mode: channel.SecurityMode,
		// One endpoint is served, so its policy is the policy of every channel
		// on it. Reading it from the endpoint rather than assuming it keeps
		// the comparison meaningful if a second endpoint is ever published.
		PolicyURI:             l.config.Endpoint.SecurityPolicyURI,
		AnonymousIdentityOnly: l.endpoints.AnonymousIdentityOnly(),
	}, nil
}

// activatedSession resolves the session and returns an opaque key identifying
// it, so a subscription can be tied to the session that created it.
func (l *Listener) activatedSession(header RequestHeader, channelID uint32, now time.Time) (string, error) {
	session, err := l.sessions.Lookup(header.AuthenticationToken, channelID, now)
	if err != nil {
		return "", err
	}
	if !session.Activated {
		return "", uacpError(StatusBadSessionNotActivated, "the session has not been activated")
	}
	return session.Key(), nil
}

// AddressSpace exposes the served address space. With a DA runtime attached the
// listener fills it from the source on demand; without one the application may
// populate it directly.
func (l *Listener) AddressSpace() *AddressSpace { return l.space }

// InvalidateAddressSpace makes the next browse of each branch go back to the
// source. The application calls it after a reconnect, because a new connection
// generation may expose a different address space.
func (l *Listener) InvalidateAddressSpace() {
	if l.populator != nil {
		l.populator.Invalidate()
	}
}

// errNoDataSource is reported when the listener has no DA runtime attached, so
// a client is told the source is unavailable rather than given empty values.
var errNoDataSource = &CodecError{
	Status:  StatusBadNotConnected,
	Message: "no OPC DA source is attached to this listener",
}

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
		// Both the nonce rule and the channel binding depend on the security
		// of the channel the request arrived on.
		security, securityErr := l.sessionSecurity(channelID)
		if securityErr != nil {
			return nil, request.Header.RequestHandle, securityErr, nil
		}
		session, serverNonce, createErr := l.sessions.Create(channelID, security, request, now)
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
		security, securityErr := l.sessionSecurity(channelID)
		if securityErr != nil {
			return nil, request.Header.RequestHandle, securityErr, nil
		}
		serverNonce, activateErr := l.sessions.Activate(request.Header.AuthenticationToken,
			channelID, security, request, l.config.Endpoint.AnonymousPolicyID, now)
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
		// The subscriptions are released by the session-end hook rather than
		// here, so every route that ends a session releases them.
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
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		response, browseErr := l.browse.Browse(context.Background(), session, request, now)
		if browseErr != nil {
			return nil, request.Header.RequestHandle, browseErr, nil
		}
		encoder.WriteBrowseResponse(response)

	case BrowseNextRequestEncodingID:
		request, requestErr := decoder.ReadBrowseNextRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		response, browseErr := l.browse.BrowseNext(session, request, now)
		if browseErr != nil {
			return nil, request.Header.RequestHandle, browseErr, nil
		}
		encoder.WriteBrowseNextResponse(response)

	case ReadRequestEncodingID:
		request, requestErr := decoder.ReadReadRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		if sessionErr := l.requireActivatedSession(request.Header, channelID, now); sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.data == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, readErr := l.data.Read(context.Background(), request, now)
		if readErr != nil {
			return nil, request.Header.RequestHandle, readErr, nil
		}
		encoder.WriteReadResponse(response)

	case WriteRequestEncodingID:
		request, requestErr := decoder.ReadWriteRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		if sessionErr := l.requireActivatedSession(request.Header, channelID, now); sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.data == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, writeErr := l.data.Write(context.Background(), request, now)
		if writeErr != nil {
			return nil, request.Header.RequestHandle, writeErr, nil
		}
		encoder.WriteWriteResponse(response)

	case CreateSubscriptionRequestEncodingID:
		request, requestErr := decoder.ReadCreateSubscriptionRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, createErr := l.subs.CreateSubscription(session, request, now)
		if createErr != nil {
			return nil, request.Header.RequestHandle, createErr, nil
		}
		encoder.WriteCreateSubscriptionResponse(response)

	case CreateMonitoredItemsRequestEncodingID:
		request, requestErr := decoder.ReadCreateMonitoredItemsRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, createErr := l.subs.CreateMonitoredItems(context.Background(), session, request, now)
		if createErr != nil {
			return nil, request.Header.RequestHandle, createErr, nil
		}
		encoder.WriteCreateMonitoredItemsResponse(response)

	case DeleteMonitoredItemsRequestEncodingID:
		request, requestErr := decoder.ReadDeleteMonitoredItemsRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, deleteErr := l.subs.DeleteMonitoredItems(context.Background(), session, request, now)
		if deleteErr != nil {
			return nil, request.Header.RequestHandle, deleteErr, nil
		}
		encoder.WriteDeleteMonitoredItemsResponse(response)

	case DeleteSubscriptionsRequestEncodingID:
		request, requestErr := decoder.ReadDeleteSubscriptionsRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, deleteErr := l.subs.DeleteSubscriptions(context.Background(), session, request, now)
		if deleteErr != nil {
			return nil, request.Header.RequestHandle, deleteErr, nil
		}
		encoder.WriteDeleteSubscriptionsResponse(response)

	case RepublishRequestEncodingID:
		request, requestErr := decoder.ReadRepublishRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, republishErr := l.subs.Republish(session, request, now)
		if republishErr != nil {
			return nil, request.Header.RequestHandle, republishErr, nil
		}
		encoder.WriteRepublishResponse(response)

	case SetPublishingModeRequestEncodingID:
		request, requestErr := decoder.ReadSetPublishingModeRequest()
		if requestErr != nil {
			return nil, 0, nil, requestErr
		}
		session, sessionErr := l.activatedSession(request.Header, channelID, now)
		if sessionErr != nil {
			return nil, request.Header.RequestHandle, sessionErr, nil
		}
		if l.subs == nil {
			return nil, request.Header.RequestHandle, errNoDataSource, nil
		}
		response, modeErr := l.subs.SetPublishingMode(session, request, now)
		if modeErr != nil {
			return nil, request.Header.RequestHandle, modeErr, nil
		}
		encoder.WriteSetPublishingModeResponse(response)

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
func (l *Listener) writeSecureMessage(conn net.Conn, state *connectionState, messageType MessageType, token ChannelSecurityToken, requestID uint32, serviceBody []byte, securityPolicyURI string, asymmetric bool) error {
	encoder, err := NewEncoder(l.config.Binary)
	if err != nil {
		return err
	}
	// The sequence number is assigned and the bytes written under one lock, so
	// a Publish answered from its own goroutine cannot interleave its chunk
	// with the read loop's, nor take a sequence number out of send order.
	state.writeMu.Lock()
	defer state.writeMu.Unlock()

	// The SecureChannelId is part of the 12 byte header of Table 57, so it is
	// not repeated here; this body starts at the security header.
	if asymmetric {
		// OPC 10000-6 6.7.7: the policy named in the response is the policy the
		// request named. The asymmetric header is the only place an OPN chunk
		// carries it, so leaving it empty leaves a client unable to tell which
		// policy secured the reply — and a conforming one refuses the channel.
		// Nothing caught this before a third-party client, because this
		// project's own decoder accepted the empty field its encoder wrote.
		security, securityErr := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{
			SecurityPolicyURI: securityPolicyURI,
		}, 0, l.config.Binary)
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
	expired := l.registry.ExpireStale(now) +
		l.sessions.ExpireStale(now) +
		l.browse.ExpireContinuationPoints(now)
	if l.subs != nil {
		// A subscription whose client stopped publishing holds a DA group open
		// on the source, so its own lifetime has to be enforced and not merely
		// reported.
		expired += l.subs.ExpireStale(context.Background(), now)
	}
	return expired
}

// Shutdown closes the listener and waits for the context.
// Shutdown stops accepting, drops live connections, and waits for everything
// the listener started to finish, or for the context to expire.
//
// It used to take a context it could not honour: the wait it needed did not
// exist, because Serve joined its goroutines privately and nothing outside
// could observe when that had happened. So Shutdown closed the socket and
// returned at once, however long a caller was prepared to wait — while the
// two other frontends, which delegate to net/http and grpc-go, really do
// drain. A caller cannot tell the difference from the signature, which is what
// made it worth fixing rather than documenting.
func (l *Listener) Shutdown(ctx context.Context) error {
	if err := l.Close(); err != nil {
		return err
	}
	// The sessions cannot outlive the listener that holds them, and ending
	// them releases the DA groups their subscriptions hold on the source.
	// Leaving that to the DA runtime's own teardown worked only because the
	// application happens to stop the runtime immediately afterwards, which
	// made this listener's shutdown depend on a caller's ordering rather than
	// on itself.
	l.sessions.TerminateAll()
	if !l.Listening() {
		// Serve was never started, so there is nothing to drain and no
		// goroutine that will ever raise the signal.
		l.drainedOnce.Do(func() { close(l.drained) })
	}
	select {
	case <-l.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// publishIdleTimeout is how long a connection may stay silent while the server
// still owes it a Publish response. A subscription sends a keep-alive at worst
// every maxPublishingInterval * MaxKeepAliveCount, so waiting that long plus
// the ordinary read timeout cannot close a connection that is behaving.
func (l *Listener) publishIdleTimeout() time.Duration {
	keepAlive := l.config.Subscriptions.MaxPublishingInterval *
		time.Duration(l.config.Subscriptions.MaxKeepAliveCount)
	return keepAlive + l.config.ReadTimeout
}

// maxOutstandingPublishesPerConnection bounds the Publish requests held for one
// connection. OPC 10000-4 5.14.5.1 has a server limit the number of active
// Publish requests, and requires it to accept more than the number of
// subscriptions created, since a client pipelines several per subscription.
const maxOutstandingPublishesPerConnection = 64

// handlePublish answers a Publish without occupying the read loop. The request
// is decoded here, because the decoder reads from the chunk this call owns, and
// the wait happens in a goroutine that writes the response when it has one.
func (l *Listener) handlePublish(conn net.Conn, state *connectionState, parts secureMessageParts, decoder *Decoder) error {
	request, err := decoder.ReadPublishRequest()
	if err != nil {
		// A body that cannot be decoded means the stream cannot be trusted.
		return err
	}
	now := time.Now().UTC()
	if serviceErr := l.publishPrecondition(); serviceErr != nil {
		return l.writeServiceFault(conn, state, parts, request.Header.RequestHandle, serviceErr)
	}
	session, sessionErr := l.activatedSession(request.Header, parts.ChannelID, now)
	if sessionErr != nil {
		return l.writeServiceFault(conn, state, parts, request.Header.RequestHandle, sessionErr)
	}

	// Table 89: a client that has run out of room is told so rather than
	// having its request silently dropped, and it then waits for one of its
	// outstanding requests before issuing another.
	if state.publishing.Add(1) > maxOutstandingPublishesPerConnection {
		state.publishing.Add(-1)
		return l.writeServiceFault(conn, state, parts, request.Header.RequestHandle,
			uacpError(StatusBadTooManyPublishRequests,
				"this connection already holds %d Publish requests",
				maxOutstandingPublishesPerConnection))
	}

	// The session stays alive for as long as this request is being served.
	// OPC 10000-4 5.7.2 terminates a session when the client "fails to issue a
	// Service request" within the timeout; a request the server is still
	// holding is one the client did issue, so the idle clock must not run
	// against it. The clock restarts when the request is answered.
	releaseRequest, live := l.sessions.BeginRequest(request.Header.AuthenticationToken)
	if !live {
		state.publishing.Add(-1)
		return l.writeServiceFault(conn, state, parts, request.Header.RequestHandle,
			uacpError(StatusBadSessionIDInvalid, "the session is not known"))
	}
	// The same clause aborts outstanding requests when a session is
	// terminated, so a Publish held for a session that ends is answered with
	// Bad_SessionClosed rather than waiting on a session that is gone.
	sessionEnded, _ := l.sessions.Ended(request.Header.AuthenticationToken)

	l.active.Add(1)
	go func() {
		defer l.active.Done()
		defer state.publishing.Add(-1)
		defer releaseRequest()

		// The wait ends with the connection or with the session, so neither a
		// client that disappears nor a session that is terminated leaves a
		// goroutine holding a request nobody will read.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		terminated := make(chan struct{})
		go func() {
			select {
			case <-state.done:
				cancel()
			case <-sessionEnded:
				close(terminated)
				cancel()
			case <-ctx.Done():
			}
		}()

		response, publishErr := l.subs.Publish(ctx, session, request, now)
		if publishErr != nil {
			select {
			case <-terminated:
				_ = l.writeServiceFault(conn, state, parts, request.Header.RequestHandle,
					uacpError(StatusBadSessionClosed, "the session was terminated"))
				return
			default:
			}
			if ctx.Err() != nil {
				// The connection is gone; there is nobody to tell.
				return
			}
			_ = l.writeServiceFault(conn, state, parts, request.Header.RequestHandle, publishErr)
			return
		}
		encoder, encodeErr := NewEncoder(l.config.Binary)
		if encodeErr != nil {
			return
		}
		encoder.WritePublishResponse(response)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			return
		}
		_ = l.writeSecureMessage(conn, state, MessageTypeSecure, parts.Token,
			parts.Sequence.RequestID, body, "", false)
	}()
	return nil
}

// publishPrecondition reports the failures that are answered before a Publish
// is held, so a client is not left waiting on a request that could never be
// served.
func (l *Listener) publishPrecondition() error {
	if l.subs == nil {
		return errNoDataSource
	}
	return nil
}

// writeServiceFault answers one request with a fault, leaving the channel open
// because the channel itself is healthy.
func (l *Listener) writeServiceFault(conn net.Conn, state *connectionState, parts secureMessageParts, requestHandle uint32, serviceErr error) error {
	status := StatusBadInternalError
	var codecErr *CodecError
	if errors.As(serviceErr, &codecErr) {
		status = codecErr.Status
	}
	encoder, err := NewEncoder(l.config.Binary)
	if err != nil {
		return err
	}
	encoder.WriteServiceFault(NewServiceFault(requestHandle, status, time.Now().UTC()))
	body, err := encoder.Bytes()
	if err != nil {
		return err
	}
	return l.writeSecureMessage(conn, state, MessageTypeSecure, parts.Token,
		parts.Sequence.RequestID, body, "", false)
}
