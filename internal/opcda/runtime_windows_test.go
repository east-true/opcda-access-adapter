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

func TestConnectionGenerationMonotonicallyInvalidatesHandles(t *testing.T) {
	session := &daThreadSession{}
	session.beginConnectionGeneration(2)
	firstGeneration := session.generation
	if !session.registrations.put(itemRegistration{ItemID: "A", ServerHandle: 10, Generation: firstGeneration}) {
		t.Fatal("failed to register first-generation handle")
	}
	session.disconnect()
	if session.registrations != nil {
		t.Fatal("registration cache survived disconnect")
	}
	session.beginConnectionGeneration(2)
	if session.generation != firstGeneration+1 {
		t.Fatalf("generation = %d, want %d", session.generation, firstGeneration+1)
	}
	if _, ok := session.registrations.get("A"); ok {
		t.Fatal("first-generation handle survived reconnect")
	}
}

func TestWindowsRuntimeQueueBackpressureAndDegradedFailFast(t *testing.T) {
	wake, err := createWakeEvent()
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()
	runtime := &windowsRuntime{
		config:   Config{Limits: DefaultLimits()},
		commands: make(chan daThreadCommand, 1),
		wake:     wake,
		status:   RuntimeStatus{State: RuntimeStateConnected},
	}
	command := daThreadCommand{context: context.Background(), name: "test", run: func(*daThreadSession) {}}
	if err := runtime.enqueue(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	err = runtime.enqueue(context.Background(), command)
	adapterErr, ok := AsAdapterError(err)
	if !ok || adapterErr.Code != CodeQueueFull {
		t.Fatalf("second enqueue error = %v, want %s", err, CodeQueueFull)
	}
	runtime.markDegraded("test degradation")
	err = runtime.enqueue(context.Background(), command)
	adapterErr, ok = AsAdapterError(err)
	if !ok || adapterErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("degraded enqueue error = %v, want %s", err, CodeRuntimeUnavailable)
	}
}

func TestCOMCallWatchdogMarksRuntimeDegraded(t *testing.T) {
	runtime := &windowsRuntime{
		config: Config{COMCallWatchdog: 10 * time.Millisecond},
		status: RuntimeStatus{
			State:        RuntimeStateConnected,
			Capabilities: Capabilities{Browse: "supported", Read: true, Write: true},
		},
	}
	finish := runtime.beginCOMWatchdog("test call")
	defer finish()
	deadline := time.Now().Add(time.Second)
	for !runtime.degraded.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := runtime.Status(context.Background())
	if status.State != RuntimeStateDegraded || status.Capabilities.Read || status.DegradedReason == "" {
		t.Fatalf("watchdog status = %+v", status)
	}
}

func TestScheduleReconnectExposesDisconnectedBackoffState(t *testing.T) {
	runtime := &windowsRuntime{
		config: Config{ReconnectInitial: time.Second, ReconnectMax: 30 * time.Second},
		status: RuntimeStatus{State: RuntimeStateConnected},
	}
	session := &daThreadSession{jitterState: 1}
	runtime.scheduleReconnect(session)
	status := runtime.Status(context.Background())
	if status.State != RuntimeStateDisconnected || session.reconnectAt.IsZero() || session.reconnectAttempt != 1 {
		t.Fatalf("scheduled reconnect = status %+v, session %+v", status, session)
	}
}

func TestSourceFailureDiagnosticPreservesOperationAndHRESULT(t *testing.T) {
	runtime := &windowsRuntime{status: RuntimeStatus{State: RuntimeStateConnecting}}
	runtime.recordSourceFailure("fallback", &comCallError{
		Operation: "CoCreateInstance(IOPCServer)",
		HRESULT:   HRESULT(-2147024891), // 0x80070005 E_ACCESSDENIED
	})

	status := runtime.Status(context.Background())
	if !status.LastSourceErrorSet {
		t.Fatal("source diagnostic was not recorded")
	}
	if got, want := status.LastSourceError.Operation, "CoCreateInstance(IOPCServer)"; got != want {
		t.Fatalf("operation = %q, want %q", got, want)
	}
	if !status.LastSourceError.HRESULTPresent || status.LastSourceError.HRESULT.Hex() != "0x80070005" {
		t.Fatalf("HRESULT diagnostic = %+v", status.LastSourceError)
	}

	runtime.updateStatus(func(status *RuntimeStatus) {
		status.LastSourceError = SourceDiagnostic{}
		status.LastSourceErrorSet = false
	})
	if runtime.Status(context.Background()).LastSourceErrorSet {
		t.Fatal("source diagnostic survived successful-connection reset")
	}
}
