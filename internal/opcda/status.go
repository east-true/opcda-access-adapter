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
}

type RuntimeStatus struct {
	State                RuntimeState
	Source               SourceConfig
	ConnectionGeneration uint64
	ReconnectCount       uint64
	Capabilities         Capabilities
	WriteEnabled         bool
	QueueDepth           int
	DegradedReason       string
}
