package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"

	"github.com/east-true/opcda-access-adapter/internal/frontend/dajson"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type writeHTTPRequest struct {
	Items []struct {
		ItemID        exactJSONString `json:"itemId"`
		DataType      string          `json:"dataType"`
		ValueEncoding string          `json:"valueEncoding"`
		Value         json.RawMessage `json:"value"`
	} `json:"items"`
}

type writeHTTPResult struct {
	ItemID    string              `json:"itemId"`
	OK        bool                `json:"ok"`
	HRESULT   *opcda.HRESULTValue `json:"hresult"`
	ErrorCode string              `json:"errorCode,omitempty"`
}

func (s *Server) handleWrite(ctx context.Context, w stdhttp.ResponseWriter, request *stdhttp.Request) {
	// This check occurs before body decoding and before WriteBatch so a disabled
	// endpoint cannot admit any source-side Write work.
	if !s.runtime.Status(ctx).WriteEnabled {
		writeLayerError(w, stdhttp.StatusForbidden, "adapter", opcda.CodeWriteDisabled, "write is disabled", nil)
		return
	}
	if !validateBrowserBoundary(w, request) {
		return
	}
	if !validateJSONRequest(w, request) {
		return
	}

	var decoded writeHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(decoded.Items) == 0 {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "items must contain at least one entry")
		return
	}
	if len(decoded.Items) > s.config.MaxWriteItems {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeRequestLimitExceeded, "Write item limit exceeded")
		return
	}

	items := make([]opcda.WriteItem, len(decoded.Items))
	for index, item := range decoded.Items {
		if item.ItemID == "" {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "itemId must not be empty")
			return
		}
		for _, character := range item.ItemID {
			if character == 0 {
				writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "itemId must not contain NUL")
				return
			}
		}
		if len([]byte(item.ItemID)) > s.config.MaxItemIDBytes {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeItemIDTooLong, "itemId exceeds configured limit")
			return
		}
		varType, err := opcda.ParseDAVarType(item.DataType)
		if err != nil {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "dataType must be a known symbolic scalar VARTYPE")
			return
		}
		if item.Value == nil {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "value is required")
			return
		}
		value, err := decodeWriteValue(varType, item.ValueEncoding, item.Value)
		if err != nil {
			writeValueError(w, err)
			return
		}
		items[index] = opcda.WriteItem{ItemID: opcda.DAItemID(item.ItemID), VarType: varType, Value: value}
	}

	results, err := s.runtime.WriteBatch(ctx, items)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if !writeResultsMatchRequest(items, results) {
		writeLayerError(w, stdhttp.StatusInternalServerError, "adapter", opcda.CodeInternalResultMismatch, "runtime returned results that do not match the Write request", nil)
		return
	}
	encoded := make([]writeHTTPResult, len(results))
	for index, result := range results {
		encoded[index] = writeHTTPResult{
			ItemID:    string(result.ItemID),
			OK:        result.ErrorCode == "" && result.HRESULTPresent && result.HRESULT.Succeeded(),
			ErrorCode: result.ErrorCode,
		}
		if result.HRESULTPresent {
			hresult := result.HRESULT.Representation()
			encoded[index].HRESULT = &hresult
		}
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		Results []writeHTTPResult `json:"results"`
	}{Results: encoded})
}

func writeResultsMatchRequest(items []opcda.WriteItem, results []opcda.WriteResult) bool {
	if len(results) != len(items) {
		return false
	}
	for index := range items {
		if results[index].ItemID != items[index].ItemID {
			return false
		}
		if !results[index].HRESULTPresent && results[index].ErrorCode == "" {
			return false
		}
	}
	return true
}

// 공유 DA JSON 규칙으로 Write 값을 해석한다.
func decodeWriteValue(varType opcda.DAVarType, encoding string, raw json.RawMessage) (any, error) {
	return dajson.DecodeValue(varType, encoding, raw)
}

func writeValueError(w stdhttp.ResponseWriter, err error) {
	if adapterErr, ok := opcda.AsAdapterError(err); ok {
		status := stdhttp.StatusBadRequest
		layer := "frontend"
		if adapterErr.Code == opcda.CodeUnsupportedVarType {
			status = stdhttp.StatusUnprocessableEntity
			layer = "adapter"
		}
		writeLayerError(w, status, layer, adapterErr.Code, adapterErr.Message, nil)
		return
	}
	writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidValue, "invalid Write value")
}
