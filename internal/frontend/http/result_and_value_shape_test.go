package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The four conditions guarding the value in encodeReadResult are defence in
// depth rather than the only guard: readResultsMatchRequest has already
// refused every combination they answer -- a value beside a failed HRESULT, a
// value with no HRESULT, a value beside an error code, and a missing value
// where the result claims success. Each mutation there survives because the
// earlier pass refuses the result before the encoder sees it, which was
// checked by mutating and running rather than by reading.

// A float that is not a number has no JSON spelling, so it travels under its
// own encoding rather than being written as something a client would read as a
// number. The VT_R4 branch has its own copy of that decision, and it was
// unpinned: the suite exercised the VT_R8 one.
func TestANonNumericFloatTravelsUnderItsOwnEncoding(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		varType  opcda.DAVarType
		value    any
		encoding string
		spelling string
	}{
		{"a VT_R4 infinity", opcda.VTR4, float32(math.Inf(1)), "float-special", `"+Infinity"`},
		{"a VT_R4 negative infinity", opcda.VTR4, float32(math.Inf(-1)), "float-special", `"-Infinity"`},
		{"a VT_R4 NaN", opcda.VTR4, float32(math.NaN()), "float-special", `"NaN"`},
		{"an ordinary VT_R4", opcda.VTR4, float32(1.5), "json", `1.5`},
		{"a VT_R8 infinity", opcda.VTR8, math.Inf(1), "float-special", `"+Infinity"`},
		{"an ordinary VT_R8", opcda.VTR8, 2.5, "json", `2.5`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &readRuntime{results: []opcda.ReadResult{{
				ItemID: "Test/Float", VarType: &testCase.varType,
				HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{
					ItemID: "Test/Float", VarType: testCase.varType, Value: testCase.value,
					QualityRaw: 0x00C0, HRESULT: opcda.SOK,
				},
			}}}
			server := New(runtime, Config{
				MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
				MaxReadItems: 4, MaxItemIDBytes: 64, MaxJSONDepth: 8,
			})
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
				bytes.NewReader([]byte(`{"source":"device","items":[{"itemId":"Test/Float"}]}`))))
			if response.Code != stdhttp.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			if !strings.Contains(body, fmt.Sprintf(`"valueEncoding":%q`, testCase.encoding)) {
				t.Errorf("the value did not travel as %s: %s", testCase.encoding, body)
			}
			if !strings.Contains(body, `"value":`+testCase.spelling) {
				t.Errorf("the value was not spelled %s: %s", testCase.spelling, body)
			}
		})
	}
}

// A browsed entry's name is what a client displays and navigates by, and both
// halves of what makes it usable are checked: it is there, and it is valid
// UTF-8. Relaying an empty name gives a client a row it cannot label; relaying
// invalid UTF-8 gives it bytes it cannot render, in a field it will put into a
// path.
func TestABrowsedEntryNeedsAUsableName(t *testing.T) {
	relayed := func(name string) bool {
		return browseResultMatchesRequest(nil, opcda.BrowseResult{
			Entries: []opcda.BrowseEntry{{
				Kind: opcda.BrowseEntryBranch, Name: name,
			}},
		}, testItemIDBytes)
	}
	if !relayed("Channel1") {
		t.Fatal("a well-formed branch was refused, so the cases below prove nothing")
	}
	if relayed("") {
		t.Error("a branch with no name was relayed")
	}
	if relayed(string([]byte{'A', 0x80})) {
		t.Error("a branch whose name is not valid UTF-8 was relayed")
	}
}

// Each typed Write decode says what it wanted, and the reason is the whole
// value of the message: a client that sent a number where a decimal string was
// required needs to be told which of the two it got wrong, not that something
// about its request was invalid.
func TestEachTypedWriteDecodeSaysWhatItWanted(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		varType opcda.DAVarType
		raw     string
		wants   string
	}{
		{"VT_I8 from a number", opcda.VTI8, `1`, "decimal string"},
		{"VT_I8 from a boolean", opcda.VTI8, `true`, "decimal string"},
		{"VT_BSTR from a number", opcda.VTBSTR, `1`, "JSON string"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := decodeWriteValue(testCase.varType, "json", json.RawMessage(testCase.raw))
			if err == nil {
				t.Fatalf("%s was accepted for %s", testCase.raw, testCase.varType)
			}
			if !strings.Contains(err.Error(), testCase.wants) {
				t.Errorf("refused as %v, which does not say a %s was wanted", err, testCase.wants)
			}
		})
	}

	// The controls: the shapes each of them does want.
	if _, err := decodeWriteValue(opcda.VTI8, "json", json.RawMessage(`"-9223372036854775808"`)); err != nil {
		t.Errorf("a VT_I8 decimal string was refused: %v", err)
	}
	if _, err := decodeWriteValue(opcda.VTBSTR, "json", json.RawMessage(`"text"`)); err != nil {
		t.Errorf("a VT_BSTR JSON string was refused: %v", err)
	}
}

// byref is still refused before any value is looked at: the adapter carries
// scalars and arrays, and a pointer to either is neither.
func TestAByRefWriteValueIsRefusedBeforeItIsRead(t *testing.T) {
	for _, varType := range []opcda.DAVarType{
		opcda.VTI4 | opcda.VTByRef,
		opcda.VTBSTR | opcda.VTArray | opcda.VTByRef,
	} {
		_, err := decodeWriteValue(varType, "json", json.RawMessage(`1`))
		if err == nil {
			t.Errorf("%s was accepted", varType)
			continue
		}
		adapterErr, ok := opcda.AsAdapterError(err)
		if !ok || adapterErr.Code != opcda.CodeUnsupportedVarType {
			t.Errorf("%s was refused as %v, not for being unsupported", varType, err)
		}
	}
	// The control: the same value under the scalar VARTYPE is accepted.
	if _, err := decodeWriteValue(opcda.VTI4, "json", json.RawMessage(`1`)); err != nil {
		t.Errorf("a scalar VT_I4 write was refused: %v", err)
	}
}

// A VARTYPE the adapter does not carry is a capability statement rather than a
// malformed request, so it is reported as one: 422 from the adapter layer
// rather than 400 from the frontend. A client that sent a well-formed request
// for a type this adapter cannot write has not made a mistake it can fix by
// correcting its JSON, and the status is what tells it so.
func TestAVarTypeTheAdapterCannotCarryIsReportedAsItsOwnLimit(t *testing.T) {
	runtime := &writeRuntime{enabled: true}
	server := New(runtime, Config{
		MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxWriteItems: 4, MaxItemIDBytes: 64, MaxJSONDepth: 8,
	})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/write",
		bytes.NewReader([]byte(
			`{"items":[{"itemId":"Test/Any","dataType":"VT_VARIANT",`+
				`"valueEncoding":"json","value":1}]}`))))

	if response.Code != stdhttp.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, string(opcda.CodeUnsupportedVarType))
	if !strings.Contains(response.Body.String(), `"layer":"adapter"`) {
		t.Errorf("a VARTYPE the adapter cannot carry was blamed on the frontend: %s",
			response.Body.String())
	}
	if runtime.calls != 0 {
		t.Errorf("the source was called %d times for a VARTYPE the adapter cannot carry", runtime.calls)
	}

	// A malformed request is still the frontend's 400, so the case above is
	// about which of the two answers applies rather than about everything
	// being 422.
	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, newJSONRequest(stdhttp.MethodPost, "/v1/write",
		bytes.NewReader([]byte(
			`{"items":[{"itemId":"Test/Any","dataType":"VT_I2",`+
				`"valueEncoding":"json","value":"not a number"}]}`))))
	if malformed.Code != stdhttp.StatusBadRequest {
		t.Errorf("a malformed value returned %d, want 400: %s", malformed.Code, malformed.Body.String())
	}
}

// failingReadRuntime answers every Read with one error, which is how a runtime
// that ran out of time reaches the frontend.
type failingReadRuntime struct {
	statusRuntime
	err error
}

func (r failingReadRuntime) ReadBatch(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
	return nil, r.err
}

// A runtime that ran out of time is not a source that refused: the request
// deadline is the adapter's own, so it is reported as a gateway timeout with
// the adapter named, rather than folded in with whatever the source said.
func TestARuntimeDeadlineIsReportedAsTheAdaptersOwn(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"the deadline passed", context.DeadlineExceeded},
		{"the request was cancelled", context.Canceled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := New(failingReadRuntime{err: testCase.err}, Config{
				MaxBodyBytes: 4096, MaxConcurrent: 2, RequestDeadline: time.Second,
				MaxReadItems: 4, MaxItemIDBytes: 64, MaxJSONDepth: 8,
			})
			response := httptest.NewRecorder()
			server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
				bytes.NewReader([]byte(`{"source":"device","items":[{"itemId":"Test/Int32"}]}`))))
			if response.Code != stdhttp.StatusGatewayTimeout {
				t.Errorf("status = %d, want 504: %s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, string(opcda.CodeRuntimeDeadline))
		})
	}
}

// VT_EMPTY and VT_NULL carry no value, so what makes one of them encodable is
// that nothing came with it. Inverting the check makes the rule its own
// opposite: an empty value carrying a number would be relayed and one carrying
// nothing refused, which is the adapter inventing a reading for a type defined
// as having none.
func TestAnEmptyReadValueIsTheAbsenceOfOne(t *testing.T) {
	for _, varType := range []opcda.DAVarType{opcda.VTEmpty, opcda.VTNull} {
		t.Run(varType.String(), func(t *testing.T) {
			if _, _, err := encodeDAValue(varType, nil); err != nil {
				t.Errorf("a %s value carrying nothing was refused: %v", varType, err)
			}
			if _, _, err := encodeDAValue(varType, int32(0)); err == nil {
				t.Errorf("a %s value carrying a number was encoded", varType)
			}
		})
	}
	// The control: a type that does carry a value still requires the right one.
	if _, _, err := encodeDAValue(opcda.VTI4, int32(1)); err != nil {
		t.Errorf("a VT_I4 value carrying an int32 was refused: %v", err)
	}
	if _, _, err := encodeDAValue(opcda.VTI4, nil); err == nil {
		t.Error("a VT_I4 value carrying nothing was encoded")
	}
}

// Two survivors in json.go are the pattern already recorded for the
// configuration file's scanner, and equivalent for the same reasons: the
// closing-delimiter check cannot fire because encoding/json returns a matched
// delimiter for every container it opened, and the trailing-value check is
// backed by validateJSONStructure walking the same bytes first.
