package app

import (
	"context"
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
)

type Service struct {
	config  Config
	runtime opcda.Runtime
	http    *frontend.Server
	server  *stdhttp.Server
	grpc    *grpcfrontend.Server
	errors  chan error

	mu       sync.Mutex
	listener net.Listener
	terminal bool
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
	if s.config.Frontend == FrontendGRPC {
		listenAddress = s.config.GRPCListenAddress
		maximumConnections = s.config.MaxGRPCConnections
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
	if s.config.Frontend == FrontendHTTP {
		s.http.SetListening(true)
		go func() {
			err := s.server.Serve(listener)
			s.http.SetListening(false)
			if err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
				s.reportListenerError(fmt.Errorf("serve HTTP: %w", err))
			}
		}()
	} else {
		go func() {
			if err := s.grpc.Serve(listener); err != nil {
				s.reportListenerError(fmt.Errorf("serve gRPC: %w", err))
			}
		}()
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
	if s.config.Frontend == FrontendHTTP {
		s.http.SetListening(false)
		frontendErr = s.server.Shutdown(ctx)
	} else {
		frontendErr = s.grpc.GracefulStop(ctx)
	}
	runtimeErr := s.runtime.Shutdown(ctx)
	return errors.Join(frontendErr, runtimeErr)
}
