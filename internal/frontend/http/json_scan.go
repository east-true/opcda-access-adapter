package http

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// scanJSONStructure walks a request body and enforces the four rules the
// endpoint decoder cannot: a nesting depth, no duplicate object key, no field
// spelled like a documented one but cased differently, and exactly one value.
//
// It walks the bytes directly rather than driving an encoding/json Decoder.
// The Decoder's Token API returns every key and every scalar as an interface
// value, which allocates one object per token; profiling the Read path found
// this pass taking 39% of a hundred-item request's time and 60% of its
// allocations, to answer four questions that need no decoded values at all.
//
// A hand-written scanner in front of a security boundary is worth only what it
// can be shown to be equivalent to, so the Decoder-driven implementation is
// kept in the test file and the two are held to the same answer over a corpus
// and a fuzz target. That is the whole reason this is safe to have.
func scanJSONStructure(body []byte, maximumDepth int) error {
	scanner := jsonScanner{data: body, maximumDepth: maximumDepth}
	if err := scanner.skipSpace(); err != nil {
		return err
	}
	if err := scanner.value(0); err != nil {
		return err
	}
	if err := scanner.skipSpace(); err != nil {
		return err
	}
	if scanner.position != len(scanner.data) {
		return fmt.Errorf("request body must contain exactly one JSON value")
	}
	return nil
}

type jsonScanner struct {
	data         []byte
	position     int
	maximumDepth int
}

func (s *jsonScanner) syntax() error {
	return fmt.Errorf("invalid JSON at byte %d", s.position)
}

// skipSpace advances over the four bytes JSON calls whitespace. Reaching the
// end is not itself an error: the caller decides whether something was
// expected there.
func (s *jsonScanner) skipSpace() error {
	for s.position < len(s.data) {
		switch s.data[s.position] {
		case ' ', '\t', '\r', '\n':
			s.position++
		default:
			return nil
		}
	}
	return nil
}

func (s *jsonScanner) value(depth int) error {
	if s.position >= len(s.data) {
		return s.syntax()
	}
	switch s.data[s.position] {
	case '{':
		return s.object(depth)
	case '[':
		return s.array(depth)
	case '"':
		_, _, err := s.string()
		return err
	case 't':
		return s.word("true")
	case 'f':
		return s.word("false")
	case 'n':
		return s.word("null")
	default:
		return s.number()
	}
}

// depthExceeded is the answer for a container opened at the limit. It carries
// its own code because an operator raising the limit needs to know that is what
// was hit.
func depthExceeded() error {
	return &requestBodyError{
		code:    opcda.CodeJSONDepthLimitExceeded,
		message: "request JSON exceeds the configured nesting-depth limit",
	}
}

func (s *jsonScanner) object(depth int) error {
	if depth >= s.maximumDepth {
		return depthExceeded()
	}
	s.position++ // the '{'
	keys := newJSONKeySet()
	for {
		if err := s.skipSpace(); err != nil {
			return err
		}
		if s.position >= len(s.data) {
			return s.syntax()
		}
		if s.data[s.position] == '}' {
			// Only an empty object may close here; after a value a comma has
			// already been consumed, and a trailing comma is not JSON.
			if !keys.empty() {
				return s.syntax()
			}
			s.position++
			return nil
		}
		if s.data[s.position] != '"' {
			return s.syntax()
		}
		raw, escaped, err := s.string()
		if err != nil {
			return err
		}
		key, err := jsonKeyValue(raw, escaped)
		if err != nil {
			return err
		}
		if err := checkCanonicalSpelling(key); err != nil {
			return err
		}
		if !keys.add(key) {
			return &requestBodyError{
				code:    opcda.CodeDuplicateJSONField,
				message: "request JSON contains a duplicate object field",
			}
		}
		if err := s.skipSpace(); err != nil {
			return err
		}
		if s.position >= len(s.data) || s.data[s.position] != ':' {
			return s.syntax()
		}
		s.position++
		if err := s.skipSpace(); err != nil {
			return err
		}
		if err := s.value(depth + 1); err != nil {
			return err
		}
		if err := s.skipSpace(); err != nil {
			return err
		}
		if s.position >= len(s.data) {
			return s.syntax()
		}
		switch s.data[s.position] {
		case ',':
			s.position++
		case '}':
			s.position++
			return nil
		default:
			return s.syntax()
		}
	}
}

func (s *jsonScanner) array(depth int) error {
	if depth >= s.maximumDepth {
		return depthExceeded()
	}
	s.position++ // the '['
	elements := 0
	for {
		if err := s.skipSpace(); err != nil {
			return err
		}
		if s.position >= len(s.data) {
			return s.syntax()
		}
		if s.data[s.position] == ']' {
			if elements != 0 {
				return s.syntax()
			}
			s.position++
			return nil
		}
		if err := s.value(depth + 1); err != nil {
			return err
		}
		elements++
		if err := s.skipSpace(); err != nil {
			return err
		}
		if s.position >= len(s.data) {
			return s.syntax()
		}
		switch s.data[s.position] {
		case ',':
			s.position++
		case ']':
			s.position++
			return nil
		default:
			return s.syntax()
		}
	}
}

// string consumes one JSON string and reports the bytes between the quotes and
// whether any of them is an escape. The body is returned rather than the value
// so a caller that does not need the value never pays to build one.
func (s *jsonScanner) string() (raw []byte, escaped bool, err error) {
	s.position++ // the opening quote
	start := s.position
	for s.position < len(s.data) {
		character := s.data[s.position]
		switch {
		case character == '"':
			body := s.data[start:s.position]
			s.position++
			return body, escaped, nil
		case character == '\\':
			escaped = true
			s.position++
			if s.position >= len(s.data) {
				return nil, false, s.syntax()
			}
			switch s.data[s.position] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				s.position++
			case 'u':
				if s.position+4 >= len(s.data) {
					return nil, false, s.syntax()
				}
				for offset := 1; offset <= 4; offset++ {
					if !isHexDigit(s.data[s.position+offset]) {
						return nil, false, s.syntax()
					}
				}
				s.position += 5
			default:
				return nil, false, s.syntax()
			}
		case character < 0x20:
			// A raw control byte is not a JSON string character.
			return nil, false, s.syntax()
		default:
			s.position++
		}
	}
	return nil, false, s.syntax()
}

func (s *jsonScanner) word(literal string) error {
	if s.position+len(literal) > len(s.data) ||
		string(s.data[s.position:s.position+len(literal)]) != literal {
		return s.syntax()
	}
	s.position += len(literal)
	return nil
}

// number consumes a JSON number and holds it to the grammar. The pre-scan does
// not care what the value is; a decoder further on does, and one that arrives
// there malformed has already been refused here.
func (s *jsonScanner) number() error {
	start := s.position
	if s.position < len(s.data) && s.data[s.position] == '-' {
		s.position++
	}
	digits := s.digits()
	if digits == 0 {
		return s.syntax()
	}
	// A leading zero may not be followed by another digit.
	if s.data[start] == '-' {
		start++
	}
	if digits > 1 && s.data[start] == '0' {
		return s.syntax()
	}
	if s.position < len(s.data) && s.data[s.position] == '.' {
		s.position++
		if s.digits() == 0 {
			return s.syntax()
		}
	}
	if s.position < len(s.data) && (s.data[s.position] == 'e' || s.data[s.position] == 'E') {
		s.position++
		if s.position < len(s.data) && (s.data[s.position] == '+' || s.data[s.position] == '-') {
			s.position++
		}
		if s.digits() == 0 {
			return s.syntax()
		}
	}
	return nil
}

func (s *jsonScanner) digits() int {
	start := s.position
	for s.position < len(s.data) && s.data[s.position] >= '0' && s.data[s.position] <= '9' {
		s.position++
	}
	return s.position - start
}

func isHexDigit(character byte) bool {
	return (character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'f') ||
		(character >= 'A' && character <= 'F')
}

// jsonKeyValue is the key as a client meant it. Keys are compared after
// unescaping, so "a" and an escaped spelling of it are the same field and one
// cannot be used to smuggle a duplicate past the other.
//
// A plain key of valid UTF-8 is its own value, and that is the case worth not
// allocating for. Everything else goes to the decoder rather than to a second
// unescaping written here: an escape has surrogate pairs and lone halves to get
// right, and invalid UTF-8 is replaced byte by byte rather than passed through.
// Both are easy to write differently by accident, and a key that decodes
// differently is a duplicate that stops being one.
func jsonKeyValue(raw []byte, escaped bool) (string, error) {
	if !escaped && utf8.Valid(raw) {
		return string(raw), nil
	}
	quoted := make([]byte, 0, len(raw)+2)
	quoted = append(quoted, '"')
	quoted = append(quoted, raw...)
	quoted = append(quoted, '"')
	var decoded string
	if err := json.Unmarshal(quoted, &decoded); err != nil {
		return "", fmt.Errorf("invalid JSON object key")
	}
	return decoded, nil
}

// checkCanonicalSpelling refuses a field spelled like a documented one but
// cased differently. A client that sends "ItemID" has not sent "itemId", and
// silently ignoring it would answer a request the client did not make.
func checkCanonicalSpelling(key string) error {
	for _, canonical := range canonicalRequestFields {
		if key != canonical && strings.EqualFold(key, canonical) {
			return &requestBodyError{
				code:    opcda.CodeInvalidRequest,
				message: "request JSON field names must use the exact documented spelling",
			}
		}
	}
	return nil
}

// jsonKeySet remembers the keys of one object. Most request objects carry a
// handful of fields, so a slice with a linear scan beats a map until there are
// enough keys for the scan to cost more than hashing them.
type jsonKeySet struct {
	small []string
	large map[string]struct{}
}

const jsonKeySetSliceLimit = 16

func newJSONKeySet() jsonKeySet {
	return jsonKeySet{}
}

func (set *jsonKeySet) empty() bool {
	return len(set.small) == 0 && len(set.large) == 0
}

// add records a key and reports whether it was new.
func (set *jsonKeySet) add(key string) bool {
	if set.large != nil {
		if _, exists := set.large[key]; exists {
			return false
		}
		set.large[key] = struct{}{}
		return true
	}
	for _, existing := range set.small {
		if existing == key {
			return false
		}
	}
	if len(set.small) == jsonKeySetSliceLimit {
		set.large = make(map[string]struct{}, jsonKeySetSliceLimit*2)
		for _, existing := range set.small {
			set.large[existing] = struct{}{}
		}
		set.small = nil
		set.large[key] = struct{}{}
		return true
	}
	set.small = append(set.small, key)
	return true
}
