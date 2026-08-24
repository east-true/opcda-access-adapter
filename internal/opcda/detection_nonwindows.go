//go:build !windows

package opcda

import "context"

func detectLocalServers(context.Context, LocalDetectionLimits) ([]DetectedLocalServer, error) {
	return nil, NewAdapterError(CodeRuntimeUnavailable, "local OPC DA detection requires Windows")
}
