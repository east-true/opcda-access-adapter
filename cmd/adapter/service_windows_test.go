//go:build windows

package main

import (
	"path/filepath"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// TestMCPServiceRejectedBeforeSCM는 stdio 설정으로 서비스 설치를 시작하지 않게 한다.
func TestMCPServiceRejectedBeforeSCM(t *testing.T) {
	// 1. 유효한 MCP 설정 파일을 준비한다.
	config := app.DefaultConfig()
	config.Frontend = app.FrontendMCP
	config.Source.CLSID = "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := app.WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}

	// 2. 관리자 권한이나 SCM 연결을 사용하기 전에 거부한다.
	if _, _, err := resolveServiceFiles(serviceInstallOptions{Name: "OPCDA_Test", ConfigPath: path}); err == nil {
		t.Fatal("MCP stdio accepted as a Windows Service")
	}
}

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
