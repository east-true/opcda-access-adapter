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
		MaxBodyBytes:    config.MaxHTTPBodyBytes,
		MaxConcurrent:   config.MaxConcurrentRequests,
		RequestDeadline: config.RequestDeadline,
		MaxReadItems:    config.Runtime.Limits.MaxReadItems,
		MaxItemIDBytes:  config.Runtime.Limits.MaxItemIDBytes,
	})
	return &Service{
		config:  config,
		runtime: runtime,
		http:    httpServer,
		server:  &stdhttp.Server{Handler: httpServer},
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
	s.listener = listener
	s.http.SetListening(true)
	go func() {
		_ = s.server.Serve(listener)
	}()
	return nil
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
