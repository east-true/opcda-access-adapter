package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"unicode/utf8"

	"github.com/east-true/opcda-access-adapter/internal/frontend/dajson"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type readHTTPRequest struct {
	Source string `json:"source"`
	Items  []struct {
		ItemID exactJSONString `json:"itemId"`
	} `json:"items"`
}

type readHTTPResult = dajson.ReadResult

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
	fields := newReadResultFields(len(results))
	for index := range results {
		encoded[index] = encodeReadResult(results[index], &fields)
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

type exactJSONString = dajson.ExactString

// 공유 DA JSON 값 계약에 위임한다.
func validJSONSurrogates(data []byte) bool {
	return dajson.ValidSurrogates(data)
}

// 공유 DA JSON 값 계약에 위임한다.
func parseHexQuad(data []byte) (uint16, bool) {
	return dajson.ParseHexQuad(data)
}
func (s *Server) decodeRequestBody(w stdhttp.ResponseWriter, request *stdhttp.Request, target any) error {
	body, err := io.ReadAll(stdhttp.MaxBytesReader(w, request.Body, s.config.MaxBodyBytes))
	if err != nil {
		return err
	}
	if !utf8.Valid(body) {
		return fmt.Errorf("request body must be valid UTF-8")
	}
	if err := scanJSONStructure(body, s.config.MaxJSONDepth); err != nil {
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

// readResultFields holds the optional fields of a whole response. Each of them
// is a pointer in the JSON shape because absence has to be distinguishable from
// a zero, and taking the address of a local made one allocation per field per
// item -- five for every value in a batch, none of them outliving the response.
//
// Handing out pointers into slices allocated once per response is the same
// output for a fixed cost. The slices are sized up front and never grown, so no
// pointer handed out can be invalidated by a later item.
type readResultFields = dajson.ReadResultFields

// 공유 DA JSON 값 계약에 위임한다.
func newReadResultFields(count int) readResultFields {
	return dajson.NewReadResultFields(count)
}

// 공유 DA JSON 값 계약에 위임한다.
func encodeReadResult(result opcda.ReadResult, fields *readResultFields) readHTTPResult {
	return dajson.EncodeReadResult(result, fields)
}

// 공유 DA JSON 값 계약에 위임한다.
func encodeDAValue(varType opcda.DAVarType, value any) (json.RawMessage, string, error) {
	return dajson.EncodeValue(varType, value)
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

// 공유 DA JSON 값 계약에 위임한다.
func encodeDAArray(array opcda.DAArray) (json.RawMessage, error) {
	return dajson.EncodeArray(array)
}
