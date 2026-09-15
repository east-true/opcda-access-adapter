package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The review an operator confirms is the last thing shown before a
// configuration is written, and three of its branches were unpinned. What
// separates them is what the operator is told: an OPC UA setup carries the
// warning ADR-0016 requires, and a frontend's own address is what appears
// after the service starts. Choosing the wrong branch would print a status URL
// for a frontend that has none, or leave out a warning the decision record
// says must be there.

type setupRun struct {
	exit    int
	stdout  string
	stderr  string
	written app.Config
	path    string
	failed  bool
}

func runSetupWith(t *testing.T, servers []opcda.DetectedLocalServer, answers string, arguments ...string) setupRun {
	t.Helper()
	result := setupRun{}
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return servers, nil
		},
		writeConfig: func(path string, config app.Config) error {
			result.path, result.written = path, config
			return nil
		},
		runForeground:   func(string) error { return nil },
		installAndStart: func(serviceInstallOptions) error { return nil },
	}
	var output, errorOutput bytes.Buffer
	result.exit = runSetup(arguments, strings.NewReader(answers), &output, &errorOutput, dependencies)
	result.stdout, result.stderr = output.String(), errorOutput.String()
	return result
}

func oneServer() []opcda.DetectedLocalServer {
	return []opcda.DetectedLocalServer{
		{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", ProgID: "Vendor.Server.1"},
	}
}

// ADR-0016 forbids describing SecurityPolicy None as production ready, and the
// guided setup is where an operator decides to use it. The warning belongs to
// the OPC UA branch alone: printing it for HTTP would be untrue, and leaving it
// out of the UA review would be the thing the decision record rules out.
func TestOnlyTheOPCUAReviewCarriesItsSecurityWarning(t *testing.T) {
	opcuaArguments := []string{
		"--config", "adapter.json",
		"--opcua-endpoint-url", "opc.tcp://127.0.0.1:4840",
		"--opcua-application-uri", "urn:example:adapter",
		"--opcua-namespace-uri", "urn:example:adapter",
		"--opcua-security-policy-uri", "http://opcfoundation.org/UA/SecurityPolicy#None",
		"--opcua-transport-profile-uri", "http://opcfoundation.org/UA-Profile/Transport/uatcp-uasc-uabinary",
	}

	// Source 1, frontend 3 (OPC UA), execution 1 (foreground), confirm.
	opcua := runSetupWith(t, oneServer(), "1\n3\n1\ny\n", opcuaArguments...)
	if opcua.exit != 0 {
		t.Fatalf("the OPC UA setup failed: exit=%d %s", opcua.exit, opcua.stderr)
	}
	if opcua.written.Frontend != app.FrontendOPCUA {
		t.Fatalf("the written configuration names the %s frontend", opcua.written.Frontend)
	}
	for _, required := range []string{
		"OPC UA security mode: None",
		"not production ready",
		"OPC UA endpoint: opc.tcp://127.0.0.1:4840",
	} {
		if !strings.Contains(opcua.stdout, required) {
			t.Errorf("the OPC UA review does not say %q:\n%s", required, opcua.stdout)
		}
	}

	// The same flow with the HTTP frontend selected instead.
	httpSetup := runSetupWith(t, oneServer(), "1\n1\n1\ny\n", "--config", "adapter.json")
	if httpSetup.exit != 0 {
		t.Fatalf("the HTTP setup failed: exit=%d %s", httpSetup.exit, httpSetup.stderr)
	}
	for _, absent := range []string{"OPC UA security mode", "OPC UA endpoint"} {
		if strings.Contains(httpSetup.stdout, absent) {
			t.Errorf("the HTTP review says %q, which is not true of it:\n%s", absent, httpSetup.stdout)
		}
	}
}

// After the service starts, the operator is given the address of the frontend
// they chose. An HTTP frontend has a status URL; a gRPC one does not, and
// naming a path on it would send the operator to something that cannot answer.
func TestTheAddressShownAfterInstallBelongsToTheChosenFrontend(t *testing.T) {
	// Source 1, frontend N, execution 2 (install the service), confirm.
	httpSetup := runSetupWith(t, oneServer(), "1\n1\n2\ny\n",
		"--config", "adapter.json", "--listen", "127.0.0.1:18080", "--service-name", "OPCDA_T")
	if httpSetup.exit != 0 {
		t.Fatalf("the HTTP setup failed: exit=%d %s", httpSetup.exit, httpSetup.stderr)
	}
	if !strings.Contains(httpSetup.stdout, "Status: http://127.0.0.1:18080/v1/status") {
		t.Errorf("the HTTP service does not name its status URL:\n%s", httpSetup.stdout)
	}

	grpcSetup := runSetupWith(t, oneServer(), "1\n2\n2\ny\n",
		"--config", "adapter.json", "--grpc-listen", "127.0.0.1:19090", "--service-name", "OPCDA_T")
	if grpcSetup.exit != 0 {
		t.Fatalf("the gRPC setup failed: exit=%d %s", grpcSetup.exit, grpcSetup.stderr)
	}
	if strings.Contains(grpcSetup.stdout, "Status: http://") {
		t.Errorf("the gRPC service was given an HTTP status URL:\n%s", grpcSetup.stdout)
	}
	if !strings.Contains(grpcSetup.stdout, "127.0.0.1:19090") {
		t.Errorf("the gRPC service does not name its own address:\n%s", grpcSetup.stdout)
	}
}

// A registered server need not expose a ProgID, and the list an operator picks
// from says so rather than showing a blank name beside a CLSID.
func TestTheSourceListNamesAServerWithNoProgID(t *testing.T) {
	servers := []opcda.DetectedLocalServer{
		{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", ProgID: "Vendor.Server.1"},
		{CLSID: "{BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB}"},
	}
	// Select the one without a ProgID, so the review shows it too.
	result := runSetupWith(t, servers, "2\n1\n1\ny\n", "--config", "adapter.json")
	if result.exit != 0 {
		t.Fatalf("setup failed: exit=%d %s", result.exit, result.stderr)
	}
	if !strings.Contains(result.stdout, "(ProgID unavailable)") {
		t.Errorf("the list left the missing ProgID blank:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "Vendor.Server.1") {
		t.Errorf("the list dropped the ProgID it does have:\n%s", result.stdout)
	}
	if result.written.Source.CLSID != servers[1].CLSID || result.written.Source.ProgID != "" {
		t.Errorf("the selected source was written as %+v", result.written.Source)
	}
}

// setup's detection timeout carries the same ceiling detect's does, and its
// help says so. An operator who asks for exactly the documented maximum must
// get it rather than an error naming a limit they did not exceed.
func TestSetupAcceptsTheLargestTimeoutItsHelpNames(t *testing.T) {
	for _, testCase := range []struct {
		timeout  string
		accepted bool
	}{
		{"24h", true},
		{"24h1ns", false},
		{"1ns", true},
		{"0s", false},
		{"-1s", false},
	} {
		t.Run(testCase.timeout, func(t *testing.T) {
			result := runSetupWith(t, oneServer(), "1\n1\n1\ny\n",
				"--config", "adapter.json", "--timeout", testCase.timeout)
			if accepted := result.exit == 0; accepted != testCase.accepted {
				t.Errorf("exit = %d (accepted = %v), want accepted = %v: %s",
					result.exit, accepted, testCase.accepted, result.stderr)
			}
		})
	}
}
