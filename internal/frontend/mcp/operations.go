package mcpfrontend

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/east-true/opcda-access-adapter/internal/frontend/dajson"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// status는 선택한 소스와 DA 런타임의 실제 상태를 값 없이 보고한다.
func (s *Server) status(ctx context.Context) any {
	// 1. 소스가 보고한 상태와 연결 세대를 그대로 읽는다.
	state := s.runtime.Status(ctx)
	source := map[string]any{"progId": state.Source.ProgID, "clsid": state.Source.CLSID, "connectionGeneration": state.ConnectionGeneration}
	if state.LastSourceErrorSet {
		diagnostic := map[string]any{"operation": state.LastSourceError.Operation}
		if state.LastSourceError.HRESULTPresent {
			diagnostic["hresult"] = state.LastSourceError.HRESULT.Representation()
		}
		source["lastError"] = diagnostic
	}

	// 2. MCP 전송 상태와 소스 capability를 서로 구분한다.
	return map[string]any{
		"runtimeState": state.State, "source": source, "writeEnabled": state.WriteEnabled,
		"capabilities": map[string]any{"browse": state.Capabilities.Browse, "read": state.Capabilities.Read, "write": state.Capabilities.Write, "subscribe": state.Capabilities.Subscribe, "properties": state.Capabilities.Properties},
		"runtime":      map[string]any{"queueDepth": state.QueueDepth, "reconnectCount": state.ReconnectCount, "subscriptionCount": state.SubscriptionCount, "degradedReason": state.DegradedReason},
		"frontend":     map[string]any{"mcp": map[string]any{"transport": "stdio"}},
	}
}

// browse는 지정한 DA 경로의 결과만 조회하고 소스 식별자를 보존한다.
func (s *Server) browse(ctx context.Context, input browseInput) (any, error) {
	// 1. 경로와 결과 크기를 명시적 한계에 맞춘다.
	if len(input.Path) > s.config.Limits.MaxBrowseDepth {
		return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "Browse path exceeds the configured depth")
	}
	for _, segment := range input.Path {
		if err := s.validText(segment); err != nil {
			return nil, err
		}
	}
	if input.Filter == "" {
		input.Filter = opcda.BrowseFilterAll
	}
	response, err := s.runtime.Browse(ctx, opcda.BrowseRequest{Path: input.Path, Filter: input.Filter})
	if err != nil {
		return nil, err
	}
	if len(response.Entries) > s.config.Limits.MaxBrowseEntries {
		return nil, opcda.NewAdapterError(opcda.CodeBrowseResultLimitExceeded, "Browse result exceeds the configured limit")
	}
	if !slices.Equal(response.Path, input.Path) {
		return nil, mismatch()
	}

	// 2. Browse가 제공하지 않은 타입과 접근 권한을 추론하지 않는다.
	entries := make([]map[string]any, len(response.Entries))
	for index, entry := range response.Entries {
		if s.validText(entry.Name) != nil || (entry.Kind != opcda.BrowseEntryBranch && entry.Kind != opcda.BrowseEntryItem) {
			return nil, mismatch()
		}
		if entry.Kind == opcda.BrowseEntryItem && entry.ItemID == nil {
			return nil, mismatch()
		}
		encoded := map[string]any{"kind": entry.Kind, "name": entry.Name, "itemId": entry.ItemID}
		if entry.ItemID != nil && s.validText(string(*entry.ItemID)) != nil {
			return nil, mismatch()
		}
		if entry.CanonicalType != nil {
			encoded["dataType"] = entry.CanonicalType.Information()
		}
		if entry.AccessRights != nil {
			encoded["accessRights"] = entry.AccessRights
		}
		entries[index] = encoded
	}
	return map[string]any{"path": response.Path, "entries": entries}, nil
}

// read는 Device Read의 순서·부분 실패와 모든 DA 메타데이터를 보존한다.
func (s *Server) read(ctx context.Context, input readInput) (any, error) {
	// 1. 캐시 읽기를 추가하지 않고 배치와 식별자 한계를 확인한다.
	ids, err := s.itemIDs(input.Items, s.config.Limits.MaxReadItems)
	if err != nil {
		return nil, err
	}
	values, err := s.runtime.ReadBatch(ctx, opcda.ReadRequest{Items: ids, Source: opcda.DADataSourceDevice})
	if err != nil {
		return nil, err
	}
	if len(values) != len(ids) {
		return nil, mismatch()
	}

	// 2. 결과의 정체성을 검증한 뒤 공유 JSON 값 계약으로 변환한다.
	fields := dajson.NewReadResultFields(len(values))
	encoded := make([]dajson.ReadResult, len(values))
	for index, value := range values {
		if value.ItemID != ids[index] || !validReadResult(value) {
			return nil, mismatch()
		}
		encoded[index] = dajson.EncodeReadResult(value, &fields)
	}
	return map[string]any{"results": encoded}, nil
}

// validReadResult는 원본 값과 항목 결과의 식별자·타입·HRESULT 일치를 확인한다.
func validReadResult(value opcda.ReadResult) bool {
	if !value.HRESULTPresent && value.ErrorCode == "" {
		return false
	}
	if value.Value == nil {
		return value.ErrorCode != "" || value.HRESULT.Failed()
	}
	return value.Value.ItemID == value.ItemID && value.VarType != nil && value.Value.VarType == *value.VarType &&
		value.Value.HRESULT == value.HRESULT
}

// write는 정책과 타입을 먼저 검증한 후 한 번만 원본 Write를 실행한다.
func (s *Server) write(ctx context.Context, input writeInput) (any, error) {
	// 1. 비활성 Write는 값을 해석하거나 원본 작업을 호출하기 전에 거부한다.
	if !s.runtime.Status(ctx).WriteEnabled {
		return nil, opcda.NewAdapterError(opcda.CodeWriteDisabled, "Write is disabled")
	}
	if len(input.Items) == 0 || len(input.Items) > s.config.Limits.MaxWriteItems {
		return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "Write batch exceeds the configured limit")
	}
	items := make([]opcda.WriteItem, len(input.Items))
	for index, item := range input.Items {
		if err := s.validText(string(item.ItemID)); err != nil {
			return nil, err
		}
		varType, err := opcda.ParseDAVarType(item.DataType)
		if err != nil {
			return nil, invalid("dataType must be a known symbolic VARTYPE")
		}
		value, err := dajson.DecodeValue(varType, item.ValueEncoding, item.Value)
		if err != nil {
			return nil, err
		}
		items[index] = opcda.WriteItem{ItemID: opcda.DAItemID(item.ItemID), VarType: varType, Value: value}
	}

	// 2. Write는 재시도하지 않으며 항목별 실패도 원래 순서로 반환한다.
	values, err := s.runtime.WriteBatch(ctx, items)
	if err != nil {
		return nil, err
	}
	if len(values) != len(items) {
		return nil, mismatch()
	}
	encoded := make([]map[string]any, len(values))
	for index, value := range values {
		if value.ItemID != items[index].ItemID || (!value.HRESULTPresent && value.ErrorCode == "") {
			return nil, mismatch()
		}
		encoded[index] = map[string]any{"itemId": value.ItemID, "ok": value.ErrorCode == "" && value.HRESULTPresent && value.HRESULT.Succeeded(), "errorCode": value.ErrorCode, "hresult": hresult(value.HRESULT, value.HRESULTPresent)}
	}
	return map[string]any{"results": encoded}, nil
}

// properties는 DA 속성 식별자·타입·값 존재 여부와 부분 오류를 그대로 전달한다.
func (s *Server) properties(ctx context.Context, input propertiesInput, available bool) (any, error) {
	// 1. 속성 대상과 배치 크기를 검증한다.
	itemID := string(input.ItemID)
	if err := s.validText(itemID); err != nil {
		return nil, err
	}
	if available {
		values, err := s.runtime.AvailableItemProperties(ctx, itemID)
		if err != nil {
			return nil, err
		}
		if len(values) > s.config.Limits.MaxItemProperties {
			return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "property list exceeds the configured limit")
		}
		encoded := make([]map[string]any, len(values))
		for index, value := range values {
			encoded[index] = map[string]any{"propertyId": value.ID, "description": value.Description, "dataType": value.VarType.Information()}
		}
		return map[string]any{"itemId": itemID, "properties": encoded}, nil
	}
	if len(input.PropertyIDs) == 0 || len(input.PropertyIDs) > s.config.Limits.MaxItemProperties {
		return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "property batch exceeds the configured limit")
	}
	ids := make([]opcda.PropertyID, len(input.PropertyIDs))
	for index, id := range input.PropertyIDs {
		ids[index] = opcda.PropertyID(id)
	}
	values, err := s.runtime.ItemProperties(ctx, opcda.ItemPropertiesRequest{ItemID: itemID, Properties: ids})
	if err != nil {
		return nil, err
	}
	if len(values) != len(ids) {
		return nil, mismatch()
	}

	// 2. 실패와 값 부재를 구분하고 실제 값만 공통 규칙으로 인코딩한다.
	encoded := make([]map[string]any, len(values))
	for index, value := range values {
		if value.ID != ids[index] {
			return nil, mismatch()
		}
		entry := map[string]any{"propertyId": value.ID, "ok": value.OK, "valuePresent": false, "hresult": hresult(value.HRESULT, value.HRESULTPresent), "errorCode": value.ErrorCode}
		if value.VarTypePresent {
			entry["dataType"] = value.VarType.Information()
		}
		if value.OK && value.ValuePresent {
			body, encoding, err := dajson.EncodeValue(value.VarType, value.Value)
			if err != nil {
				entry["ok"] = false
				entry["errorCode"] = opcda.CodeUnsupportedVarType
			} else {
				entry["value"] = json.RawMessage(body)
				entry["valueEncoding"] = encoding
				entry["valuePresent"] = true
			}
		}
		encoded[index] = entry
	}
	return map[string]any{"itemId": itemID, "results": encoded}, nil
}

// hresult는 부재와 성공 코드 0을 서로 구분한다.
func hresult(value opcda.HRESULT, present bool) *opcda.HRESULTValue {
	if !present {
		return nil
	}
	representation := value.Representation()
	return &representation
}

// mismatch는 런타임 결과의 정체성 또는 구조 불일치를 명시적으로 보고한다.
func mismatch() error {
	return opcda.NewAdapterError(opcda.CodeInternalResultMismatch, "DA result does not match the requested identity or structure")
}
