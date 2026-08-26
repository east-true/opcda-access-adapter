package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"strings"
	"sync"
	"time"

	grpcfrontend "github.com/east-true/opcda-access-adapter/internal/frontend/grpc"
	frontend "github.com/east-true/opcda-access-adapter/internal/frontend/http"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	"github.com/east-true/opcda-access-adapter/internal/opcua"
)

type Service struct {
	config  Config
	runtime opcda.Runtime
	http    *frontend.Server
	server  *stdhttp.Server
	grpc    *grpcfrontend.Server
	opcua   *opcua.Listener
	errors  chan error

	mu       sync.Mutex
	listener net.Listener
	terminal bool

	// stopWatch ends the connection-generation watch that invalidates the UA
	// address space after a reconnect.
	stopWatch chan struct{}
	watchDone chan struct{}
}

const startupCleanupTimeout = 10 * time.Second

func New(config Config, runtime opcda.Runtime) (*Service, error) {
	if err := config.finalizeAndValidate(); err != nil {
		return nil, fmt.Errorf("invalid application configuration: %w", err)
	}
	if runtime == nil {
		var err error
		runtime, err = opcda.New(config.Runtime)
		if err != nil {
			return nil, fmt.Errorf("create OPC DA runtime: %w", err)
		}
	}
	service := &Service{
		config:  config,
		runtime: runtime,
		errors:  make(chan error, 1),
	}
	switch config.Frontend {
	case FrontendOPCUA:
		listener, err := newOPCUAListener(config, runtime)
		if err != nil {
			return nil, err
		}
		service.opcua = listener
	case FrontendHTTP:
		httpServer := frontend.New(runtime, frontend.Config{
			MaxBodyBytes:        config.MaxHTTPBodyBytes,
			MaxConcurrent:       config.MaxConcurrentRequests,
			RequestDeadline:     config.RequestDeadline,
			MaxReadItems:        config.Runtime.Limits.MaxReadItems,
			MaxWriteItems:       config.Runtime.Limits.MaxWriteItems,
			MaxBrowseEntries:    config.Runtime.Limits.MaxBrowseEntries,
			MaxBrowseDepth:      config.Runtime.Limits.MaxBrowseDepth,
			MaxItemIDBytes:      config.Runtime.Limits.MaxItemIDBytes,
			MaxJSONDepth:        config.MaxJSONDepth,
			RequireLoopbackHost: listenAddressIsLoopback(config.HTTPListenAddress),
		})
		service.http = httpServer
		service.server = &stdhttp.Server{
			Handler:           httpServer,
			ReadHeaderTimeout: config.HTTPReadHeaderTimeout,
			ReadTimeout:       config.HTTPReadTimeout,
			WriteTimeout:      config.HTTPWriteTimeout,
			IdleTimeout:       config.HTTPIdleTimeout,
			MaxHeaderBytes:    config.MaxHTTPHeaderBytes,
		}
	case FrontendGRPC:
		service.grpc = grpcfrontend.New(runtime, grpcfrontend.Config{
			MaxConcurrent:       config.MaxConcurrentGRPCRPCs,
			MaxConcurrentStream: config.MaxGRPCStreams,
			MaxReceiveBytes:     config.MaxGRPCReceiveBytes,
			MaxSendBytes:        config.MaxGRPCSendBytes,
			MaxMetadataBytes:    config.MaxGRPCMetadataBytes,
			ConnectionTimeout:   config.GRPCConnectionTimeout,
			MaxConnectionIdle:   config.GRPCMaxConnectionIdle,
			MaxConnectionAge:    config.GRPCMaxConnectionAge,
			MaxConnectionGrace:  config.GRPCMaxConnectionGrace,
			KeepaliveMinTime:    config.GRPCKeepaliveMinTime,
			RequestDeadline:     config.RequestDeadline,
			MaxReadItems:        config.Runtime.Limits.MaxReadItems,
			MaxWriteItems:       config.Runtime.Limits.MaxWriteItems,
			MaxBrowseEntries:    config.Runtime.Limits.MaxBrowseEntries,
			MaxBrowseDepth:      config.Runtime.Limits.MaxBrowseDepth,
			MaxItemIDBytes:      config.Runtime.Limits.MaxItemIDBytes,

			MaxSubscribeItems:      config.Runtime.Limits.MaxSubscriptionItems,
			MaxSubscriptionStreams: config.Runtime.Limits.MaxSubscriptions,
		})
	}
	return service, nil
}

func listenAddressIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("frontend listener already started")
	}
	if s.terminal {
		return errors.New("service cannot be restarted after shutdown or startup failure")
	}
	listenAddress := s.config.HTTPListenAddress
	maximumConnections := s.config.MaxHTTPConnections
	switch s.config.Frontend {
	case FrontendGRPC:
		listenAddress = s.config.GRPCListenAddress
		maximumConnections = s.config.MaxGRPCConnections
	case FrontendOPCUA:
		listenAddress = s.config.OPCUAListenAddress
		// The UA listener enforces its own connection bound, so the shared
		// bounded listener is given the same ceiling rather than a tighter one
		// that would silently override it.
		maximumConnections = opcuaMaxConnections
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		s.terminal = true
		cleanupContext, cancel := context.WithTimeout(context.Background(), startupCleanupTimeout)
		defer cancel()
		cleanupErr := s.runtime.Shutdown(cleanupContext)
		return errors.Join(fmt.Errorf("listen on %s: %w", listenAddress, err), cleanupErr)
	}
	listener = newBoundedListener(listener, maximumConnections)
	s.listener = listener
	switch s.config.Frontend {
	case FrontendHTTP:
		s.http.SetListening(true)
		go func() {
			err := s.server.Serve(listener)
			s.http.SetListening(false)
			if err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
				s.reportListenerError(fmt.Errorf("serve HTTP: %w", err))
			}
		}()
	case FrontendGRPC:
		go func() {
			if err := s.grpc.Serve(listener); err != nil {
				s.reportListenerError(fmt.Errorf("serve gRPC: %w", err))
			}
		}()
	case FrontendOPCUA:
		go func() {
			if err := s.opcua.Serve(listener); err != nil {
				s.reportListenerError(fmt.Errorf("serve OPC UA: %w", err))
			}
		}()
		s.startAddressSpaceWatch()
	}
	return nil
}

func (s *Service) reportListenerError(err error) {
	select {
	case s.errors <- err:
	default:
	}
}

// Errors reports an unexpected asynchronous frontend listener failure.
// Graceful shutdown does not emit an error.
func (s *Service) Errors() <-chan error {
	return s.errors
}

// boundedListener limits accepted TCP connections independently from the
// handler concurrency bound. This prevents incomplete headers and slow bodies
// from growing connection state without limit.
type boundedListener struct {
	net.Listener
	permits chan struct{}
}

func newBoundedListener(listener net.Listener, maximum int) net.Listener {
	return &boundedListener{Listener: listener, permits: make(chan struct{}, maximum)}
}

func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.permits <- struct{}{}:
			return &boundedConnection{Conn: connection, release: func() { <-l.permits }}, nil
		default:
			_ = connection.Close()
		}
	}
}

type boundedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (s *Service) Address() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Service) Frontend() FrontendType { return s.config.Frontend }

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.terminal = true
	s.mu.Unlock()
	var frontendErr error
	switch s.config.Frontend {
	case FrontendHTTP:
		s.http.SetListening(false)
		frontendErr = s.server.Shutdown(ctx)
	case FrontendGRPC:
		frontendErr = s.grpc.GracefulStop(ctx)
	case FrontendOPCUA:
		s.stopAddressSpaceWatch()
		frontendErr = s.opcua.Shutdown(ctx)
	}
	runtimeErr := s.runtime.Shutdown(ctx)
	return errors.Join(frontendErr, runtimeErr)
}

// opcuaMaxConnections is the ceiling the shared bounded listener applies to the
// UA frontend. The UA listener enforces its own, tighter bound; this only stops
// the accept loop from growing without limit.
const opcuaMaxConnections = 256

// newOPCUAListener builds the UA listener from the reviewed configuration.
//
// Only SecurityMode None is implemented. ADR-0016 forbids describing that as
// production ready, and the guided setup says so where an operator will see it.
// opcuaManufacturerName is what the standard Server BuildInfo reports as the
// manufacturer. It names the project, not a vendor: this adapter is not a
// product of any OPC vendor and must not appear to be one.
const opcuaManufacturerName = "opcda-access-adapter"

func newOPCUAListener(config Config, runtime opcda.Runtime) (*opcua.Listener, error) {
	listenerConfig := opcua.DefaultListenerConfig()
	listenerConfig.Endpoint = opcua.EndpointConfig{
		EndpointURL:         config.OPCUA.EndpointURL,
		ApplicationURI:      config.OPCUA.ApplicationURI,
		ProductURI:          config.OPCUA.ProductURI,
		ApplicationName:     config.OPCUA.ApplicationName,
		SecurityPolicyURI:   config.OPCUA.SecurityPolicyURI,
		TransportProfileURI: config.OPCUA.TransportProfileURI,
		AnonymousPolicyID:   config.OPCUA.AnonymousPolicyID,
	}
	listenerConfig.AddressSpace = opcua.AddressSpaceConfig{
		NamespaceURI:     config.OPCUA.NamespaceURI,
		SourceFolderName: config.OPCUA.SourceFolderName,
		ManufacturerName: opcuaManufacturerName,
		SoftwareVersion:  config.OPCUA.SoftwareVersion,
		BuildNumber:      config.OPCUA.BuildNumber,
	}
	listenerConfig.DataAccess.MaxNodesPerRead = config.Runtime.Limits.MaxReadItems
	listenerConfig.DataAccess.MaxNodesPerWrite = config.Runtime.Limits.MaxWriteItems
	listenerConfig.DataAccess.RequestTimeout = config.RequestDeadline
	// The address space bound is shared, so browsing and addressing items
	// directly draw on one budget rather than two.
	listenerConfig.DataAccess.MaxNodes = listenerConfig.Population.MaxNodes
	listenerConfig.Subscriptions.MaxNodes = listenerConfig.Population.MaxNodes
	listenerConfig.Subscriptions.MaxMonitoredItems = config.Runtime.Limits.MaxSubscriptionItems
	listenerConfig.Subscriptions.MaxSubscriptions = config.Runtime.Limits.MaxSubscriptions
	listenerConfig.Browse.MaxReferencesPerNode = config.Runtime.Limits.MaxBrowseEntries
	listenerConfig.Population.MaxDepth = config.Runtime.Limits.MaxBrowseDepth

	// OPC 10000-6 Table 57 advises that the first SecureChannelId after a
	// restart should be unlikely to collide with one a previously connected
	// client still holds, so the counters are seeded rather than starting at a
	// fixed value.
	channelSeed, err := randomSeed()
	if err != nil {
		return nil, err
	}
	tokenSeed, err := randomSeed()
	if err != nil {
		return nil, err
	}
	listener, err := opcua.NewListenerWithRuntime(listenerConfig, runtime, channelSeed, tokenSeed)
	if err != nil {
		return nil, fmt.Errorf("create OPC UA listener: %w", err)
	}
	return listener, nil
}

func randomSeed() (uint32, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("seed OPC UA identifiers: %w", err)
	}
	return binary.LittleEndian.Uint32(raw[:]), nil
}

// addressSpaceWatchInterval is how often the connection generation is checked.
// A reconnect is not urgent to observe: until the address space is invalidated
// a client sees the previous generation's nodes, which the DA runtime will
// refuse to read anyway.
const addressSpaceWatchInterval = 2 * time.Second

// startAddressSpaceWatch is called from Start with s.mu held, so it assigns the
// watch fields directly.
//
// startAddressSpaceWatch invalidates the UA address space when the DA runtime
// reconnects. A new connection generation may expose a different address space,
// and item registrations from the previous generation are already invalid, so
// the cached nodes must not be served as if they were current.
//
// It also expires stale secure channels, sessions, and continuation points on
// the same tick, which keeps that housekeeping on one owned goroutine rather
// than an internal timer inside the listener.
func (s *Service) startAddressSpaceWatch() {
	stop := make(chan struct{})
	done := make(chan struct{})
	s.stopWatch = stop
	s.watchDone = done

	lastGeneration := s.runtime.Status(context.Background()).ConnectionGeneration
	go func() {
		defer close(done)
		ticker := time.NewTicker(addressSpaceWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				status := s.runtime.Status(context.Background())
				if status.ConnectionGeneration != lastGeneration {
					lastGeneration = status.ConnectionGeneration
					s.opcua.InvalidateAddressSpace()
				}
				s.opcua.ExpireStaleChannels(time.Now().UTC())
			}
		}
	}()
}

func (s *Service) stopAddressSpaceWatch() {
	s.mu.Lock()
	stop, done := s.stopWatch, s.watchDone
	s.stopWatch, s.watchDone = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}
