package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func TestGuidedSetupRequiresExplicitSourceFrontendAndServiceChoice(t *testing.T) {
	servers := []opcda.DetectedLocalServer{
		{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", ProgID: "A.Server.1"},
		{CLSID: "{BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB}", ProgID: "B.Server.1"},
	}
	var written app.Config
	var writtenPath string
	var installed serviceInstallOptions
	foregroundCalls := 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return servers, nil
		},
		writeConfig: func(path string, config app.Config) error {
			writtenPath, written = path, config
			return nil
		},
		runForeground: func(string) error {
			foregroundCalls++
			return nil
		},
		installAndStart: func(options serviceInstallOptions) error {
			installed = options
			return nil
		},
	}
	var output, errorOutput bytes.Buffer
	code := runSetup(
		[]string{"--config", "chosen.json", "--listen", "127.0.0.1:18080", "--service-name", "OPCDA_B"},
		strings.NewReader("2\n1\n2\ny\n"),
		&output,
		&errorOutput,
		dependencies,
	)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errorOutput.String())
	}
	if writtenPath != "chosen.json" || written.Source.CLSID != servers[1].CLSID || written.Source.ProgID != "" {
		t.Fatalf("path=%q config=%+v", writtenPath, written)
	}
	if written.HTTPListenAddress != "127.0.0.1:18080" || written.WriteEnabled {
		t.Fatalf("unsafe or incorrect config = %+v", written)
	}
	if installed != (serviceInstallOptions{Name: "OPCDA_B", ConfigPath: "chosen.json"}) || foregroundCalls != 0 {
		t.Fatalf("installed=%+v foreground=%d", installed, foregroundCalls)
	}
	if !strings.Contains(output.String(), "LocalService") || !strings.Contains(output.String(), "DCOM") {
		t.Fatalf("service security review missing: %s", output.String())
	}
}

func TestGuidedSetupDoesNotAutoSelectSingleCandidate(t *testing.T) {
	writes := 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{A}", ProgID: "Only.Server"}}, nil
		},
		writeConfig: func(string, app.Config) error { writes++; return nil },
	}
	var output, errorOutput bytes.Buffer
	code := runSetup(nil, strings.NewReader(""), &output, &errorOutput, dependencies)
	if code != 1 || writes != 0 {
		t.Fatalf("exit=%d writes=%d stderr=%s", code, writes, errorOutput.String())
	}
	if !strings.Contains(output.String(), "Select one source [1-1]") {
		t.Fatalf("single source was not presented for explicit selection: %s", output.String())
	}
}

func TestGuidedSetupSaveOnlyWritesLoadableConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter.json")
	actionCalls := 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", ProgID: "Vendor.Server.1"}}, nil
		},
		writeConfig: app.WriteConfigFileExclusive,
		runForeground: func(string) error {
			actionCalls++
			return nil
		},
		installAndStart: func(serviceInstallOptions) error {
			actionCalls++
			return nil
		},
	}
	var output, errorOutput bytes.Buffer
	code := runSetup([]string{"--config", path, "--enable-write"}, strings.NewReader("1\n1\n3\nyes\n"), &output, &errorOutput, dependencies)
	if code != 0 || actionCalls != 0 {
		t.Fatalf("exit=%d actions=%d stderr=%s", code, actionCalls, errorOutput.String())
	}
	loaded, err := app.LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.WriteEnabled || loaded.Source.CLSID == "" {
		t.Fatalf("loaded config = %+v", loaded)
	}
}

func TestGuidedSetupExplicitGRPCSelectionWritesGRPCConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grpc-adapter.json")
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC}", ProgID: "Vendor.GRPC.1"}}, nil
		},
		writeConfig: app.WriteConfigFileExclusive,
	}
	var output, errorOutput bytes.Buffer
	code := runSetup(
		[]string{"--config", path, "--grpc-listen", "127.0.0.1:55051"},
		strings.NewReader("1\n2\n3\ny\n"),
		&output,
		&errorOutput,
		dependencies,
	)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errorOutput.String())
	}
	loaded, err := app.LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Frontend != app.FrontendGRPC || loaded.GRPCListenAddress != "127.0.0.1:55051" || loaded.WriteEnabled {
		t.Fatalf("loaded config = %+v", loaded)
	}
	if !strings.Contains(output.String(), "frontend: gRPC") {
		t.Fatalf("gRPC review missing: %s", output.String())
	}
}

func TestPowerShellConfigPathQuoteIsLiteral(t *testing.T) {
	if got, want := quotePowerShellLiteral(`C:\Program Files\operator's adapter.json`), `'C:\Program Files\operator''s adapter.json'`; got != want {
		t.Fatalf("quote=%q want=%q", got, want)
	}
}

func TestGuidedSetupCancelHasNoSideEffects(t *testing.T) {
	writes, actions := 0, 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{A}"}}, nil
		},
		writeConfig:     func(string, app.Config) error { writes++; return nil },
		runForeground:   func(string) error { actions++; return nil },
		installAndStart: func(serviceInstallOptions) error { actions++; return nil },
	}
	var output, errorOutput bytes.Buffer
	code := runSetup(nil, strings.NewReader("1\n1\n1\nn\n"), &output, &errorOutput, dependencies)
	if code != 0 || writes != 0 || actions != 0 {
		t.Fatalf("exit=%d writes=%d actions=%d", code, writes, actions)
	}
	if !strings.Contains(output.String(), "no configuration or service was created") {
		t.Fatalf("cancellation output = %s", output.String())
	}
}

func TestGuidedSetupDoesNotFallbackOrRetryAfterServiceFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "adapter.json")
	installs, foreground := 0, 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{A}"}}, nil
		},
		writeConfig:   app.WriteConfigFileExclusive,
		runForeground: func(string) error { foreground++; return nil },
		installAndStart: func(serviceInstallOptions) error {
			installs++
			return errors.New("access denied")
		},
	}
	var output, errorOutput bytes.Buffer
	code := runSetup([]string{"--config", path}, strings.NewReader("1\n1\n2\ny\n"), &output, &errorOutput, dependencies)
	if code != 1 || installs != 1 || foreground != 0 {
		t.Fatalf("exit=%d installs=%d foreground=%d", code, installs, foreground)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reviewed config was not retained: %v", err)
	}
	if !strings.Contains(errorOutput.String(), "no foreground fallback or automatic retry") {
		t.Fatalf("failure contract missing: %s", errorOutput.String())
	}
}

func TestGuidedSetupRejectsEmptyDetectionInvalidFlagsAndBoundedInput(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		return nil, nil
	}
	var output, errorOutput bytes.Buffer
	if code := runSetup(nil, strings.NewReader(""), &output, &errorOutput, guidedSetupDependencies{detect: detector}); code != 1 {
		t.Fatalf("empty detection exit=%d", code)
	}

	called := false
	detector = func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		called = true
		return []opcda.DetectedLocalServer{{CLSID: "{A}"}}, nil
	}
	invalid := [][]string{{"extra"}, {"--timeout", "0s"}, {"--max-results", "5000"}, {"--service-name", "bad name"}, {"--listen", "missing-port"}, {"--grpc-listen", "missing-port"}, {"--config", strings.Repeat("x", 4097)}}
	for _, arguments := range invalid {
		output.Reset()
		errorOutput.Reset()
		if code := runSetup(arguments, strings.NewReader(""), &output, &errorOutput, guidedSetupDependencies{detect: detector}); code != 2 {
			t.Fatalf("arguments=%v exit=%d", arguments, code)
		}
	}
	if called {
		t.Fatal("detector called for invalid setup flags")
	}

	longInput := strings.Repeat("1", maximumPromptLineBytes+1) + "\n"
	output.Reset()
	errorOutput.Reset()
	code := runSetup(nil, strings.NewReader(longInput), &output, &errorOutput, guidedSetupDependencies{detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		return []opcda.DetectedLocalServer{{CLSID: "{A}"}}, nil
	}})
	if code != 1 || !strings.Contains(errorOutput.String(), "exceeds") {
		t.Fatalf("long input exit=%d stderr=%s", code, errorOutput.String())
	}
}

func TestGuidedSetupRefusesExistingConfigBeforeAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter.json")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	actions := 0
	dependencies := guidedSetupDependencies{
		detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
			return []opcda.DetectedLocalServer{{CLSID: "{A}"}}, nil
		},
		writeConfig:     app.WriteConfigFileExclusive,
		runForeground:   func(string) error { actions++; return nil },
		installAndStart: func(serviceInstallOptions) error { actions++; return nil },
	}
	var output, errorOutput bytes.Buffer
	code := runSetup([]string{"--config", path}, strings.NewReader("1\n1\n2\ny\n"), &output, &errorOutput, dependencies)
	if code != 1 || actions != 0 || !strings.Contains(errorOutput.String(), "already exists") {
		t.Fatalf("exit=%d actions=%d stderr=%s", code, actions, errorOutput.String())
	}
}
