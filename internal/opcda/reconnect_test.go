package opcda

import (
	"testing"
	"time"
)

func TestReconnectDelayIsExponentialJitteredAndBounded(t *testing.T) {
	initial, maximum := time.Second, 30*time.Second
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
	for attempt, expected := range want {
		if got := reconnectDelay(uint32(attempt), initial, maximum, 200); got != expected {
			t.Fatalf("attempt %d = %s, want %s", attempt, got, expected)
		}
	}
	for jitter := uint64(0); jitter < 401; jitter++ {
		got := reconnectDelay(20, initial, maximum, jitter)
		if got <= 0 || got > maximum {
			t.Fatalf("jitter %d produced out-of-bound delay %s", jitter, got)
		}
	}
}

func TestConnectionLossClassificationIsConservative(t *testing.T) {
	for _, hr := range []HRESULT{
		rpcEConnectionTerminated, rpcEServerDied, rpcEServerDiedDNE,
		rpcEDisconnected, coEObjectNotConnected, rpcSServerUnavailable,
		rpcSCallFailed, rpcSCallFailedDNE,
	} {
		if !isConnectionLossHRESULT(hr) {
			t.Fatalf("%s was not classified as connection loss", hr.Hex())
		}
	}
	if isConnectionLossHRESULT(HRESULT(-1073479673)) {
		t.Fatal("OPC item error was classified as connection loss")
	}
	if isConnectionLoss(NewAdapterError(CodeRuntimeUnavailable, "unavailable")) {
		t.Fatal("adapter error was classified as COM connection loss")
	}
}

func TestRuntimeLimitsRejectUnsafeConfiguredCeiling(t *testing.T) {
	limits := DefaultLimits()
	limits.CommandQueue = 4097
	if err := limits.ValidateForConfiguration(); err == nil {
		t.Fatal("command queue above hard ceiling was accepted")
	}
}
