//go:build !windows

package opcda

import "context"

// New starts a non-Windows status-only runtime for development and HTTP
// contract tests. It never simulates a DA server or returns process data.
func New(config Config) (Runtime, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &unsupportedRuntime{config: config}, nil
}

type unsupportedRuntime struct {
	config Config
}

func (r *unsupportedRuntime) Status(context.Context) RuntimeStatus {
	state := RuntimeStateUnsupportedPlatform
	if r.config.Source.ProgID == "" && r.config.Source.CLSID == "" {
		state = RuntimeStateNotConfigured
	}
	return RuntimeStatus{
		State:        state,
		Source:       r.config.Source,
		WriteEnabled: r.config.WriteEnabled,
		Capabilities: Capabilities{
			Browse: "unavailable",
			Read:   false,
			Write:  false,
		},
	}
}

func (*unsupportedRuntime) Browse(context.Context, BrowseRequest) (BrowseResult, error) {
	return BrowseResult{}, NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime requires Windows")
}

func (*unsupportedRuntime) ReadBatch(context.Context, ReadRequest) ([]ReadResult, error) {
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime requires Windows")
}

func (r *unsupportedRuntime) WriteBatch(context.Context, []WriteItem) ([]WriteResult, error) {
	if !r.config.WriteEnabled {
		return nil, NewAdapterError(CodeWriteDisabled, "write is disabled")
	}
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime requires Windows")
}

func (*unsupportedRuntime) Shutdown(context.Context) error { return nil }
