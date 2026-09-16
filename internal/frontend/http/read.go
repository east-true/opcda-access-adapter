package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	stdhttp "net/http"
	"strconv"
	"unicode/utf8"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type readHTTPRequest struct {
	Source string `json:"source"`
	Items  []struct {
		ItemID exactJSONString `json:"itemId"`
	} `json:"items"`
}

type readHTTPResult struct {
	ItemID            string                `json:"itemId"`
	OK                bool                  `json:"ok"`
	DataType          *opcda.DAVarTypeInfo  `json:"dataType,omitempty"`
	CanonicalDataType *opcda.DAVarTypeInfo  `json:"canonicalDataType,omitempty"`
	ValueEncoding     string                `json:"valueEncoding,omitempty"`
	Value             json.RawMessage       `json:"value,omitempty"`
	Quality           *uint16               `json:"quality,omitempty"`
	Timestamp         *string               `json:"timestamp"`
	TimestampPresent  bool                  `json:"timestampPresent"`
	HRESULT           *opcda.HRESULTValue   `json:"hresult"`
	AccessRights      *opcda.DAAccessRights `json:"accessRights,omitempty"`
	ErrorCode         string                `json:"errorCode,omitempty"`
}

func (s *Server) handleRead(ctx context.Context, w stdhttp.ResponseWriter, request *stdhttp.Request) {
	if !validateJSONRequest(w, request) {
		return
	}
	var decoded readHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		writeDecodeError(w, err)
		return
	}
	if decoded.Source == "" {
		decoded.Source = string(opcda.DADataSourceDevice)
	}
	if decoded.Source != string(opcda.DADataSourceDevice) {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "source must be device")
		return
	}
	if len(decoded.Items) == 0 {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "items must contain at least one entry")
		return
	}
	if len(decoded.Items) > s.config.MaxReadItems {
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeRequestLimitExceeded, "Read item limit exceeded")
		return
	}

	itemIDs := make([]opcda.DAItemID, len(decoded.Items))
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
		itemIDs[index] = opcda.DAItemID(item.ItemID)
	}

	results, err := s.runtime.ReadBatch(ctx, opcda.ReadRequest{
		Items: itemIDs, Source: opcda.DADataSourceDevice,
	})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if !readResultsMatchRequest(itemIDs, results) {
		writeLayerError(w, stdhttp.StatusInternalServerError, "adapter", opcda.CodeInternalResultMismatch, "runtime returned results that do not match the Read request", nil)
		return
	}
	encoded := make([]readHTTPResult, len(results))
	for index := range results {
		encoded[index] = encodeReadResult(results[index])
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		Results []readHTTPResult `json:"results"`
	}{Results: encoded})
}

func readResultsMatchRequest(items []opcda.DAItemID, results []opcda.ReadResult) bool {
	if len(results) != len(items) {
		return false
	}
	for index := range items {
		if results[index].ItemID != items[index] {
			return false
		}
		result := results[index]
		if result.Value != nil {
			if result.ErrorCode != "" || !result.HRESULTPresent || result.HRESULT.Failed() || result.VarType == nil ||
				result.Value.ItemID != result.ItemID || result.Value.VarType != *result.VarType || result.Value.HRESULT != result.HRESULT {
				return false
			}
		} else if result.ErrorCode == "" && (!result.HRESULTPresent || result.HRESULT.Succeeded()) {
			return false
		}
	}
	return true
}

func writeDecodeError(w stdhttp.ResponseWriter, err error) {
	var maxBytesError *stdhttp.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, stdhttp.StatusRequestEntityTooLarge, opcda.CodeRequestBodyTooLarge, "request body exceeds configured limit")
		return
	}
	var bodyError *requestBodyError
	if errors.As(err, &bodyError) {
		writeError(w, stdhttp.StatusBadRequest, bodyError.code, bodyError.message)
		return
	}
	writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, "request body is not valid for this endpoint")
}

type exactJSONString string

func (value *exactJSONString) UnmarshalJSON(data []byte) error {
	if !validJSONSurrogates(data) {
		return fmt.Errorf("JSON string contains an unpaired UTF-16 surrogate")
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*value = exactJSONString(decoded)
	return nil
}

func validJSONSurrogates(data []byte) bool {
	for index := 0; index+1 < len(data); {
		if data[index] != '\\' {
			index++
			continue
		}
		if data[index+1] != 'u' {
			index += 2
			continue
		}
		code, ok := parseHexQuad(data[index+2:])
		if !ok {
			return true // The JSON decoder will report malformed escape syntax.
		}
		if 0xD800 <= code && code <= 0xDBFF {
			if index+12 > len(data) || data[index+6] != '\\' || data[index+7] != 'u' {
				return false
			}
			low, ok := parseHexQuad(data[index+8:])
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			index += 12
		} else if 0xDC00 <= code && code <= 0xDFFF {
			return false
		} else {
			index += 6
		}
	}
	return true
}

func parseHexQuad(data []byte) (uint16, bool) {
	if len(data) < 4 {
		return 0, false
	}
	var value uint16
	for _, character := range data[:4] {
		value <<= 4
		switch {
		case '0' <= character && character <= '9':
			value |= uint16(character - '0')
		case 'a' <= character && character <= 'f':
			value |= uint16(character-'a') + 10
		case 'A' <= character && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func (s *Server) decodeRequestBody(w stdhttp.ResponseWriter, request *stdhttp.Request, target any) error {
	body, err := io.ReadAll(stdhttp.MaxBytesReader(w, request.Body, s.config.MaxBodyBytes))
	if err != nil {
		return err
	}
	if !utf8.Valid(body) {
		return fmt.Errorf("request body must be valid UTF-8")
	}
	if err := validateJSONStructure(body, s.config.MaxJSONDepth); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return fmt.Errorf("request body must contain exactly one JSON value")
}

func encodeReadResult(result opcda.ReadResult) readHTTPResult {
	encoded := readHTTPResult{
		ItemID:       string(result.ItemID),
		ErrorCode:    result.ErrorCode,
		AccessRights: result.AccessRights,
	}
	if result.VarType != nil {
		info := result.VarType.Information()
		encoded.DataType = &info
	}
	if result.CanonicalType != nil {
		info := result.CanonicalType.Information()
		encoded.CanonicalDataType = &info
	}
	if result.HRESULTPresent {
		hresult := result.HRESULT.Representation()
		encoded.HRESULT = &hresult
	}
	if result.Value == nil || result.ErrorCode != "" || !result.HRESULTPresent || result.HRESULT.Failed() {
		return encoded
	}

	value, encoding, err := encodeDAValue(result.Value.VarType, result.Value.Value)
	if err != nil {
		// A value this frontend cannot carry is a limit of the frontend rather
		// than a malformed one, and the two codes send a client to different
		// places: one to check what it sent, the other to check what this
		// adapter supports.
		encoded.ErrorCode = string(opcda.CodeInvalidValue)
		if adapterErr, ok := opcda.AsAdapterError(err); ok {
			encoded.ErrorCode = string(adapterErr.Code)
		}
		return encoded
	}
	var timestamp *string
	if result.Value.TimestampPresent {
		text, err := result.Value.Timestamp.UTC().MarshalText()
		if err != nil {
			encoded.ErrorCode = string(opcda.CodeInvalidValue)
			return encoded
		}
		formatted := string(text)
		timestamp = &formatted
	}
	encoded.OK = true
	encoded.Value = value
	encoded.ValueEncoding = encoding
	quality := result.Value.QualityRaw
	encoded.Quality = &quality
	encoded.TimestampPresent = result.Value.TimestampPresent
	encoded.Timestamp = timestamp
	return encoded
}

func encodeDAValue(varType opcda.DAVarType, value any) (json.RawMessage, string, error) {
	if varType.IsByRef() {
		return nil, "", opcda.NewAdapterError(opcda.CodeUnsupportedVarType,
			fmt.Sprintf("this frontend does not publish %s values", varType))
	}
	if varType.IsArray() {
		array, ok := value.(opcda.DAArray)
		if !ok {
			return nil, "", opcda.NewAdapterError(opcda.CodeInvalidValue,
				fmt.Sprintf("a %s value did not carry an array", varType))
		}
		encoded, err := encodeDAArray(array)
		if err != nil {
			return nil, "", err
		}
		return encoded, arrayValueEncoding, nil
	}
	encoding := "json"
	var transportValue any
	var typeMatches bool
	switch varType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		typeMatches = value == nil
	case opcda.VTI1:
		transportValue, typeMatches = value.(int8)
	case opcda.VTUI1:
		transportValue, typeMatches = value.(uint8)
	case opcda.VTI2:
		transportValue, typeMatches = value.(int16)
	case opcda.VTUI2:
		transportValue, typeMatches = value.(uint16)
	case opcda.VTI4, opcda.VTInt, opcda.VTError:
		transportValue, typeMatches = value.(int32)
	case opcda.VTUI4, opcda.VTUInt:
		transportValue, typeMatches = value.(uint32)
	case opcda.VTI8:
		typed, ok := value.(int64)
		typeMatches = ok
		transportValue = strconv.FormatInt(typed, 10)
	case opcda.VTUI8:
		typed, ok := value.(uint64)
		typeMatches = ok
		transportValue = strconv.FormatUint(typed, 10)
	case opcda.VTR4:
		typed, ok := value.(float32)
		typeMatches = ok
		transportValue = typed
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			transportValue = specialFloat(float64(typed))
			encoding = "float-special"
		}
	case opcda.VTR8:
		typed, ok := value.(float64)
		typeMatches = ok
		transportValue = typed
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			transportValue = specialFloat(typed)
			encoding = "float-special"
		}
	case opcda.VTBool:
		transportValue, typeMatches = value.(bool)
	case opcda.VTBSTR:
		transportValue, typeMatches = value.(string)
	default:
		return nil, "", fmt.Errorf("unsupported VARTYPE %s", varType)
	}
	if !typeMatches {
		return nil, "", fmt.Errorf("value type does not match VARTYPE %s", varType)
	}
	encoded, err := json.Marshal(transportValue)
	if err != nil {
		return nil, "", err
	}
	return encoded, encoding, nil
}

func specialFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "+Infinity"
	default:
		return "-Infinity"
	}
}

func writeOperationError(w stdhttp.ResponseWriter, err error) {
	if sourceError, ok := opcda.AsSourceError(err); ok {
		hresult := sourceError.HRESULT.Representation()
		writeLayerError(w, stdhttp.StatusServiceUnavailable, "source", opcda.CodeDAMethodFailed, sourceError.Operation+" failed", &hresult)
		return
	}
	if adapterError, ok := opcda.AsAdapterError(err); ok {
		status := stdhttp.StatusServiceUnavailable
		switch adapterError.Code {
		case opcda.CodeInvalidRequest, opcda.CodeRequestLimitExceeded, opcda.CodeItemIDTooLong, opcda.CodeInvalidValue, opcda.CodeBSTRTooLong:
			status = stdhttp.StatusBadRequest
		case opcda.CodeWriteDisabled:
			status = stdhttp.StatusForbidden
		case opcda.CodeRuntimeDeadline:
			status = stdhttp.StatusGatewayTimeout
		case opcda.CodeBrowseUnsupported, opcda.CodePropertiesUnsupported,
			opcda.CodeBrowseResultLimitExceeded, opcda.CodeUnsupportedVarType,
			opcda.CodeArrayTooLarge:
			status = stdhttp.StatusUnprocessableEntity
		}
		writeLayerError(w, status, "adapter", adapterError.Code, adapterError.Message, nil)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		writeLayerError(w, stdhttp.StatusGatewayTimeout, "adapter", opcda.CodeRuntimeDeadline, "request deadline exceeded", nil)
		return
	}
	writeLayerError(w, stdhttp.StatusInternalServerError, "adapter", opcda.CodeInternalError, "internal adapter error", nil)
}

// arrayValueEncoding names the encoding an array value travels under. It is a
// value of its own rather than "json" because what follows is an object
// describing a shape, not the value itself, and a client that does not know
// arrays must not read it as one.
const arrayValueEncoding = "array"

// jsonArrayDimension is one dimension as a client sees it.
type jsonArrayDimension struct {
	LowerBound int32  `json:"lowerBound"`
	Length     uint32 `json:"length"`
}

// jsonArrayValue is the shape ADR-0019 decision 5 publishes. The elements are
// flat and the dimensions are beside them, which is what separates this from
// the flattening design.md §20.4 forbids: that one discards the shape, and this
// one carries it.
type jsonArrayValue struct {
	ElementDataType opcda.DAVarTypeInfo  `json:"elementDataType"`
	Dimensions      []jsonArrayDimension `json:"dimensions"`
	Elements        []json.RawMessage    `json:"elements"`
}

// encodeDAArray writes an array as its description plus its elements in the
// published order: the last dimension varies fastest.
//
// Elements use the same per-type rules a scalar uses, so nothing new has to be
// learned to read one. The exception is a non-finite float, which a scalar
// names through a sibling valueEncoding field that an element does not have:
// inside an array it is spelled in place, which keeps the array
// self-describing because a string in a float array is exactly one of the
// three names and a number is a number.
func encodeDAArray(array opcda.DAArray) (json.RawMessage, error) {
	dimensions := make([]jsonArrayDimension, len(array.Dimensions))
	for index, dimension := range array.Dimensions {
		dimensions[index] = jsonArrayDimension{
			LowerBound: dimension.LowerBound,
			Length:     dimension.Length,
		}
	}
	elements := make([]json.RawMessage, len(array.Elements))
	for index, element := range array.Elements {
		encoded, _, err := encodeDAValue(array.ElementType, element)
		if err != nil {
			return nil, arrayElementError(index, err)
		}
		elements[index] = encoded
	}
	return json.Marshal(jsonArrayValue{
		ElementDataType: array.ElementType.Information(),
		Dimensions:      dimensions,
		Elements:        elements,
	})
}

// arrayElementError says which element was wrong while keeping the reason. A
// client told only that an element is invalid has to guess which of a thousand
// it was.
func arrayElementError(index int, err error) error {
	adapterErr, ok := opcda.AsAdapterError(err)
	if !ok {
		return fmt.Errorf("array element %d: %w", index, err)
	}
	return opcda.NewAdapterError(adapterErr.Code,
		fmt.Sprintf("array element %d: %s", index, adapterErr.Message))
}
