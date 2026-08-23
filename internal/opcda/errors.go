package opcda

import "errors"

type ErrorCode string

const (
	CodeInvalidRequest            ErrorCode = "INVALID_REQUEST"
	CodeRequestBodyTooLarge       ErrorCode = "REQUEST_BODY_TOO_LARGE"
	CodeRequestLimitExceeded      ErrorCode = "REQUEST_LIMIT_EXCEEDED"
	CodeQueueFull                 ErrorCode = "QUEUE_FULL"
	CodeRuntimeUnavailable        ErrorCode = "RUNTIME_UNAVAILABLE"
	CodeRuntimeDeadline           ErrorCode = "RUNTIME_DEADLINE_EXCEEDED"
	CodeWriteDisabled             ErrorCode = "WRITE_DISABLED"
	CodeBrowseUnsupported         ErrorCode = "BROWSE_UNSUPPORTED"
	CodeBrowseResultLimitExceeded ErrorCode = "BROWSE_RESULT_LIMIT_EXCEEDED"
	CodeUnsupportedVarType        ErrorCode = "UNSUPPORTED_VARTYPE"
	CodeInvalidValue              ErrorCode = "INVALID_VALUE"
	CodeItemIDTooLong             ErrorCode = "ITEM_ID_TOO_LONG"
	CodeBSTRTooLong               ErrorCode = "BSTR_TOO_LONG"
	CodeRegisteredItemLimit       ErrorCode = "REGISTERED_ITEM_LIMIT_EXCEEDED"
)

// AdapterError identifies an adapter/runtime failure without replacing source
// HRESULTs. Source item failures remain represented in result entries.
type AdapterError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *AdapterError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *AdapterError) Unwrap() error { return e.Cause }

func NewAdapterError(code ErrorCode, message string) *AdapterError {
	return &AdapterError{Code: code, Message: message}
}

func AsAdapterError(err error) (*AdapterError, bool) {
	var target *AdapterError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// SourceError is a method-level COM/OPC DA failure. Per-item HRESULTs remain
// in batch results and are not promoted to this request-level error.
type SourceError struct {
	Operation string
	HRESULT   HRESULT
}

func (e *SourceError) Error() string {
	return e.Operation + " failed: " + e.HRESULT.Hex()
}

func AsSourceError(err error) (*SourceError, bool) {
	var target *SourceError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
