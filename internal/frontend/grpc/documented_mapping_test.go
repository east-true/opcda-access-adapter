package grpcfrontend

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/grpc/status"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The gRPC reference used to describe this mapping as a "typical" status per
// layer, which named six statuses for the adapter layer and left out the two it
// really produces there: Internal, for a result-identity failure and for
// anything unclassified, and Canceled. A client switching on the status had no
// way to learn either from the document.
//
// It is now a per-code table, written from what mapOperationError answers
// rather than from reading its switch -- the same table for HTTP was
// transcribed by eye first and four rows came out wrong. This keeps it true.
func TestDocumentedGRPCMappingMatchesTheFrontend(t *testing.T) {
	documented := documentedMapping(t)
	if len(documented) < 15 {
		t.Fatalf("only %d codes were found in the mapping table; it moved", len(documented))
	}

	for code, want := range documented {
		got := status.Code(mapOperationError(opcda.NewAdapterError(opcda.ErrorCode(code), "documented")))
		if got.String() != want {
			t.Errorf("%s is documented as %s, and the frontend answers %s", code, want, got)
		}
	}

	// The other direction: a code the switch names has to appear in the table.
	// Codes it does not name arrive as Unavailable, which the table says in the
	// row that carries the default, so they need no row of their own.
	for _, code := range codesTheSwitchNames(t) {
		if _, ok := documented[code]; !ok {
			t.Errorf("mapOperationError maps %s to a specific status, and the table omits it", code)
		}
	}
}

// documentedMapping reads the table, which groups codes under the status they
// arrive as, and flattens it to one entry per code.
func documentedMapping(t *testing.T) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repositoryRootFromGRPCTest(t), "docs", "grpc-api.md"))
	if err != nil {
		t.Fatalf("read the gRPC reference: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Za-z]+)` \\| (.+) \\|$")
	mapping := map[string]string{}
	// A Windows checkout has CRLF line endings, and the row ends at "|\r"
	// rather than at "|". Without this the table reads as empty on Windows and
	// as complete everywhere else -- which is how it got here.
	for _, match := range row.FindAllStringSubmatch(withoutCarriageReturns(body), -1) {
		grpcStatus := match[1]
		// The default row lists codes that reach Unavailable by falling through
		// rather than by being named, so it is not evidence about any of them.
		if strings.Contains(match[2], "anything not named above") {
			continue
		}
		for _, code := range regexp.MustCompile("`([A-Z][A-Z0-9_]+)`").FindAllStringSubmatch(match[2], -1) {
			mapping[code[1]] = grpcStatus
		}
	}
	return mapping
}

// codesTheSwitchNames is every error code mapOperationError treats specially,
// read from its source. A code it stops naming should lose its row with it.
func codesTheSwitchNames(t *testing.T) []string {
	t.Helper()
	root := repositoryRootFromGRPCTest(t)
	body, err := os.ReadFile(filepath.Join(root, "internal", "frontend", "grpc", "server.go"))
	if err != nil {
		t.Fatalf("read the gRPC frontend: %v", err)
	}
	source := string(body)
	start := strings.Index(source, "func mapOperationError(")
	if start < 0 {
		t.Fatal("mapOperationError is gone; this test names it")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("cannot find the end of mapOperationError")
	}
	body2, err := os.ReadFile(filepath.Join(root, "internal", "opcda", "errors.go"))
	if err != nil {
		t.Fatalf("read the error codes: %v", err)
	}
	byIdentifier := map[string]string{}
	declaration := regexp.MustCompile(`(Code[A-Za-z0-9]+)\s+ErrorCode\s*=\s*"([A-Z0-9_]+)"`)
	for _, match := range declaration.FindAllStringSubmatch(string(body2), -1) {
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

func withoutCarriageReturns(body []byte) string {
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

func repositoryRootFromGRPCTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	// internal/frontend/grpc -> three levels up from this file's directory.
	root := filepath.Dir(file)
	for range 3 {
		root = filepath.Dir(root)
	}
	return root
}
