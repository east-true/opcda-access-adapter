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
	"time"
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
	var decoded readHTTPRequest
	if err := s.decodeRequestBody(w, request, &decoded); err != nil {
		var maxBytesError *stdhttp.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, stdhttp.StatusRequestEntityTooLarge, opcda.CodeRequestBodyTooLarge, "request body exceeds configured limit")
			return
		}
		writeError(w, stdhttp.StatusBadRequest, opcda.CodeInvalidRequest, err.Error())
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
	encoded := make([]readHTTPResult, len(results))
	for index := range results {
		encoded[index] = encodeReadResult(results[index])
	}
	writeJSON(w, stdhttp.StatusOK, struct {
		Results []readHTTPResult `json:"results"`
	}{Results: encoded})
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

	value, encoding, err := encodeDAValue(result.Value.Value)
	if err != nil {
		encoded.ErrorCode = string(opcda.CodeInvalidValue)
		return encoded
	}
	encoded.OK = true
	encoded.Value = value
	encoded.ValueEncoding = encoding
	quality := result.Value.QualityRaw
	encoded.Quality = &quality
	encoded.TimestampPresent = result.Value.TimestampPresent
	if result.Value.TimestampPresent {
		timestamp := result.Value.Timestamp.UTC().Format(time.RFC3339Nano)
		encoded.Timestamp = &timestamp
	}
	return encoded
}

func encodeDAValue(value any) (json.RawMessage, string, error) {
	encoding := "json"
	var transportValue any = value
	switch typed := value.(type) {
	case int64:
		transportValue = strconv.FormatInt(typed, 10)
	case uint64:
		transportValue = strconv.FormatUint(typed, 10)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			transportValue = specialFloat(float64(typed))
			encoding = "float-special"
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			transportValue = specialFloat(typed)
			encoding = "float-special"
		}
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
		writeLayerError(w, stdhttp.StatusServiceUnavailable, "source", "DA_METHOD_FAILED", sourceError.Operation+" failed", &hresult)
		return
	}
	if adapterError, ok := opcda.AsAdapterError(err); ok {
		status := stdhttp.StatusServiceUnavailable
		switch adapterError.Code {
		case opcda.CodeInvalidRequest, opcda.CodeRequestLimitExceeded, opcda.CodeItemIDTooLong:
			status = stdhttp.StatusBadRequest
		case opcda.CodeRuntimeDeadline:
			status = stdhttp.StatusGatewayTimeout
		}
		writeLayerError(w, status, "adapter", adapterError.Code, adapterError.Message, nil)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		writeLayerError(w, stdhttp.StatusGatewayTimeout, "adapter", opcda.CodeRuntimeDeadline, "request deadline exceeded", nil)
		return
	}
	writeLayerError(w, stdhttp.StatusInternalServerError, "adapter", "INTERNAL_ERROR", "internal adapter error", nil)
}
