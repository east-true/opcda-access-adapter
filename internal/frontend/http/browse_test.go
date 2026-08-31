package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type browseRuntime struct {
	statusRuntime
	request opcda.BrowseRequest
	result  opcda.BrowseResult
	err     error
	calls   int
}

func (runtime *browseRuntime) Browse(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
	runtime.calls++
	runtime.request = request
	return runtime.result, runtime.err
}

func TestBrowsePreservesNavigationAndExactItemID(t *testing.T) {
	itemID := opcda.DAItemID("Channel 1.Device.温度")
	dataType := opcda.VTR4
	runtime := &browseRuntime{result: opcda.BrowseResult{
		Path: []string{"Channel 1"},
		Entries: []opcda.BrowseEntry{
			{Kind: opcda.BrowseEntryBranch, Name: "Nested"},
			{Kind: opcda.BrowseEntryItem, Name: "温度", ItemID: &itemID, CanonicalType: &dataType, AccessRights: &opcda.DAAccessRights{Raw: 1, Read: true}},
		},
	}}
	server := newBrowseTestServer(runtime)
	response := httptest.NewRecorder()
	body := []byte(`{"path":["Channel 1"],"filter":"all"}`)
	server.ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/browse", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(runtime.request.Path) != 1 || runtime.request.Path[0] != "Channel 1" {
		t.Fatalf("path changed: %+v", runtime.request.Path)
	}
	var decoded struct {
		Entries []struct {
			Kind   string  `json:"kind"`
			ItemID *string `json:"itemId"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Entries) != 2 || decoded.Entries[0].ItemID != nil || decoded.Entries[1].ItemID == nil || *decoded.Entries[1].ItemID != string(itemID) {
		t.Fatalf("unexpected entries: %+v", decoded.Entries)
	}
}

func TestBrowseUnsupportedAndLimitAreAdapterErrors(t *testing.T) {
	tests := []struct {
		code opcda.ErrorCode
	}{
		{code: opcda.CodeBrowseUnsupported},
		{code: opcda.CodeBrowseResultLimitExceeded},
	}
	for _, test := range tests {
		runtime := &browseRuntime{err: opcda.NewAdapterError(test.code, "browse unavailable")}
		response := httptest.NewRecorder()
		newBrowseTestServer(runtime).ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/browse", bytes.NewReader([]byte(`{"path":[],"filter":"all"}`))))
		if response.Code != http.StatusUnprocessableEntity || !bytes.Contains(response.Body.Bytes(), []byte(`"layer":"adapter"`)) {
			t.Fatalf("%s response = %d %s", test.code, response.Code, response.Body.String())
		}
	}
}

func TestBrowseValidationPreventsRuntimeCall(t *testing.T) {
	runtime := &browseRuntime{}
	response := httptest.NewRecorder()
	newBrowseTestServer(runtime).ServeHTTP(response, newJSONRequest(http.MethodPost, "/v1/browse", bytes.NewReader([]byte(`{"path":[""],"filter":"all"}`))))
	if response.Code != http.StatusBadRequest || runtime.calls != 0 {
		t.Fatalf("response = %d, runtime calls = %d", response.Code, runtime.calls)
	}
}

func TestBrowseFailsClosedOnInvalidRuntimeIdentity(t *testing.T) {
	emptyItemID := opcda.DAItemID("")
	tests := []struct {
		name   string
		result opcda.BrowseResult
	}{
		{name: "path mismatch", result: opcda.BrowseResult{Path: []string{"Other"}}},
		{name: "unknown kind", result: opcda.BrowseResult{Entries: []opcda.BrowseEntry{{Kind: "unknown", Name: "A"}}}},
		{name: "item without ItemID", result: opcda.BrowseResult{Entries: []opcda.BrowseEntry{{Kind: opcda.BrowseEntryItem, Name: "A"}}}},
		// A branch may carry the ItemID the source gave it; what it may not
		// carry is a malformed one, which is what this now checks.
		{name: "branch with an empty ItemID", result: opcda.BrowseResult{Entries: []opcda.BrowseEntry{{Kind: opcda.BrowseEntryBranch, Name: "A", ItemID: &emptyItemID}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &browseRuntime{result: test.result}
			response := httptest.NewRecorder()
			newBrowseTestServer(runtime).ServeHTTP(response,
				newJSONRequest(http.MethodPost, "/v1/browse", bytes.NewBufferString(`{"path":[],"filter":"all"}`)))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, string(opcda.CodeInternalResultMismatch))
		})
	}
}

func newBrowseTestServer(runtime opcda.Runtime) *Server {
	return New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxBrowseEntries: 10, MaxBrowseDepth: 4, MaxItemIDBytes: 1024,
	})
}

// A.3.1.2 has a wrapper obtain a branch's ItemID from GetItemID, so a branch
// entry carries one when the source names it. The frontend used to refuse that
// outright, on the belief that a branch has no ItemID.
func TestABranchCarriesTheItemIDTheSourceNamedIt(t *testing.T) {
	named := opcda.DAItemID("Plant.Line1")
	runtime := &browseRuntime{result: opcda.BrowseResult{
		Entries: []opcda.BrowseEntry{{Kind: opcda.BrowseEntryBranch, Name: "Line1", ItemID: &named}},
	}}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 4, RequestDeadline: time.Second,
		MaxBrowseEntries: 10, MaxItemIDBytes: 128, MaxJSONDepth: 8,
	})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, newJSONRequest(http.MethodPost, "/v1/browse",
		strings.NewReader(`{"path":[],"filter":"all"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"itemId":"Plant.Line1"`) {
		t.Fatalf("the branch ItemID did not survive: %s", recorder.Body.String())
	}
}
