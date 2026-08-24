package http

import (
	"bytes"
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type fuzzReadRuntime struct{ statusRuntime }

func (fuzzReadRuntime) ReadBatch(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	results := make([]opcda.ReadResult, len(request.Items))
	for index, itemID := range request.Items {
		results[index] = opcda.ReadResult{ItemID: itemID, HRESULT: opcda.HRESULT(-1), HRESULTPresent: true}
	}
	return results, nil
}

func FuzzReadHTTPBody(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"source":"device","items":[{"itemId":"A"}]}`),
		[]byte(`{"items":[{"itemId":"\uD800"}]}`),
		[]byte(`{"items":[{"itemId":"A","itemId":"B"}]}`),
		[]byte(`{"items":[[[[[[[[[0]]]]]]]]]}`),
		[]byte(`not json`),
	} {
		f.Add(seed)
	}
	server := New(fuzzReadRuntime{}, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 1, RequestDeadline: time.Second,
		MaxReadItems: 4, MaxItemIDBytes: 128,
	})
	f.Fuzz(func(t *testing.T, body []byte) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read", bytes.NewReader(body)))
		if response.Code < 200 || response.Code > 599 {
			t.Fatalf("invalid HTTP status %d", response.Code)
		}
	})
}

func FuzzExactJSONString(f *testing.F) {
	for _, seed := range []string{`"plain"`, `"\uD83D\uDE00"`, `"\uD800"`, `"\\uD800"`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var value exactJSONString
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			return
		}
		encoded, err := json.Marshal(string(value))
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip exactJSONString
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("accepted string did not round trip: %v", err)
		}
		if roundTrip != value {
			t.Fatalf("round trip changed value %q to %q", value, roundTrip)
		}
	})
}

func FuzzDecodeWriteValue(f *testing.F) {
	f.Add(uint16(opcda.VTI2), "json", []byte(`42`))
	f.Add(uint16(opcda.VTR8), "float-special", []byte(`"NaN"`))
	f.Add(uint16(opcda.VTBSTR), "json", []byte(`"text"`))
	f.Fuzz(func(t *testing.T, rawType uint16, encoding string, raw []byte) {
		_, _ = decodeWriteValue(opcda.DAVarType(rawType), encoding, json.RawMessage(raw))
	})
}
