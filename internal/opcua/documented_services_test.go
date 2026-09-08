package opcua

import (
	"regexp"
	"strings"
	"testing"
)

// The mapping document tells a client which OPC 10000-4 services this server
// answers and which it refuses with Bad_ServiceUnsupported. That table is the
// UA frontend's service contract, and the frontend is under active development:
// a service added to the dispatch without a row, or a row for one that stopped
// being dispatched, would leave the contract quietly wrong.
//
// Both directions are checked against listener.go, which is where every service
// is dispatched. Publish is dispatched by an equality test before the switch
// rather than by a case, because it is held rather than answered, so this looks
// for the encoding identifier anywhere in the file rather than for a case.
func TestDocumentedServicesMatchTheDispatch(t *testing.T) {
	answered, refused := documentedServices(t)
	if len(answered) < 15 {
		t.Fatalf("only %d answered services were found; the table moved", len(answered))
	}
	dispatched := dispatchedServices(t)

	for service := range answered {
		if !dispatched[service] {
			t.Errorf("%s is documented as answered, and the listener dispatches no such request", service)
		}
	}
	for service := range dispatched {
		if !answered[service] {
			if refused[service] {
				t.Errorf("%s is documented as not implemented, and the listener dispatches it", service)
				continue
			}
			t.Errorf("the listener dispatches %s, and the service table has no row for it", service)
		}
	}
}

// documentedServices reads the two columns of the service table. A dash means
// the set is empty for that service set, not a service named "-".
func documentedServices(t *testing.T) (map[string]bool, map[string]bool) {
	t.Helper()
	body := readNormalisedFile(t, repositoryRootFromOPCUATest(t), "docs", "opcua-mapping.md")
	start := strings.Index(body, "## Which services this server answers")
	if start < 0 {
		t.Fatal("the service table is gone; this test names its heading")
	}
	end := strings.Index(body[start:], "\n## ")
	if end < 0 {
		t.Fatal("cannot find the end of the service table section")
	}
	section := body[start : start+end]

	answered, refused := map[string]bool{}, map[string]bool{}
	row := regexp.MustCompile(`(?m)^\| 5\.\d+ [A-Za-z]+ \| ([^|]*) \| ([^|]*) \|`)
	name := regexp.MustCompile("`([A-Za-z][A-Za-z0-9]+)`")
	for _, match := range row.FindAllStringSubmatch(section, -1) {
		for _, service := range name.FindAllStringSubmatch(match[1], -1) {
			answered[service[1]] = true
		}
		for _, service := range name.FindAllStringSubmatch(match[2], -1) {
			refused[service[1]] = true
		}
	}
	return answered, refused
}

// dispatchedServices is every service the listener names an encoding identifier
// for, read from its source rather than from a list kept beside it.
func dispatchedServices(t *testing.T) map[string]bool {
	t.Helper()
	source := readNormalisedFile(t, repositoryRootFromOPCUATest(t), "internal", "opcua", "listener.go")
	dispatched := map[string]bool{}
	for _, match := range regexp.MustCompile(`([A-Za-z][A-Za-z0-9]+)RequestEncodingID`).
		FindAllStringSubmatch(source, -1) {
		dispatched[match[1]] = true
	}
	return dispatched
}
