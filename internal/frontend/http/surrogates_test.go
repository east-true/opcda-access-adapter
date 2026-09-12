package http

import (
	"strings"
	"testing"
)

// validJSONSurrogates rejects a \uXXXX escape sequence whose UTF-16 surrogates
// do not pair. It runs on request bodies, so what it reads is whatever a client
// sent, and an ItemID that survives it is passed to the DA server byte for byte.
//
// It is fuzzed, which proves it does not crash. The mutation sweep showed that
// nothing proved it decides correctly: seven mutations survived across this
// function and parseHexQuad, including one that removes the low-surrogate range
// check entirely. `low < 0xDC00 || low > 0xDFFF` becomes unsatisfiable when the
// `||` turns into `&&` -- no value is both below 0xDC00 and above 0xDFFF -- so
// a high surrogate followed by any escape at all would have been accepted.
//
// Every body below is a raw string holding the literal characters backslash-u,
// not the code point they denote. Writing the code point instead produces a
// body with no escape in it, which this function passes without looking at
// anything -- a test that would report success for every mutation. The first
// case guards against that by construction.
func TestSurrogateValidationAcceptsOnlyWellFormedPairs(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"no escapes at all", `"Channel1.Device1"`, true},
		{"an escape that is not a surrogate", `"\u0041"`, true},
		{"the last code point below the high range", `"\uD7FF"`, true},
		{"the first code point above the low range", `"\uE000"`, true},

		// Every corner of the valid pair. D800 and DBFF are the first and last
		// high surrogates, DC00 and DFFF the first and last low ones, so a
		// comparison loosened or tightened by one shows up here.
		{"first high with first low", `"\uD800\uDC00"`, true},
		{"first high with last low", `"\uD800\uDFFF"`, true},
		{"last high with first low", `"\uDBFF\uDC00"`, true},
		{"last high with last low", `"\uDBFF\uDFFF"`, true},

		// A high surrogate followed by something that is not a low one. The
		// second and third are what the && mutation lets through.
		{"high surrogate alone", `"\uD800"`, false},
		{"high surrogate then a plain escape", `"\uD800\u0041"`, false},
		{"high surrogate then one below the low range", `"\uD800\uDBFF"`, false},
		{"high surrogate then one above the low range", `"\uD800\uE000"`, false},
		{"high surrogate then a literal character", `"\uD800A!!!!!"`, false},
		{"high surrogate then a non-u escape", `"\uD800\n12345"`, false},
		// The escape after a high surrogate has to be backslash AND u. This
		// one is a backslash followed by something else, and the four bytes
		// after it would parse as a valid low surrogate -- so a check that
		// only rejects when both characters are wrong would accept it, and
		// the later range test would not notice.
		{"high surrogate then a backslash that is not u", `"\uD800\nDC00"`, false},

		// A low surrogate with no high one before it.
		{"first low surrogate alone", `"\uDC00"`, false},
		{"last low surrogate alone", `"\uDFFF"`, false},
		{"low surrogate after a plain escape", `"\u0041\uDC00"`, false},

		// Case is not significant in a hex quad, and the letter digits are
		// where parseHexQuad's ranges live.
		{"lower case hex pair", `"\ud800\udfff"`, true},
		{"mixed case hex pair", `"\uD800\udFFF"`, true},
		{"lower case unpaired low", `"\udc00"`, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Without a literal escape there is nothing for the function to
			// decide, and the case would pass under every mutation.
			if testCase.name != "no escapes at all" && !strings.Contains(testCase.body, `\u`) {
				t.Fatalf("the body %s carries no escape, so this case tests nothing", testCase.body)
			}
			if got := validJSONSurrogates([]byte(testCase.body)); got != testCase.valid {
				t.Errorf("validJSONSurrogates(%s) = %v, want %v", testCase.body, got, testCase.valid)
			}
		})
	}
}

// index+12 > len(data) is the read-ahead guard for the partner escape, and its
// boundary is a body that ends exactly where the pair ends. Well-formed JSON
// always has a closing quote after it, so this is reached through the
// function's own contract rather than through a request -- but the guard is
// what keeps the read in bounds, and a test that never sits on it leaves the
// comparison free to move.
func TestSurrogatePairEndingExactlyAtTheEndIsAccepted(t *testing.T) {
	pair := []byte(`\uD800\uDC00`)
	if len(pair) != 12 {
		t.Fatalf("the pair is %d bytes; this test is about the twelfth", len(pair))
	}
	if !validJSONSurrogates(pair) {
		t.Error("a complete pair ending exactly at the end of the data was rejected")
	}
	if validJSONSurrogates(pair[:11]) {
		t.Error("a pair one byte short was accepted")
	}
}

// A truncated escape must not be read past the end of the body. The JSON
// decoder reports malformed syntax on its own, so what is asked here is that
// this function neither reads out of bounds nor accepts a pair it never saw.
func TestSurrogateValidationHandlesTruncation(t *testing.T) {
	const full = `"\uD800\uDC00"`
	const highComplete = len(`"\uD800`)
	const pairComplete = len(`"\uD800\uDC00`)

	for length := 1; length < len(full); length++ {
		truncated := full[:length]
		t.Run(truncated, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("reading %q panicked: %v", truncated, recovered)
				}
			}()
			valid := validJSONSurrogates([]byte(truncated))
			if length >= highComplete && length < pairComplete && valid {
				t.Errorf("a high surrogate with a truncated partner was accepted: %q", truncated)
			}
		})
	}
}

func TestParseHexQuadAcceptsEveryDigitAndNothingElse(t *testing.T) {
	for _, testCase := range []struct {
		in    string
		want  uint16
		valid bool
	}{
		{"0000", 0x0000, true},
		{"9999", 0x9999, true},
		{"aaaa", 0xAAAA, true},
		{"ffff", 0xFFFF, true},
		{"AAAA", 0xAAAA, true},
		{"FFFF", 0xFFFF, true},
		{"0a0A", 0x0A0A, true},
		// The character either side of each accepted range, which is where a
		// comparison loosened by one would start accepting.
		{"000/", 0, false},
		{"000:", 0, false},
		{"000`", 0, false},
		{"000g", 0, false},
		{"000@", 0, false},
		{"000G", 0, false},
		{"abc", 0, false}, // shorter than a quad
	} {
		t.Run(testCase.in, func(t *testing.T) {
			got, ok := parseHexQuad([]byte(testCase.in))
			if ok != testCase.valid {
				t.Fatalf("parseHexQuad(%q) ok = %v, want %v", testCase.in, ok, testCase.valid)
			}
			if ok && got != testCase.want {
				t.Errorf("parseHexQuad(%q) = 0x%04X, want 0x%04X", testCase.in, got, testCase.want)
			}
		})
	}
}

// The real path: a body carrying an unpaired surrogate is refused before the
// ItemID inside it reaches the DA layer, and a well-formed pair decodes to the
// code point it denotes rather than to the escape text.
func TestARequestBodyWithAnUnpairedSurrogateIsRefused(t *testing.T) {
	var value exactJSONString
	if err := value.UnmarshalJSON([]byte(`"\uD800"`)); err == nil {
		t.Fatal("an unpaired high surrogate was accepted into an ItemID")
	}
	if err := value.UnmarshalJSON([]byte(`"\uD800\uDC00"`)); err != nil {
		t.Fatalf("a well-formed surrogate pair was refused: %v", err)
	}
	if string(value) != "\U00010000" {
		t.Errorf("the decoded pair is %q, want the code point it encodes", string(value))
	}
}
