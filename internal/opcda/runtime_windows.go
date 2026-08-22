//go:build windows

package opcda

import "context"

// New is intentionally truthful during the bootstrap phase: it exposes the
// lifecycle/status surface but does not pretend a configured OPC DA server is
// connected before the dedicated COM runtime has been initialized.
func New(config Config) (Runtime, error) {
	return &bootstrapRuntime{config: config}, nil
}

type bootstrapRuntime struct {
	config Config
}

func (r *bootstrapRuntime) Status(context.Context) RuntimeStatus {
	state := RuntimeStateStopped
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

func (*bootstrapRuntime) Browse(context.Context, BrowseRequest) (BrowseResult, error) {
	return BrowseResult{}, NewAdapterError(CodeRuntimeUnavailable, "OPC DA COM runtime is not initialized")
}

func (*bootstrapRuntime) ReadBatch(context.Context, ReadRequest) ([]ReadResult, error) {
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA COM runtime is not initialized")
}

func (r *bootstrapRuntime) WriteBatch(context.Context, []WriteItem) ([]WriteResult, error) {
	if !r.config.WriteEnabled {
		return nil, NewAdapterError(CodeWriteDisabled, "write is disabled")
	}
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA COM runtime is not initialized")
}

func (*bootstrapRuntime) Shutdown(context.Context) error { return nil }
