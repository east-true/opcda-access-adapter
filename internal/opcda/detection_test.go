package opcda

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultLocalDetectionLimitsAreBounded(t *testing.T) {
	limits := DefaultLocalDetectionLimits()
	if limits.MaxServers != 256 || limits.MaxProgIDCodeUnits != 1024 {
		t.Fatalf("detection limits = %+v", limits)
	}
	if err := limits.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDetectedLocalServersSortDeterministically(t *testing.T) {
	servers := []DetectedLocalServer{
		{CLSID: "{C}", ProgID: "Z.Server"},
		{CLSID: "{B}", ProgID: "A.Server"},
		{CLSID: "{A}", ProgID: "A.Server"},
	}
	sortDetectedLocalServers(servers)
	if servers[0].CLSID != "{A}" || servers[1].CLSID != "{B}" || servers[2].CLSID != "{C}" {
		t.Fatalf("servers = %+v", servers)
	}
}

func TestDetectLocalServersRejectsInvalidLimitsBeforePlatformCall(t *testing.T) {
	tests := []LocalDetectionLimits{
		{MaxServers: -1, MaxProgIDCodeUnits: 1},
		{MaxServers: 4097, MaxProgIDCodeUnits: 1},
		{MaxServers: 1, MaxProgIDCodeUnits: -1},
		{MaxServers: 1, MaxProgIDCodeUnits: 65537},
	}
	for _, limits := range tests {
		if _, err := DetectLocalServers(context.Background(), limits); err == nil {
			t.Fatalf("limits %+v were accepted", limits)
		}
	}
}

func TestDetectLocalServersHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DetectLocalServers(ctx, LocalDetectionLimits{})
	if err == nil {
		t.Fatal("cancelled detection succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error %v", err)
	}
}
