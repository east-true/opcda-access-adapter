package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

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

func TestUnknownEndpointIsFrontendError(t *testing.T) {
	server := New(statusRuntime{}, Config{MaxBodyBytes: 1024, MaxConcurrent: 1, RequestDeadline: time.Second})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/other", nil))
	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
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
