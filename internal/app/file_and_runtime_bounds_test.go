package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A deeper sweep of this package drew four more bounds and two lifecycle
// checks. The bounds are the shape the rest of this work has been: the suite
// refused something clearly outside each limit, so nothing said a value of
// exactly the documented size is accepted.

// A configuration file of exactly the documented size loads. The limit exists
// so a file cannot be made large enough to exhaust the process reading it, and
// a limit that refuses the size it names would be one byte smaller than the
// documentation says.
func TestAConfigurationFileMayBeExactlyTheDocumentedSize(t *testing.T) {
	// A valid configuration padded out with whitespace to a chosen length.
	// Whitespace rather than fields, so the size is the only thing that
	// changes between the two cases.
	fileOf := func(t *testing.T, size int) string {
		t.Helper()
		body := `{"version":3,` + testSource +
			`,"frontend":{"type":"http","httpListen":"127.0.0.1:8080"}}`
		if len(body) > size {
			t.Fatalf("the smallest valid configuration is %d bytes, past the %d asked for",
				len(body), size)
		}
		padded := body + strings.Repeat("\n", size-len(body))
		path := filepath.Join(t.TempDir(), "adapter.json")
		if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	if _, err := LoadConfigFile(fileOf(t, MaximumConfigFileBytes)); err != nil {
		t.Errorf("a file of exactly %d bytes was refused: %v", MaximumConfigFileBytes, err)
	}
	_, err := LoadConfigFile(fileOf(t, MaximumConfigFileBytes+1))
	if err == nil {
		t.Fatalf("a file of %d bytes was accepted", MaximumConfigFileBytes+1)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("an oversized file was refused as %v, not for its size", err)
	}
}

// The configuration path has its own length bound, and the same reasoning: a
// path of exactly the documented length is one an operator may use.
func TestAConfigurationPathMayBeExactlyTheDocumentedLength(t *testing.T) {
	// A relative path of a chosen length, built from plain name characters so
	// nothing else about it can be what is refused.
	pathOf := func(length int) string {
		return strings.Repeat("p", length-len(".json")) + ".json"
	}
	if err := ValidateConfigFilePath(pathOf(maximumConfigPathBytes)); err != nil {
		t.Errorf("a path of exactly %d bytes was refused: %v", maximumConfigPathBytes, err)
	}
	err := ValidateConfigFilePath(pathOf(maximumConfigPathBytes + 1))
	if err == nil {
		t.Fatalf("a path of %d bytes was accepted", maximumConfigPathBytes+1)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("an over-long path was refused as %v, not for its length", err)
	}
	if err := ValidateConfigFilePath(""); err == nil {
		t.Error("an empty path was accepted")
	}
}

// The runtime's own bounds are validated through the application
// configuration rather than beside it, and the delegation was unpinned:
// dropping its result would accept a runtime configuration the DA core
// refuses, and the adapter would start with limits nothing had agreed to.
func TestTheRuntimeBoundsAreValidatedThroughTheApplicationConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		adjust func(*Config)
	}{
		{
			name:   "a command queue of no depth",
			adjust: func(c *Config) { c.Runtime.Limits.CommandQueue = -1 },
		},
		{
			name:   "a Read batch of no items",
			adjust: func(c *Config) { c.Runtime.Limits.MaxReadItems = -1 },
		},
		{
			name:   "a watchdog longer than the runtime allows",
			adjust: func(c *Config) { c.Runtime.COMCallWatchdog = 25 * time.Hour },
		},
		{
			name:   "a reconnect maximum longer than the runtime allows",
			adjust: func(c *Config) { c.Runtime.ReconnectMax = 25 * time.Hour },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := DefaultConfig()
			testCase.adjust(&config)
			if err := config.finalizeAndValidate(); err == nil {
				t.Error("a runtime configuration the DA core refuses was accepted")
			}
		})
	}

	// The control: the defaults pass the same delegation, so the cases above
	// are about the runtime bounds rather than about everything being refused.
	base := DefaultConfig()
	if err := base.finalizeAndValidate(); err != nil {
		t.Errorf("the default runtime configuration was refused: %v", err)
	}
	// And the source and Write flag reach the runtime, which is what the
	// delegation is carrying besides the check.
	withSource := DefaultConfig()
	withSource.Source = opcda.SourceConfig{ProgID: "Vendor.Server.1"}
	withSource.WriteEnabled = true
	if err := withSource.finalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if withSource.Runtime.Source.ProgID != "Vendor.Server.1" || !withSource.Runtime.WriteEnabled {
		t.Errorf("the runtime was configured as %+v", withSource.Runtime)
	}
}

// The address-space watch is stopped on the shutdown path, and stopping it
// twice has to be safe: Shutdown may be reached from the signal handler and
// from a listener failure at once, and closing an already-closed channel is a
// panic in a process that is trying to exit cleanly.
func TestStoppingTheAddressSpaceWatchTwiceIsSafe(t *testing.T) {
	config := DefaultConfig()
	config.Frontend = FrontendOPCUA
	config.OPCUAListenAddress = "127.0.0.1:0"
	config.OPCUA = testOPCUAEndpoint()
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	// Start took the OPC UA path, so a watch is running.
	if service.stopWatch == nil {
		t.Fatal("the OPC UA frontend started without an address-space watch")
	}

	service.stopAddressSpaceWatch()
	if service.stopWatch != nil {
		t.Error("the watch was stopped but its channel was kept")
	}
	// The second stop finds nothing to stop and returns rather than closing a
	// closed channel.
	service.stopAddressSpaceWatch()

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil {
		t.Errorf("shutting down after the watch was already stopped failed: %v", err)
	}
}

// One survivor in the watch is a gap rather than an equivalent, and it is
// worth naming. The loop invalidates the OPC UA address space when the DA
// connection generation changes, which is what stops a client reading a space
// the reconnect may have replaced. Observing it needs the populator's browsed
// set, which the listener holds privately and exposes nothing for; the service
// holds a concrete listener rather than an interface, so there is no seam to
// substitute one either. Adding an accessor for the test alone would widen a
// production API to watch a field, so it is written down instead.

// One more survivor in this package is unreachable rather than untested. New
// allocates a DA runtime when the caller passes none, and reports a failure to
// do so -- but finalizeAndValidate has already validated the same runtime
// configuration by the time that line runs, so the allocation cannot fail for
// any configuration that gets there. Both arms answer alike for every input a
// caller can supply.
