package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A configuration file names one frontend, and setup.md says each version
// "keeps the selected frontend unambiguous": an HTTP config has only
// httpListen, a gRPC config only grpcListen, an OPC UA config only opcuaListen
// plus its opcua object, and "a non-UA frontend may not carry OPC UA
// settings."
//
// All of that is enforced, and none of it was tested from the file side. The
// listen-address rules live in each frontend's own arm of a switch and the OPC
// UA rule lives after it, so nothing showed the three agreeing -- and the two
// mutations that removed an arm survived. What the rules are for is an
// operator who set opcuaListen and forgot to change the type: INV-10 says that
// fails explicitly rather than running an HTTP adapter that never says so.

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const testSource = `"source":{"clsid":"{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"}`

func TestAFrontendMayNotCarryAnotherFrontendsSettings(t *testing.T) {
	// The control: each frontend loads when it carries only its own settings,
	// or the refusals below would prove nothing.
	for _, testCase := range []struct{ name, frontend string }{
		{"HTTP", `{"type":"http","httpListen":"127.0.0.1:8080"}`},
		{"gRPC", `{"type":"grpc","grpcListen":"127.0.0.1:50051"}`},
	} {
		t.Run("only its own/"+testCase.name, func(t *testing.T) {
			path := writeConfigFile(t, `{"version":3,`+testSource+`,"frontend":`+testCase.frontend+`}`)
			if _, err := LoadConfigFile(path); err != nil {
				t.Fatalf("a config carrying only its own settings was refused: %v", err)
			}
		})
	}

	for _, testCase := range []struct {
		name     string
		frontend string
	}{
		{"HTTP with an OPC UA listen address",
			`{"type":"http","httpListen":"127.0.0.1:8080","opcuaListen":"127.0.0.1:4840"}`},
		{"HTTP with an OPC UA endpoint object",
			`{"type":"http","httpListen":"127.0.0.1:8080","opcua":{"endpointUrl":"opc.tcp://127.0.0.1:4840"}}`},
		{"gRPC with an OPC UA listen address",
			`{"type":"grpc","grpcListen":"127.0.0.1:50051","opcuaListen":"127.0.0.1:4840"}`},
		{"gRPC with an OPC UA endpoint object",
			`{"type":"grpc","grpcListen":"127.0.0.1:50051","opcua":{"endpointUrl":"opc.tcp://127.0.0.1:4840"}}`},
		{"HTTP with a gRPC listen address",
			`{"type":"http","httpListen":"127.0.0.1:8080","grpcListen":"127.0.0.1:50051"}`},
		{"gRPC with an HTTP listen address",
			`{"type":"grpc","grpcListen":"127.0.0.1:50051","httpListen":"127.0.0.1:8080"}`},
		{"OPC UA with an HTTP listen address",
			`{"type":"opcua","opcuaListen":"127.0.0.1:4840","httpListen":"127.0.0.1:8080"}`},
		{"OPC UA with a gRPC listen address",
			`{"type":"opcua","opcuaListen":"127.0.0.1:4840","grpcListen":"127.0.0.1:50051"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeConfigFile(t, `{"version":3,`+testSource+`,"frontend":`+testCase.frontend+`}`)
			if _, err := LoadConfigFile(path); err == nil {
				t.Error("a config naming one frontend and carrying another's settings was accepted")
			}
		})
	}
}

// The scanner that runs before the decoder bounds how deeply a configuration
// may nest, and the bound survived being tightened by one. No valid
// configuration nests as deeply as the limit allows, so a document at the
// limit is refused either way -- for its unknown fields rather than for its
// depth. The reason is what separates a correct bound from one short of it, so
// these cases assert which rule answered rather than only that one did.
//
// Depth is counted the way the scanner counts it: the outermost value is zero,
// and each value inside a container is one deeper than the container.
func TestTheNestingLimitIsTheDepthItSaysItIs(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		body       string
		forNesting bool
	}{
		{"objects to exactly the limit", `{"a":{"b":{"c":{"d":1}}}}`, false},
		{"objects one level past it", `{"a":{"b":{"c":{"d":{"e":1}}}}}`, true},
		// Arrays recurse through their own arm of the scanner, so they need
		// their own pair: a scan that stopped at an array's first clean element
		// would never reach the depth that refuses this one.
		{"arrays to exactly the limit", `{"a":[[[1]]]}`, false},
		{"arrays one level past it", `{"a":[[[[1]]]]}`, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := LoadConfigFile(writeConfigFile(t, testCase.body))
			if err == nil {
				t.Fatal("a configuration of unknown fields was accepted")
			}
			if forNesting := strings.Contains(err.Error(), "nesting"); forNesting != testCase.forNesting {
				t.Errorf("refused as %v; refused for nesting = %v, want %v",
					err, forNesting, testCase.forNesting)
			}
		})
	}
}

// The scanner walks the whole document before the decoder sees it, and the
// walk has two shapes -- an object's values and an array's elements. Both
// recurse, and a scan that stopped at the first child that came back clean
// would leave everything after it unexamined. A malformed field buried inside
// a container is what tells the two apart.
func TestTheScannerReachesWhatIsBuriedInsideAContainer(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{"a duplicate field at the top level",
			`{"version":3,"version":3}`},
		{"a duplicate field inside an object",
			`{"version":3,"frontend":{"type":"http","type":"grpc"}}`},
		{"a duplicate field inside an object inside an object",
			`{"version":3,"frontend":{"opcua":{"endpointUrl":"a","endpointUrl":"b"}}}`},
		{"a duplicate field inside an array element",
			`{"version":3,"unknown":[{"a":1,"a":2}]}`},
		{"a non-string object key is impossible, but a bad token inside one is caught",
			`{"version":3,"frontend":{"type":}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := LoadConfigFile(writeConfigFile(t, testCase.body)); err == nil {
				t.Error("a configuration with a malformed field inside a container was accepted")
			}
		})
	}
}

// A configuration file holds exactly one JSON value. A second one following it
// is not trailing whitespace but a second configuration, and which of the two
// would take effect is not something a file should be able to leave open.
func TestAConfigurationFileHoldsExactlyOneValue(t *testing.T) {
	single := `{"version":3,` + testSource + `,"frontend":{"type":"http","httpListen":"127.0.0.1:8080"}}`
	if _, err := LoadConfigFile(writeConfigFile(t, single)); err != nil {
		t.Fatalf("a single value was refused, so the cases below prove nothing: %v", err)
	}
	// Trailing whitespace is not a second value.
	if _, err := LoadConfigFile(writeConfigFile(t, single+"\n\n")); err != nil {
		t.Errorf("trailing whitespace was refused: %v", err)
	}
	for _, trailing := range []string{single, "{}", "null", "1", "[]"} {
		if _, err := LoadConfigFile(writeConfigFile(t, single+"\n"+trailing)); err == nil {
			t.Errorf("a file carrying a second JSON value (%s) was accepted", trailing)
		}
	}
}

// Three survivors in config_file.go are equivalent mutants rather than gaps,
// checked by mutating and running rather than by reading:
//
//   - The closing-delimiter check at the end of scanConfigJSONValue cannot
//     fire. encoding/json returns a matched closing delimiter for every
//     container it opened, so skipping the check changes nothing a document
//     can reach.
//   - The trailing-token checks in validateConfigJSONStructure and in
//     requireJSONEOF back each other up: a file carrying a second JSON value
//     is refused by whichever still runs, and only the wording differs. Which
//     of the two accurate messages appears is not a contract worth pinning.
//
// The cleanup that removes a half-written file when WriteConfigFileExclusive
// fails is a fourth, and it is not equivalent -- it is untested because
// reaching it needs the encode to fail after the file exists, and the package
// offers no seam for that.
