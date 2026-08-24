package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func newJSONRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type statusRuntime struct{}

func (statusRuntime) Status(context.Context) opcda.RuntimeStatus {
	return opcda.RuntimeStatus{
		State:                opcda.RuntimeStateConnected,
		Source:               opcda.SourceConfig{ProgID: "Example.Server.1"},
		ConnectionGeneration: 7,
		ReconnectCount:       3,
		QueueDepth:           2,
		Capabilities:         opcda.Capabilities{Browse: "supported", Read: true, Write: true},
		LastSourceErrorSet:   true,
		LastSourceError: opcda.SourceDiagnostic{
			Operation:      "CoCreateInstance(IOPCServer)",
			HRESULT:        opcda.HRESULT(-2147024891),
			HRESULTPresent: true,
		},
	}
}
func (statusRuntime) Browse(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return opcda.BrowseResult{}, nil
}
func (statusRuntime) ReadBatch(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
	return nil, nil
}
func (statusRuntime) WriteBatch(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error) {
	return nil, nil
}
func (statusRuntime) Shutdown(context.Context) error { return nil }

func TestStatusIncludesRuntimeAndListenerState(t *testing.T) {
	server := New(statusRuntime{}, Config{MaxBodyBytes: 1024, MaxConcurrent: 1, RequestDeadline: time.Second})
	server.SetListening(true)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	server.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("content type = %q, want %q", got, want)
	}
	for name, want := range map[string]string{
		"Cache-Control":                "no-store",
		"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if body := response.Body.String(); body == "" || !containsAll(body, []string{"connected", "Example.Server.1", "connectionGeneration", "reconnectCount", "queueDepth", "CoCreateInstance(IOPCServer)", "0x80070005", "listening"}) {
		t.Fatalf("unexpected body: %s", body)
	}
	var decoded struct {
		Frontend struct {
			HTTP struct {
				Listening bool `json:"listening"`
			} `json:"http"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Frontend.HTTP.Listening {
		t.Fatal("expected listening status to be true")
	}
}

func TestLoopbackHostPolicyRejectsDNSRebinding(t *testing.T) {
	server := New(statusRuntime{}, Config{
		MaxBodyBytes: 1024, MaxConcurrent: 1, RequestDeadline: time.Second,
		RequireLoopbackHost: true,
	})
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "LOCALHOST.:8080", "[::1]:8080"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
		request.Host = host
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("loopback Host %q status = %d: %s", host, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Host = "attacker.example:8080"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("untrusted Host status = %d: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, string(opcda.CodeUntrustedHost))

	for _, host := range []string{"", "::1", "[[::1]]", "[::1]:bad", "localhost:bad", "127.0.0.1.attacker.example"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
		request.Host = host
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusMisdirectedRequest {
			t.Fatalf("malformed or untrusted Host %q status = %d: %s", host, response.Code, response.Body.String())
		}
	}
}

func TestUnknownEndpointIsFrontendError(t *testing.T) {
	server := New(statusRuntime{}, Config{MaxBodyBytes: 1024, MaxConcurrent: 1, RequestDeadline: time.Second})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/other", nil))
	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestExactRequestTargetsAndMethodsAreEnforced(t *testing.T) {
	server := New(statusRuntime{}, Config{MaxBodyBytes: 1024, MaxConcurrent: 2, RequestDeadline: time.Second})
	for _, target := range []string{"/v1/status?debug=true", "/v1/status?", "/v1%2fstatus", "http://adapter.example/v1/status"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("target %q status = %d: %s", target, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/status", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d Allow=%q: %s", response.Code, response.Header().Get("Allow"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/status", bytes.NewBufferString("ignored"))
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status body response = %d: %s", response.Code, response.Body.String())
	}
}

func TestBrowserOriginIsRejectedFromStatus(t *testing.T) {
	server := New(statusRuntime{}, Config{MaxBodyBytes: 1024, MaxConcurrent: 1, RequestDeadline: time.Second})
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header["Origin"] = []string{""}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, string(opcda.CodeBrowserOriginRejected))
}

func TestJSONContentHeadersAreUnambiguous(t *testing.T) {
	runtime := &readRuntime{}
	server := newReadTestServer(runtime, 4096, 10)
	tests := []struct {
		name    string
		prepare func(*http.Request)
		code    opcda.ErrorCode
	}{
		{name: "content encoding", prepare: func(request *http.Request) {
			request.Header.Set("Content-Encoding", "gzip")
		}, code: opcda.CodeUnsupportedContentEncoding},
		{name: "duplicate content type", prepare: func(request *http.Request) {
			request.Header.Add("Content-Type", "application/json")
		}, code: opcda.CodeUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, "/v1/read", bytes.NewBufferString(`{"items":[{"itemId":"A"}]}`))
			test.prepare(request)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, string(test.code))
		})
	}
	if runtime.calls != 0 {
		t.Fatalf("runtime called %d times", runtime.calls)
	}
}

func containsAll(value string, values []string) bool {
	for _, required := range values {
		if !contains(value, required) {
			return false
		}
	}
	return true
}

func contains(value, required string) bool {
	for index := 0; index+len(required) <= len(value); index++ {
		if value[index:index+len(required)] == required {
			return true
		}
	}
	return false
}
