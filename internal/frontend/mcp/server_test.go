package mcpfrontend

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jiind/opcda-access-adapter/internal/opcda"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testRuntime struct {
	opcda.Runtime
	state       opcda.RuntimeStatus
	read        func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error)
	write       func(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error)
	browse      func(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error)
	subscribe   func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error)
	unsubscribe func(context.Context, opcda.SubscriptionID) error
	available   func(context.Context, string) ([]opcda.AvailableProperty, error)
	properties  func(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error)
}

// Status는 테스트가 지정한 DA 상태만 반환한다.
func (r *testRuntime) Status(context.Context) opcda.RuntimeStatus {
	return r.state
}

// ReadBatch는 원본에 전달한 요청을 테스트에서 관찰하게 한다.
func (r *testRuntime) ReadBatch(ctx context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	return r.read(ctx, request)
}

// WriteBatch는 Write의 횟수와 타입을 테스트에 전달한다.
func (r *testRuntime) WriteBatch(ctx context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	return r.write(ctx, items)
}

// Browse는 소스 경로와 반환 정체성을 테스트에서 지정한다.
func (r *testRuntime) Browse(ctx context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return r.browse(ctx, request)
}

// Subscribe는 테스트 DA 그룹을 생성한다.
func (r *testRuntime) Subscribe(ctx context.Context, request opcda.SubscribeRequest) (opcda.Subscription, error) {
	return r.subscribe(ctx, request)
}

// Unsubscribe는 그룹 정리 호출을 테스트에서 확인하게 한다.
func (r *testRuntime) Unsubscribe(ctx context.Context, id opcda.SubscriptionID) error {
	return r.unsubscribe(ctx, id)
}

// AvailableItemProperties는 실제 속성 식별자와 타입을 전달한다.
func (r *testRuntime) AvailableItemProperties(ctx context.Context, id string) ([]opcda.AvailableProperty, error) {
	return r.available(ctx, id)
}

// ItemProperties는 순서와 부분 오류를 포함한 속성 결과를 전달한다.
func (r *testRuntime) ItemProperties(ctx context.Context, request opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return r.properties(ctx, request)
}

// testSession은 실제 SDK 클라이언트와 stdio와 동일한 바이트 전송으로 연결한다.
func testSession(t *testing.T, runtime *testRuntime) (*mcp.ClientSession, *Server, context.Context) {
	t.Helper()
	// 1. 소켓 리스너 없이 전이중 바이트 연결과 서버를 구성한다.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	server, err := New(runtime, Config{Limits: opcda.DefaultLimits(), RequestDeadline: time.Second})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	left, right := net.Pipe()
	finished := make(chan error, 1)
	go func() {
		finished <- server.Run(ctx, left, left)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "adapter-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: right, Writer: right}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	// 2. 클라이언트 종료가 서버와 소유 DA 그룹도 종료하는지 확인한다.
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("MCP session did not stop after client EOF")
		}
	})
	return session, server, ctx
}

// callTool은 프로토콜을 통해 호출하고 JSON 정밀도를 유지한 원문을 반환한다.
func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name, arguments string) (*mcp.CallToolResult, json.RawMessage) {
	t.Helper()
	response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
	if err != nil {
		t.Fatalf("%s protocol failure: %v", name, err)
	}
	if len(response.Content) == 0 {
		t.Fatalf("%s returned no text representation", name)
	}
	text, ok := response.Content[0].(*mcp.TextContent)
	if !ok || !json.Valid([]byte(text.Text)) {
		t.Fatalf("%s returned invalid JSON text", name)
	}
	return response, json.RawMessage(text.Text)
}

// TestClientDiscoversOnlyDANativeTools는 도구 집합과 Write의 부작용 표시를 검증한다.
func TestClientDiscoversOnlyDANativeTools(t *testing.T) {
	session, _, ctx := testSession(t, &testRuntime{})
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 9 {
		t.Fatalf("tool discovery: %v, tools=%v", err, tools)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "da_write" && (tool.Annotations.ReadOnlyHint || !*tool.Annotations.DestructiveHint || tool.Annotations.IdempotentHint) {
			t.Fatal("Write annotations permit unsafe assumptions")
		}
	}
	response, _ := callTool(t, ctx, session, "da_status", `{}`)
	if response.IsError {
		t.Fatal("Status failed")
	}
}

// TestReadPreservesExactMetadataAndPartialFailures는 실제 MCP 결과에서 DA 정보를 확인한다.
func TestReadPreservesExactMetadataAndPartialFailures(t *testing.T) {
	runtime := &testRuntime{read: func(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
		if request.Source != opcda.DADataSourceDevice || request.Items[0] != " Exact.ID " || request.Items[1] != "missing" {
			t.Error("Read request was normalised or reordered")
		}
		return []opcda.ReadResult{
			readValue(" Exact.ID ", opcda.VTUI8, uint64(math.MaxUint64)),
			{ItemID: "missing", HRESULT: opcda.HRESULT(-1073479673), HRESULTPresent: true},
		}, nil
	}}
	session, _, ctx := testSession(t, runtime)
	response, raw := callTool(t, ctx, session, "da_read", `{"items":[{"itemId":" Exact.ID "},{"itemId":"missing"}]}`)
	if response.IsError {
		t.Fatalf("Read failed: %s", raw)
	}
	var decoded struct {
		Results []struct {
			ItemID           string             `json:"itemId"`
			OK               bool               `json:"ok"`
			Value            string             `json:"value"`
			Quality          uint16             `json:"quality"`
			Timestamp        *string            `json:"timestamp"`
			TimestampPresent bool               `json:"timestampPresent"`
			HRESULT          opcda.HRESULTValue `json:"hresult"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	first, second := decoded.Results[0], decoded.Results[1]
	if first.Value != "18446744073709551615" || first.ItemID != " Exact.ID " || first.Quality != 32 || first.TimestampPresent || first.Timestamp != nil || !first.OK || second.OK || second.HRESULT.Hex != "0xC0040007" {
		t.Fatalf("source semantics changed: %s", raw)
	}
}

// readValue는 실제 VARTYPE 폭과 메타데이터가 일치하는 항목 결과를 만든다.
func readValue(id opcda.DAItemID, varType opcda.DAVarType, value any) opcda.ReadResult {
	return opcda.ReadResult{ItemID: id, VarType: &varType, CanonicalType: &varType, HRESULTPresent: true,
		Value: &opcda.DAValue{ItemID: id, VarType: varType, Value: value, QualityRaw: 32}}
}

// TestWritePolicyTypesAndNoRetry는 기본 차단·정확한 숫자 폭·일회성 실행을 검증한다.
func TestWritePolicyTypesAndNoRetry(t *testing.T) {
	var calls atomic.Int32
	runtime := &testRuntime{write: func(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
		calls.Add(1)
		if _, ok := items[0].Value.(int16); !ok {
			t.Error("Write scalar width changed")
		}
		return nil, &opcda.SourceError{Operation: "IOPCSyncIO.Write", HRESULT: opcda.HRESULT(-2147467259)}
	}}
	session, _, ctx := testSession(t, runtime)
	response, _ := callTool(t, ctx, session, "da_write", `{"items":[{"itemId":"x","dataType":"VT_I2","valueEncoding":"json","value":7}]}`)
	if !response.IsError || calls.Load() != 0 {
		t.Fatal("disabled Write reached DA")
	}
	// 별도 세션에서 활성 정책을 고정해 상태 데이터 레이스를 만들지 않는다.
	enabled := &testRuntime{state: opcda.RuntimeStatus{WriteEnabled: true}, write: runtime.write}
	writer, _, writeCtx := testSession(t, enabled)
	for _, value := range []string{"32768", "1.5", `"7"`} {
		response, _ = callTool(t, writeCtx, writer, "da_write", `{"items":[{"itemId":"x","dataType":"VT_I2","valueEncoding":"json","value":`+value+`}]}`)
		if !response.IsError || calls.Load() != 0 {
			t.Fatal("invalid Write value reached DA")
		}
	}
	response, raw := callTool(t, writeCtx, writer, "da_write", `{"items":[{"itemId":"x","dataType":"VT_I2","valueEncoding":"json","value":7}]}`)
	if !response.IsError || calls.Load() != 1 || !strings.Contains(string(raw), "0x80004005") {
		t.Fatalf("Write failure lost or retried: calls=%d result=%s", calls.Load(), raw)
	}
}

// TestInvalidInputsNeverReachDA는 모호한 JSON·오타·부적합한 타입을 입력 경계에서 거부한다.
func TestInvalidInputsNeverReachDA(t *testing.T) {
	var calls atomic.Int32
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		calls.Add(1)
		return nil, nil
	}}
	session, _, ctx := testSession(t, runtime)
	for _, raw := range []string{
		`{"items":[{"ItemId":"x"}]}`,
		`{"items":[{"itemId":"x\u0000"}]}`,
		`{"items":[{"itemId":"x"}],"source":"cache"}`,
		`{"items":null}`,
		`{"items":[]}`,
		`{"items":[{"itemId":7}]}`,
	} {
		response, _ := callTool(t, ctx, session, "da_read", raw)
		if !response.IsError {
			t.Fatalf("invalid arguments accepted: %s", raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid inputs reached DA")
	}
}

type testSubscription struct {
	info    opcda.SubscriptionInfo
	mu      sync.Mutex
	values  []opcda.SubscriptionValue
	updates chan struct{}
	done    chan struct{}
	once    sync.Once
}

// Info는 그룹 생성 시 정해진 원본 메타데이터를 반환한다.
func (s *testSubscription) Info() opcda.SubscriptionInfo {
	return s.info
}

// Updates는 실제 코어와 같은 병합 집합 알림을 제공한다.
func (s *testSubscription) Updates() <-chan struct{} {
	return s.updates
}

// Done은 원본 연결 종료 또는 명시적 해제를 알린다.
func (s *testSubscription) Done() <-chan struct{} {
	return s.done
}

// Err는 무효화 이후 명시적 종료 오류를 반환한다.
func (s *testSubscription) Err() error {
	return invalidated()
}

// Drain은 현재 집합을 반환하고 별도 과거 값은 보관하지 않는다.
func (s *testSubscription) Drain() []opcda.SubscriptionValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.values
	s.values = nil
	return values
}

// set는 한 항목의 최신 값만 유지하는 DA 코어의 동작을 모사한다.
func (s *testSubscription) set(value int32) {
	// 1. 이전 값은 같은 항목의 새 값으로 대체한다.
	read := readValue("x", opcda.VTI4, value)
	s.mu.Lock()
	s.values = []opcda.SubscriptionValue{{ItemID: read.ItemID, Value: read.Value, VarType: read.VarType, CanonicalType: read.CanonicalType, HRESULTPresent: true}}
	s.mu.Unlock()

	// 2. 콜백을 막지 않는 한 칸의 깨움 신호만 제공한다.
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// TestSubscriptionsCoalesceInvalidateAndRelease는 세션 소유권과 DA 그룹 수명주기를 검증한다.
func TestSubscriptionsCoalesceInvalidateAndRelease(t *testing.T) {
	handle := &testSubscription{info: opcda.SubscriptionInfo{ID: "sub-1-1", ConnectionGeneration: 1, RequestedUpdateRate: 250 * time.Millisecond, RevisedUpdateRate: 300 * time.Millisecond, ActiveItemCount: 1, Items: []opcda.SubscriptionItemStatus{{ItemID: "x", Active: true, HRESULTPresent: true}}}, updates: make(chan struct{}, 1), done: make(chan struct{})}
	var released atomic.Int32
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return handle, nil
	}, unsubscribe: func(_ context.Context, id opcda.SubscriptionID) error {
		if id != "sub-1-1" {
			t.Error("unowned group released")
		}
		released.Add(1)
		handle.once.Do(func() {
			close(handle.done)
		})
		return nil
	}}
	session, server, ctx := testSession(t, runtime)
	response, raw := callTool(t, ctx, session, "da_subscribe", `{"items":[{"itemId":"x"}],"requestedUpdateRateMs":250}`)
	if response.IsError || !strings.Contains(string(raw), `"revisedUpdateRateMs":300`) {
		t.Fatalf("source revised rate lost: %s", raw)
	}
	handle.set(1)
	handle.set(2)
	response, raw = callTool(t, ctx, session, "da_subscription_next", `{"subscriptionId":"sub-1-1"}`)
	if response.IsError || !strings.Contains(string(raw), `"value":2`) || strings.Contains(string(raw), `"value":1`) {
		t.Fatalf("coalescing changed: %s", raw)
	}
	started := time.Now()
	response, raw = callTool(t, ctx, session, "da_subscription_next", `{"subscriptionId":"sub-1-1","waitMs":40}`)
	if response.IsError || time.Since(started) < 30*time.Millisecond || !strings.Contains(string(raw), `"results":[]`) {
		t.Fatalf("stale update signal or empty timeout: %s", raw)
	}
	response, _ = callTool(t, ctx, session, "da_subscription_next", `{"subscriptionId":"sub-other"}`)
	if !response.IsError {
		t.Fatal("unowned subscription accepted")
	}
	handle.set(3)
	handle.once.Do(func() {
		close(handle.done)
	})
	response, raw = callTool(t, ctx, session, "da_subscription_next", `{"subscriptionId":"sub-1-1"}`)
	if !response.IsError || !strings.Contains(string(raw), "SUBSCRIPTION_INVALIDATED") || strings.Contains(string(raw), `"value":3`) {
		t.Fatalf("invalidated values delivered: %s", raw)
	}
	if err := server.closeSubscriptions(ctx); err != nil || released.Load() != 1 {
		t.Fatalf("cleanup: calls=%d err=%v", released.Load(), err)
	}
}

// TestSubscribeSessionEOFCleansGroups는 명시적 해제를 잊어도 EOF가 그룹을 해제하는지 검증한다.
func TestSubscribeSessionEOFCleansGroups(t *testing.T) {
	var released atomic.Int32
	runtime := &testRuntime{subscribe: func(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
		return &testSubscription{info: opcda.SubscriptionInfo{ID: "sub-1-1", ActiveItemCount: 1, Items: []opcda.SubscriptionItemStatus{{ItemID: "x", Active: true, HRESULTPresent: true}}}, updates: make(chan struct{}, 1), done: make(chan struct{})}, nil
	}, unsubscribe: func(context.Context, opcda.SubscriptionID) error {
		released.Add(1)
		return nil
	}}
	t.Run("client", func(t *testing.T) {
		session, _, ctx := testSession(t, runtime)
		response, raw := callTool(t, ctx, session, "da_subscribe", `{"items":[{"itemId":"x"}],"requestedUpdateRateMs":250}`)
		if response.IsError {
			t.Fatalf("subscribe: %s", raw)
		}
	})
	if released.Load() != 1 {
		t.Fatal("MCP EOF did not release its DA group")
	}
}

// TestReadRejectsResultIdentityMismatch는 잘못된 원본 결과를 성공으로 내보내지 않게 한다.
func TestReadRejectsResultIdentityMismatch(t *testing.T) {
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return []opcda.ReadResult{readValue("wrong", opcda.VTI4, int32(1))}, nil
	}}
	session, _, ctx := testSession(t, runtime)
	response, raw := callTool(t, ctx, session, "da_read", `{"items":[{"itemId":"x"}]}`)
	if !response.IsError || !strings.Contains(string(raw), "INTERNAL_RESULT_MISMATCH") {
		t.Fatalf("identity mismatch accepted: %s", raw)
	}
}

// TestArgumentLimits는 최대 입력·중복 키·재귀 제한을 검사한다.
func TestArgumentLimits(t *testing.T) {
	for _, raw := range []string{strings.Repeat(" ", MaxInputBytes+1), strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), `{"a":1,"\u0061":2}`} {
		if err := validateArguments(json.RawMessage(raw)); err == nil {
			t.Fatal("unbounded or ambiguous arguments accepted")
		}
	}
	response, _ := result(map[string]any{"value": strings.Repeat("x", MaxOutputBytes)})
	if !response.IsError {
		t.Fatal("oversized output accepted")
	}
	response, _ = result(map[string]any{"value": strings.Repeat("\\", (MaxOutputBytes-5000)/5)})
	if !response.IsError {
		t.Fatal("JSON string escaping exceeded the output limit")
	}
}

// TestClientCancellationReachesDAOnce는 MCP 취소가 작업에 전달되고 자동 재시도가 없는지 검증한다.
func TestClientCancellationReachesDAOnce(t *testing.T) {
	// 1. DA 작업이 시작된 뒤 클라이언트가 해당 호출만 취소한다.
	started := make(chan struct{})
	stopped := make(chan struct{})
	var calls atomic.Int32
	runtime := &testRuntime{read: func(ctx context.Context, _ opcda.ReadRequest) ([]opcda.ReadResult, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}}
	session, _, ctx := testSession(t, runtime)
	requestCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := session.CallTool(requestCtx, &mcp.CallToolParams{Name: "da_read", Arguments: map[string]any{"items": []map[string]string{{"itemId": "x"}}}})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("Read did not start")
	}
	cancel()

	// 2. 원본 요청이 취소돼도 세션은 다음 명시적 호출에 사용할 수 있다.
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP cancellation did not reach DA")
	}
	if err := <-done; err == nil || calls.Load() != 1 {
		t.Fatal("cancelled operation succeeded or was retried")
	}
	response, _ := callTool(t, ctx, session, "da_status", `{}`)
	if response.IsError {
		t.Fatal("cancellation terminated the MCP session")
	}
}

// TestBrowseAndPropertiesKeepSourceContracts는 선택 경로와 속성의 순서·부재·오류를 검증한다.
func TestBrowseAndPropertiesKeepSourceContracts(t *testing.T) {
	runtime := &testRuntime{
		browse: func(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
			if len(request.Path) != 1 || request.Path[0] != " Exact " {
				t.Error("Browse path changed")
			}
			id := opcda.DAItemID(" exact.item ")
			return opcda.BrowseResult{Path: request.Path, Entries: []opcda.BrowseEntry{{Kind: opcda.BrowseEntryItem, Name: "source name", ItemID: &id}}}, nil
		},
		available: func(context.Context, string) ([]opcda.AvailableProperty, error) {
			return []opcda.AvailableProperty{{ID: 101, Description: "source description", VarType: opcda.VTEmpty}}, nil
		},
		properties: func(_ context.Context, request opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
			return []opcda.ItemPropertyValue{
				{ID: request.Properties[0], OK: true, HRESULTPresent: true, VarTypePresent: true, VarType: opcda.VTEmpty, ValuePresent: false},
				{ID: request.Properties[1], HRESULT: opcda.HRESULT(-1073479165), HRESULTPresent: true, ErrorCode: "DA_ITEM_ERROR"},
			}, nil
		},
	}
	session, _, ctx := testSession(t, runtime)
	response, raw := callTool(t, ctx, session, "da_browse", `{"path":[" Exact "]}`)
	if response.IsError || !strings.Contains(string(raw), `"itemId":" exact.item "`) || strings.Contains(string(raw), `"dataType"`) {
		t.Fatalf("Browse identity or absent metadata changed: %s", raw)
	}
	response, raw = callTool(t, ctx, session, "da_available_item_properties", `{"itemId":"x"}`)
	if response.IsError || !strings.Contains(string(raw), "VT_EMPTY") {
		t.Fatalf("source VT_EMPTY lost: %s", raw)
	}
	response, raw = callTool(t, ctx, session, "da_item_properties", `{"itemId":"x","propertyIds":[101,999]}`)
	if response.IsError || !strings.Contains(string(raw), `"valuePresent":false`) || !strings.Contains(string(raw), "DA_ITEM_ERROR") {
		t.Fatalf("property absence or partial failure changed: %s", raw)
	}
}

// TestReadSpecialValuesAndArrayShape는 특수 실수와 배열의 실제 차원 정보를 검증한다.
func TestReadSpecialValuesAndArrayShape(t *testing.T) {
	for _, test := range []struct {
		name     string
		varType  opcda.DAVarType
		value    any
		expected string
	}{
		{"float", opcda.VTR8, math.Inf(-1), `"value":"-Infinity"`},
		{"array", opcda.VTI4 | opcda.VTArray, opcda.DAArray{ElementType: opcda.VTI4, Dimensions: []opcda.DADimension{{LowerBound: -2, Length: 2}}, Elements: []any{int32(1), int32(2)}}, `"lowerBound":-2`},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
				return []opcda.ReadResult{readValue("x", test.varType, test.value)}, nil
			}}
			session, _, ctx := testSession(t, runtime)
			response, raw := callTool(t, ctx, session, "da_read", `{"items":[{"itemId":"x"}]}`)
			if response.IsError || !strings.Contains(string(raw), test.expected) {
				t.Fatal(fmt.Sprintf("representation lost: %s", raw))
			}
		})
	}
}
