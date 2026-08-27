//go:build windows

package opcda

import (
	"context"
	"errors"
	"testing"
)

func TestIOPCItemPropertiesIIDMatchesOfficialIDL(t *testing.T) {
	if got, want := iidIOPCItemProperties.String(), "{39C13A72-011E-11D0-9675-0020AFD8ADB3}"; got != want {
		t.Fatalf("IOPCItemProperties IID = %s, want %s", got, want)
	}
}

// A source that never connects still has to refuse a malformed request rather
// than queue it, so the request bounds are checked before the DA thread.
func TestItemPropertyRequestsAreBoundedBeforeReachingTheSource(t *testing.T) {
	runtime := &windowsRuntime{config: Config{Limits: DefaultLimits()}}
	ctx := context.Background()

	tooMany := make([]PropertyID, DefaultLimits().MaxItemProperties+1)
	for index := range tooMany {
		tooMany[index] = PropertyEUUnits
	}
	for _, testCase := range []struct {
		name    string
		request ItemPropertiesRequest
		want    ErrorCode
	}{
		{"empty ItemID", ItemPropertiesRequest{Properties: []PropertyID{PropertyEUUnits}}, CodeInvalidRequest},
		{"ItemID with NUL", ItemPropertiesRequest{ItemID: "Test\x00Float", Properties: []PropertyID{PropertyEUUnits}}, CodeInvalidRequest},
		{"no properties", ItemPropertiesRequest{ItemID: "Test/Float"}, CodeInvalidRequest},
		{"more properties than the bound", ItemPropertiesRequest{ItemID: "Test/Float", Properties: tooMany}, CodeRequestLimitExceeded},
		// The value, quality and timestamp have a path of their own that
		// carries them together. Asking for them here is refused rather than
		// answered a second, poorer way.
		{"the item value", ItemPropertiesRequest{ItemID: "Test/Float", Properties: []PropertyID{PropertyValue}}, CodeInvalidRequest},
		{"the item quality", ItemPropertiesRequest{ItemID: "Test/Float", Properties: []PropertyID{PropertyQuality}}, CodeInvalidRequest},
		{"the item timestamp", ItemPropertiesRequest{ItemID: "Test/Float", Properties: []PropertyID{PropertyTimestamp}}, CodeInvalidRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runtime.ItemProperties(ctx, testCase.request)
			var adapterErr *AdapterError
			if !errors.As(err, &adapterErr) {
				t.Fatalf("err = %v, want an adapter error", err)
			}
			if adapterErr.Code != testCase.want {
				t.Fatalf("code = %s, want %s", adapterErr.Code, testCase.want)
			}
		})
	}

	if _, err := runtime.AvailableItemProperties(ctx, ""); err == nil {
		t.Fatal("an empty ItemID was accepted for property discovery")
	}
}

// A source with no IOPCItemProperties answers PROPERTIES_UNSUPPORTED on both
// paths. It is a capability, not a failure: the source is working correctly.
func TestASourceWithoutItemPropertiesAnswersUnsupported(t *testing.T) {
	session := &daThreadSession{}
	if _, err := session.queryAvailableProperties("Test/Float", DefaultLimits()); !hasCode(err, CodePropertiesUnsupported) {
		t.Fatalf("discovery err = %v", err)
	}
	_, err := session.getItemProperties(
		ItemPropertiesRequest{ItemID: "Test/Float", Properties: []PropertyID{PropertyEUUnits}},
		DefaultLimits())
	if !hasCode(err, CodePropertiesUnsupported) {
		t.Fatalf("read err = %v", err)
	}
}

func hasCode(err error, code ErrorCode) bool {
	adapterErr, ok := AsAdapterError(err)
	return ok && adapterErr.Code == code
}
