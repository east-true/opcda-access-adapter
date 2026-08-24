package http

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func TestJSONStructureRejectsDuplicateEscapedKeysAndExcessDepth(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		depth int
		code  opcda.ErrorCode
	}{
		{name: "duplicate", body: `{"source":"device","\u0073ource":"device"}`, depth: 8, code: opcda.CodeDuplicateJSONField},
		{name: "nested duplicate", body: `{"items":[{"itemId":"A","itemId":"B"}]}`, depth: 8, code: opcda.CodeDuplicateJSONField},
		{name: "depth", body: `{"items":[[]]}`, depth: 2, code: opcda.CodeJSONDepthLimitExceeded},
		{name: "case alias", body: `{"Items":[]}`, depth: 8, code: opcda.CodeInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateJSONStructure([]byte(test.body), test.depth)
			var bodyError *requestBodyError
			if !errors.As(err, &bodyError) || bodyError.code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
		})
	}
	if err := validateJSONStructure([]byte(`{"items":[{"itemId":"A"}]}`), 3); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestHTTPJSONErrorsAreStableAndDoNotReflectFields(t *testing.T) {
	runtime := &readRuntime{}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxReadItems: 10, MaxItemIDBytes: 1024, MaxJSONDepth: 3,
	})
	tests := []struct {
		name string
		body string
		code opcda.ErrorCode
	}{
		{name: "duplicate", body: `{"items":[{"itemId":"A","itemId":"B"}]}`, code: opcda.CodeDuplicateJSONField},
		{name: "depth", body: `{"items":[[[[]]]]}`, code: opcda.CodeJSONDepthLimitExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewBufferString(test.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, string(test.code))
		})
	}

	attackerField := strings.Repeat("attacker-controlled-field", 100)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewBufferString(`{"items":[],"`+attackerField+`":true}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), attackerField) || response.Body.Len() > 512 {
		t.Fatalf("parser error reflected attacker-controlled field: %d bytes", response.Body.Len())
	}
	if runtime.calls != 0 {
		t.Fatalf("runtime called %d times", runtime.calls)
	}
}
