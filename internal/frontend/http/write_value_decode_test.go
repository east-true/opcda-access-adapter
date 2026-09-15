package http

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// decodeWriteValue turns the JSON a client sent into the exact Go type the
// declared VARTYPE demands. It is where a Write stops being text, and INV-10
// applies to all of it: a value that does not fit is refused rather than
// coerced into something that looks like it worked.
//
// The sweep found most of its guards unpinned. The float branch is three
// separate reasons joined by ||, and turning either || into && leaves only a
// value that fails every test refused, so an infinity or a NaN would have gone
// to the source. VT_EMPTY's `text != "null"` survived inversion, which means
// nothing said it refuses anything else.
//
// Infinity and NaN reach this function as text because Go's ParseFloat accepts
// the words: a body that gets past the JSON decoder cannot carry them, but the
// separate float-special encoding exists precisely so a client can ask for them
// deliberately, and this branch is what keeps them out of the plain one.

func TestAPlainJSONFloatRefusesInfinityAndNaN(t *testing.T) {
	for _, varType := range []opcda.DAVarType{opcda.VTR4, opcda.VTR8} {
		t.Run(varType.String(), func(t *testing.T) {
			// The control: an ordinary number decodes, so a guard that refused
			// everything would not pass the cases below.
			if _, err := decodeWriteValue(varType, "json", json.RawMessage(`1.5`)); err != nil {
				t.Fatalf("an ordinary float was refused: %v", err)
			}
			for _, text := range []string{
				`Inf`, `+Inf`, `-Inf`, `Infinity`, `-Infinity`, `NaN`,
				// Out of range for the width: ParseFloat reports both an error
				// and an infinity, so this reaches the guard by two routes.
				`1e400`,
				// Not a number at all.
				`""`, `true`, `"1.5"`,
			} {
				if value, err := decodeWriteValue(varType, "json", json.RawMessage(text)); err == nil {
					t.Errorf("%s as a plain JSON %s was accepted, decoding %#v",
						text, varType, value)
				}
			}
		})
	}

	// VT_R4 narrows, so a value only a float64 can hold is out of range for it
	// while remaining ordinary for VT_R8.
	beyondFloat32 := json.RawMessage(`1e39`)
	if _, err := decodeWriteValue(opcda.VTR4, "json", beyondFloat32); err == nil {
		t.Error("a value past the float32 range was accepted for VT_R4")
	}
	if _, err := decodeWriteValue(opcda.VTR8, "json", beyondFloat32); err != nil {
		t.Errorf("a value inside the float64 range was refused for VT_R8: %v", err)
	}
}

// float-special is the encoding that exists so a client can ask for these
// deliberately. Keeping it working is what makes refusing them in plain JSON a
// contract rather than a limitation.
func TestFloatSpecialCarriesWhatPlainJSONRefuses(t *testing.T) {
	for _, testCase := range []struct {
		text string
		is   func(float64) bool
	}{
		{`"+Infinity"`, func(v float64) bool { return math.IsInf(v, 1) }},
		{`"-Infinity"`, func(v float64) bool { return math.IsInf(v, -1) }},
		{`"NaN"`, math.IsNaN},
	} {
		t.Run(testCase.text, func(t *testing.T) {
			value, err := decodeWriteValue(opcda.VTR8, "float-special", json.RawMessage(testCase.text))
			if err != nil {
				t.Fatalf("float-special refused %s: %v", testCase.text, err)
			}
			typed, ok := value.(float64)
			if !ok || !testCase.is(typed) {
				t.Errorf("decoded %#v, which is not what %s names", value, testCase.text)
			}
		})
	}

	// The vocabulary is exactly those three spellings. A positive infinity
	// carries its sign, and the unsigned word is not one of them -- worth
	// asserting, because a client that guesses would otherwise be refused with
	// no test saying that is deliberate.
	for _, text := range []string{`"Infinity"`, `"inf"`, `"nan"`, `"+inf"`, `"1.5"`} {
		if _, err := decodeWriteValue(opcda.VTR8, "float-special", json.RawMessage(text)); err == nil {
			t.Errorf("float-special accepted %s, which is not one of its three names", text)
		}
	}

	// And it is valid only for the two float types, so it cannot be used to
	// smuggle a special value into an integer.
	if _, err := decodeWriteValue(opcda.VTI4, "float-special", json.RawMessage(`"NaN"`)); err == nil {
		t.Error("float-special was accepted for VT_I4")
	}
}

// An empty or null value is written as the JSON null and nothing else. The
// inverted check would have accepted any text at all and returned nothing,
// which is a Write that silently did something other than what was asked.
func TestAnEmptyWriteValueMustBeTheJSONNull(t *testing.T) {
	for _, varType := range []opcda.DAVarType{opcda.VTEmpty, opcda.VTNull} {
		t.Run(varType.String(), func(t *testing.T) {
			value, err := decodeWriteValue(varType, "json", json.RawMessage(`null`))
			if err != nil {
				t.Fatalf("a JSON null was refused: %v", err)
			}
			if value != nil {
				t.Errorf("a JSON null decoded as %#v, want nothing", value)
			}
			for _, text := range []string{`0`, `""`, `false`, `"null"`, `[]`, `{}`} {
				if _, err := decodeWriteValue(varType, "json", json.RawMessage(text)); err == nil {
					t.Errorf("%s was accepted as an empty value", text)
				}
			}
		})
	}
}

// A boolean is the JSON true or false, not a number and not a quoted word.
func TestABooleanWriteValueIsNotANumber(t *testing.T) {
	for _, testCase := range []struct {
		text     string
		want     any
		accepted bool
	}{
		{`true`, true, true},
		{`false`, false, true},
		{`1`, nil, false},
		{`0`, nil, false},
		{`"true"`, nil, false},
		{`null`, nil, false},
	} {
		t.Run(testCase.text, func(t *testing.T) {
			value, err := decodeWriteValue(opcda.VTBool, "json", json.RawMessage(testCase.text))
			if accepted := err == nil; accepted != testCase.accepted {
				t.Fatalf("%s: accepted = %v, want %v (%v)", testCase.text, accepted, testCase.accepted, err)
			}
			if testCase.accepted && value != testCase.want {
				t.Errorf("%s decoded as %#v, want %#v", testCase.text, value, testCase.want)
			}
		})
	}
}
