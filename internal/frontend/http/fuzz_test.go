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

// The item property endpoints added with the DA-native property calls. Every
// other HTTP body this frontend accepts is fuzzed, and these arrived without a
// target -- on a surface that has already produced two defects, one of them in
// the JSON field handling this drives.
func FuzzItemPropertiesHTTPBody(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"itemId":"A","propertyIds":[100,102]}`),
		[]byte(`{"itemId":"A","propertyIds":[]}`),
		// The exact-spelling rule and the duplicate-field rule apply here too.
		[]byte(`{"itemId":"A","PropertyIds":[100]}`),
		[]byte(`{"itemId":"A","propertyIds":[100],"propertyIds":[102]}`),
		[]byte(`{"itemId":"\uD800","propertyIds":[100]}`),
		[]byte(`{"itemId":"A","propertyIds":[[[[[0]]]]]}`),
		[]byte(`not json`),
	} {
		f.Add(seed)
	}
	server := New(fuzzPropertyRuntime{}, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 1, RequestDeadline: time.Second,
		MaxItemIDBytes: 128, MaxItemProperties: 8, MaxJSONDepth: 6,
	})
	f.Fuzz(func(t *testing.T, body []byte) {
		for _, target := range []string{"/v1/properties", "/v1/properties/available"} {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, target, bytes.NewReader(body)))
			if response.Code < 200 || response.Code > 599 {
				t.Fatalf("%s answered invalid HTTP status %d", target, response.Code)
			}
		}
	})
}

// fuzzPropertyRuntime answers every property, so the fuzzer drives the encoder
// rather than short-circuiting on a source that offers nothing.
type fuzzPropertyRuntime struct{ statusRuntime }

func (fuzzPropertyRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	return []opcda.AvailableProperty{{ID: opcda.PropertyEUUnits, VarType: opcda.VTBSTR}}, nil
}

func (fuzzPropertyRuntime) ItemProperties(_ context.Context, request opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	values := make([]opcda.ItemPropertyValue, 0, len(request.Properties))
	for _, id := range request.Properties {
		values = append(values, opcda.ItemPropertyValue{
			ID: id, OK: true, VarType: opcda.VTBSTR, VarTypePresent: true,
			Value: "x", ValuePresent: true, HRESULTPresent: true,
		})
	}
	return values, nil
}
