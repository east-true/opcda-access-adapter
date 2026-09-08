package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// INV-1 allows exactly one source protocol, OPC DA, and names the ones that
// would break the project's scope: Modbus, S7, BACnet, EtherNet/IP, an MQTT or
// OPC UA or REST or database source. Design §5.2 separately forbids an existing
// OPC DA or OPC UA implementation library as a core dependency, which is why
// both the DA client and the UA server here are hand-written.
//
// Both are claims about what this module depends on, and nothing checked either.
// A denylist naming Modbus and S7 would not help: the dependency that breaks
// INV-1 is the one nobody thought to name. So this is an allowlist. Every
// module the build requires is listed here with the reason it is allowed, and
// anything else fails until somebody decides it belongs.
//
// That makes a new dependency a reviewed event rather than a silent one, which
// is the same treatment scripts/spec-check/check.py gives an upstream schema
// change.
func TestModuleDependsOnOnlyTheReviewedSet(t *testing.T) {
	reviewed := map[string]string{
		"golang.org/x/sys":           "syscall access for the Windows COM and service work",
		"google.golang.org/grpc":     "the gRPC frontend's transport",
		"google.golang.org/protobuf": "the generated bindings for the committed schema",
		// Pulled in by grpc rather than chosen here. They are listed so that a
		// change in what grpc drags along is visible too.
		"golang.org/x/net":                          "indirect: grpc's HTTP/2 implementation",
		"golang.org/x/text":                         "indirect: golang.org/x/net's IDNA and normalisation",
		"google.golang.org/genproto/googleapis/rpc": "indirect: grpc's status protobufs",
	}

	required := requiredModules(t)
	if len(required) == 0 {
		t.Fatal("no requirements were parsed out of go.mod; the parser or the file moved")
	}
	for _, module := range required {
		if _, ok := reviewed[module]; !ok {
			t.Errorf("go.mod requires %q, which is not in the reviewed set.\n"+
				"INV-1 allows one source protocol and design §5.2 forbids an OPC DA or "+
				"OPC UA implementation library as a core dependency. If this module is "+
				"legitimate, add it here with the reason it is allowed; that decision is "+
				"the point of this test, not an obstacle to it.", module)
		}
	}
	for module := range reviewed {
		if !contains(required, module) {
			t.Errorf("%q is in the reviewed set and go.mod no longer requires it; "+
				"drop it here so the list stays a description of the build", module)
		}
	}
}

// requiredModules is every module path in go.mod's require blocks, direct and
// indirect alike. The Go toolchain is not consulted: this has to work offline
// and has to read what the committed file says.
func requiredModules(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repositoryRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	var modules []string
	// A require line is a module path and a version, inside a block or on its
	// own, with an optional "// indirect" comment.
	line := regexp.MustCompile(`(?m)^\s*(?:require\s+)?([a-z0-9.\-]+\.[a-z]{2,}/[^\s]+)\s+v[^\s]+`)
	for _, match := range line.FindAllStringSubmatch(strings.ReplaceAll(string(body), "\r\n", "\n"), -1) {
		modules = append(modules, match[1])
	}
	sort.Strings(modules)
	return modules
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
