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
	MaxBodyBytes     int64
	MaxConcurrent    int
	RequestDeadline  time.Duration
	MaxReadItems     int
	MaxWriteItems    int
	MaxBrowseEntries int
	MaxBrowseDepth   int
	MaxItemIDBytes   int
}

type Server struct {
	runtime   opcda.Runtime
	config    Config
	listening atomic.Bool
	requests  chan struct{}
}

func New(runtime opcda.Runtime, config Config) *Server {
	if config.MaxReadItems <= 0 {
		config.MaxReadItems = opcda.DefaultLimits().MaxReadItems
	}
	if config.MaxWriteItems <= 0 {
		config.MaxWriteItems = opcda.DefaultLimits().MaxWriteItems
	}
	if config.MaxItemIDBytes <= 0 {
		config.MaxItemIDBytes = opcda.DefaultLimits().MaxItemIDBytes
	}
	if config.MaxBrowseEntries <= 0 {
		config.MaxBrowseEntries = opcda.DefaultLimits().MaxBrowseEntries
	}
	if config.MaxBrowseDepth <= 0 {
		config.MaxBrowseDepth = opcda.DefaultLimits().MaxBrowseDepth
	}
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
	case r.Method == http.MethodPost && r.URL.Path == "/v1/read":
		s.handleRead(ctx, w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/browse":
		s.handleBrowse(ctx, w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/write":
		s.handleWrite(ctx, w, r)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
	}
}

func (s *Server) handleStatus(ctx context.Context, w http.ResponseWriter) {
	status := s.runtime.Status(ctx)
	type sourceErrorResponse struct {
		Operation string              `json:"operation"`
		HRESULT   *opcda.HRESULTValue `json:"hresult,omitempty"`
	}
	var lastSourceError *sourceErrorResponse
	if status.LastSourceErrorSet {
		lastSourceError = &sourceErrorResponse{Operation: status.LastSourceError.Operation}
		if status.LastSourceError.HRESULTPresent {
			representation := status.LastSourceError.HRESULT.Representation()
			lastSourceError.HRESULT = &representation
		}
	}
	response := struct {
		State  string `json:"state"`
		Source struct {
			ProgID               string               `json:"progId,omitempty"`
			CLSID                string               `json:"clsid,omitempty"`
			ConnectionGeneration uint64               `json:"connectionGeneration"`
			LastError            *sourceErrorResponse `json:"lastError,omitempty"`
		} `json:"source"`
		Capabilities struct {
			Browse    string `json:"browse"`
			Read      bool   `json:"read"`
			Write     bool   `json:"write"`
			Subscribe bool   `json:"subscribe"`
		} `json:"capabilities"`
		WriteEnabled bool `json:"writeEnabled"`
		Runtime      struct {
			QueueDepth     int    `json:"queueDepth"`
			ReconnectCount uint64 `json:"reconnectCount"`
			DegradedReason string `json:"degradedReason,omitempty"`
		} `json:"runtime"`
		Frontend struct {
			HTTP struct {
				Listening bool `json:"listening"`
			} `json:"http"`
		} `json:"frontend"`
	}{
		State: string(status.State),
		Source: struct {
			ProgID               string               `json:"progId,omitempty"`
			CLSID                string               `json:"clsid,omitempty"`
			ConnectionGeneration uint64               `json:"connectionGeneration"`
			LastError            *sourceErrorResponse `json:"lastError,omitempty"`
		}{
			ProgID:               status.Source.ProgID,
			CLSID:                status.Source.CLSID,
			ConnectionGeneration: status.ConnectionGeneration,
			LastError:            lastSourceError,
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
		Runtime: struct {
			QueueDepth     int    `json:"queueDepth"`
			ReconnectCount uint64 `json:"reconnectCount"`
			DegradedReason string `json:"degradedReason,omitempty"`
		}{
			QueueDepth:     status.QueueDepth,
			ReconnectCount: status.ReconnectCount,
			DegradedReason: status.DegradedReason,
		},
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
	writeLayerError(w, status, "frontend", code, message, nil)
}

func writeLayerError(w http.ResponseWriter, status int, layer string, code opcda.ErrorCode, message string, hresult *opcda.HRESULTValue) {
	writeJSON(w, status, struct {
		Error struct {
			Layer   string              `json:"layer"`
			Code    string              `json:"code"`
			Message string              `json:"message"`
			HRESULT *opcda.HRESULTValue `json:"hresult,omitempty"`
		} `json:"error"`
	}{
		Error: struct {
			Layer   string              `json:"layer"`
			Code    string              `json:"code"`
			Message string              `json:"message"`
			HRESULT *opcda.HRESULTValue `json:"hresult,omitempty"`
		}{
			Layer:   layer,
			Code:    string(code),
			Message: message,
			HRESULT: hresult,
		},
	})
}
