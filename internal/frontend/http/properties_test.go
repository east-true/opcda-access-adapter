package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Go's JSON decoder matches field names case-insensitively, which is why every
// documented request field is checked against its exact spelling. A field that
// is not on that list is the one place the rule does not apply.
func TestPropertyRequestFieldsRequireTheirExactSpelling(t *testing.T) {
	runtime := statusRuntime{
		available:      []opcda.AvailableProperty{{ID: opcda.PropertyEUUnits}},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{},
	}
	config := Config{MaxBodyBytes: 4096, MaxConcurrent: 4, RequestDeadline: time.Second, MaxJSONDepth: 8}
	server := New(runtime, config)

	for _, body := range []string{
		`{"itemId":"Test/Float","PropertyIds":[100]}`,
		`{"itemId":"Test/Float","propertyids":[100]}`,
		`{"ItemId":"Test/Float","propertyIds":[100]}`,
	} {
		postJSON(t, server, "/v1/properties", body, http.StatusBadRequest)
	}
	// The documented spelling is accepted.
	postJSON(t, server, "/v1/properties", `{"itemId":"Test/Float","propertyIds":[100]}`, http.StatusOK)
}

// An available property always states a VARTYPE, and VT_EMPTY is zero, so the
// field is reported rather than inferred from a zero value. Inferring it made
// this frontend disagree with the gRPC one about the same source answer.
func TestAnAvailablePropertyAlwaysReportsItsStatedType(t *testing.T) {
	runtime := statusRuntime{
		available: []opcda.AvailableProperty{
			{ID: opcda.PropertyEUUnits, VarType: opcda.VTEmpty},
		},
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{},
	}
	config := Config{MaxBodyBytes: 4096, MaxConcurrent: 4, RequestDeadline: time.Second, MaxJSONDepth: 8}
	body := postJSON(t, New(runtime, config), "/v1/properties/available", `{"itemId":"Test/Float"}`, http.StatusOK)
	if !strings.Contains(body, `"VT_EMPTY"`) {
		t.Fatalf("a stated VT_EMPTY was dropped: %s", body)
	}
}
