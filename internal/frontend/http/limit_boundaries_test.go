package http

import (
	"bytes"
	"context"
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

// The item counts have the same shape as the lengths above, and the same gap:
// the suite refused a batch clearly over each limit, so nothing said a request
// of exactly the configured size is served. An operator who sets a batch limit
// of eight has told clients to send eight.
func TestABatchOfExactlyTheConfiguredSizeIsServed(t *testing.T) {
	const limit = 4

	t.Run("Read", func(t *testing.T) {
		runtime := &readRuntime{}
		server := New(runtime, Config{
			MaxBodyBytes: 8192, MaxConcurrent: 2, RequestDeadline: time.Second,
			MaxReadItems: limit, MaxItemIDBytes: 64, MaxJSONDepth: 8,
		})
		read := func(count int) *httptest.ResponseRecorder {
			varType := opcda.VTI4
			items := make([]string, count)
			runtime.results = make([]opcda.ReadResult, count)
			for index := range items {
				itemID := fmt.Sprintf("Test/Item%d", index)
				items[index] = fmt.Sprintf(`{"itemId":%q}`, itemID)
				runtime.results[index] = opcda.ReadResult{
					ItemID: opcda.DAItemID(itemID), VarType: &varType,
					HRESULT: opcda.SOK, HRESULTPresent: true,
					Value: &opcda.DAValue{ItemID: opcda.DAItemID(itemID), VarType: varType,
						Value: int32(1), QualityRaw: 0x00C0, HRESULT: opcda.SOK},
				}
			}
			body := fmt.Sprintf(`{"source":"device","items":[%s]}`, strings.Join(items, ","))
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/read", bytes.NewReader([]byte(body))))
			return response
		}
		if atLimit := read(limit); atLimit.Code != http.StatusOK {
			t.Errorf("a Read of exactly %d items was refused: %d %s",
				limit, atLimit.Code, atLimit.Body.String())
		}
		overLimit := read(limit + 1)
		if overLimit.Code == http.StatusOK {
			t.Errorf("a Read of %d items was served", limit+1)
		}
		assertErrorCode(t, overLimit, string(opcda.CodeRequestLimitExceeded))
	})

	t.Run("Write", func(t *testing.T) {
		runtime := &writeRuntime{enabled: true}
		server := New(runtime, Config{
			MaxBodyBytes: 8192, MaxConcurrent: 2, RequestDeadline: time.Second,
			MaxWriteItems: limit, MaxItemIDBytes: 64, MaxJSONDepth: 8,
		})
		write := func(count int) *httptest.ResponseRecorder {
			items := make([]string, count)
			runtime.results = make([]opcda.WriteResult, count)
			for index := range items {
				itemID := fmt.Sprintf("Test/Item%d", index)
				items[index] = fmt.Sprintf(
					`{"itemId":%q,"dataType":"VT_I2","valueEncoding":"json","value":1}`, itemID)
				runtime.results[index] = opcda.WriteResult{
					ItemID: opcda.DAItemID(itemID), HRESULT: opcda.SOK, HRESULTPresent: true,
				}
			}
			body := fmt.Sprintf(`{"items":[%s]}`, strings.Join(items, ","))
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/write", bytes.NewReader([]byte(body))))
			return response
		}
		if atLimit := write(limit); atLimit.Code != http.StatusOK {
			t.Errorf("a Write of exactly %d items was refused: %d %s",
				limit, atLimit.Code, atLimit.Body.String())
		}
		overLimit := write(limit + 1)
		if overLimit.Code == http.StatusOK {
			t.Errorf("a Write of %d items was served", limit+1)
		}
		assertErrorCode(t, overLimit, string(opcda.CodeRequestLimitExceeded))
	})
}

// The properties endpoints carry their own copy of the ItemID length check,
// and a copy is its own bound: pinning the Read endpoint's says nothing about
// this one. An ItemID that can be read must be one whose properties can be
// asked for, or the two endpoints disagree about what is addressable.
func TestThePropertiesEndpointAddressesTheSameItemIDsAsRead(t *testing.T) {
	const limit = 32
	server := New(fuzzPropertyRuntime{}, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxItemIDBytes: limit, MaxItemProperties: 8, MaxJSONDepth: 8,
	})
	ask := func(itemID string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"itemId":%q,"propertyIds":[100]}`, itemID)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/properties", bytes.NewReader([]byte(body))))
		return response
	}

	atLimit := ask(strings.Repeat("i", limit))
	if strings.Contains(atLimit.Body.String(), string(opcda.CodeItemIDTooLong)) {
		t.Errorf("an ItemID of exactly %d bytes was refused as too long: %s",
			limit, atLimit.Body.String())
	}
	overLimit := ask(strings.Repeat("i", limit+1))
	assertErrorCode(t, overLimit, string(opcda.CodeItemIDTooLong))
}

// The available-properties endpoint bounds what the source reports rather than
// what the client asked for: a source offering more properties than the
// configured limit is refused rather than truncated, because a truncated list
// would have a client believe a property does not exist. Exactly the limit is
// the largest list that can be relayed, and it must be.
type countingPropertyRuntime struct {
	statusRuntime
	count int
}

func (r countingPropertyRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	available := make([]opcda.AvailableProperty, r.count)
	for index := range available {
		available[index] = opcda.AvailableProperty{
			// 100 upward are vendor-specific, so none collides with the three
			// this endpoint refuses.
			ID: opcda.PropertyID(100 + index), VarType: opcda.VTBSTR,
		}
	}
	return available, nil
}

func (countingPropertyRuntime) ItemProperties(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "not asked for here")
}

func TestASourceMayOfferExactlyTheConfiguredPropertyCount(t *testing.T) {
	const limit = 4
	ask := func(count int) *httptest.ResponseRecorder {
		server := New(countingPropertyRuntime{count: count}, Config{
			MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
			MaxItemIDBytes: 64, MaxItemProperties: limit, MaxJSONDepth: 8,
		})
		response := httptest.NewRecorder()
		server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/properties/available",
			bytes.NewReader([]byte(`{"itemId":"Test/Float"}`))))
		return response
	}

	atLimit := ask(limit)
	if atLimit.Code != http.StatusOK {
		t.Errorf("a source offering exactly %d properties was refused: %d %s",
			limit, atLimit.Code, atLimit.Body.String())
	}
	overLimit := ask(limit + 1)
	if overLimit.Code == http.StatusOK {
		t.Errorf("a source offering %d properties was relayed", limit+1)
	}
	assertErrorCode(t, overLimit, string(opcda.CodeRequestLimitExceeded))
}
