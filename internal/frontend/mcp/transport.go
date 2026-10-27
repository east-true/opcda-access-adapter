package mcpfrontend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type boundedTransport struct {
	inner mcp.Transport
}

type frameReader struct {
	input   io.ReadCloser
	scanner *bufio.Scanner
	pending []byte
}

// newFrameReader는 SDK가 해석하기 전에 줄 단위 MCP 메시지를 엄격하게 검증한다.
func newFrameReader(input io.ReadCloser) *frameReader {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxInputBytes+2)
	return &frameReader{input: input, scanner: scanner}
}

// Read는 크기·문자·중복 키·깊이를 검증한 단일 객체 프레임만 전달한다.
func (r *frameReader) Read(buffer []byte) (int, error) {
	// 1. 전체 프레임을 유한 버퍼로 읽고 JSON-RPC batch를 거부한다.
	if len(r.pending) == 0 {
		if !r.scanner.Scan() {
			if r.scanner.Err() != nil {
				return 0, fmt.Errorf("invalid or oversized MCP frame")
			}
			return 0, io.EOF
		}
		line := r.scanner.Bytes()
		if err := validateArguments(line); err != nil {
			return 0, fmt.Errorf("invalid MCP frame")
		}
		index := 0
		for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
			index++
		}
		if index == len(line) || line[index] != '{' {
			return 0, fmt.Errorf("MCP requires one JSON-RPC object per line")
		}
		r.pending = append(line, '\n')
	}

	// 2. 검증한 메시지를 SDK의 요청 단위 입력에 나눠 전달한다.
	n := copy(buffer, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// Close는 대기 중인 원본 파이프도 닫아 취소 시 읽기를 깨운다.
func (r *frameReader) Close() error {
	return r.input.Close()
}

type boundedConnection struct {
	mcp.Connection
	mu          sync.Mutex
	pending     map[jsonrpc.ID]struct{}
	initialized bool
}

// Connect는 SDK가 보유할 미완료 요청의 개수를 제한하는 연결을 구성한다.
func (t *boundedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &boundedConnection{Connection: connection, pending: make(map[jsonrpc.ID]struct{})}, nil
}

// Read는 SDK의 비동기 큐에 들어가기 전에 요청을 제한하고 취소 알림은 통과시킨다.
func (c *boundedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	// 1. 전송에서 읽은 메시지와 필요한 제어 알림을 구분한다.
	for {
		message, err := c.Connection.Read(ctx)
		if err != nil {
			return nil, err
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok {
			return message, nil
		}
		if !request.ID.IsValid() {
			if request.Method == "notifications/cancelled" {
				return message, nil
			}
			if request.Method == "notifications/initialized" && !c.initialized {
				c.initialized = true
				return message, nil
			}
			continue
		}

		// 2. 중복 ID는 세션 오류이며 과도한 요청은 보관하지 않고 거부한다.
		c.mu.Lock()
		_, duplicate := c.pending[request.ID]
		full := len(c.pending) >= MaxInflight
		if !full && !duplicate {
			c.pending[request.ID] = struct{}{}
		}
		c.mu.Unlock()
		if duplicate {
			return nil, fmt.Errorf("duplicate outstanding MCP request identifier")
		}
		if !full {
			return message, nil
		}
		if err := c.Connection.Write(ctx, &jsonrpc.Response{ID: request.ID, Error: &jsonrpc.Error{Code: -32000, Message: "QUEUE_FULL"}}); err != nil {
			return nil, err
		}
	}
}

// Write는 응답 크기를 검증하고 전송 완료 후에만 요청 수용 공간을 반환한다.
func (c *boundedConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	// 1. SDK가 만드는 응답에도 동일한 출력 한계를 적용한다.
	body, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		return err
	}
	if len(body) > MaxOutputBytes {
		return fmt.Errorf("MCP response exceeds the output limit")
	}

	// 2. 느린 소비자의 응답이 전송되기 전까지 추가 요청을 제한한다.
	if err := c.Connection.Write(ctx, message); err != nil {
		return err
	}
	if response, ok := message.(*jsonrpc.Response); ok {
		c.mu.Lock()
		delete(c.pending, response.ID)
		c.mu.Unlock()
	}
	return nil
}
