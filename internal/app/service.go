package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"sync"

	frontend "github.com/east-true/opcda-access-adapter/internal/frontend/http"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type Service struct {
	config  Config
	runtime opcda.Runtime
	http    *frontend.Server
	server  *stdhttp.Server

	mu       sync.Mutex
	listener net.Listener
}

func New(config Config, runtime opcda.Runtime) (*Service, error) {
	if runtime == nil {
		var err error
		runtime, err = opcda.New(config.Runtime)
		if err != nil {
			return nil, fmt.Errorf("create OPC DA runtime: %w", err)
		}
	}
	httpServer := frontend.New(runtime, frontend.Config{
		MaxBodyBytes:     config.MaxHTTPBodyBytes,
		MaxConcurrent:    config.MaxConcurrentRequests,
		RequestDeadline:  config.RequestDeadline,
		MaxReadItems:     config.Runtime.Limits.MaxReadItems,
		MaxWriteItems:    config.Runtime.Limits.MaxWriteItems,
		MaxBrowseEntries: config.Runtime.Limits.MaxBrowseEntries,
		MaxBrowseDepth:   config.Runtime.Limits.MaxBrowseDepth,
		MaxItemIDBytes:   config.Runtime.Limits.MaxItemIDBytes,
	})
	return &Service{
		config:  config,
		runtime: runtime,
		http:    httpServer,
		server: &stdhttp.Server{
			Handler:           httpServer,
			ReadHeaderTimeout: config.HTTPReadHeaderTimeout,
			ReadTimeout:       config.HTTPReadTimeout,
			WriteTimeout:      config.HTTPWriteTimeout,
			IdleTimeout:       config.HTTPIdleTimeout,
			MaxHeaderBytes:    config.MaxHTTPHeaderBytes,
		},
	}, nil
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("HTTP listener already started")
	}
	listener, err := net.Listen("tcp", s.config.HTTPListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.config.HTTPListenAddress, err)
	}
	listener = newBoundedListener(listener, s.config.MaxHTTPConnections)
	s.listener = listener
	s.http.SetListening(true)
	go func() {
		_ = s.server.Serve(listener)
	}()
	return nil
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

func (s *Service) Shutdown(ctx context.Context) error {
	s.http.SetListening(false)
	httpErr := s.server.Shutdown(ctx)
	runtimeErr := s.runtime.Shutdown(ctx)
	return errors.Join(httpErr, runtimeErr)
}
