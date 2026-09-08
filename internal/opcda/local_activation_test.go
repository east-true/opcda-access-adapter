package opcda

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// INV-8 says remote DCOM is not this project's responsibility, and
// docs/security-windows.md makes the operator a precise promise about how that
// is achieved: the runtime "calls CoCreateInstance with only
// CLSCTX_INPROC_SERVER | CLSCTX_LOCAL_SERVER" and "never requests
// CLSCTX_REMOTE_SERVER or uses CoCreateInstanceEx".
//
// That promise is what lets an operator reason about the DCOM Launch and
// Activation ACLs that apply, so it is a security claim rather than a
// stylistic one -- and nothing enforced it. A single added flag, or a switch to
// CoCreateInstanceEx for its COSERVERINFO parameter, would have made the
// document wrong without failing anything.
//
// This checks the promise as written: the two permitted context bits are the
// only ones this package declares, every activation passes exactly them, and
// the Ex form is never bound.
func TestActivationNeverLeavesTheLocalMachine(t *testing.T) {
	source := packageSource(t)

	// CLSCTX_REMOTE_SERVER is 0x10. A constant for it has no honest use here,
	// so its absence is checked by value as well as by name: a differently
	// named constant would pass a check that only read names.
	for _, match := range regexp.MustCompile(`(?m)^\s*(clsctx[A-Za-z]*)\s*=\s*(0x[0-9A-Fa-f]+|\d+)`).
		FindAllStringSubmatch(source, -1) {
		if match[2] == "0x10" || match[2] == "16" {
			t.Errorf("%s = %s is CLSCTX_REMOTE_SERVER, which this package promises never to request",
				match[1], match[2])
		}
	}
	if strings.Contains(source, "CoCreateInstanceEx") {
		t.Error("CoCreateInstanceEx is bound here; it carries a COSERVERINFO and " +
			"docs/security-windows.md promises the adapter never uses it")
	}

	// Every activation passes the same two bits. The call is through a syscall
	// proc rather than a typed API, so the context is the third argument and is
	// matched positionally.
	call := regexp.MustCompile(`procCoCreateInstance\.Call\(\s*[^\n]*\n\s*[^\n]*\n\s*([^,\n]+),`)
	activations := call.FindAllStringSubmatch(source, -1)
	if len(activations) == 0 {
		t.Fatal("no CoCreateInstance call was found; this test names that call")
	}
	for _, activation := range activations {
		context := strings.TrimSpace(activation[1])
		switch context {
		case "clsctxInprocServer|clsctxLocalServer", "clsctxInprocServer | clsctxLocalServer":
			// The runtime activating the configured source.
		case "clsctxInprocServer":
			// Detection, which activates only the in-process categories manager
			// and no vendor class at all.
		default:
			t.Errorf("CoCreateInstance is called with %q; the permitted contexts are "+
				"clsctxInprocServer|clsctxLocalServer for the source and "+
				"clsctxInprocServer for detection", context)
		}
	}
}

// packageSource is every non-test Go file in this package, including the
// Windows-only ones. Reading them as text rather than compiling them is what
// lets this run on any GOOS: the promise is about what the code says, and the
// file that says it is only built for Windows.
func packageSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	source := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		source += strings.ReplaceAll(string(body), "\r\n", "\n")
	}
	return source
}
