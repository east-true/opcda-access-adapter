package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunConfigCommandRequiresAndUsesConfig(t *testing.T) {
	var path string
	runner := func(value string) error { path = value; return nil }
	var output, errorOutput bytes.Buffer
	if code := runConfigCommand([]string{"--config", "adapter.json"}, &output, &errorOutput, runner); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errorOutput.String())
	}
	if path != "adapter.json" {
		t.Fatalf("path=%q", path)
	}
	if code := runConfigCommand(nil, &output, &errorOutput, runner); code != 2 {
		t.Fatalf("missing config exit=%d", code)
	}
}

func TestRunConfigCommandReportsRunnerFailure(t *testing.T) {
	var output, errorOutput bytes.Buffer
	code := runConfigCommand([]string{"--config", "adapter.json"}, &output, &errorOutput, func(string) error {
		return errors.New("failed")
	})
	if code != 1 || !strings.Contains(errorOutput.String(), "failed") {
		t.Fatalf("exit=%d stderr=%s", code, errorOutput.String())
	}
}
