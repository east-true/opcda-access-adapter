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

type readRuntime struct {
	statusRuntime
	request opcda.ReadRequest
	results []opcda.ReadResult
	calls   int
}

func (runtime *readRuntime) ReadBatch(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	runtime.calls++
	runtime.request = request
	return runtime.results, nil
}

func TestReadPreservesOrderWidthsQualityTimestampAndPartialFailure(t *testing.T) {
	intType, wideType, floatType := opcda.VTI2, opcda.VTI8, opcda.VTR8
	timestamp := time.Date(2026, 8, 23, 1, 2, 3, 456700000, time.UTC)
	failedHR := opcda.HRESULT(-1073479673)
	runtime := &readRuntime{results: []opcda.ReadResult{
		{
			ItemID: " Exact.Int2 ", VarType: &intType, CanonicalType: &intType,
			AccessRights: &opcda.DAAccessRights{Raw: 3, Read: true, Write: true},
			HRESULT:      opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{VarType: opcda.VTI2, Value: int16(42), QualityRaw: 0x01C0, Timestamp: timestamp, TimestampPresent: true},
		},
		{ItemID: "Missing", HRESULT: failedHR, HRESULTPresent: true},
		{
			ItemID: "Wide", VarType: &wideType, CanonicalType: &wideType,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{VarType: opcda.VTI8, Value: int64(9007199254740993), QualityRaw: 0x00C0},
		},
		{
			ItemID: "Infinity", VarType: &floatType, CanonicalType: &floatType,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{VarType: opcda.VTR8, Value: math.Inf(1), QualityRaw: 0x00C0},
		},
	}}
	server := newReadTestServer(runtime, 4096, 10)
	body := []byte(`{"source":"device","items":[{"itemId":" Exact.Int2 "},{"itemId":"Missing"},{"itemId":"Wide"},{"itemId":"Infinity"}]}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := runtime.request.Items[0]; got != " Exact.Int2 " {
		t.Fatalf("ItemID changed to %q", got)
	}

	var decoded struct {
		Results []struct {
			ItemID           string          `json:"itemId"`
			OK               bool            `json:"ok"`
			ValueEncoding    string          `json:"valueEncoding"`
			Value            json.RawMessage `json:"value"`
			Quality          uint16          `json:"quality"`
			TimestampPresent bool            `json:"timestampPresent"`
			HRESULT          *struct {
				Value int32  `json:"value"`
				Hex   string `json:"hex"`
			} `json:"hresult"`
		} `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 4 || decoded.Results[0].ItemID != " Exact.Int2 " || decoded.Results[1].ItemID != "Missing" {
		t.Fatalf("result order changed: %+v", decoded.Results)
	}
	if !decoded.Results[0].OK || decoded.Results[0].Quality != 0x01C0 || !decoded.Results[0].TimestampPresent {
		t.Fatalf("first result lost metadata: %+v", decoded.Results[0])
	}
	if decoded.Results[1].OK || decoded.Results[1].HRESULT == nil || decoded.Results[1].HRESULT.Hex != failedHR.Hex() {
		t.Fatalf("partial failure lost HRESULT: %+v", decoded.Results[1])
	}
	if got := string(decoded.Results[2].Value); got != `"9007199254740993"` {
		t.Fatalf("I8 value = %s", got)
	}
	if decoded.Results[3].ValueEncoding != "float-special" || string(decoded.Results[3].Value) != `"+Infinity"` {
		t.Fatalf("special float = %+v", decoded.Results[3])
	}
}

func TestReadRejectsOversizeAndUnknownFieldsBeforeRuntime(t *testing.T) {
	runtime := &readRuntime{}
	tests := []struct {
		name   string
		body   []byte
		limit  int64
		status int
	}{
		{name: "body", body: []byte(`{"items":[{"itemId":"A"}]}`), limit: 8, status: http.StatusRequestEntityTooLarge},
		{name: "schema", body: []byte(`{"items":[{"itemId":"A","mappedName":"B"}]}`), limit: 1024, status: http.StatusBadRequest},
		{name: "trailing", body: []byte(`{"items":[{"itemId":"A"}]} {}`), limit: 1024, status: http.StatusBadRequest},
		{name: "unpaired surrogate", body: []byte(`{"items":[{"itemId":"\uD800"}]}`), limit: 1024, status: http.StatusBadRequest},
		{name: "embedded NUL", body: []byte(`{"items":[{"itemId":"A\u0000B"}]}`), limit: 1024, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newReadTestServer(runtime, test.limit, 10)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewReader(test.body)))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
	if runtime.calls != 0 {
		t.Fatalf("runtime called %d times for invalid requests", runtime.calls)
	}
}

func TestExactJSONStringAllowsEscapedBackslashAndValidSurrogatePair(t *testing.T) {
	tests := []string{`"\\uD800"`, `"\uD83D\uDE00"`}
	for _, input := range tests {
		var value exactJSONString
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", input, err)
		}
	}
}

func TestJSONValueEncodingIsLossless(t *testing.T) {
	tests := []struct {
		value    any
		want     string
		encoding string
	}{
		{value: int16(-32768), want: "-32768", encoding: "json"},
		{value: uint32(4294967295), want: "4294967295", encoding: "json"},
		{value: int64(-9223372036854775808), want: `"-9223372036854775808"`, encoding: "json"},
		{value: uint64(18446744073709551615), want: `"18446744073709551615"`, encoding: "json"},
		{value: math.NaN(), want: `"NaN"`, encoding: "float-special"},
	}
	for _, test := range tests {
		got, encoding, err := encodeDAValue(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != test.want || encoding != test.encoding {
			t.Fatalf("encode(%T) = %s, %q; want %s, %q", test.value, got, encoding, test.want, test.encoding)
		}
	}
}

func TestReadFailsClosedOnRuntimeResultMismatch(t *testing.T) {
	runtime := &readRuntime{results: []opcda.ReadResult{{ItemID: "Different"}}}
	server := newReadTestServer(runtime, 4096, 10)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewBufferString(`{"items":[{"itemId":"Expected"}]}`)))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, string(opcda.CodeInternalResultMismatch))
}

func newReadTestServer(runtime opcda.Runtime, bodyLimit int64, itemLimit int) *Server {
	return New(runtime, Config{
		MaxBodyBytes: bodyLimit, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxReadItems: itemLimit, MaxItemIDBytes: 1024,
	})
}
