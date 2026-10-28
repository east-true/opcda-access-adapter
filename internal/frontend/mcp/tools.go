package mcpfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jiind/opcda-access-adapter/internal/frontend/dajson"
	"github.com/jiind/opcda-access-adapter/internal/opcda"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type itemInput struct {
	ItemID dajson.ExactString `json:"itemId"`
}

type browseInput struct {
	Path   []string           `json:"path"`
	Filter opcda.BrowseFilter `json:"filter"`
}

type readInput struct {
	Source string      `json:"source"`
	Items  []itemInput `json:"items"`
}

type writeInput struct {
	Items []struct {
		ItemID        dajson.ExactString `json:"itemId"`
		DataType      string             `json:"dataType"`
		ValueEncoding string             `json:"valueEncoding"`
		Value         json.RawMessage    `json:"value"`
	} `json:"items"`
}

type propertiesInput struct {
	ItemID      dajson.ExactString `json:"itemId"`
	PropertyIDs []uint32           `json:"propertyIds"`
}

type toolDefinition struct {
	name        string
	description string
	schema      string
	readOnly    bool
}

// registerTools는 정확한 입력 스키마와 부작용 표시를 가진 고정 도구 집합을 공개한다.
func (s *Server) registerTools() error {
	// 1. 모든 도구는 하나의 명시적 소스만 사용하며 자유 형식 실행을 제공하지 않는다.
	definitions := []toolDefinition{
		{"da_status", "Inspect the configured DA source, capabilities, write policy and connection generation.", `{"type":"object","additionalProperties":false}`, true},
		{"da_browse", "Browse one exact DA path. Names and ItemIDs are never normalised. Browse may be unsupported.", `{"type":"object","properties":{"path":{"type":"array","items":{"type":"string","minLength":1}},"filter":{"enum":["all","branch","item"]}},"additionalProperties":false}`, true},
		{"da_read", "Read a bounded batch from the DA device. Preserve raw Quality, timestamp presence, VARTYPE and per-item HRESULT. Non-Good Quality remains data.", `{"type":"object","properties":{"source":{"enum":["device"]},"items":{"type":"array","minItems":1,"items":{"type":"object","properties":{"itemId":{"type":"string","minLength":1}},"required":["itemId"],"additionalProperties":false}}},"required":["items"],"additionalProperties":false}`, true},
		{"da_write", "Write exact typed values once. Disabled by default. Never retry automatically: timeout does not prove a Write was not executed. VT_I8/VT_UI8 use decimal strings; arrays carry explicit dimensions.", `{"type":"object","properties":{"items":{"type":"array","minItems":1,"items":{"type":"object","properties":{"itemId":{"type":"string","minLength":1},"dataType":{"type":"string"},"valueEncoding":{"enum":["json","float-special","array"]},"value":{}},"required":["itemId","dataType","valueEncoding","value"],"additionalProperties":false}}},"required":["items"],"additionalProperties":false}`, false},
		{"da_available_item_properties", "List the DA properties a source offers for one exact ItemID, with source property IDs and VARTYPEs.", `{"type":"object","properties":{"itemId":{"type":"string","minLength":1}},"required":["itemId"],"additionalProperties":false}`, true},
		{"da_item_properties", "Read DA properties in request order. Properties 2, 3 and 4 are refused; use da_read for value, Quality and timestamp together.", `{"type":"object","properties":{"itemId":{"type":"string","minLength":1},"propertyIds":{"type":"array","minItems":1,"items":{"type":"integer","minimum":0,"maximum":4294967295}}},"required":["itemId","propertyIds"],"additionalProperties":false}`, true},
		{"da_subscribe", "Create one DA group owned by this MCP session. No notification queue, automatic retry or resubscribe. Receive current coalesced values with da_subscription_next and release with da_unsubscribe.", `{"type":"object","properties":{"items":{"type":"array","minItems":1,"uniqueItems":true,"items":{"type":"object","properties":{"itemId":{"type":"string","minLength":1}},"required":["itemId"],"additionalProperties":false}},"requestedUpdateRateMs":{"type":"integer","minimum":1,"maximum":3600000},"percentDeadband":{"type":"number","minimum":0,"maximum":100}},"required":["items","requestedUpdateRateMs"],"additionalProperties":false}`, false},
		{"da_subscription_next", "Drain one DA group's current per-item coalesced set, optionally waiting up to waitMs. An empty timeout is not disconnect. SUBSCRIPTION_INVALIDATED requires explicit resubscribe; no values are replayed.", `{"type":"object","properties":{"subscriptionId":{"type":"string","minLength":1,"maxLength":128},"waitMs":{"type":"integer","minimum":0,"maximum":30000}},"required":["subscriptionId"],"additionalProperties":false}`, false},
		{"da_unsubscribe", "Release one DA group owned by this MCP session. The identifier cannot be reused.", `{"type":"object","properties":{"subscriptionId":{"type":"string","minLength":1,"maxLength":128}},"required":["subscriptionId"],"additionalProperties":false}`, false},
	}

	// 2. SDK의 저수준 핸들러 앞에서 스키마를 검증해 입력값을 오류에 포함하지 않는다.
	for _, definition := range definitions {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(definition.schema), &schema); err != nil {
			return err
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return err
		}
		s.protocol.AddTool(&mcp.Tool{Name: definition.name, Description: definition.description, InputSchema: &schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: definition.readOnly, DestructiveHint: new(definition.name == "da_write"), OpenWorldHint: new(false)}},
			func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				// 1. 모호한 JSON과 스키마에 맞지 않는 입력을 DA 호출 전에 거부한다.
				raw := request.Params.Arguments
				if len(raw) == 0 {
					raw = json.RawMessage(`{}`)
				}
				var value any
				if err := validateArguments(raw); err != nil {
					return toolError(err), nil
				}
				if err := json.Unmarshal(raw, &value); err != nil || resolved.Validate(value) != nil {
					return toolError(opcda.NewAdapterError(opcda.CodeInvalidRequest, "tool arguments do not match the input schema")), nil
				}

				// 2. 각 DA 작업에는 기존의 유한 요청 제한 시간을 적용한다.
				ctx, cancel := context.WithTimeout(ctx, s.config.RequestDeadline)
				defer cancel()
				response, err := s.call(ctx, definition.name, raw)
				if err != nil {
					return toolError(err), nil
				}
				return result(response)
			})
	}
	return nil
}

// validateArguments는 JSON의 중복 키·UTF-8·surrogate·깊이·단일 값 조건을 검증한다.
func validateArguments(raw json.RawMessage) error {
	// 1. SDK의 입력 해석으로 원문이 대체되기 전에 문자와 구조를 확인한다.
	if len(raw) > MaxInputBytes || !utf8.Valid(raw) || !dajson.ValidSurrogates(raw) {
		return invalid("invalid tool argument encoding or size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanArgumentValue(decoder, 0); err != nil {
		return err
	}

	// 2. 하나의 값 뒤에는 공백과 EOF만 허용한다.
	if _, err := decoder.Token(); err != io.EOF {
		return invalid("tool arguments must contain exactly one JSON value")
	}
	return nil
}

// scanArgumentValue는 객체별 중복 키와 중첩 깊이를 제한하며 한 값을 소비한다.
func scanArgumentValue(decoder *json.Decoder, depth int) error {
	// 1. 최대 깊이를 넘기기 전에 재귀를 중단한다.
	if depth > 64 {
		return opcda.NewAdapterError(opcda.CodeJSONDepthLimitExceeded, "MCP JSON depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return invalid("invalid tool JSON")
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}

	// 2. 각 컨테이너를 확인하며 이스케이프 해제 후 동일한 키도 거부한다.
	keys := make(map[string]struct{})
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return invalid("invalid tool JSON key")
			}
			name, ok := key.(string)
			if !ok {
				return invalid("invalid tool JSON key")
			}
			if _, duplicate := keys[name]; duplicate {
				return opcda.NewAdapterError(opcda.CodeDuplicateJSONField, "duplicate tool argument")
			}
			keys[name] = struct{}{}
		}
		if err := scanArgumentValue(decoder, depth+1); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return invalid("invalid tool JSON container")
	}
	return nil
}

// invalid는 클라이언트 값을 노출하지 않는 입력 오류를 구성한다.
func invalid(message string) error {
	return opcda.NewAdapterError(opcda.CodeInvalidRequest, message)
}

// validText는 원본 ItemID와 경로를 변형하지 않고 허용 범위만 확인한다.
func (s *Server) validText(text string) error {
	if text == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return invalid("ItemID or path must be nonempty valid UTF-8 without NUL")
	}
	if len(text) > s.config.Limits.MaxItemIDBytes {
		return opcda.NewAdapterError(opcda.CodeItemIDTooLong, "ItemID exceeds the configured limit")
	}
	return nil
}

// itemIDs는 클라이언트 순서와 중복을 그대로 유지하면서 배치 한계를 검증한다.
func (s *Server) itemIDs(items []itemInput, maximum int) ([]opcda.DAItemID, error) {
	// 1. 할당 전에 배치 크기를 제한한다.
	if len(items) == 0 || len(items) > maximum {
		return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "DA item batch exceeds the configured limit")
	}

	// 2. 정확한 식별자만 DA 계약으로 전달한다.
	ids := make([]opcda.DAItemID, len(items))
	for index, item := range items {
		if err := s.validText(string(item.ItemID)); err != nil {
			return nil, err
		}
		ids[index] = opcda.DAItemID(item.ItemID)
	}
	return ids, nil
}

// call은 도구 입력을 DA-native 작업에 직접 연결한다.
func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "da_status":
		return s.status(ctx), nil
	case "da_browse":
		var input browseInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid Browse arguments")
		}
		return s.browse(ctx, input)
	case "da_read":
		var input readInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid Read arguments")
		}
		return s.read(ctx, input)
	case "da_write":
		var input writeInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid Write arguments")
		}
		return s.write(ctx, input)
	case "da_available_item_properties", "da_item_properties":
		var input propertiesInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid property arguments")
		}
		return s.properties(ctx, input, name == "da_available_item_properties")
	case "da_subscribe":
		var input subscribeInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid Subscribe arguments")
		}
		return s.subscribe(ctx, input)
	case "da_subscription_next", "da_unsubscribe":
		var input subscriptionInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalid("invalid subscription arguments")
		}
		if name == "da_unsubscribe" {
			return s.unsubscribe(ctx, input.SubscriptionID)
		}
		return s.next(ctx, input)
	default:
		return nil, fmt.Errorf("unknown MCP tool")
	}
}
