package mcpfrontend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jiind/opcda-access-adapter/internal/opcda"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	MaxInputBytes  = 1 << 20
	MaxOutputBytes = 4 << 20
	MaxInflight    = 16
)

type Config struct {
	Limits          opcda.Limits
	RequestDeadline time.Duration
	Version         string
}

type Server struct {
	runtime       opcda.Runtime
	config        Config
	protocol      *mcp.Server
	mu            sync.Mutex
	started       bool
	closed        bool
	subscriptions map[opcda.SubscriptionID]*ownedSubscription
}

// New는 기존 DA Runtime을 사용하는 단일 stdio 세션의 도구를 구성한다.
func New(runtime opcda.Runtime, config Config) (*Server, error) {
	// 1. 입력 계약과 자원 한계를 확인한다.
	if runtime == nil || config.RequestDeadline <= 0 || config.RequestDeadline > 24*time.Hour {
		return nil, fmt.Errorf("MCP requires a runtime and a bounded positive request deadline")
	}
	if err := config.Limits.ValidateForConfiguration(); err != nil {
		return nil, err
	}
	if config.Version == "" {
		config.Version = "dev"
	}

	// 2. SDK의 기본 폐기 로거를 사용하고 도구 외 기능을 공개하지 않는다.
	s := &Server{runtime: runtime, config: config, subscriptions: make(map[opcda.SubscriptionID]*ownedSubscription)}
	s.protocol = mcp.NewServer(&mcp.Implementation{Name: "opcda-access-adapter", Version: config.Version}, &mcp.ServerOptions{
		Instructions: "Access exactly one explicitly configured local OPC DA server. Preserve exact ItemIDs, VARTYPE, raw Quality, timestamp presence and HRESULT. Non-Good Quality is data, not evidence of a healthy measurement. Write is disabled by default and must never be retried automatically. Subscriptions coalesce per item; reconnect requires explicit resubscribe.",
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	if err := s.registerTools(); err != nil {
		return nil, err
	}
	return s, nil
}

// Run은 한 MCP 세션을 실행하고 EOF·취소·전송 실패 시 소유한 DA 그룹을 해제한다.
func (s *Server) Run(ctx context.Context, input io.ReadCloser, output io.WriteCloser) error {
	// 1. 프런트엔드는 한 번만 실행하며 전송의 수명을 세션에 묶는다.
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return fmt.Errorf("MCP frontend cannot be restarted")
	}
	s.started = true
	s.mu.Unlock()
	defer input.Close()
	defer output.Close()

	// 2. SDK 앞에서 프레임 크기와 응답 대기 요청 수를 제한한다.
	transport := &boundedTransport{inner: &mcp.IOTransport{Reader: newFrameReader(input), Writer: output, MaxLineLength: MaxInputBytes + 2}}
	err := s.protocol.Run(ctx, transport)

	// 3. 호출 취소와 별개의 정리 컨텍스트로 남은 DA 그룹을 해제한다.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.config.RequestDeadline)
	defer cancel()
	cleanupErr := s.closeSubscriptions(cleanup)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	if err != nil {
		// SDK·전송 오류에 클라이언트 입력이 포함되어도 로그로 유출하지 않는다.
		err = fmt.Errorf("MCP stdio session failed")
	}
	return errors.Join(err, cleanupErr)
}

// result는 JSON 숫자 정밀도를 바꾸지 않고 구조화된 결과와 호환용 텍스트를 제공한다.
func result(value any) (*mcp.CallToolResult, error) {
	// 1. 결과를 한 번 직렬화해 정수 문자열과 명시적 값 존재 여부를 보존한다.
	body, err := json.Marshal(value)
	if err != nil {
		return toolError(opcda.NewAdapterError(opcda.CodeInvalidValue, "DA result cannot be represented as JSON")), nil
	}

	// 2. 텍스트와 구조화 결과가 함께 들어가는 응답의 총 크기를 제한한다.
	if len(body) > (MaxOutputBytes-4096)/2 {
		return toolError(opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "MCP result exceeds the output limit; use a smaller batch")), nil
	}
	response := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, StructuredContent: json.RawMessage(body)}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > MaxOutputBytes-4096 {
		return toolError(opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "MCP escaped result exceeds the output limit; use a smaller batch")), nil
	}
	return response, nil
}

// toolError는 원본 값과 원본 입력을 포함하지 않는 명시적 도구 실패를 구성한다.
func toolError(err error) *mcp.CallToolResult {
	// 1. 전송 오류·어댑터 오류·소스 오류의 계층과 원래 HRESULT를 구분한다.
	detail := map[string]any{"layer": "adapter", "code": opcda.CodeInternalError, "message": "MCP operation failed"}
	if source, ok := opcda.AsSourceError(err); ok {
		detail = map[string]any{"layer": "source", "code": opcda.CodeDAMethodFailed, "message": "DA method failed", "operation": source.Operation, "hresult": source.HRESULT.Representation()}
	} else if adapter, ok := opcda.AsAdapterError(err); ok {
		detail["code"] = adapter.Code
		detail["message"] = string(adapter.Code)
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		detail["code"] = opcda.CodeRuntimeDeadline
		detail["message"] = "MCP operation cancelled or deadline exceeded"
	}

	// 2. 실패는 MCP 프로토콜 오류 대신 isError 결과로 전달한다.
	body, _ := json.Marshal(map[string]any{"error": detail})
	return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(body), Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}
}
