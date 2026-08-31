package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

// validateJSONStructure rejects ambiguous duplicate object keys and bounds
// nesting before the endpoint schema decoder allocates request structures.
// Keys are compared after JSON unescaping, so alternate escape spellings do
// not bypass duplicate detection.
func validateJSONStructure(body []byte, maximumDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0, maximumDepth); err != nil {
		return err
	}
	if _, err := decoder.Token(); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("request body must contain exactly one JSON value")
}

func scanJSONValue(decoder *json.Decoder, depth, maximumDepth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	if depth >= maximumDepth {
		return &requestBodyError{
			code:    opcda.CodeJSONDepthLimitExceeded,
			message: "request JSON exceeds the configured nesting-depth limit",
		}
	}

	if delimiter == '{' {
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			for _, canonical := range canonicalRequestFields {
				if key != canonical && strings.EqualFold(key, canonical) {
					return &requestBodyError{
						code:    opcda.CodeInvalidRequest,
						message: "request JSON field names must use the exact documented spelling",
					}
				}
			}
			if _, exists := keys[key]; exists {
				return &requestBodyError{
					code:    opcda.CodeDuplicateJSONField,
					message: "request JSON contains a duplicate object field",
				}
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder, depth+1, maximumDepth); err != nil {
				return err
			}
		}
	} else {
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1, maximumDepth); err != nil {
				return err
			}
		}
	}

	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	expected := json.Delim('}')
	if delimiter == '[' {
		expected = ']'
	}
	if closing != expected {
		return fmt.Errorf("unexpected JSON closing delimiter %q", closing)
	}
	return nil
}
