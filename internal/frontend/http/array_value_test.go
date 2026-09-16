package http

import (
	"bytes"
	"encoding/json"
	"math"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// An array travels as its shape plus its elements, under an encoding of its
// own. design.md §20.4 forbids flattening it to a JSON array, and what it
// forbids is the loss rather than the flat list: the elements here are flat and
// the dimensions are published beside them, so a client can rebuild the array
// from the description alone.
//
// These cases go through the handler rather than the encoder, because what a
// client can rely on is the body it receives.

func arrayReadResponse(t *testing.T, array opcda.DAArray) map[string]any {
	t.Helper()
	varType := array.ElementType | opcda.VTArray
	runtime := &readRuntime{results: []opcda.ReadResult{{
		ItemID: "Test/Array", VarType: &varType,
		HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Array", VarType: varType, Value: array,
			QualityRaw: 0x00C0, HRESULT: opcda.SOK,
		},
	}}}
	server := New(runtime, Config{
		MaxBodyBytes: 65536, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxReadItems: 4, MaxItemIDBytes: 64, MaxJSONDepth: 16,
	})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
		bytes.NewReader([]byte(`{"source":"device","items":[{"itemId":"Test/Array"}]}`))))
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 {
		t.Fatalf("the response carried %d results: %s", len(decoded.Results), response.Body.String())
	}
	return decoded.Results[0]
}

func TestAReadArrayPublishesItsShapeBesideItsElements(t *testing.T) {
	array := opcda.DAArray{
		ElementType: opcda.VTI4,
		Dimensions: []opcda.DADimension{
			{LowerBound: 1, Length: 3}, {LowerBound: 0, Length: 2},
		},
		Elements: []any{int32(10), int32(11), int32(12), int32(13), int32(14), int32(15)},
	}
	item := arrayReadResponse(t, array)

	if got := item["valueEncoding"]; got != arrayValueEncoding {
		t.Errorf("valueEncoding = %v, want %q", got, arrayValueEncoding)
	}
	value, ok := item["value"].(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want an object describing the array", item["value"])
	}

	elementType, ok := value["elementDataType"].(map[string]any)
	if !ok || elementType["name"] != "VT_I4" {
		t.Errorf("elementDataType = %#v", value["elementDataType"])
	}
	// The element type is a scalar: an array of arrays is not a shape this
	// carries, and saying the elements are arrays would be untrue.
	if elementType["array"] == true {
		t.Error("the element type was published as an array")
	}

	dimensions, ok := value["dimensions"].([]any)
	if !ok || len(dimensions) != 2 {
		t.Fatalf("dimensions = %#v", value["dimensions"])
	}
	first, _ := dimensions[0].(map[string]any)
	if first["lowerBound"] != float64(1) || first["length"] != float64(3) {
		t.Errorf("the first dimension was published as %#v", dimensions[0])
	}
	second, _ := dimensions[1].(map[string]any)
	if second["lowerBound"] != float64(0) || second["length"] != float64(2) {
		t.Errorf("the second dimension was published as %#v", dimensions[1])
	}

	elements, ok := value["elements"].([]any)
	if !ok || len(elements) != 6 {
		t.Fatalf("elements = %#v", value["elements"])
	}
	for index, want := range []float64{10, 11, 12, 13, 14, 15} {
		if elements[index] != want {
			t.Errorf("element %d = %#v, want %v", index, elements[index], want)
		}
	}
}

// The lower bound is the source's, not a normalisation. A client that reads an
// array starting at one and writes back to index one must reach the element it
// read.
func TestAReadArrayKeepsTheLowerBoundTheSourceGave(t *testing.T) {
	for _, lowerBound := range []int32{-3, 0, 1, 7} {
		item := arrayReadResponse(t, opcda.DAArray{
			ElementType: opcda.VTI2,
			Dimensions:  []opcda.DADimension{{LowerBound: lowerBound, Length: 2}},
			Elements:    []any{int16(1), int16(2)},
		})
		value := item["value"].(map[string]any)
		dimensions := value["dimensions"].([]any)
		first := dimensions[0].(map[string]any)
		if first["lowerBound"] != float64(lowerBound) {
			t.Errorf("a lower bound of %d was published as %#v", lowerBound, first["lowerBound"])
		}
	}
}

// Elements follow the same per-type rules a scalar does, so nothing new has to
// be learned to read one. A non-finite float is the exception: a scalar names
// it through a sibling valueEncoding an element does not have, so inside an
// array it is spelled in place.
func TestArrayElementsUseTheSameRulesScalarsDo(t *testing.T) {
	t.Run("wide integers stay decimal strings", func(t *testing.T) {
		item := arrayReadResponse(t, opcda.DAArray{
			ElementType: opcda.VTI8,
			Dimensions:  []opcda.DADimension{{Length: 2}},
			Elements:    []any{int64(-9223372036854775808), int64(1)},
		})
		elements := item["value"].(map[string]any)["elements"].([]any)
		if elements[0] != "-9223372036854775808" || elements[1] != "1" {
			t.Errorf("elements = %#v", elements)
		}
	})

	t.Run("non-finite floats are spelled in place", func(t *testing.T) {
		item := arrayReadResponse(t, opcda.DAArray{
			ElementType: opcda.VTR8,
			Dimensions:  []opcda.DADimension{{Length: 4}},
			Elements: []any{
				1.5,
				math.Inf(1),
				math.Inf(-1),
				math.NaN(),
			},
		})
		elements := item["value"].(map[string]any)["elements"].([]any)
		if elements[0] != float64(1.5) {
			t.Errorf("a finite element was published as %#v", elements[0])
		}
		for index, want := range map[int]string{1: "+Infinity", 2: "-Infinity", 3: "NaN"} {
			if elements[index] != want {
				t.Errorf("element %d = %#v, want %q", index, elements[index], want)
			}
		}
	})
}

// A Write supplies the same description a Read publishes, so a client can send
// back what it was given. The adapter never infers a shape.
func TestAWrittenArrayReachesTheSourceAsItWasDescribed(t *testing.T) {
	runtime := &writeRuntime{enabled: true, results: []opcda.WriteResult{
		{ItemID: "Test/Array", HRESULT: opcda.SOK, HRESULTPresent: true},
	}}
	server := New(runtime, Config{
		MaxBodyBytes: 65536, MaxConcurrent: 2, RequestDeadline: time.Second,
		MaxWriteItems: 4, MaxItemIDBytes: 64, MaxJSONDepth: 16,
	})
	body := `{"items":[{"itemId":"Test/Array","dataType":"VT_I4|VT_ARRAY",` +
		`"valueEncoding":"array","value":{` +
		`"elementDataType":"VT_I4",` +
		`"dimensions":[{"lowerBound":1,"length":2},{"lowerBound":0,"length":2}],` +
		`"elements":[1,2,3,4]}}]}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/write",
		bytes.NewReader([]byte(body))))
	if response.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(runtime.items) != 1 {
		t.Fatalf("the source received %d items", len(runtime.items))
	}
	written := runtime.items[0]
	if written.VarType != opcda.VTI4|opcda.VTArray {
		t.Errorf("the source was asked for %s", written.VarType)
	}
	array, ok := written.Value.(opcda.DAArray)
	if !ok {
		t.Fatalf("the source received %#v, not an array", written.Value)
	}
	if array.ElementType != opcda.VTI4 {
		t.Errorf("element type = %s", array.ElementType)
	}
	want := []opcda.DADimension{{LowerBound: 1, Length: 2}, {LowerBound: 0, Length: 2}}
	for index, dimension := range want {
		if array.Dimensions[index] != dimension {
			t.Errorf("dimension %d = %+v, want %+v", index, array.Dimensions[index], dimension)
		}
	}
	for index, element := range []any{int32(1), int32(2), int32(3), int32(4)} {
		if array.Elements[index] != element {
			t.Errorf("element %d = %#v, want %#v", index, array.Elements[index], element)
		}
	}
}

// A body that contradicts itself is refused rather than reshaped: the shape is
// what a client asked the source to write, and guessing at it would write
// somewhere the client did not name.
func TestAWrittenArrayMustAgreeWithItself(t *testing.T) {
	arrayValue := func(body string) (any, error) {
		return decodeWriteValue(opcda.VTI4|opcda.VTArray, arrayValueEncoding, json.RawMessage(body))
	}

	if _, err := arrayValue(`{"dimensions":[{"lowerBound":0,"length":2}],"elements":[1,2]}`); err != nil {
		t.Fatalf("a self-consistent array was refused: %v", err)
	}
	for _, testCase := range []struct {
		name string
		body string
	}{
		{"fewer elements than the shape describes",
			`{"dimensions":[{"lowerBound":0,"length":3}],"elements":[1,2]}`},
		{"more elements than the shape describes",
			`{"dimensions":[{"lowerBound":0,"length":1}],"elements":[1,2]}`},
		{"no dimensions at all", `{"dimensions":[],"elements":[1]}`},
		{"an element of the wrong type", `{"dimensions":[{"length":1}],"elements":["x"]}`},
		{"an element type that disagrees with the declared one",
			`{"elementDataType":"VT_I2","dimensions":[{"length":1}],"elements":[1]}`},
		{"a field nothing defines",
			`{"dimensions":[{"length":1}],"elements":[1],"stride":2}`},
		{"a flat array with no shape at all", `[1,2,3]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := arrayValue(testCase.body); err == nil {
				t.Error("a contradictory array body was accepted")
			}
		})
	}

	// The two encodings name different shapes, and neither may stand in for the
	// other: an array under "json" would be read as a scalar, and a scalar
	// under "array" as a shape that is not there.
	if _, err := decodeWriteValue(opcda.VTI4|opcda.VTArray, "json",
		json.RawMessage(`{"dimensions":[{"length":1}],"elements":[1]}`)); err == nil {
		t.Error("an array value under the json encoding was accepted")
	}
	if _, err := decodeWriteValue(opcda.VTI4, arrayValueEncoding, json.RawMessage(`1`)); err == nil {
		t.Error("a scalar value under the array encoding was accepted")
	}
}

// An array Write round trips through what a Read published, which is the
// property a client depends on and the one neither side can check alone.
func TestAnArrayWrittenBackIsTheArrayThatWasRead(t *testing.T) {
	original := opcda.DAArray{
		ElementType: opcda.VTR8,
		Dimensions:  []opcda.DADimension{{LowerBound: -1, Length: 3}},
		Elements:    []any{1.5, math.Inf(1), math.NaN()},
	}
	published, err := encodeDAArray(original)
	if err != nil {
		t.Fatalf("encodeDAArray: %v", err)
	}
	// What a client sends back is the object it was handed, with the element
	// type named the way the read published it.
	var asRead map[string]json.RawMessage
	if err := json.Unmarshal(published, &asRead); err != nil {
		t.Fatal(err)
	}
	asRead["elementDataType"] = json.RawMessage(`"VT_R8"`)
	returned, err := json.Marshal(asRead)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := decodeWriteValue(opcda.VTR8|opcda.VTArray, arrayValueEncoding, returned)
	if err != nil {
		t.Fatalf("the published array was refused when written back: %v", err)
	}
	array, ok := decoded.(opcda.DAArray)
	if !ok {
		t.Fatalf("decoded %#v", decoded)
	}
	if array.ElementType != original.ElementType ||
		len(array.Dimensions) != 1 || array.Dimensions[0] != original.Dimensions[0] {
		t.Errorf("shape = %s %+v", array.ElementType, array.Dimensions)
	}
	if array.Elements[0] != 1.5 {
		t.Errorf("element 0 = %#v", array.Elements[0])
	}
	if !math.IsInf(array.Elements[1].(float64), 1) {
		t.Errorf("element 1 = %#v, want +Inf", array.Elements[1])
	}
	if !math.IsNaN(array.Elements[2].(float64)) {
		t.Errorf("element 2 = %#v, want NaN", array.Elements[2])
	}
}

// The special-float rule inside an array has an arm per float width, and only
// the VT_R8 one had been exercised -- the same gap the scalar encoding had
// before #202. A VT_R4 element that is not a number travels the same way a
// VT_R8 one does.
//
// The other half is that the rule belongs to floats alone: a VT_BSTR element
// is a JSON string because a string is what it is, and reading one as a
// float-special name would refuse every ordinary string an array carries.
func TestTheSpecialFloatRuleInsideAnArrayBelongsToFloatsAlone(t *testing.T) {
	decodeElements := func(t *testing.T, elementType opcda.DAVarType, elements string) (any, error) {
		t.Helper()
		body := `{"dimensions":[{"length":1}],"elements":[` + elements + `]}`
		return decodeWriteValue(elementType|opcda.VTArray, arrayValueEncoding, json.RawMessage(body))
	}

	for _, elementType := range []opcda.DAVarType{opcda.VTR4, opcda.VTR8} {
		t.Run(elementType.String(), func(t *testing.T) {
			decoded, err := decodeElements(t, elementType, `"NaN"`)
			if err != nil {
				t.Fatalf("a NaN element was refused: %v", err)
			}
			array := decoded.(opcda.DAArray)
			switch typed := array.Elements[0].(type) {
			case float32:
				if !math.IsNaN(float64(typed)) {
					t.Errorf("element = %v, want NaN", typed)
				}
			case float64:
				if !math.IsNaN(typed) {
					t.Errorf("element = %v, want NaN", typed)
				}
			default:
				t.Errorf("element decoded as %#v", array.Elements[0])
			}

			// An ordinary number still decodes as a number, so the rule is
			// about the spelling rather than about the type.
			if _, err := decodeElements(t, elementType, `1.5`); err != nil {
				t.Errorf("an ordinary float element was refused: %v", err)
			}
			// And a string that is not one of the three names is not a value.
			if _, err := decodeElements(t, elementType, `"almost"`); err == nil {
				t.Error("a string that names no special float was accepted")
			}
		})
	}

	t.Run("a string element is a string", func(t *testing.T) {
		decoded, err := decodeElements(t, opcda.VTBSTR, `"NaN"`)
		if err != nil {
			t.Fatalf("a VT_BSTR element spelled like a special float was refused: %v", err)
		}
		array := decoded.(opcda.DAArray)
		if array.Elements[0] != "NaN" {
			t.Errorf("a VT_BSTR element decoded as %#v, want the string", array.Elements[0])
		}
	})
}
