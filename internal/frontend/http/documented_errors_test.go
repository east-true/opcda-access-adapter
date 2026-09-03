package http

import (
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The HTTP reference documents every error code the adapter can return and the
// status each carries. Both halves were wrong before this test existed.
//
// Nine of the twenty-nine codes were documented nowhere, and they were not the
// obscure ones: RUNTIME_UNAVAILABLE, INVALID_VALUE and REQUEST_LIMIT_EXCEEDED
// are the three most used codes in the adapter. The documented set had drifted
// towards the transport-hardening codes, which are written once each, while the
// codes a client actually meets went unrecorded.
//
// Then writing the table down by reading the switch by eye put four statuses on
// the wrong row. A client reads this table to decide what to retry, so it is
// checked against the code that answers, in both directions: every code the
// adapter declares has a row, and every row's status is one the frontend really
// produces for that code.
func TestDocumentedErrorCodesMatchTheAdapter(t *testing.T) {
	documented := documentedErrorRows(t)
	if len(documented) < 25 {
		t.Fatalf("only %d error rows were found; the table moved", len(documented))
	}
	produced := statusesTheFrontendProduces(t)

	for _, code := range definedErrorCodes(t) {
		status, ok := documented[code]
		if !ok {
			t.Errorf("%s is an error code the adapter can return, and no row documents it", code)
			continue
		}
		if !produced[code][status] {
			t.Errorf("%s is documented as HTTP %d; the frontend answers %v",
				code, status, sortedStatuses(produced[code]))
		}
	}
}

// statusesTheFrontendProduces is every status each code can leave the frontend
// with. A code reaches a client three ways, and all three are accounted for:
//
//   - through writeOperationError, which maps an adapter error onto a status
//     and answers 503 for anything it does not name. That one is exercised
//     rather than read, so a change to the switch moves this test with it.
//   - from a handler that writes a literal status beside the code.
//   - inside a requestBodyError, which writeDecodeError always writes as 400.
func statusesTheFrontendProduces(t *testing.T) map[string]map[int]bool {
	t.Helper()
	produced := map[string]map[int]bool{}
	add := func(code string, status int) {
		if produced[code] == nil {
			produced[code] = map[int]bool{}
		}
		produced[code][status] = true
	}

	codes := definedErrorCodes(t)
	byIdentifier := errorCodeIdentifiers(t)
	for _, code := range codes {
		recorder := httptest.NewRecorder()
		writeOperationError(recorder, opcda.NewAdapterError(opcda.ErrorCode(code), "documented"))
		add(code, recorder.Code)
	}

	source := frontendSource(t)
	// server.go imports net/http as http and the rest as stdhttp, so both
	// spellings have to be recognised; matching one silently drops five codes.
	literal := regexp.MustCompile(`(?:stdhttp|http)\.Status([A-Za-z]+),(?:\s*"[a-z]+",)?\s*opcda\.(Code[A-Za-z0-9]+)`)
	for _, match := range literal.FindAllStringSubmatch(source, -1) {
		if code, ok := byIdentifier[match[2]]; ok {
			add(code, statusFromName(t, match[1]))
		}
	}
	body := regexp.MustCompile(`code:\s*opcda\.(Code[A-Za-z0-9]+)`)
	for _, match := range body.FindAllStringSubmatch(source, -1) {
		if code, ok := byIdentifier[match[1]]; ok {
			add(code, 400)
		}
	}
	return produced
}

// statusFromName turns the Go constant's name back into its number, so the scan
// above does not carry a hand-written table of the two that would then drift.
func statusFromName(t *testing.T, name string) int {
	t.Helper()
	for status := 100; status < 600; status++ {
		if spaceless(httpStatusText(status)) == name {
			return status
		}
	}
	t.Fatalf("no HTTP status is named %q", name)
	return 0
}

func documentedErrorRows(t *testing.T) map[string]int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repositoryRootFromTest(t), "docs", "http-api.md"))
	if err != nil {
		t.Fatalf("read the HTTP reference: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Z][A-Z0-9_]+)` \\| (\\d{3}) \\|")
	rows := map[string]int{}
	// CRLF, for the same reason the gRPC table reader normalises it: a row
	// anchored to the end of a line finds nothing in a Windows checkout.
	for _, match := range row.FindAllStringSubmatch(strings.ReplaceAll(string(body), "\r\n", "\n"), -1) {
		status, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("row %s has an unreadable status %q", match[1], match[2])
		}
		rows[match[1]] = status
	}
	return rows
}

// definedErrorCodes and errorCodeIdentifiers read the declarations themselves,
// rather than a second list that would drift from the first.
func definedErrorCodes(t *testing.T) []string {
	t.Helper()
	var codes []string
	for _, match := range errorCodeDeclarations(t) {
		codes = append(codes, match[2])
	}
	return codes
}

func errorCodeIdentifiers(t *testing.T) map[string]string {
	t.Helper()
	byIdentifier := map[string]string{}
	for _, match := range errorCodeDeclarations(t) {
		byIdentifier[match[1]] = match[2]
	}
	return byIdentifier
}

func errorCodeDeclarations(t *testing.T) [][]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repositoryRootFromTest(t), "internal", "opcda", "errors.go"))
	if err != nil {
		t.Fatalf("read the error codes: %v", err)
	}
	declaration := regexp.MustCompile(`(Code[A-Za-z0-9]+)\s+ErrorCode\s*=\s*"([A-Z0-9_]+)"`)
	return declaration.FindAllStringSubmatch(string(body), -1)
}

func frontendSource(t *testing.T) string {
	t.Helper()
	directory := filepath.Dir(testFile(t))
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the frontend package: %v", err)
	}
	source := ""
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && filepath.Ext(name) == ".go" && !isTestFile(name) {
			body, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			source += string(body)
		}
	}
	return source
}

func isTestFile(name string) bool {
	return len(name) > len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go"
}

func testFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	return file
}

func repositoryRootFromTest(t *testing.T) string {
	t.Helper()
	// internal/frontend/http -> three levels up from this file's directory.
	root := filepath.Dir(testFile(t))
	for range 3 {
		root = filepath.Dir(root)
	}
	return root
}

func sortedStatuses(statuses map[int]bool) []int {
	var out []int
	for status := 100; status < 600; status++ {
		if statuses[status] {
			out = append(out, status)
		}
	}
	return out
}

func spaceless(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if r != ' ' && r != '-' {
			out = append(out, r)
		}
	}
	return string(out)
}

func httpStatusText(status int) string {
	return stdhttp.StatusText(status)
}
