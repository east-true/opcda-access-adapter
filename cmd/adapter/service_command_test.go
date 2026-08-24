package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestServiceCommandsDispatchValidatedOperations(t *testing.T) {
	var installed, dispatched serviceInstallOptions
	var uninstalled string
	dependencies := serviceCommandDependencies{
		installAndStart: func(options serviceInstallOptions) error { installed = options; return nil },
		uninstall:       func(name string) error { uninstalled = name; return nil },
		runDispatcher:   func(options serviceInstallOptions) error { dispatched = options; return nil },
	}
	var output, errorOutput bytes.Buffer
	if code := runServiceCommand([]string{"install", "--config", "a.json", "--name", "Adapter_A"}, &output, &errorOutput, dependencies); code != 0 {
		t.Fatalf("install exit=%d stderr=%s", code, errorOutput.String())
	}
	if installed != (serviceInstallOptions{Name: "Adapter_A", ConfigPath: "a.json"}) {
		t.Fatalf("installed=%+v", installed)
	}
	if code := runServiceCommand([]string{"run", "--config", "a.json", "--name", "Adapter_A"}, &output, &errorOutput, dependencies); code != 0 {
		t.Fatalf("run exit=%d", code)
	}
	if dispatched != installed {
		t.Fatalf("dispatched=%+v", dispatched)
	}
	if code := runServiceCommand([]string{"uninstall", "--name", "Adapter_A"}, &output, &errorOutput, dependencies); code != 0 {
		t.Fatalf("uninstall exit=%d", code)
	}
	if uninstalled != "Adapter_A" {
		t.Fatalf("uninstalled=%q", uninstalled)
	}
}

func TestServiceCommandsRejectInvalidInputAndReportFailure(t *testing.T) {
	calls := 0
	dependencies := serviceCommandDependencies{
		installAndStart: func(serviceInstallOptions) error { calls++; return errors.New("denied") },
		uninstall:       func(string) error { calls++; return errors.New("denied") },
		runDispatcher:   func(serviceInstallOptions) error { calls++; return errors.New("denied") },
	}
	tests := [][]string{
		nil,
		{"unknown"},
		{"install"},
		{"install", "--config", "a", "--name", "bad name"},
		{"run", "--config", "a", "extra"},
		{"uninstall", "extra"},
	}
	for _, arguments := range tests {
		var output, errorOutput bytes.Buffer
		if code := runServiceCommand(arguments, &output, &errorOutput, dependencies); code != 2 {
			t.Fatalf("arguments=%v exit=%d", arguments, code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid input caused %d service calls", calls)
	}
	var output, errorOutput bytes.Buffer
	if code := runServiceCommand([]string{"install", "--config", "a"}, &output, &errorOutput, dependencies); code != 1 || !strings.Contains(errorOutput.String(), "denied") {
		t.Fatalf("failure exit=%d stderr=%s", code, errorOutput.String())
	}
}

func TestServiceCommandHelpDoesNotExecuteOperation(t *testing.T) {
	calls := 0
	dependencies := serviceCommandDependencies{installAndStart: func(serviceInstallOptions) error { calls++; return nil }}
	var output, errorOutput bytes.Buffer
	if code := runServiceCommand([]string{"install", "--help"}, &output, &errorOutput, dependencies); code != 0 || calls != 0 {
		t.Fatalf("exit=%d calls=%d", code, calls)
	}
}
