package opcda

type RuntimeState string

const (
	RuntimeStateStarting            RuntimeState = "starting"
	RuntimeStateNotConfigured       RuntimeState = "not_configured"
	RuntimeStateConnecting          RuntimeState = "connecting"
	RuntimeStateConnected           RuntimeState = "connected"
	RuntimeStateDisconnected        RuntimeState = "disconnected"
	RuntimeStateReconnecting        RuntimeState = "reconnecting"
	RuntimeStateDegraded            RuntimeState = "degraded"
	RuntimeStateUnsupportedPlatform RuntimeState = "unsupported_platform"
	RuntimeStateStopping            RuntimeState = "stopping"
	RuntimeStateStopped             RuntimeState = "stopped"
)

type SourceConfig struct {
	ProgID string
	CLSID  string
}

type Capabilities struct {
	Browse    string
	Read      bool
	Write     bool
	Subscribe bool
	// Properties reports whether the source implements IOPCItemProperties,
	// which is what OPC 10000-8 Table A.1 is mapped from. Like Browse it is
	// "supported", "unsupported" or "unavailable" rather than a bool, because
	// a source that has not been asked yet is not the same as one that said no.
	Properties string
}

// SourceDiagnostic is bounded operational metadata about the most recent
// source-level failure. It never contains an ItemID or process value.
type SourceDiagnostic struct {
	Operation      string
	HRESULT        HRESULT
	HRESULTPresent bool
}

type RuntimeStatus struct {
	State                RuntimeState
	Source               SourceConfig
	ConnectionGeneration uint64
	ReconnectCount       uint64
	Capabilities         Capabilities
	WriteEnabled         bool
	QueueDepth           int
	SubscriptionCount    int
	DegradedReason       string
	LastSourceError      SourceDiagnostic
	LastSourceErrorSet   bool
}
