package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func TestDetectCommandEmitsBoundedLocalRegistrationJSON(t *testing.T) {
	var received opcda.LocalDetectionLimits
	detector := func(_ context.Context, limits opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		received = limits
		return []opcda.DetectedLocalServer{
			{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", ProgID: "Vendor.Server.1"},
			{CLSID: "{BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB}"},
		}, nil
	}
	var output, errorOutput bytes.Buffer
	handled, exitCode := handleUtilityCommand(
		[]string{"detect", "--max-results", "17", "--max-progid-code-units", "80"},
		&output, &errorOutput, detector,
	)
	if !handled || exitCode != 0 {
		t.Fatalf("handled=%t exit=%d stderr=%s", handled, exitCode, errorOutput.String())
	}
	if received.MaxServers != 17 || received.MaxProgIDCodeUnits != 80 {
		t.Fatalf("limits = %+v", received)
	}
	var decoded localDetectionOutput
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Scope != "local" || decoded.Category != "OPC_DA_20" || decoded.CategoryID != opcda.OPCDAServer20CategoryID {
		t.Fatalf("metadata = %+v", decoded)
	}
	if len(decoded.Servers) != 2 || decoded.Servers[0].ProgID != "Vendor.Server.1" || decoded.Servers[1].ProgID != "" {
		t.Fatalf("servers = %+v", decoded.Servers)
	}
}

func TestDetectCommandRepresentsEmptyResultAsArray(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		return nil, nil
	}
	var output, errorOutput bytes.Buffer
	if code := runDetect(nil, &output, &errorOutput, detector); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errorOutput.String())
	}
	if !strings.Contains(output.String(), `"servers": []`) {
		t.Fatalf("empty result = %s", output.String())
	}
}

func TestDetectCommandFailsExplicitly(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		return nil, errors.New("registry unavailable")
	}
	var output, errorOutput bytes.Buffer
	if code := runDetect(nil, &output, &errorOutput, detector); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if output.Len() != 0 || !strings.Contains(errorOutput.String(), "registry unavailable") {
		t.Fatalf("stdout=%q stderr=%q", output.String(), errorOutput.String())
	}
}

func TestDetectCommandPreservesAdapterErrorCode(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		return nil, opcda.NewAdapterError(opcda.CodeDetectionResultLimitExceeded, "too many registrations")
	}
	var output, errorOutput bytes.Buffer
	if code := runDetect(nil, &output, &errorOutput, detector); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(errorOutput.String(), "DETECTION_RESULT_LIMIT_EXCEEDED") {
		t.Fatalf("stderr=%q", errorOutput.String())
	}
}

func TestDetectCommandRejectsInvalidArgumentsWithoutCallingDetector(t *testing.T) {
	calls := 0
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		calls++
		return nil, nil
	}
	tests := [][]string{{"unexpected"}, {"--timeout", "0s"}, {"--max-results", "0"}, {"--max-results", "-1"}, {"--unknown"}}
	for _, arguments := range tests {
		var output, errorOutput bytes.Buffer
		if code := runDetect(arguments, &output, &errorOutput, detector); code != 2 {
			t.Fatalf("arguments=%v exit=%d", arguments, code)
		}
	}
	if calls != 0 {
		t.Fatalf("detector called %d times", calls)
	}
}

func TestUtilityCommandHelpAndNoAutomaticDetection(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		t.Fatal("detector called")
		return nil, nil
	}
	var output, errorOutput bytes.Buffer
	if handled, code := handleUtilityCommand([]string{"--help"}, &output, &errorOutput, detector); !handled || code != 0 {
		t.Fatalf("help handled=%t exit=%d", handled, code)
	}
	if !strings.Contains(output.String(), "detect") {
		t.Fatalf("help = %q", output.String())
	}
	if handled, _ := handleUtilityCommand(nil, &output, &errorOutput, detector); handled {
		t.Fatal("normal adapter startup was treated as a utility command")
	}
}
