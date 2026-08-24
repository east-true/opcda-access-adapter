//go:build !windows

package opcda

import (
	"context"
	"testing"
)

func TestLocalDetectionIsExplicitlyUnavailableOffWindows(t *testing.T) {
	_, err := DetectLocalServers(context.Background(), LocalDetectionLimits{})
	adapterError, ok := AsAdapterError(err)
	if !ok || adapterError.Code != CodeRuntimeUnavailable {
		t.Fatalf("error = %#v", err)
	}
}
