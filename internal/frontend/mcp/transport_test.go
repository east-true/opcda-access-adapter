package mcpfrontend

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type testConnection struct {
	incoming []jsonrpc.Message
	outgoing []jsonrpc.Message
}

// Read는 전송 수용량 검증에 사용할 다음 메시지를 반환한다.
func (c *testConnection) Read(context.Context) (jsonrpc.Message, error) {
	if len(c.incoming) == 0 {
		return nil, io.EOF
	}
	message := c.incoming[0]
	c.incoming = c.incoming[1:]
	return message, nil
}

// Write는 실제로 전송 경계를 통과한 응답만 보관한다.
func (c *testConnection) Write(_ context.Context, message jsonrpc.Message) error {
	c.outgoing = append(c.outgoing, message)
	return nil
}

// Close는 메모리 전송의 종료를 지원한다.
func (c *testConnection) Close() error {
	return nil
}

// SessionID는 테스트 전송의 단일 세션 식별자를 반환한다.
func (c *testConnection) SessionID() string {
	return "test"
}

// TestTransportBoundsOutstandingRequests는 SDK 큐 앞의 실제 수용 한계를 검증한다.
func TestTransportBoundsOutstandingRequests(t *testing.T) {
	// 1. 처리 중인 요청이 한계를 넘으면 추가 요청을 보관하지 않는다.
	inner := &testConnection{}
	for index := 1; index <= MaxInflight+2; index++ {
		id, _ := jsonrpc.MakeID(float64(index))
		inner.incoming = append(inner.incoming, &jsonrpc.Request{ID: id, Method: "tools/call"})
	}
	connection := &boundedConnection{Connection: inner, pending: make(map[jsonrpc.ID]struct{})}
	for index := 0; index < MaxInflight; index++ {
		if _, err := connection.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := connection.Read(context.Background()); err != io.EOF || len(inner.outgoing) != 2 || len(connection.pending) != MaxInflight {
		t.Fatal("unbounded requests reached the SDK")
	}

	// 2. 응답을 전송한 뒤에만 다음 요청을 수용한다.
	id, _ := jsonrpc.MakeID(float64(1))
	if err := connection.Write(context.Background(), &jsonrpc.Response{ID: id, Result: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	id, _ = jsonrpc.MakeID(float64(99))
	inner.incoming = append(inner.incoming, &jsonrpc.Request{ID: id, Method: "tools/call"})
	if _, err := connection.Read(context.Background()); err != nil || len(connection.pending) != MaxInflight {
		t.Fatal("request capacity was not released after response")
	}
}

// TestTransportRejectsDuplicateIDsAndOversizedOutput는 요청 오인과 출력 무제한 증가를 방지한다.
func TestTransportRejectsDuplicateIDsAndOversizedOutput(t *testing.T) {
	// 1. 아직 응답하지 않은 ID의 재사용은 세션 오류다.
	id, _ := jsonrpc.MakeID("same")
	inner := &testConnection{incoming: []jsonrpc.Message{&jsonrpc.Request{ID: id, Method: "ping"}, &jsonrpc.Request{ID: id, Method: "ping"}}}
	connection := &boundedConnection{Connection: inner, pending: make(map[jsonrpc.ID]struct{})}
	if _, err := connection.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Read(context.Background()); err == nil {
		t.Fatal("duplicate outstanding request ID accepted")
	}

	// 2. 전송 전에 과대 응답을 거부한다.
	body, _ := json.Marshal(strings.Repeat("x", MaxOutputBytes))
	if err := connection.Write(context.Background(), &jsonrpc.Response{ID: id, Result: body}); err == nil || len(inner.outgoing) != 0 {
		t.Fatal("oversized response reached the output")
	}
}

// TestFrameBoundaryRejectsAmbiguousInput는 SDK보다 앞에서 전체 프레임을 검증한다.
func TestFrameBoundaryRejectsAmbiguousInput(t *testing.T) {
	for _, raw := range []string{
		`{"id":1,"id":2}` + "\n",
		`{"method":"\ud800"}` + "\n",
		"{\"method\":\"\xff\"}\n",
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n",
		strings.Repeat(" ", MaxInputBytes+1) + "{}\n",
		`{} {}` + "\n",
	} {
		if _, err := io.ReadAll(newFrameReader(io.NopCloser(strings.NewReader(raw)))); err == nil {
			t.Fatal("ambiguous MCP frame accepted")
		}
	}
}
