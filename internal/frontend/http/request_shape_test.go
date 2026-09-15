package http

import (
	"bytes"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Eight survivors in this package, all in the shape a request may take rather
// than in what it asks the source for. Each is a rule a client is held to, and
// none had a case at the value that decides it.

// A Host header naming a loopback address is what the loopback policy is
// checked against, and a bracketed IPv6 literal is the awkward spelling. The
// two bracket tests do different jobs -- the first decides whether this is a
// bracketed literal at all, the second whether it is a well-formed one -- and
// joining them the other way makes a half-bracketed host pass as an ordinary
// name, so "[::1" would be read as a host called "[::1".
func TestABracketedHostMustCarryBothOfItsBrackets(t *testing.T) {
	for _, testCase := range []struct {
		value    string
		loopback bool
	}{
		{"[::1]", true},
		{"[::1]:8080", true},
		{"127.0.0.1", true},
		{"127.0.0.1:8080", true},
		{"localhost", true},
		{"localhost.", true},
		// Half a bracketed literal is not a host name that happens to start
		// with a bracket.
		{"[::1", false},
		{"::1]", false},
		{"[[::1]]", false},
		// Not loopback at all.
		{"192.0.2.1", false},
		{"example.test", false},
		{"", false},
		// A port of zero is not a port a client connected to.
		{"127.0.0.1:0", false},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			if got := isLoopbackRequestHost(testCase.value); got != testCase.loopback {
				t.Errorf("isLoopbackRequestHost(%q) = %v, want %v", testCase.value, got, testCase.loopback)
			}
		})
	}
}

// A Browse filter is one of three words. The default fills in "all" when the
// field is absent, and each of the three must be carried through rather than
// refused -- a client told about "item" and then refused it has been told
// something untrue.
func TestABrowseFilterIsOneOfExactlyThreeWords(t *testing.T) {
	for _, testCase := range []struct {
		body     string
		filter   opcda.BrowseFilter
		accepted bool
	}{
		{`{"path":[],"filter":"all"}`, opcda.BrowseFilterAll, true},
		{`{"path":[],"filter":"branch"}`, opcda.BrowseFilterBranch, true},
		{`{"path":[],"filter":"item"}`, opcda.BrowseFilterItem, true},
		{`{"path":[]}`, opcda.BrowseFilterAll, true},
		{`{"path":[],"filter":""}`, opcda.BrowseFilterAll, true},
		{`{"path":[],"filter":"ALL"}`, "", false},
		{`{"path":[],"filter":"items"}`, "", false},
		{`{"path":[],"filter":"leaf"}`, "", false},
	} {
		t.Run(testCase.body, func(t *testing.T) {
			runtime := &browseRuntime{}
			server := newBrowseTestServer(runtime)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/browse",
				bytes.NewReader([]byte(testCase.body))))
			if accepted := response.Code == stdhttp.StatusOK; accepted != testCase.accepted {
				t.Fatalf("status = %d (accepted = %v), want accepted = %v: %s",
					response.Code, accepted, testCase.accepted, response.Body.String())
			}
			if testCase.accepted && runtime.request.Filter != testCase.filter {
				t.Errorf("the filter reached the runtime as %q, want %q",
					runtime.request.Filter, testCase.filter)
			}
		})
	}
}

// A request body holds exactly one JSON value. A second one after it is not
// trailing whitespace but a second request, and which of the two the adapter
// would act on is not something a client should be able to leave open.
func TestARequestBodyHoldsExactlyOneJSONValue(t *testing.T) {
	const single = `{"path":[]}`
	for _, testCase := range []struct {
		name     string
		body     string
		accepted bool
	}{
		{"one value", single, true},
		{"one value and trailing whitespace", single + "\n\n  ", true},
		{"two values", single + "\n" + single, false},
		{"a value followed by a fragment", single + "\n{", false},
		{"a value followed by a bare number", single + " 1", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newBrowseTestServer(&browseRuntime{})
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/browse",
				bytes.NewReader([]byte(testCase.body))))
			if accepted := response.Code == stdhttp.StatusOK; accepted != testCase.accepted {
				t.Errorf("status = %d (accepted = %v), want accepted = %v: %s",
					response.Code, accepted, testCase.accepted, response.Body.String())
			}
		})
	}
}

// The HTTP server fills in a default for every bound left unset, each behind
// its own check. A check that fired for a value the operator did set would
// silently replace it, and the configuration an operator can see would no
// longer be the one the server runs.
func TestAnExplicitHTTPBoundIsNotReplacedByItsDefault(t *testing.T) {
	explicit := Config{
		MaxBodyBytes:        1,
		MaxConcurrent:       1,
		RequestDeadline:     time.Nanosecond,
		MaxReadItems:        1,
		MaxWriteItems:       1,
		MaxBrowseEntries:    1,
		MaxBrowseDepth:      1,
		MaxItemIDBytes:      1,
		MaxItemProperties:   1,
		MaxJSONDepth:        1,
		RequireLoopbackHost: true,
	}
	if got := New(&browseRuntime{}, explicit).config; got != explicit {
		t.Errorf("the server runs a configuration the operator did not set:\ngot  %+v\nwant %+v",
			got, explicit)
	}

	// And a bound left at zero does get a default, or the checks would be
	// doing nothing at all.
	defaulted := New(&browseRuntime{}, Config{}).config
	if defaulted.MaxReadItems == 0 || defaulted.MaxWriteItems == 0 ||
		defaulted.MaxBrowseEntries == 0 || defaulted.MaxBrowseDepth == 0 ||
		defaulted.MaxItemIDBytes == 0 {
		t.Errorf("an unset configuration was left unfilled: %+v", defaulted)
	}
}

// An ItemID of exactly the configured length is addressable, and a client was
// told that length. Refusing it would make the documented bound one byte
// smaller than it says.
func TestAWrittenItemIDMayBeExactlyTheConfiguredLength(t *testing.T) {
	const limit = 24
	runtime := &writeRuntime{enabled: true, results: []opcda.WriteResult{
		{ItemID: opcda.DAItemID(strings.Repeat("i", limit)), HRESULT: opcda.SOK, HRESULTPresent: true},
	}}
	server := New(runtime, Config{
		MaxItemIDBytes: limit, MaxWriteItems: 4, MaxJSONDepth: 16, MaxBodyBytes: 1 << 16,
		MaxConcurrent: 4, RequestDeadline: time.Second,
	})
	body := func(itemID string) string {
		return `{"items":[{"itemId":"` + itemID + `","dataType":"VT_I2","valueEncoding":"json","value":1}]}`
	}
	atLimit := httptest.NewRecorder()
	server.ServeHTTP(atLimit, newJSONRequest(stdhttp.MethodPost, "/v1/write",
		bytes.NewReader([]byte(body(strings.Repeat("i", limit))))))
	if atLimit.Code == stdhttp.StatusBadRequest &&
		strings.Contains(atLimit.Body.String(), string(opcda.CodeItemIDTooLong)) {
		t.Errorf("an ItemID of exactly %d bytes was refused as too long: %s", limit, atLimit.Body.String())
	}

	past := httptest.NewRecorder()
	server.ServeHTTP(past, newJSONRequest(stdhttp.MethodPost, "/v1/write",
		bytes.NewReader([]byte(body(strings.Repeat("i", limit+1))))))
	if !strings.Contains(past.Body.String(), string(opcda.CodeItemIDTooLong)) {
		t.Errorf("an ItemID of %d bytes was not refused for its length: %d %s",
			limit+1, past.Code, past.Body.String())
	}
}

// Two survivors in this package are equivalent mutants rather than gaps,
// checked by mutating and running rather than by reading:
//
//   - isLoopbackRequestHost's `HasPrefix("[") || HasSuffix("]")` to &&: with
//     the conjunction a half-bracketed value is treated as an ordinary host
//     name instead of a malformed literal, and an ordinary host name carrying
//     a bracket can never be loopback -- "localhost" has none and net.ParseIP
//     rejects them -- so both forms answer false for every input that differs.
//   - decodeRequestBody's `ensureJSONEOF` check: validateJSONStructure has
//     already walked the same bytes and refused a second value, so removing
//     this one leaves the body refused by the earlier pass. It is
//     defence in depth rather than the only guard, which is why a request
//     carrying two JSON values is still rejected with it disabled.
