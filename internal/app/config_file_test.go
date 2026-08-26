package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func TestConfigFileRoundTripPreservesGuidedChoices(t *testing.T) {
	config, err := GuidedSetupConfig(
		opcda.SourceConfig{CLSID: "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"},
		"127.0.0.1:18080",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adapter.json")
	if err := WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != config.Source || loaded.HTTPListenAddress != "127.0.0.1:18080" || !loaded.WriteEnabled {
		t.Fatalf("loaded config = %+v", loaded)
	}
	if loaded.Runtime.Source != config.Source || !loaded.Runtime.WriteEnabled {
		t.Fatalf("runtime config = %+v", loaded.Runtime)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows os.Chmod exposes only the read-only attribute; access control is
	// inherited from the selected deployment directory and is validated in the
	// native service scenario.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("configuration permissions = %o", info.Mode().Perm())
	}
}

func TestConfigFileRefusesExistingPath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "adapter.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := GuidedSetupConfig(opcda.SourceConfig{ProgID: "Vendor.Server.1"}, "127.0.0.1:8080", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfigFileExclusive(path, config); !errors.Is(err, ErrConfigFileExists) {
		t.Fatalf("write error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func TestConfigFileConcurrentCreateHasOneWinner(t *testing.T) {
	config, err := GuidedSetupConfig(opcda.SourceConfig{ProgID: "Vendor.Server.1"}, "127.0.0.1:8080", false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adapter.json")
	const writers = 16
	results := make(chan error, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- WriteConfigFileExclusive(path, config)
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConfigFileExists) {
			t.Fatalf("unexpected writer error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful writers = %d", successes)
	}
	if _, err := LoadConfigFile(path); err != nil {
		t.Fatalf("winning configuration is invalid: %v", err)
	}
}

func TestLoadConfigFileRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"unknown field":   `{"version":1,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false,"extra":true}`,
		"duplicate field": `{"version":1,"version":1,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		"trailing value":  `{"version":1,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false} {}`,
		"future version":  `{"version":4,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		// An older version may not carry OPC UA settings, and a non-UA frontend
		// may not carry them at any version.
		"opcua before version 3": `{"version":2,"source":{"clsid":"{A}"},"frontend":{"type":"opcua","opcuaListen":"127.0.0.1:4840"},"writeEnabled":false}`,
		"opcua without endpoint": `{"version":3,"source":{"clsid":"{A}"},"frontend":{"type":"opcua","opcuaListen":"127.0.0.1:4840"},"writeEnabled":false}`,
		"opcua settings on http": `{"version":3,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080","opcuaListen":"127.0.0.1:4840"},"writeEnabled":false}`,
		"opcua with grpc listen": `{"version":3,"source":{"clsid":"{A}"},"frontend":{"type":"opcua","opcuaListen":"127.0.0.1:4840","grpcListen":"127.0.0.1:50051","opcua":{"endpointUrl":"opc.tcp://127.0.0.1:4840","applicationUri":"urn:a","securityPolicyUri":"urn:p","transportProfileUri":"urn:t","namespaceUri":"urn:n","sourceFolderName":"Source"}},"writeEnabled":false}`,
		"opcua without policy":   `{"version":3,"source":{"clsid":"{A}"},"frontend":{"type":"opcua","opcuaListen":"127.0.0.1:4840","opcua":{"endpointUrl":"opc.tcp://127.0.0.1:4840","applicationUri":"urn:a","transportProfileUri":"urn:t","namespaceUri":"urn:n","sourceFolderName":"Source"}},"writeEnabled":false}`,
		"wrong frontend":         `{"version":1,"source":{"clsid":"{A}"},"frontend":{"type":"grpc","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		"two sources":            `{"version":1,"source":{"progId":"A","clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		"missing source":         `{"version":1,"source":{},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		"external missing port":  `{"version":1,"source":{"progId":"A"},"frontend":{"type":"http","httpListen":"0.0.0.0"},"writeEnabled":false}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adapter.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfigFile(path); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestConfigFileGRPCRoundTripAndLegacyHTTP(t *testing.T) {
	config, err := GuidedSetupFrontendConfig(
		opcda.SourceConfig{CLSID: "{BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB}"},
		FrontendGRPC,
		"127.0.0.1:55051",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "grpc.json")
	if err := WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Frontend != FrontendGRPC || loaded.GRPCListenAddress != "127.0.0.1:55051" || loaded.Source != config.Source {
		t.Fatalf("loaded gRPC config = %+v", loaded)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy-http.json")
	legacy := `{"version":1,"source":{"progId":"Vendor.Server.1"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyLoaded, err := LoadConfigFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if legacyLoaded.Frontend != FrontendHTTP || legacyLoaded.HTTPListenAddress != "127.0.0.1:8080" {
		t.Fatalf("legacy config = %+v", legacyLoaded)
	}
}

func TestLoadConfigFileRejectsOversizedBodyAndPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter.json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", MaximumConfigFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized file error = %v", err)
	}
	if _, err := LoadConfigFile(strings.Repeat("x", maximumConfigPathBytes+1)); err == nil {
		t.Fatal("oversized path was accepted")
	}
}

func TestGuidedSetupConfigRequiresOneExplicitSource(t *testing.T) {
	if _, err := GuidedSetupConfig(opcda.SourceConfig{}, "127.0.0.1:8080", false); err == nil {
		t.Fatal("missing source was accepted")
	}
	if _, err := GuidedSetupConfig(opcda.SourceConfig{ProgID: "A", CLSID: "{A}"}, "127.0.0.1:8080", false); err == nil {
		t.Fatal("two sources were accepted")
	}
}
