package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func newJSONRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type statusRuntime struct {
	available      []opcda.AvailableProperty
	propertyValues map[opcda.PropertyID]opcda.ItemPropertyValue
}

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

// Subscribe is not exposed by any frontend in this phase; the stub only
// satisfies the DA runtime interface.
func (statusRuntime) Subscribe(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
	return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "subscribe is not exposed by this frontend")
}

func (statusRuntime) Unsubscribe(context.Context, opcda.SubscriptionID) error {
	return opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "subscribe is not exposed by this frontend")
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

func (r statusRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	if r.available == nil {
		return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
	}
	return r.available, nil
}

func (r statusRuntime) ItemProperties(_ context.Context, request opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	if r.propertyValues == nil {
		return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
	}
	values := make([]opcda.ItemPropertyValue, 0, len(request.Properties))
	for _, id := range request.Properties {
		value := r.propertyValues[id]
		value.ID = id
		values = append(values, value)
	}
	return values, nil
}

func postJSON(t *testing.T, server *Server, target, body string, wantStatus int) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, newJSONRequest(http.MethodPost, target, strings.NewReader(body)))
	if recorder.Code != wantStatus {
		t.Fatalf("%s answered %d, want %d: %s", target, recorder.Code, wantStatus, recorder.Body.String())
	}
	return recorder.Body.String()
}

// The HTTP frontend publishes capabilities.properties too, so it exposes the
// same two DA operations. Being DA-native it passes the source's identifiers,
// VARTYPEs and HRESULTs through rather than mapping them onto anything.
func TestHTTPItemPropertiesArePassedThrough(t *testing.T) {
	runtime := statusRuntime{
		available: []opcda.AvailableProperty{
			{ID: opcda.PropertyEUUnits, Description: "EU Units", VarType: opcda.VTBSTR},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
			opcda.PropertyEUUnits: {OK: true, VarType: opcda.VTBSTR, VarTypePresent: true,
				Value: "degC", ValuePresent: true, HRESULTPresent: true},
			opcda.PropertyHighEU: {HRESULT: -1073479674, HRESULTPresent: true},
		},
	}
	config := Config{MaxBodyBytes: 4096, MaxConcurrent: 4, RequestDeadline: time.Second, MaxJSONDepth: 8}
	server := New(runtime, config)

	body := postJSON(t, server, "/v1/properties/available", `{"itemId":"Test/Float"}`, http.StatusOK)
	if !strings.Contains(body, `"propertyId":100`) || !strings.Contains(body, `"EU Units"`) {
		t.Fatalf("available properties = %s", body)
	}

	body = postJSON(t, server, "/v1/properties", `{"itemId":"Test/Float","propertyIds":[100,102]}`, http.StatusOK)
	if !strings.Contains(body, `"degC"`) {
		t.Fatalf("property values = %s", body)
	}
	// The refused property keeps the source's exact HRESULT and no value.
	if !strings.Contains(body, `"0xC0040006"`) {
		t.Fatalf("a refused property lost its HRESULT: %s", body)
	}

	postJSON(t, server, "/v1/properties", `{"itemId":"Test/Float"}`, http.StatusBadRequest)
	postJSON(t, server, "/v1/properties/available", `{"itemId":""}`, http.StatusBadRequest)

	// A source without IOPCItemProperties is working correctly, and answers the
	// way one without IOPCBrowseServerAddressSpace does.
	postJSON(t, New(statusRuntime{}, config), "/v1/properties/available",
		`{"itemId":"Test/Float"}`, http.StatusUnprocessableEntity)
}
