package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	stdhttp "net/http"
	"strconv"

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

func decodeWriteValue(varType opcda.DAVarType, encoding string, raw json.RawMessage) (any, error) {
	if varType.IsArray() || varType.IsByRef() {
		return nil, opcda.NewAdapterError(opcda.CodeUnsupportedVarType, "array and byref Write values are unsupported")
	}
	if encoding == "float-special" {
		return decodeSpecialFloat(varType, raw)
	}
	if encoding != "json" {
		return nil, opcda.NewAdapterError(opcda.CodeInvalidValue, "valueEncoding must be json, or float-special for VT_R4/VT_R8")
	}

	text := string(raw)
	switch varType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		if text != "null" {
			return nil, invalidWriteValue(varType)
		}
		return nil, nil
	case opcda.VTI1:
		value, err := strconv.ParseInt(text, 10, 8)
		return int8(value), numericWriteError(varType, err)
	case opcda.VTUI1:
		value, err := strconv.ParseUint(text, 10, 8)
		return uint8(value), numericWriteError(varType, err)
	case opcda.VTI2:
		value, err := strconv.ParseInt(text, 10, 16)
		return int16(value), numericWriteError(varType, err)
	case opcda.VTUI2:
		value, err := strconv.ParseUint(text, 10, 16)
		return uint16(value), numericWriteError(varType, err)
	case opcda.VTI4, opcda.VTInt, opcda.VTError:
		value, err := strconv.ParseInt(text, 10, 32)
		return int32(value), numericWriteError(varType, err)
	case opcda.VTUI4, opcda.VTUInt:
		value, err := strconv.ParseUint(text, 10, 32)
		return uint32(value), numericWriteError(varType, err)
	case opcda.VTI8:
		textValue, err := decodeExactString(raw)
		if err != nil {
			return nil, opcda.NewAdapterError(opcda.CodeInvalidValue, "VT_I8 value must be a decimal string")
		}
		value, parseErr := strconv.ParseInt(textValue, 10, 64)
		return value, numericWriteError(varType, parseErr)
	case opcda.VTUI8:
		textValue, err := decodeExactString(raw)
		if err != nil {
			return nil, opcda.NewAdapterError(opcda.CodeInvalidValue, "VT_UI8 value must be a decimal string")
		}
		value, parseErr := strconv.ParseUint(textValue, 10, 64)
		return value, numericWriteError(varType, parseErr)
	case opcda.VTR4:
		value, err := strconv.ParseFloat(text, 32)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, invalidWriteValue(varType)
		}
		return float32(value), nil
	case opcda.VTR8:
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, invalidWriteValue(varType)
		}
		return value, nil
	case opcda.VTBool:
		if text == "true" {
			return true, nil
		}
		if text == "false" {
			return false, nil
		}
		return nil, invalidWriteValue(varType)
	case opcda.VTBSTR:
		value, err := decodeExactString(raw)
		if err != nil {
			return nil, opcda.NewAdapterError(opcda.CodeInvalidValue, "VT_BSTR value must be a JSON string")
		}
		return value, nil
	default:
		return nil, opcda.NewAdapterError(opcda.CodeUnsupportedVarType, fmt.Sprintf("unsupported Write VARTYPE %s", varType))
	}
}

func decodeSpecialFloat(varType opcda.DAVarType, raw json.RawMessage) (any, error) {
	if varType != opcda.VTR4 && varType != opcda.VTR8 {
		return nil, opcda.NewAdapterError(opcda.CodeInvalidValue, "float-special is valid only for VT_R4 and VT_R8")
	}
	value, err := decodeExactString(raw)
	if err != nil {
		return nil, invalidWriteValue(varType)
	}
	var number float64
	switch value {
	case "NaN":
		number = math.NaN()
	case "+Infinity":
		number = math.Inf(1)
	case "-Infinity":
		number = math.Inf(-1)
	default:
		return nil, invalidWriteValue(varType)
	}
	if varType == opcda.VTR4 {
		return float32(number), nil
	}
	return number, nil
}

func decodeExactString(raw json.RawMessage) (string, error) {
	var value exactJSONString
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return "", err
	}
	return string(value), nil
}

func numericWriteError(varType opcda.DAVarType, err error) error {
	if err != nil {
		return invalidWriteValue(varType)
	}
	return nil
}

func invalidWriteValue(varType opcda.DAVarType) error {
	return opcda.NewAdapterError(opcda.CodeInvalidValue, fmt.Sprintf("value is not losslessly representable as %s", varType))
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
