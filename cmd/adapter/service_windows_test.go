//go:build windows

package main

import (
	"path/filepath"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func TestResolveServiceFilesUsesAbsoluteValidatedPaths(t *testing.T) {
	config, err := app.GuidedSetupConfig(
		opcda.SourceConfig{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"},
		"127.0.0.1:18080",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adapter.json")
	if err := app.WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}
	resolved, executable, err := resolveServiceFiles(serviceInstallOptions{Name: "OPCDA_Test", ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(resolved.ConfigPath) || !filepath.IsAbs(executable) || resolved.Name != "OPCDA_Test" {
		t.Fatalf("resolved=%+v executable=%q", resolved, executable)
	}
}

func TestResolveServiceFilesRejectsInvalidNameAndConfig(t *testing.T) {
	if _, _, err := resolveServiceFiles(serviceInstallOptions{Name: "bad name", ConfigPath: "missing"}); err == nil {
		t.Fatal("invalid service name was accepted")
	}
	if _, _, err := resolveServiceFiles(serviceInstallOptions{Name: "OPCDA_Test", ConfigPath: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing config was accepted")
	}
}
