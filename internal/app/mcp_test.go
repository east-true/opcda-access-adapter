package app

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type testWriteCloser struct {
	bytes.Buffer
}

// Close는 메모리 출력의 종료를 지원한다.
func (w *testWriteCloser) Close() error {
	return nil
}

// TestMCPRequiresExplicitSelection는 HTTP 기본값과 MCP의 비활성 상태를 검증한다.
func TestMCPRequiresExplicitSelection(t *testing.T) {
	// 1. 기본 실행은 MCP 입력을 읽거나 런타임을 만들지 않는다.
	config := DefaultConfig()
	if config.Frontend != FrontendHTTP {
		t.Fatal("default frontend changed")
	}
	if err := RunMCP(context.Background(), config, nil, nil, nil, "test"); err == nil {
		t.Fatal("MCP ran without explicit selection")
	}

	// 2. MCP를 선택한 서비스 경로는 소스 활성화 전에 거부한다.
	config.Frontend = FrontendMCP
	if _, err := New(config, nil); err == nil || !strings.Contains(err.Error(), "MCP") {
		t.Fatal("MCP created a network service")
	}
}

// TestMCPConfigRoundTripAndExclusivity는 버전 4와 다른 프런트엔드 설정의 혼합 거부를 검증한다.
func TestMCPConfigRoundTripAndExclusivity(t *testing.T) {
	// 1. MCP에는 네트워크 주소를 저장하지 않는다.
	config := DefaultConfig()
	config.Frontend = FrontendMCP
	config.Source.CLSID = "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfigFile(path)
	if err != nil || loaded.Frontend != FrontendMCP || loaded.WriteEnabled {
		t.Fatalf("MCP config round trip failed: %v", err)
	}

	// 2. 이전 버전과 다른 프런트엔드의 설정을 명시적으로 거부한다.
	for _, raw := range []string{
		`{"version":3,` + testSource + `,"frontend":{"type":"mcp"}}`,
		`{"version":4,` + testSource + `,"frontend":{"type":"mcp","httpListen":"127.0.0.1:8080"}}`,
		`{"version":4,` + testSource + `,"frontend":{"type":"mcp","grpcListen":"127.0.0.1:50051"}}`,
		`{"version":4,` + testSource + `,"frontend":{"type":"mcp","opcuaListen":"127.0.0.1:4840"}}`,
		`{"version":4,` + testSource + `,"frontend":{"type":"mcp","opcua":{}}}`,
	} {
		if _, err := LoadConfigFile(writeConfigFile(t, raw)); err == nil {
			t.Fatalf("ambiguous MCP config accepted: %s", raw)
		}
	}
}

// TestMCPProcessEOFShutsRuntimeDown는 stdio EOF가 소스를 종료하며 TCP를 열지 않는지 검증한다.
func TestMCPProcessEOFShutsRuntimeDown(t *testing.T) {
	config := DefaultConfig()
	config.Frontend = FrontendMCP
	// 네트워크 프런트엔드 주소는 MCP 실행에 사용되지 않는다.
	config.HTTPListenAddress = "invalid-unused-http-address"
	config.GRPCListenAddress = "invalid-unused-grpc-address"
	config.OPCUAListenAddress = "invalid-unused-ua-address"
	runtime := &shutdownTrackingRuntime{}
	if err := RunMCP(context.Background(), config, runtime, io.NopCloser(strings.NewReader("")), &testWriteCloser{}, "test"); err != nil {
		t.Fatal(err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatal("EOF did not shut down the DA runtime")
	}
}

// TestMCPEnvironmentSelection는 환경 설정의 명시 선택을 확인한다.
func TestMCPEnvironmentSelection(t *testing.T) {
	t.Setenv("OPCDA_FRONTEND", "mcp")
	config, err := LoadConfig()
	if err != nil || config.Frontend != FrontendMCP {
		t.Fatalf("explicit MCP environment selection failed: %v", err)
	}
}
