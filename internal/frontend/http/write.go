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

func decodeWriteValue(varType opcda.DAVarType, encoding string, raw json.RawMessage) (any, error) {
	if varType.IsByRef() {
		return nil, opcda.NewAdapterError(opcda.CodeUnsupportedVarType, "byref Write values are unsupported")
	}
	if varType.IsArray() {
		if encoding != arrayValueEncoding {
			return nil, opcda.NewAdapterError(opcda.CodeInvalidValue,
				"an array value must carry valueEncoding "+arrayValueEncoding)
		}
		return decodeWriteArray(varType, raw)
	}
	if encoding == arrayValueEncoding {
		return nil, opcda.NewAdapterError(opcda.CodeInvalidValue,
			"valueEncoding "+arrayValueEncoding+" requires an array dataType")
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

// decodeWriteArray reads the shape a client supplies for an array Write. It is
// the same description a Read publishes, so a client can send back what it was
// given: the adapter never infers a shape, and a body whose elements disagree
// with its dimensions is refused rather than reshaped.
func decodeWriteArray(varType opcda.DAVarType, raw json.RawMessage) (any, error) {
	var supplied struct {
		ElementDataType *string              `json:"elementDataType"`
		Dimensions      []jsonArrayDimension `json:"dimensions"`
		Elements        []json.RawMessage    `json:"elements"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&supplied); err != nil {
		return nil, opcda.NewAdapterError(opcda.CodeInvalidValue,
			"an array value must be an object carrying dimensions and elements")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, opcda.NewAdapterError(opcda.CodeInvalidValue,
			"an array value must be exactly one JSON object")
	}

	elementType := varType.Base()
	// The element type may be named, and then it has to agree. Leaving it out
	// is not a disagreement: the dataType already carries it.
	if supplied.ElementDataType != nil {
		named, err := opcda.ParseDAVarType(*supplied.ElementDataType)
		if err != nil {
			return nil, opcda.NewAdapterError(opcda.CodeInvalidValue,
				"elementDataType must be a known symbolic VARTYPE")
		}
		if named != elementType {
			return nil, opcda.NewAdapterError(opcda.CodeTypeMismatch,
				fmt.Sprintf("elementDataType %s does not match the declared %s",
					named, varType))
		}
	}

	array := opcda.DAArray{
		ElementType: elementType,
		Dimensions:  make([]opcda.DADimension, len(supplied.Dimensions)),
		Elements:    make([]any, len(supplied.Elements)),
	}
	for index, dimension := range supplied.Dimensions {
		array.Dimensions[index] = opcda.DADimension{
			LowerBound: dimension.LowerBound,
			Length:     dimension.Length,
		}
	}
	for index, element := range supplied.Elements {
		value, err := decodeWriteArrayElement(elementType, element)
		if err != nil {
			return nil, arrayElementError(index, err)
		}
		array.Elements[index] = value
	}
	// The shape and the elements have to describe each other. What the runtime
	// is willing to carry is a separate question, answered by the DA layer's
	// own bounds, so a client's mistake is not reported as an operator's limit.
	if err := array.MatchesItsShape(); err != nil {
		return nil, err
	}
	return array, nil
}

// decodeWriteArrayElement reads one element by the same rules a scalar uses.
// A non-finite float is the exception a scalar names through valueEncoding,
// which an element does not have: inside an array it is spelled in place, so
// one of the three names where a number would go is that value rather than a
// malformed one.
func decodeWriteArrayElement(elementType opcda.DAVarType, raw json.RawMessage) (any, error) {
	if elementType == opcda.VTR4 || elementType == opcda.VTR8 {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return decodeSpecialFloat(elementType, raw)
		}
	}
	return decodeWriteValue(elementType, "json", raw)
}
