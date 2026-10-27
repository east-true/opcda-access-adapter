package dajson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const arrayValueEncoding = "array"

type ExactString string

type ReadResult struct {
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

type ReadResultFields struct {
	types      []opcda.DAVarTypeInfo
	canonical  []opcda.DAVarTypeInfo
	hresults   []opcda.HRESULTValue
	qualities  []uint16
	timestamps []string
	scratch    []byte
	next       int
}

type jsonArrayDimension struct {
	LowerBound int32  `json:"lowerBound"`
	Length     uint32 `json:"length"`
}
type jsonArrayValue struct {
	ElementDataType opcda.DAVarTypeInfo  `json:"elementDataType"`
	Dimensions      []jsonArrayDimension `json:"dimensions"`
	Elements        []json.RawMessage    `json:"elements"`
}

// 문자열의 UTF-16 쌍과 원래 문자를 보존하며 JSON을 해석한다.
func (value *ExactString) UnmarshalJSON(data []byte) error {
	// 1. surrogate 쌍과 이스케이프 없는 문자열을 먼저 확인한다.
	if !ValidSurrogates(data) {
		return fmt.Errorf("JSON string contains an unpaired UTF-16 surrogate")
	}
	if unquoted, ok := unquotedJSONString(data); ok {
		*value = ExactString(unquoted)
		return nil
	}
	// 2. 이스케이프가 있는 문자열은 표준 JSON 해석에 맡긴다.
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*value = ExactString(decoded)
	return nil
}

// 이스케이프가 없는 유효한 UTF-8 문자열을 빠르게 읽는다.
func unquotedJSONString(data []byte) (string, bool) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return "", false
	}
	body := data[1 : len(data)-1]
	for _, character := range body {
		if character == '\\' || character == '"' || character < 0x20 {
			return "", false
		}
	}
	if !utf8.Valid(body) {
		return "", false
	}
	return string(body), true
}

// JSON 문자열의 UTF-16 surrogate 쌍을 검증한다.
func ValidSurrogates(data []byte) bool {
	for index := 0; index+1 < len(data); {
		if data[index] != '\\' {
			index++
			continue
		}
		if data[index+1] != 'u' {
			index += 2
			continue
		}
		code, ok := ParseHexQuad(data[index+2:])
		if !ok {
			return true
		}
		if 0xD800 <= code && code <= 0xDBFF {
			if index+12 > len(data) || data[index+6] != '\\' || data[index+7] != 'u' {
				return false
			}
			low, ok := ParseHexQuad(data[index+8:])
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

// 네 자리 16진수를 UTF-16 코드 단위로 읽는다.
func ParseHexQuad(data []byte) (uint16, bool) {
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

// 한 응답의 선택 필드 저장 공간을 한 번 할당한다.
func NewReadResultFields(count int) ReadResultFields {
	return ReadResultFields{
		types:      make([]opcda.DAVarTypeInfo, count),
		canonical:  make([]opcda.DAVarTypeInfo, count),
		hresults:   make([]opcda.HRESULTValue, count),
		qualities:  make([]uint16, count),
		timestamps: make([]string, count),
		scratch:    make([]byte, 0, 64),
	}
}

// DA 항목 결과와 메타데이터를 손실 없는 JSON 표현으로 변환한다.
func EncodeReadResult(result opcda.ReadResult, fields *ReadResultFields) ReadResult {
	// 1. 값 유무와 독립적인 타입·접근 권한·HRESULT를 구성한다.
	slot := fields.next
	fields.next++
	encoded := ReadResult{
		ItemID:       string(result.ItemID),
		ErrorCode:    result.ErrorCode,
		AccessRights: result.AccessRights,
	}
	if result.VarType != nil {
		fields.types[slot] = result.VarType.Information()
		encoded.DataType = &fields.types[slot]
	}
	if result.CanonicalType != nil {
		fields.canonical[slot] = result.CanonicalType.Information()
		encoded.CanonicalDataType = &fields.canonical[slot]
	}
	if result.HRESULTPresent {
		fields.hresults[slot] = result.HRESULT.Representation()
		encoded.HRESULT = &fields.hresults[slot]
	}
	if result.Value == nil || result.ErrorCode != "" || !result.HRESULTPresent || result.HRESULT.Failed() {
		return encoded
	}

	// 2. 원본 값을 손실 없이 표현하고 시각의 존재 여부를 별도로 유지한다.
	value, encoding, err := EncodeValue(result.Value.VarType, result.Value.Value)
	if err != nil {
		encoded.ErrorCode = string(opcda.CodeInvalidValue)
		if adapterErr, ok := opcda.AsAdapterError(err); ok {
			encoded.ErrorCode = string(adapterErr.Code)
		}
		return encoded
	}
	var timestamp *string
	if result.Value.TimestampPresent {
		utc := result.Value.Timestamp.UTC()
		if year := utc.Year(); year < 0 || year >= 10000 {
			encoded.ErrorCode = string(opcda.CodeInvalidValue)
			return encoded
		}
		fields.timestamps[slot] = string(utc.AppendFormat(fields.scratch[:0], time.RFC3339Nano))
		timestamp = &fields.timestamps[slot]
	}
	encoded.OK = true
	encoded.Value = value
	encoded.ValueEncoding = encoding
	fields.qualities[slot] = result.Value.QualityRaw
	encoded.Quality = &fields.qualities[slot]
	encoded.TimestampPresent = result.Value.TimestampPresent
	encoded.Timestamp = timestamp
	return encoded
}

// VARTYPE에 맞는 스칼라 또는 배열의 JSON 표현과 인코딩을 반환한다.
func EncodeValue(varType opcda.DAVarType, value any) (json.RawMessage, string, error) {
	// 1. 배열과 간접 참조를 먼저 구분한다.
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
		encoded, err := EncodeArray(array)
		if err != nil {
			return nil, "", err
		}
		return encoded, arrayValueEncoding, nil
	}
	// 2. 실제 Go 값의 폭과 VARTYPE이 일치할 때만 JSON 표현을 만든다.
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

// JSON 숫자로 표현할 수 없는 실수의 명시적 이름을 반환한다.
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

// 배열의 차원·하한·길이와 마지막 차원이 빠르게 변하는 원소 순서를 보존한다.
func EncodeArray(array opcda.DAArray) (json.RawMessage, error) {
	// 1. 각 차원의 원래 하한과 길이를 유지한다.
	dimensions := make([]jsonArrayDimension, len(array.Dimensions))
	for index, dimension := range array.Dimensions {
		dimensions[index] = jsonArrayDimension{
			LowerBound: dimension.LowerBound,
			Length:     dimension.Length,
		}
	}
	// 2. 원소 순서를 유지하며 스칼라와 같은 타입 규칙을 적용한다.
	elements := make([]json.RawMessage, len(array.Elements))
	for index, element := range array.Elements {
		encoded, _, err := EncodeValue(array.ElementType, element)
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

// 원래 오류 코드에 실패한 배열 원소의 위치를 덧붙인다.
func arrayElementError(index int, err error) error {
	adapterErr, ok := opcda.AsAdapterError(err)
	if !ok {
		return fmt.Errorf("array element %d: %w", index, err)
	}
	return opcda.NewAdapterError(adapterErr.Code,
		fmt.Sprintf("array element %d: %s", index, adapterErr.Message))
}

// 명시한 VARTYPE과 인코딩에 맞춰 Write 값을 엄격하게 해석한다.
func DecodeValue(varType opcda.DAVarType, encoding string, raw json.RawMessage) (any, error) {
	// 1. 명시한 인코딩이 배열·특수 실수·스칼라와 일치하는지 확인한다.
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

	// 2. 숫자 폭과 JSON 타입을 보존하며 값을 해석한다.
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

// 명시적으로 표시된 비유한 실수를 원래 실수 폭으로 해석한다.
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

// 원문 문자열의 문자와 단일 JSON 값 조건을 검증한다.
func decodeExactString(raw json.RawMessage) (string, error) {
	var value ExactString
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return "", err
	}
	return string(value), nil
}

// 숫자 해석 오류를 값 검증 오류로 변환한다.
func numericWriteError(varType opcda.DAVarType, err error) error {
	if err != nil {
		return invalidWriteValue(varType)
	}
	return nil
}

// 값을 노출하지 않고 VARTYPE 불일치를 보고한다.
func invalidWriteValue(varType opcda.DAVarType) error {
	return opcda.NewAdapterError(opcda.CodeInvalidValue, fmt.Sprintf("value is not losslessly representable as %s", varType))
}

// 클라이언트가 제공한 차원과 원소를 검증해 DA 배열을 구성한다.
func decodeWriteArray(varType opcda.DAVarType, raw json.RawMessage) (any, error) {
	// 1. 원소 타입과 차원 구조를 엄격한 JSON으로 읽는다.
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

	// 2. 선언한 차원과 원소가 서로 일치하는 DA 배열을 구성한다.
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
	if err := array.MatchesItsShape(); err != nil {
		return nil, err
	}
	return array, nil
}

// 배열 원소를 스칼라와 같은 타입 규칙으로 해석한다.
func decodeWriteArrayElement(elementType opcda.DAVarType, raw json.RawMessage) (any, error) {
	if elementType == opcda.VTR4 || elementType == opcda.VTR8 {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return decodeSpecialFloat(elementType, raw)
		}
	}
	return DecodeValue(elementType, "json", raw)
}

// 추가 JSON 값이나 후행 쓰레기 데이터가 없는지 확인한다.
func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return fmt.Errorf("request body must contain exactly one JSON value")
}
