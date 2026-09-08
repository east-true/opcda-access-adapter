package http

import (
	"context"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type Config struct {
	MaxBodyBytes        int64
	MaxConcurrent       int
	RequestDeadline     time.Duration
	MaxReadItems        int
	MaxWriteItems       int
	MaxBrowseEntries    int
	MaxBrowseDepth      int
	MaxItemIDBytes      int
	MaxItemProperties   int
	MaxJSONDepth        int
	RequireLoopbackHost bool
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
	if config.MaxItemProperties <= 0 {
		config.MaxItemProperties = opcda.DefaultLimits().MaxItemProperties
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
	if config.MaxJSONDepth <= 0 {
		config.MaxJSONDepth = 64
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
	setResponseSecurityHeaders(w)

	select {
	case s.requests <- struct{}{}:
		defer func() { <-s.requests }()
	default:
		writeError(w, http.StatusServiceUnavailable, opcda.CodeQueueFull, "too many concurrent HTTP requests")
		return
	}
	if s.config.RequireLoopbackHost && !isLoopbackRequestHost(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, opcda.CodeUntrustedHost, "request Host is not loopback")
		return
	}
	if !validateRequestTarget(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.config.RequestDeadline)
	defer cancel()

	switch r.URL.Path {
	case "/v1/status":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			writeError(w, http.StatusBadRequest, opcda.CodeInvalidRequest, "status request must not contain a body")
			return
		}
		if !validateBrowserBoundary(w, r) {
			return
		}
		s.handleStatus(ctx, w)
	case "/v1/read":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !validateBrowserBoundary(w, r) {
			return
		}
		s.handleRead(ctx, w, r)
	case "/v1/browse":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !validateBrowserBoundary(w, r) {
			return
		}
		s.handleBrowse(ctx, w, r)
	case "/v1/properties/available":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !validateBrowserBoundary(w, r) {
			return
		}
		s.handleAvailableProperties(ctx, w, r)
	case "/v1/properties":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !validateBrowserBoundary(w, r) {
			return
		}
		s.handleItemProperties(ctx, w, r)
	case "/v1/write":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleWrite(ctx, w, r)
	default:
		writeError(w, http.StatusNotFound, opcda.CodeNotFound, "endpoint not found")
	}
}

func validateRequestTarget(w http.ResponseWriter, request *http.Request) bool {
	if request.URL.IsAbs() || request.URL.RawPath != "" || request.URL.RawQuery != "" || request.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, opcda.CodeInvalidRequest, "request target must use an exact v0 path without encoding or query parameters")
		return false
	}
	return true
}

func validateBrowserBoundary(w http.ResponseWriter, request *http.Request) bool {
	if len(request.Header.Values("Origin")) != 0 {
		writeError(w, http.StatusForbidden, opcda.CodeBrowserOriginRejected, "browser-originated requests are not accepted")
		return false
	}
	return true
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed, opcda.CodeMethodNotAllowed, "method is not allowed for this endpoint")
}

func setResponseSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func isLoopbackRequestHost(value string) bool {
	if value == "" {
		return false
	}
	host := value
	if parsedHost, port, err := net.SplitHostPort(value); err == nil {
		parsedPort, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || parsedPort == 0 {
			return false
		}
		host = parsedHost
	} else if strings.HasPrefix(value, "[") || strings.HasSuffix(value, "]") {
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return false
		}
		host = value[1 : len(value)-1]
		if strings.ContainsAny(host, "[]") {
			return false
		}
	} else if strings.Contains(value, ":") {
		return false
	}
	host = strings.ToLower(host)
	if host == "localhost" || host == "localhost." {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validateJSONRequest(w http.ResponseWriter, request *http.Request) bool {
	if len(request.Header.Values("Content-Encoding")) != 0 {
		writeError(w, http.StatusUnsupportedMediaType, opcda.CodeUnsupportedContentEncoding, "Content-Encoding is not supported")
		return false
	}
	contentTypes := request.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		writeError(w, http.StatusUnsupportedMediaType, opcda.CodeUnsupportedMediaType, "exactly one Content-Type: application/json header is required")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, opcda.CodeUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
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
			Browse     string `json:"browse"`
			Read       bool   `json:"read"`
			Write      bool   `json:"write"`
			Subscribe  bool   `json:"subscribe"`
			Properties string `json:"properties"`
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
			Browse     string `json:"browse"`
			Read       bool   `json:"read"`
			Write      bool   `json:"write"`
			Subscribe  bool   `json:"subscribe"`
			Properties string `json:"properties"`
		}{
			Browse:     status.Capabilities.Browse,
			Read:       status.Capabilities.Read,
			Write:      status.Capabilities.Write,
			Subscribe:  status.Capabilities.Subscribe,
			Properties: status.Capabilities.Properties,
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
