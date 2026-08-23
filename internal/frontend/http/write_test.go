package http

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type writeRuntime struct {
	statusRuntime
	enabled bool
	items   []opcda.WriteItem
	results []opcda.WriteResult
	calls   int
}

func (runtime *writeRuntime) Status(context.Context) opcda.RuntimeStatus {
	status := runtime.statusRuntime.Status(context.Background())
	status.WriteEnabled = runtime.enabled
	return status
}

func (runtime *writeRuntime) WriteBatch(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	runtime.calls++
	runtime.items = items
	return runtime.results, nil
}

func TestWriteDisabledRejectsBeforeSourceCall(t *testing.T) {
	runtime := &writeRuntime{}
	server := newWriteTestServer(runtime, 10)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/write", bytes.NewReader([]byte(`not even JSON`))))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if runtime.calls != 0 {
		t.Fatalf("WriteBatch called %d times while disabled", runtime.calls)
	}
	assertErrorCode(t, response, string(opcda.CodeWriteDisabled))
}

func TestWritePreservesExplicitWidthsOrderAndPartialHRESULT(t *testing.T) {
	denied := opcda.HRESULT(-1073479671)
	runtime := &writeRuntime{enabled: true, results: []opcda.WriteResult{
		{ItemID: "Exact.Int2", HRESULT: opcda.SOK, HRESULTPresent: true},
		{ItemID: "Wide", HRESULT: denied, HRESULTPresent: true},
	}}
	server := newWriteTestServer(runtime, 10)
	body := []byte(`{"items":[{"itemId":"Exact.Int2","dataType":"VT_I2","valueEncoding":"json","value":-32768},{"itemId":"Wide","dataType":"VT_UI8","valueEncoding":"json","value":"18446744073709551615"}]}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/write", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if runtime.calls != 1 || len(runtime.items) != 2 {
		t.Fatalf("runtime calls/items = %d/%d", runtime.calls, len(runtime.items))
	}
	if value, ok := runtime.items[0].Value.(int16); !ok || value != math.MinInt16 {
		t.Fatalf("first value = %T(%v), want int16", runtime.items[0].Value, runtime.items[0].Value)
	}
	if value, ok := runtime.items[1].Value.(uint64); !ok || value != math.MaxUint64 {
		t.Fatalf("second value = %T(%v), want uint64", runtime.items[1].Value, runtime.items[1].Value)
	}
	var decoded struct {
		Results []struct {
			ItemID  string `json:"itemId"`
			OK      bool   `json:"ok"`
			HRESULT struct {
				Hex string `json:"hex"`
			} `json:"hresult"`
		} `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 2 || decoded.Results[0].ItemID != "Exact.Int2" || !decoded.Results[0].OK {
		t.Fatalf("first result/order lost: %+v", decoded.Results)
	}
	if decoded.Results[1].OK || decoded.Results[1].HRESULT.Hex != denied.Hex() {
		t.Fatalf("partial failure HRESULT lost: %+v", decoded.Results[1])
	}
}

func TestWriteRejectsOverflowAndAmbiguousWidthsBeforeSourceCall(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "I2 overflow", body: `{"items":[{"itemId":"A","dataType":"VT_I2","valueEncoding":"json","value":32768}]}`},
		{name: "I8 must be string", body: `{"items":[{"itemId":"A","dataType":"VT_I8","valueEncoding":"json","value":9007199254740993}]}`},
		{name: "integer rejects fraction", body: `{"items":[{"itemId":"A","dataType":"VT_I4","valueEncoding":"json","value":1.0}]}`},
		{name: "BOOL rejects number", body: `{"items":[{"itemId":"A","dataType":"VT_BOOL","valueEncoding":"json","value":1}]}`},
		{name: "SAFEARRAY unsupported", body: `{"items":[{"itemId":"A","dataType":"VT_VARIANT","valueEncoding":"json","value":[]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &writeRuntime{enabled: true}
			server := newWriteTestServer(runtime, 10)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/write", bytes.NewBufferString(test.body)))
			if response.Code != http.StatusBadRequest && response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if runtime.calls != 0 {
				t.Fatalf("runtime called %d times", runtime.calls)
			}
		})
	}
}

func TestWriteDecodesExplicitSpecialFloats(t *testing.T) {
	value, err := decodeWriteValue(opcda.VTR4, "float-special", json.RawMessage(`"-Infinity"`))
	if err != nil {
		t.Fatal(err)
	}
	if typed, ok := value.(float32); !ok || !math.IsInf(float64(typed), -1) {
		t.Fatalf("value = %T(%v)", value, value)
	}
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != want {
		t.Fatalf("error code = %q, want %q", decoded.Error.Code, want)
	}
}

func newWriteTestServer(runtime opcda.Runtime, itemLimit int) *Server {
	return New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxWriteItems: itemLimit, MaxItemIDBytes: 1024,
	})
}
