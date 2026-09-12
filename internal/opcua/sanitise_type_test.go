package opcua

import (
	"strings"
	"testing"
)

// A message type is the first three bytes off the wire, before anything has
// been validated, so its contents are entirely the peer's choice. When the type
// is not one this server accepts, those bytes go into an error message --
// sanitiseType exists so that what reaches a log line is printable ASCII and
// nothing else.
//
// It had no test. A mutation sweep found the guard silently removable:
// replacing the `||` in `character < 0x20 || character > 0x7E` with `&&` makes
// the condition unsatisfiable, because no byte is both below 0x20 and above
// 0x7E, so every byte passes through untouched. The function still compiled,
// still ran, still returned a string, and the whole suite passed.
//
// The boundaries are checked too. 0x20 and 0x7E are the first and last
// printable characters and must survive; 0x1F and 0x7F are their neighbours and
// must not. Those cases are what stop the comparison being loosened by one.
func TestSanitiseTypeReplacesEveryNonPrintableByte(t *testing.T) {
	for _, testCase := range []struct {
		name string
		in   MessageType
		want string
	}{
		{"printable ASCII is kept", MessageType{'H', 'E', 'L'}, "HEL"},
		{"NUL bytes are replaced", MessageType{0x00, 0x00, 0x00}, "???"},
		{"control bytes are replaced", MessageType{0x01, 0x0A, 0x1B}, "???"},
		{"high bytes are replaced", MessageType{0x80, 0xFE, 0xFF}, "???"},
		{"a mix keeps only the printable byte", MessageType{0x00, 'A', 0xFF}, "?A?"},
		// The boundaries themselves.
		{"0x1F is below printable", MessageType{0x1F, 'A', 'B'}, "?AB"},
		{"0x20 is printable", MessageType{0x20, 'A', 'B'}, " AB"},
		{"0x7E is printable", MessageType{0x7E, 'A', 'B'}, "~AB"},
		{"0x7F is above printable", MessageType{0x7F, 'A', 'B'}, "?AB"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := sanitiseType(testCase.in); got != testCase.want {
				t.Errorf("sanitiseType(%v) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

// The guard is only worth having if the real decode path uses it, so this goes
// through DecodeMessageHeader with bytes a peer could actually send. An escape
// sequence is the case that motivates the function: it is exactly what a log
// reader's terminal would act on.
func TestAnUnknownMessageTypeReachesNoLogLineAsRawBytes(t *testing.T) {
	// An ANSI escape followed by bytes that would colour a terminal, then a
	// size and chunk that are otherwise well formed.
	header := []byte{0x1B, '[', 0x07, 'F', 0x08, 0x00, 0x00, 0x00}
	_, err := DecodeMessageHeader(header, 65536)
	if err == nil {
		t.Fatal("an undefined message type was accepted")
	}
	message := err.Error()
	for _, raw := range []byte{0x1B, 0x07} {
		if strings.ContainsRune(message, rune(raw)) {
			t.Errorf("the error message carries raw byte 0x%02X: %q", raw, message)
		}
	}
	if !strings.Contains(message, "?[?") {
		t.Errorf("the message type was not sanitised into the error: %q", message)
	}
}
