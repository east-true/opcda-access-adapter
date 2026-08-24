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

func TestRuntimeLimitsRejectUnsafeAggregateBudgets(t *testing.T) {
	if err := DefaultLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("defaults must remain valid: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Limits)
	}{
		{name: "Read BSTRs", mutate: func(limits *Limits) { limits.MaxReadItems = 129 }},
		{name: "Write BSTRs", mutate: func(limits *Limits) { limits.MaxWriteItems = 129 }},
		{name: "Browse BSTRs", mutate: func(limits *Limits) { limits.MaxBrowseEntries = 1025 }},
		{name: "Read ItemIDs", mutate: func(limits *Limits) {
			limits.MaxBSTRCodeUnits = 1
			limits.MaxReadItems = 1025
			limits.MaxItemIDBytes = 65536
		}},
		{name: "registration ItemIDs", mutate: func(limits *Limits) {
			limits.MaxBSTRCodeUnits = 1
			limits.MaxRegisteredItems = 131073
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("aggregate runtime budget above hard ceiling was accepted")
			}
		})
	}
}
