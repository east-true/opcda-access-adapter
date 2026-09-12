package http

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The HTTP reference documents MaxItemIDBytes and MaxItemProperties as bounds,
// and the mutation sweep found neither pinned at the value that decides the
// answer: loosening either comparison by one survived. What the suite tested
// was the far side -- something clearly too long is refused -- so nothing said
// that an ItemID of exactly the configured length is still accepted.
//
// Tightening by one refuses an ItemID the operator's own configuration allows,
// and the adapter passes ItemIDs through byte for byte, so the length it
// accepts is part of what a client can address.

func TestAnItemIDOfExactlyTheLimitIsAccepted(t *testing.T) {
	const limit = 64
	runtime := &readRuntime{}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxReadItems: 4, MaxItemIDBytes: limit,
	})

	read := func(itemID string) *httptest.ResponseRecorder {
		varType := opcda.VTI4
		runtime.results = []opcda.ReadResult{{
			ItemID: opcda.DAItemID(itemID), VarType: &varType,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{ItemID: opcda.DAItemID(itemID), VarType: varType,
				Value: int32(1), QualityRaw: 0x00C0, HRESULT: opcda.SOK},
		}}
		body := fmt.Sprintf(`{"source":"device","items":[{"itemId":%q}]}`, itemID)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewReader([]byte(body))))
		return response
	}

	atLimit := read(strings.Repeat("i", limit))
	if atLimit.Code != http.StatusOK {
		t.Errorf("an ItemID of exactly %d bytes was refused: %d %s",
			limit, atLimit.Code, atLimit.Body.String())
	}

	overLimit := read(strings.Repeat("i", limit+1))
	if overLimit.Code == http.StatusOK {
		t.Errorf("an ItemID of %d bytes was accepted", limit+1)
	}
	assertErrorCode(t, overLimit, string(opcda.CodeItemIDTooLong))
}

// The bound counts bytes, not characters, which matters for any ItemID a
// non-English deployment would use: a limit read as characters would admit
// several times the memory the configuration allows.
func TestTheItemIDLimitCountsBytesNotCharacters(t *testing.T) {
	const limit = 12
	runtime := &readRuntime{}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxReadItems: 4, MaxItemIDBytes: limit,
	})

	// Four three-byte characters are exactly the limit; five are over it while
	// still being only five characters.
	for _, testCase := range []struct {
		name     string
		itemID   string
		accepted bool
	}{
		{"four three-byte characters", strings.Repeat("가", 4), true},
		{"five three-byte characters", strings.Repeat("가", 5), false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			varType := opcda.VTI4
			runtime.results = []opcda.ReadResult{{
				ItemID: opcda.DAItemID(testCase.itemID), VarType: &varType,
				HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{ItemID: opcda.DAItemID(testCase.itemID), VarType: varType,
					Value: int32(1), QualityRaw: 0x00C0, HRESULT: opcda.SOK},
			}}
			body := fmt.Sprintf(`{"source":"device","items":[{"itemId":%q}]}`, testCase.itemID)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewReader([]byte(body))))
			if accepted := response.Code == http.StatusOK; accepted != testCase.accepted {
				t.Errorf("%d bytes in %d characters: accepted = %v, want %v (%s)",
					len([]byte(testCase.itemID)), len([]rune(testCase.itemID)),
					accepted, testCase.accepted, response.Body.String())
			}
		})
	}
}

func TestExactlyTheItemPropertyLimitIsAccepted(t *testing.T) {
	const limit = 8
	runtime := fuzzPropertyRuntime{}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxItemIDBytes: 128, MaxItemProperties: limit, MaxJSONDepth: 8,
	})

	request := func(count int) *httptest.ResponseRecorder {
		ids := make([]string, count)
		for i := range ids {
			// 100 upward are vendor-specific identifiers, and none of them is
			// value, quality or timestamp, which this endpoint refuses.
			ids[i] = fmt.Sprintf("%d", 100+i)
		}
		body := fmt.Sprintf(`{"itemId":"Test/Float","propertyIds":[%s]}`, strings.Join(ids, ","))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/properties", bytes.NewReader([]byte(body))))
		return response
	}

	atLimit := request(limit)
	if atLimit.Code == http.StatusBadRequest &&
		strings.Contains(atLimit.Body.String(), string(opcda.CodeRequestLimitExceeded)) {
		t.Errorf("exactly %d properties was refused as over the limit: %s", limit, atLimit.Body.String())
	}

	overLimit := request(limit + 1)
	if overLimit.Code != http.StatusBadRequest {
		t.Errorf("%d properties returned %d, want 400", limit+1, overLimit.Code)
	}
	assertErrorCode(t, overLimit, string(opcda.CodeRequestLimitExceeded))
}
