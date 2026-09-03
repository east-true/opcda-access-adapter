package opcua

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Part 8 Tables A.4 and A.5 map what the source said, and the mapping document
// binds every row of both. Neither table covers the adapter refusing or failing
// before the source is reached, so that mapping is this project's own -- and it
// was written down nowhere. A UA client meeting Bad_NotConnected or
// Bad_TooManyOperations had no way to learn what produced it.
//
// Writing the table down showed why lists like it are worth having:
// Bad_ServiceUnsupported carried two of the three optional DA 2.05a interfaces
// and not IOPCItemProperties, which fell through to Bad_InternalError. Nothing
// could reach it, so it was a trap for the next path rather than a live defect.
//
// This checks the table against statusForRuntimeError in both directions.
func TestDocumentedRuntimeStatusMappingMatchesTheService(t *testing.T) {
	documented := documentedRuntimeStatuses(t)
	if len(documented) < 10 {
		t.Fatalf("only %d codes were found in the mapping table; it moved", len(documented))
	}

	for code, want := range documented {
		got := statusForRuntimeError(opcda.NewAdapterError(opcda.ErrorCode(code), "documented"))
		if got != want {
			t.Errorf("%s is documented as 0x%08X, and the service answers 0x%08X",
				code, uint32(want), uint32(got))
		}
	}

	// The other direction: a code the switch names has to appear in the table.
	// Codes it does not name reach Bad_InternalError by falling through, which
	// the default row states rather than listing them.
	for _, code := range codesTheRuntimeSwitchNames(t) {
		if _, ok := documented[code]; !ok {
			t.Errorf("statusForRuntimeError maps %s to a specific status, and the table omits it", code)
		}
	}
}

// documentedRuntimeStatuses reads the table, which groups codes under the status
// they arrive as, and flattens it to one entry per code. The status names are
// resolved against the constants rather than a second copy of their values.
func documentedRuntimeStatuses(t *testing.T) map[string]StatusCode {
	t.Helper()
	byName := map[string]StatusCode{
		"Bad_NotConnected":       StatusBadNotConnected,
		"Bad_Timeout":            StatusBadTimeout,
		"Bad_NotWritable":        StatusBadNotWritable,
		"Bad_TooManyOperations":  StatusBadTooManyOperations,
		"Bad_TypeMismatch":       StatusBadTypeMismatch,
		"Bad_DataTypeIdUnknown":  StatusBadDataTypeIDUnknown,
		"Bad_ServiceUnsupported": StatusBadServiceUnsupported,
		"Bad_InternalError":      StatusBadInternalError,
	}
	body := readNormalisedFile(t, repositoryRootFromOPCUATest(t), "docs", "opcua-mapping.md")
	row := regexp.MustCompile("(?m)^\\| `(Bad_[A-Za-z]+)` \\| (.+) \\|$")
	mapping := map[string]StatusCode{}
	for _, match := range row.FindAllStringSubmatch(body, -1) {
		// The default row names no code of its own, and the Part 8 tables above
		// have a different shape, so neither reaches this pattern.
		if strings.Contains(match[2], "anything not named above") {
			continue
		}
		status, ok := byName[match[1]]
		if !ok {
			continue
		}
		for _, code := range regexp.MustCompile("`([A-Z][A-Z0-9_]+)`").FindAllStringSubmatch(match[2], -1) {
			mapping[code[1]] = status
		}
	}
	return mapping
}

// codesTheRuntimeSwitchNames is every error code statusForRuntimeError treats
// specially, read from its source so a code it stops naming loses its row too.
func codesTheRuntimeSwitchNames(t *testing.T) []string {
	t.Helper()
	root := repositoryRootFromOPCUATest(t)
	source := readNormalisedFile(t, root, "internal", "opcua", "readwrite.go")
	start := strings.Index(source, "func statusForRuntimeError(")
	if start < 0 {
		t.Fatal("statusForRuntimeError is gone; this test names it")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of statusForRuntimeError")
	}
	byIdentifier := map[string]string{}
	declaration := regexp.MustCompile(`(Code[A-Za-z0-9]+)\s+ErrorCode\s*=\s*"([A-Z0-9_]+)"`)
	for _, match := range declaration.FindAllStringSubmatch(
		readNormalisedFile(t, root, "internal", "opcda", "errors.go"), -1) {
		byIdentifier[match[1]] = match[2]
	}
	var named []string
	for _, match := range regexp.MustCompile(`case ([^:]+):`).FindAllStringSubmatch(source[start:start+end], -1) {
		for _, identifier := range regexp.MustCompile(`opcda\.(Code[A-Za-z0-9]+)`).FindAllStringSubmatch(match[1], -1) {
			if code, ok := byIdentifier[identifier[1]]; ok {
				named = append(named, code)
			}
		}
	}
	return named
}

// readNormalisedFile is the only way this test reads a file. A Windows checkout
// uses CRLF, and every pattern here -- a table row anchored to end of line, and
// the "\n}\n" that ends a function -- silently matches nothing against it.
func readNormalisedFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

func repositoryRootFromOPCUATest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	// internal/opcua -> two levels up from this file's directory.
	root := filepath.Dir(file)
	for range 2 {
		root = filepath.Dir(root)
	}
	return root
}
