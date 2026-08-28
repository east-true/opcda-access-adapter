package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// OPC DA item properties, the same two operations the DA runtime offers and
// OPC 10000-8 Table A.1 is mapped from. The HTTP frontend is DA-native, so it
// passes the source's property identifiers, VARTYPEs and HRESULTs through
// rather than mapping them onto anything.

type availablePropertiesHTTPRequest struct {
	ItemID exactJSONString `json:"itemId"`
}

type availablePropertyHTTP struct {
	PropertyID  uint32               `json:"propertyId"`
	Description string               `json:"description,omitempty"`
	DataType    *opcda.DAVarTypeInfo `json:"dataType,omitempty"`
}

type itemPropertiesHTTPRequest struct {
	ItemID      exactJSONString `json:"itemId"`
	PropertyIDs []uint32        `json:"propertyIds"`
}

type itemPropertyHTTPResult struct {
	PropertyID    uint32               `json:"propertyId"`
	OK            bool                 `json:"ok"`
	DataType      *opcda.DAVarTypeInfo `json:"dataType,omitempty"`
	ValueEncoding string               `json:"valueEncoding,omitempty"`
	Value         json.RawMessage      `json:"value,omitempty"`
	ValuePresent  bool                 `json:"valuePresent"`
	HRESULT       *opcda.HRESULTValue  `json:"hresult"`
	ErrorCode     string               `json:"errorCode,omitempty"`
}

func (s *Server) handleAvailableProperties(ctx context.Context, w stdhttp.ResponseWriter, request *stdhttp.Request) {
	if !validateJSONRequest(w, request) {
		return
	}
	var decoded availablePropertiesHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		writeDecodeError(w, err)
		return
	}
	itemID := string(decoded.ItemID)
	if !s.validPropertyItemID(w, itemID) {
		return
	}
	available, err := s.runtime.AvailableItemProperties(ctx, itemID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if len(available) > s.config.MaxItemProperties {
		writeLayerError(w, stdhttp.StatusUnprocessableEntity, "adapter",
			opcda.CodeRequestLimitExceeded, "source reported more item properties than the configured limit", nil)
		return
	}
	properties := make([]availablePropertyHTTP, len(available))
	for index, property := range available {
		properties[index] = availablePropertyHTTP{
			PropertyID:  uint32(property.ID),
			Description: property.Description,
		}
		if property.VarType != 0 {
			information := property.VarType.Information()
			properties[index].DataType = &information
		}
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		ItemID     string                  `json:"itemId"`
		Properties []availablePropertyHTTP `json:"properties"`
	}{ItemID: itemID, Properties: properties})
}

func (s *Server) handleItemProperties(ctx context.Context, w stdhttp.ResponseWriter, request *stdhttp.Request) {
	if !validateJSONRequest(w, request) {
		return
	}
	var decoded itemPropertiesHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		writeDecodeError(w, err)
		return
	}
	itemID := string(decoded.ItemID)
	if !s.validPropertyItemID(w, itemID) {
		return
	}
	if len(decoded.PropertyIDs) == 0 {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "propertyIds must contain at least one entry")
		return
	}
	if len(decoded.PropertyIDs) > s.config.MaxItemProperties {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeRequestLimitExceeded, "item property limit exceeded")
		return
	}
	properties := make([]opcda.PropertyID, len(decoded.PropertyIDs))
	for index, id := range decoded.PropertyIDs {
		properties[index] = opcda.PropertyID(id)
	}
	values, err := s.runtime.ItemProperties(ctx, opcda.ItemPropertiesRequest{
		ItemID: itemID, Properties: properties,
	})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if len(values) != len(properties) {
		writeLayerError(w, stdhttp.StatusInternalServerError, "adapter",
			opcda.CodeInternalResultMismatch, "runtime returned a different number of item property results", nil)
		return
	}
	results := make([]itemPropertyHTTPResult, len(values))
	for index, value := range values {
		if value.ID != properties[index] {
			writeLayerError(w, stdhttp.StatusInternalServerError, "adapter",
				opcda.CodeInternalResultMismatch, "runtime returned item property results out of request order", nil)
			return
		}
		results[index] = encodeItemPropertyHTTP(value)
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		ItemID  string                   `json:"itemId"`
		Results []itemPropertyHTTPResult `json:"results"`
	}{ItemID: itemID, Results: results})
}

func encodeItemPropertyHTTP(value opcda.ItemPropertyValue) itemPropertyHTTPResult {
	encoded := itemPropertyHTTPResult{
		PropertyID: uint32(value.ID),
		ErrorCode:  value.ErrorCode,
	}
	if value.HRESULTPresent {
		hresult := value.HRESULT.Representation()
		encoded.HRESULT = &hresult
	}
	if value.VarTypePresent {
		information := value.VarType.Information()
		encoded.DataType = &information
	}
	if !value.OK {
		// A property the source refused keeps its HRESULT and carries nothing
		// else. Nothing is substituted for it.
		return encoded
	}
	if !value.ValuePresent {
		// The source answered and gave no value. Absence is absence; reporting
		// it as a failure would invent a refusal the source never made.
		encoded.OK = true
		return encoded
	}
	encodedValue, encoding, err := encodeDAValue(value.VarType, value.Value)
	if err != nil {
		encoded.ErrorCode = string(opcda.CodeUnsupportedVarType)
		return encoded
	}
	encoded.OK = true
	encoded.Value = encodedValue
	encoded.ValueEncoding = encoding
	encoded.ValuePresent = true
	return encoded
}

func (s *Server) validPropertyItemID(w stdhttp.ResponseWriter, itemID string) bool {
	if itemID == "" {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "itemId must not be empty")
		return false
	}
	if len([]byte(itemID)) > s.config.MaxItemIDBytes {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeItemIDTooLong, "itemId exceeds configured limit")
		return false
	}
	for _, character := range itemID {
		if character == 0 {
			writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "itemId must not contain NUL")
			return false
		}
	}
	return true
}
