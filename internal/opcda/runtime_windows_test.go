//go:build windows

package opcda

import (
	"context"
	"testing"
	"time"
)

func TestWindowsRuntimeRepeatedStartStopWithoutSource(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		runtime, err := New(Config{Limits: DefaultLimits()})
		if err != nil {
			t.Fatalf("iteration %d: New() error = %v", iteration, err)
		}
		if state := runtime.Status(context.Background()).State; state != RuntimeStateNotConfigured {
			t.Fatalf("iteration %d: state = %q, want %q", iteration, state, RuntimeStateNotConfigured)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = runtime.Shutdown(ctx)
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: Shutdown() error = %v", iteration, err)
		}
		if state := runtime.Status(context.Background()).State; state != RuntimeStateStopped {
			t.Fatalf("iteration %d: final state = %q, want %q", iteration, state, RuntimeStateStopped)
		}
	}
}

func TestWindowsRuntimeRejectsInvalidFoundationConfig(t *testing.T) {
	tests := []Config{
		{Limits: Limits{}},
		{
			Source: SourceConfig{ProgID: "Vendor.Server", CLSID: "{00000000-0000-0000-0000-000000000000}"},
			Limits: DefaultLimits(),
		},
	}
	for _, config := range tests {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) unexpectedly succeeded", config)
		}
	}
}

func TestIOPCServerIIDMatchesOfficialIDL(t *testing.T) {
	if got, want := iidIOPCServer.String(), "{39C13A4D-011E-11D0-9675-0020AFD8ADB3}"; got != want {
		t.Fatalf("IOPCServer IID = %s, want %s", got, want)
	}
}
