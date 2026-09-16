package http

import (
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type requestBodyError struct {
	code    opcda.ErrorCode
	message string
}

var canonicalRequestFields = [...]string{
	"source", "items", "itemId", "path", "filter", "dataType", "valueEncoding", "value",
	"propertyIds",
}

func (e *requestBodyError) Error() string {
	return e.message
}
