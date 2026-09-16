package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The pre-scan is a security boundary: it is what stops a body from nesting
// past the configured depth, from carrying the same field twice, and from
// spelling a documented field with different cases. It used to drive an
// encoding/json Decoder, which answered those three questions by decoding
// every key and every scalar into an interface value -- 39% of a hundred-item
// Read's time and 60% of its allocations, to learn things that need no values.
//
// The replacement walks the bytes. A hand-written scanner in front of a
// security boundary is worth exactly what it can be shown to be equivalent to,
// so the implementation it replaced is kept here and the two are held to the
// same answer: the same accept or reject, and the same error code when they
// reject. This file is that proof, and the fuzz target beside it extends the
// proof to inputs nobody thought to write down.

// referenceValidateJSONStructure is the Decoder-driven implementation, kept
// verbatim as the thing the scanner has to agree with.
func referenceValidateJSONStructure(body []byte, maximumDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := referenceScanJSONValue(decoder, 0, maximumDepth); err != nil {
		return err
	}
	if _, err := decoder.Token(); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("request body must contain exactly one JSON value")
}

func referenceScanJSONValue(decoder *json.Decoder, depth, maximumDepth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	if depth >= maximumDepth {
		return &requestBodyError{
			code:    opcda.CodeJSONDepthLimitExceeded,
			message: "request JSON exceeds the configured nesting-depth limit",
		}
	}

	if delimiter == '{' {
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			for _, canonical := range canonicalRequestFields {
				if key != canonical && strings.EqualFold(key, canonical) {
					return &requestBodyError{
						code:    opcda.CodeInvalidRequest,
						message: "request JSON field names must use the exact documented spelling",
					}
				}
			}
			if _, exists := keys[key]; exists {
				return &requestBodyError{
					code:    opcda.CodeDuplicateJSONField,
					message: "request JSON contains a duplicate object field",
				}
			}
			keys[key] = struct{}{}
			if err := referenceScanJSONValue(decoder, depth+1, maximumDepth); err != nil {
				return err
			}
		}
	} else {
		for decoder.More() {
			if err := referenceScanJSONValue(decoder, depth+1, maximumDepth); err != nil {
				return err
			}
		}
	}

	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	expected := json.Delim('}')
	if delimiter == '[' {
		expected = ']'
	}
	if closing != expected {
		return fmt.Errorf("unexpected JSON closing delimiter %q", closing)
	}
	return nil
}

// errorCodeOf reports the adapter error code an answer carries, or "" for a
// plain syntax error. The codes are the part a client acts on, so agreeing on
// accept or reject is not enough on its own.
func errorCodeOf(err error) string {
	if err == nil {
		return ""
	}
	var bodyErr *requestBodyError
	if errors.As(err, &bodyErr) {
		return string(bodyErr.code)
	}
	return "SYNTAX"
}

func assertSameAnswer(t *testing.T, body string, depth int) {
	t.Helper()
	want := referenceValidateJSONStructure([]byte(body), depth)
	got := scanJSONStructure([]byte(body), depth)
	if (want == nil) != (got == nil) {
		t.Errorf("depth=%d body=%q\n reference: %v\n scanner:   %v", depth, body, want, got)
		return
	}
	if wantCode, gotCode := errorCodeOf(want), errorCodeOf(got); wantCode != gotCode {
		t.Errorf("depth=%d body=%q refused with %s, reference refused with %s\n reference: %v\n scanner:   %v",
			depth, body, gotCode, wantCode, want, got)
	}
}

func TestTheScannerAgreesWithTheDecoderItReplaced(t *testing.T) {
	bodies := []string{
		// Ordinary request shapes.
		`{}`,
		`[]`,
		`{"source":"device","items":[{"itemId":"A"}]}`,
		`{"items":[{"itemId":"A","dataType":"VT_I2","valueEncoding":"json","value":1}]}`,
		`{"path":[],"filter":"all"}`,
		`{"itemId":"Test/Float","propertyIds":[100,101]}`,
		// Every scalar shape.
		`{"a":null}`, `{"a":true}`, `{"a":false}`,
		`{"a":0}`, `{"a":-0}`, `{"a":1}`, `{"a":-1}`, `{"a":1.5}`, `{"a":-1.5}`,
		`{"a":1e3}`, `{"a":1E3}`, `{"a":1e+3}`, `{"a":1e-3}`, `{"a":1.5e-3}`,
		`{"a":""}`, `{"a":"text"}`, `{"a":"\u00e9"}`, `{"a":"\ud83d\ude00"}`,
		`{"a":"tab\tnewline\n"}`, `{"a":"quote\"backslash\\"}`,
		`{"a":"온도"}`,
		// Numbers a scanner is easy to get wrong.
		`{"a":01}`, `{"a":00}`, `{"a":-}`, `{"a":+1}`, `{"a":.5}`, `{"a":1.}`,
		`{"a":1e}`, `{"a":1e+}`, `{"a":0.0}`, `{"a":0e0}`, `{"a":10}`,
		// Duplicates, at every level and by every spelling.
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`{"a":[{"b":1,"b":2}]}`,
		`{"itemId":"A","\u0069temId":"B"}`,
		`{"a":1,"b":1}`,
		// Canonical spellings and near misses.
		`{"itemId":"A"}`, `{"ItemId":"A"}`, `{"ITEMID":"A"}`, `{"itemid":"A"}`,
		`{"source":"device"}`, `{"Source":"device"}`,
		`{"itemIdX":"A"}`,
		// Nesting.
		`{"a":{"b":{"c":{"d":1}}}}`,
		`[[[[1]]]]`,
		`{"a":[{"b":[{"c":1}]}]}`,
		// Malformed in every direction.
		``, ` `, `   `, `{`, `}`, `[`, `]`, `{"a"}`, `{"a":}`, `{:1}`, `{"a":1,}`,
		`[1,]`, `[,]`, `{,}`, `{"a":1"b":2}`, `[1 2]`, `"unterminated`,
		`{"a":"unterminated}`, `tru`, `nul`, `fals`, `TRUE`, `None`,
		`{"a":"\x"}`, `{"a":"\u00"}`, `{"a":"\uZZZZ"}`,
		"{\"a\":\"raw\tcontrol\"}",
		// More than one value, which is its own rule.
		`{} {}`, `{}{}`, `1 2`, `"a" "b"`, `{} `, ` {} `, `{}` + "\n",
		`[] []`,
		// Whitespace in every legal position.
		"{ \"a\" : 1 , \"b\" : [ 1 , 2 ] }",
		"\n\t{\"a\":1}\r\n",
	}

	for _, depth := range []int{1, 2, 3, 4, 8, 64} {
		for _, body := range bodies {
			assertSameAnswer(t, body, depth)
		}
	}
}

// FuzzScannerAgreesWithTheDecoder extends the agreement to inputs nobody wrote
// down. The property is the whole point: whatever the two are given, they
// answer the same way.
func FuzzScannerAgreesWithTheDecoder(f *testing.F) {
	for _, seed := range []string{
		`{"source":"device","items":[{"itemId":"A"}]}`,
		`{"a":{"b":{"c":1}}}`,
		`{"a":1,"a":2}`,
		`{"ItemId":"A"}`,
		`{"a":"\ud83d\ude00"}`,
		`[1,2,3]`,
		`{} {}`,
		`{"a":01}`,
		``,
	} {
		f.Add(seed, 4)
	}
	f.Fuzz(func(t *testing.T, body string, depth int) {
		// A depth of zero or less refuses every container in both, and a huge
		// one is the same as none; neither is a shape the server configures.
		if depth < 1 || depth > 256 {
			t.Skip()
		}
		if len(body) > 4096 {
			t.Skip()
		}
		want := referenceValidateJSONStructure([]byte(body), depth)
		got := scanJSONStructure([]byte(body), depth)
		if (want == nil) != (got == nil) {
			t.Fatalf("body=%q depth=%d\n reference: %v\n scanner:   %v", body, depth, want, got)
		}
		if wantCode, gotCode := errorCodeOf(want), errorCodeOf(got); wantCode != gotCode {
			t.Fatalf("body=%q depth=%d: scanner %s, reference %s", body, depth, gotCode, wantCode)
		}
	})
}

// The scanner is also held to the rules directly, so a change that broke both
// it and the reference in the same way would still be caught.
func TestTheScannerEnforcesEachRuleItExistsFor(t *testing.T) {
	if err := scanJSONStructure([]byte(`{"source":"device","items":[{"itemId":"A"}]}`), 8); err != nil {
		t.Fatalf("an ordinary body was refused: %v", err)
	}
	for _, testCase := range []struct {
		name  string
		body  string
		depth int
		code  opcda.ErrorCode
	}{
		{"one level past the depth", `{"a":{"b":1}}`, 1, opcda.CodeJSONDepthLimitExceeded},
		{"a duplicate field", `{"a":1,"a":2}`, 8, opcda.CodeDuplicateJSONField},
		{"a duplicate spelled with an escape", `{"a":1,"\u0061":2}`, 8, opcda.CodeDuplicateJSONField},
		{"a documented field cased differently", `{"ItemId":"A"}`, 8, opcda.CodeInvalidRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := scanJSONStructure([]byte(testCase.body), testCase.depth)
			if err == nil {
				t.Fatal("accepted")
			}
			var bodyErr *requestBodyError
			if !errors.As(err, &bodyErr) || bodyErr.code != testCase.code {
				t.Errorf("refused as %v, want %s", err, testCase.code)
			}
		})
	}
	// Exactly the depth is not past it.
	if err := scanJSONStructure([]byte(`{"a":{"b":1}}`), 2); err != nil {
		t.Errorf("a body nested to exactly the depth was refused: %v", err)
	}
	// And a second value is refused however much whitespace separates it.
	if err := scanJSONStructure([]byte("{}\n\t {}"), 8); err == nil {
		t.Error("a body carrying two values was accepted")
	}
	if err := scanJSONStructure([]byte("  {}  \n"), 8); err != nil {
		t.Errorf("trailing whitespace was refused: %v", err)
	}
}
