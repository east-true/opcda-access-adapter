package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type Config struct {
	MaxBodyBytes    int64
	MaxConcurrent   int
	RequestDeadline time.Duration
}

type Server struct {
	runtime   opcda.Runtime
	config    Config
	listening atomic.Bool
	requests  chan struct{}
}

func New(runtime opcda.Runtime, config Config) *Server {
	return &Server{
		runtime:  runtime,
		config:   config,
		requests: make(chan struct{}, config.MaxConcurrent),
	}
}

func (s *Server) SetListening(value bool) {
	s.listening.Store(value)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case s.requests <- struct{}{}:
		defer func() { <-s.requests }()
	default:
		writeError(w, http.StatusServiceUnavailable, opcda.CodeQueueFull, "too many concurrent HTTP requests")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.config.RequestDeadline)
	defer cancel()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/status":
		s.handleStatus(ctx, w)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
	}
}

func (s *Server) handleStatus(ctx context.Context, w http.ResponseWriter) {
	status := s.runtime.Status(ctx)
	response := struct {
		State  string `json:"state"`
		Source struct {
			ProgID               string `json:"progId,omitempty"`
			CLSID                string `json:"clsid,omitempty"`
			ConnectionGeneration uint64 `json:"connectionGeneration"`
		} `json:"source"`
		Capabilities struct {
			Browse    string `json:"browse"`
			Read      bool   `json:"read"`
			Write     bool   `json:"write"`
			Subscribe bool   `json:"subscribe"`
		} `json:"capabilities"`
		WriteEnabled bool `json:"writeEnabled"`
		Frontend     struct {
			HTTP struct {
				Listening bool `json:"listening"`
			} `json:"http"`
		} `json:"frontend"`
	}{
		State: string(status.State),
		Source: struct {
			ProgID               string `json:"progId,omitempty"`
			CLSID                string `json:"clsid,omitempty"`
			ConnectionGeneration uint64 `json:"connectionGeneration"`
		}{
			ProgID:               status.Source.ProgID,
			CLSID:                status.Source.CLSID,
			ConnectionGeneration: status.ConnectionGeneration,
		},
		Capabilities: struct {
			Browse    string `json:"browse"`
			Read      bool   `json:"read"`
			Write     bool   `json:"write"`
			Subscribe bool   `json:"subscribe"`
		}{
			Browse:    status.Capabilities.Browse,
			Read:      status.Capabilities.Read,
			Write:     status.Capabilities.Write,
			Subscribe: status.Capabilities.Subscribe,
		},
		WriteEnabled: status.WriteEnabled,
		Frontend: struct {
			HTTP struct {
				Listening bool `json:"listening"`
			} `json:"http"`
		}{},
	}
	response.Frontend.HTTP.Listening = s.listening.Load()
	writeJSON(w, http.StatusOK, response)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code opcda.ErrorCode, message string) {
	writeJSON(w, status, struct {
		Error struct {
			Layer   string `json:"layer"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Error: struct {
			Layer   string `json:"layer"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}{
			Layer:   "frontend",
			Code:    string(code),
			Message: message,
		},
	})
}
