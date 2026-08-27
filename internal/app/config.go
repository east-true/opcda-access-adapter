package app

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type Config struct {
	Source             opcda.SourceConfig
	Frontend           FrontendType
	HTTPListenAddress  string
	GRPCListenAddress  string
	OPCUAListenAddress string
	// OPCUA carries the endpoint description this adapter publishes. Its
	// security policy and transport profile URIs have no defaults: the known
	// URIs are defined by OPC 10000-7, and a server publishing a wrong one
	// would be unusable by a real client.
	OPCUA                  OPCUAFrontendConfig
	WriteEnabled           bool
	MaxHTTPBodyBytes       int64
	MaxHTTPConnections     int
	MaxConcurrentRequests  int
	MaxHTTPHeaderBytes     int
	MaxJSONDepth           int
	MaxGRPCReceiveBytes    int
	MaxGRPCSendBytes       int
	MaxGRPCConnections     int
	MaxConcurrentGRPCRPCs  int
	MaxGRPCStreams         uint32
	MaxGRPCMetadataBytes   uint32
	GRPCConnectionTimeout  time.Duration
	GRPCMaxConnectionIdle  time.Duration
	GRPCMaxConnectionAge   time.Duration
	GRPCMaxConnectionGrace time.Duration
	GRPCKeepaliveMinTime   time.Duration
	HTTPReadHeaderTimeout  time.Duration
	HTTPReadTimeout        time.Duration
	HTTPWriteTimeout       time.Duration
	HTTPIdleTimeout        time.Duration
	RequestDeadline        time.Duration
	Runtime                opcda.Config
}

type FrontendType string

const (
	FrontendHTTP  FrontendType = "http"
	FrontendGRPC  FrontendType = "grpc"
	FrontendOPCUA FrontendType = "opcua"
)

const (
	maximumInflightHTTPBodyBytes    = uint64(256 << 20)
	maximumInflightHeaderBytes      = uint64(64 << 20)
	maximumInflightGRPCReceiveBytes = uint64(256 << 20)
	maximumInflightGRPCSendBytes    = uint64(256 << 20)
	maximumInflightGRPCMetadata     = uint64(64 << 20)
)

func DefaultConfig() Config {
	limits := opcda.DefaultLimits()
	return Config{
		Frontend:               FrontendHTTP,
		HTTPListenAddress:      "127.0.0.1:8080",
		GRPCListenAddress:      "127.0.0.1:50051",
		OPCUAListenAddress:     "127.0.0.1:4840",
		MaxHTTPBodyBytes:       1 << 20,
		MaxHTTPConnections:     64,
		MaxConcurrentRequests:  32,
		MaxHTTPHeaderBytes:     32 << 10,
		MaxJSONDepth:           64,
		MaxGRPCReceiveBytes:    1 << 20,
		MaxGRPCSendBytes:       4 << 20,
		MaxGRPCConnections:     16,
		MaxConcurrentGRPCRPCs:  32,
		MaxGRPCStreams:         16,
		MaxGRPCMetadataBytes:   32 << 10,
		GRPCConnectionTimeout:  5 * time.Second,
		GRPCMaxConnectionIdle:  2 * time.Minute,
		GRPCMaxConnectionAge:   30 * time.Minute,
		GRPCMaxConnectionGrace: 30 * time.Second,
		GRPCKeepaliveMinTime:   30 * time.Second,
		HTTPReadHeaderTimeout:  5 * time.Second,
		HTTPReadTimeout:        15 * time.Second,
		HTTPWriteTimeout:       15 * time.Second,
		HTTPIdleTimeout:        30 * time.Second,
		RequestDeadline:        10 * time.Second,
		Runtime: opcda.Config{
			Limits:           limits,
			ReconnectInitial: time.Second,
			ReconnectMax:     30 * time.Second,
			COMCallWatchdog:  30 * time.Second,
		},
	}
}

// LoadConfig reads only bounded, explicit v0 settings. It intentionally has no
// configuration surface for sources other than the single local OPC DA server.
func LoadConfig() (Config, error) {
	config := DefaultConfig()
	config.Source.ProgID = os.Getenv("OPCDA_SOURCE_PROG_ID")
	config.Source.CLSID = os.Getenv("OPCDA_SOURCE_CLSID")
	config.Frontend = FrontendType(valueOrDefault("OPCDA_FRONTEND", string(config.Frontend)))
	config.HTTPListenAddress = valueOrDefault("OPCDA_HTTP_LISTEN", config.HTTPListenAddress)
	config.GRPCListenAddress = valueOrDefault("OPCDA_GRPC_LISTEN", config.GRPCListenAddress)
	config.OPCUAListenAddress = valueOrDefault("OPCDA_OPCUA_LISTEN", config.OPCUAListenAddress)
	config.OPCUA.EndpointURL = valueOrDefault("OPCDA_OPCUA_ENDPOINT_URL", config.OPCUA.EndpointURL)
	config.OPCUA.ApplicationURI = valueOrDefault("OPCDA_OPCUA_APPLICATION_URI", config.OPCUA.ApplicationURI)
	config.OPCUA.ProductURI = valueOrDefault("OPCDA_OPCUA_PRODUCT_URI", config.OPCUA.ProductURI)
	config.OPCUA.ApplicationName = valueOrDefault("OPCDA_OPCUA_APPLICATION_NAME", config.OPCUA.ApplicationName)
	config.OPCUA.SecurityPolicyURI = valueOrDefault("OPCDA_OPCUA_SECURITY_POLICY_URI", config.OPCUA.SecurityPolicyURI)
	config.OPCUA.TransportProfileURI = valueOrDefault("OPCDA_OPCUA_TRANSPORT_PROFILE_URI", config.OPCUA.TransportProfileURI)
	config.OPCUA.NamespaceURI = valueOrDefault("OPCDA_OPCUA_NAMESPACE_URI", config.OPCUA.NamespaceURI)
	config.OPCUA.SourceFolderName = valueOrDefault("OPCDA_OPCUA_SOURCE_FOLDER", config.OPCUA.SourceFolderName)

	var err error
	if config.WriteEnabled, err = boolEnv("OPCDA_WRITE_ENABLED", false); err != nil {
		return Config{}, err
	}
	if config.MaxHTTPBodyBytes, err = int64Env("OPCDA_MAX_HTTP_BODY_BYTES", config.MaxHTTPBodyBytes); err != nil {
		return Config{}, err
	}
	if config.MaxHTTPConnections, err = intEnv("OPCDA_MAX_HTTP_CONNECTIONS", config.MaxHTTPConnections); err != nil {
		return Config{}, err
	}
	if config.MaxConcurrentRequests, err = intEnv("OPCDA_MAX_CONCURRENT_REQUESTS", config.MaxConcurrentRequests); err != nil {
		return Config{}, err
	}
	if config.MaxHTTPHeaderBytes, err = intEnv("OPCDA_MAX_HTTP_HEADER_BYTES", config.MaxHTTPHeaderBytes); err != nil {
		return Config{}, err
	}
	if config.MaxJSONDepth, err = intEnv("OPCDA_MAX_JSON_DEPTH", config.MaxJSONDepth); err != nil {
		return Config{}, err
	}
	if config.MaxGRPCReceiveBytes, err = intEnv("OPCDA_MAX_GRPC_RECEIVE_BYTES", config.MaxGRPCReceiveBytes); err != nil {
		return Config{}, err
	}
	if config.MaxGRPCSendBytes, err = intEnv("OPCDA_MAX_GRPC_SEND_BYTES", config.MaxGRPCSendBytes); err != nil {
		return Config{}, err
	}
	if config.MaxGRPCConnections, err = intEnv("OPCDA_MAX_GRPC_CONNECTIONS", config.MaxGRPCConnections); err != nil {
		return Config{}, err
	}
	if config.MaxConcurrentGRPCRPCs, err = intEnv("OPCDA_MAX_CONCURRENT_GRPC_RPCS", config.MaxConcurrentGRPCRPCs); err != nil {
		return Config{}, err
	}
	if config.MaxGRPCStreams, err = uint32Env("OPCDA_MAX_GRPC_STREAMS", config.MaxGRPCStreams); err != nil {
		return Config{}, err
	}
	if config.MaxGRPCMetadataBytes, err = uint32Env("OPCDA_MAX_GRPC_METADATA_BYTES", config.MaxGRPCMetadataBytes); err != nil {
		return Config{}, err
	}
	if config.GRPCConnectionTimeout, err = durationEnv("OPCDA_GRPC_CONNECTION_TIMEOUT", config.GRPCConnectionTimeout); err != nil {
		return Config{}, err
	}
	if config.GRPCMaxConnectionIdle, err = durationEnv("OPCDA_GRPC_MAX_CONNECTION_IDLE", config.GRPCMaxConnectionIdle); err != nil {
		return Config{}, err
	}
	if config.GRPCMaxConnectionAge, err = durationEnv("OPCDA_GRPC_MAX_CONNECTION_AGE", config.GRPCMaxConnectionAge); err != nil {
		return Config{}, err
	}
	if config.GRPCMaxConnectionGrace, err = durationEnv("OPCDA_GRPC_MAX_CONNECTION_AGE_GRACE", config.GRPCMaxConnectionGrace); err != nil {
		return Config{}, err
	}
	if config.GRPCKeepaliveMinTime, err = durationEnv("OPCDA_GRPC_KEEPALIVE_MIN_TIME", config.GRPCKeepaliveMinTime); err != nil {
		return Config{}, err
	}
	if config.HTTPReadHeaderTimeout, err = durationEnv("OPCDA_HTTP_READ_HEADER_TIMEOUT", config.HTTPReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPReadTimeout, err = durationEnv("OPCDA_HTTP_READ_TIMEOUT", config.HTTPReadTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPWriteTimeout, err = durationEnv("OPCDA_HTTP_WRITE_TIMEOUT", config.HTTPWriteTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPIdleTimeout, err = durationEnv("OPCDA_HTTP_IDLE_TIMEOUT", config.HTTPIdleTimeout); err != nil {
		return Config{}, err
	}
	if config.RequestDeadline, err = durationEnv("OPCDA_REQUEST_DEADLINE", config.RequestDeadline); err != nil {
		return Config{}, err
	}
	if config.Runtime.ReconnectInitial, err = durationEnv("OPCDA_RECONNECT_INITIAL", config.Runtime.ReconnectInitial); err != nil {
		return Config{}, err
	}
	if config.Runtime.ReconnectMax, err = durationEnv("OPCDA_RECONNECT_MAX", config.Runtime.ReconnectMax); err != nil {
		return Config{}, err
	}
	if config.Runtime.COMCallWatchdog, err = durationEnv("OPCDA_COM_CALL_WATCHDOG", config.Runtime.COMCallWatchdog); err != nil {
		return Config{}, err
	}
	limitSettings := []struct {
		key    string
		target *int
	}{
		{key: "OPCDA_COMMAND_QUEUE", target: &config.Runtime.Limits.CommandQueue},
		{key: "OPCDA_MAX_READ_ITEMS", target: &config.Runtime.Limits.MaxReadItems},
		{key: "OPCDA_MAX_WRITE_ITEMS", target: &config.Runtime.Limits.MaxWriteItems},
		{key: "OPCDA_MAX_BROWSE_ENTRIES", target: &config.Runtime.Limits.MaxBrowseEntries},
		{key: "OPCDA_MAX_BROWSE_DEPTH", target: &config.Runtime.Limits.MaxBrowseDepth},
		{key: "OPCDA_MAX_REGISTERED_ITEMS", target: &config.Runtime.Limits.MaxRegisteredItems},
		{key: "OPCDA_MAX_ITEM_ID_BYTES", target: &config.Runtime.Limits.MaxItemIDBytes},
		{key: "OPCDA_MAX_BSTR_CODE_UNITS", target: &config.Runtime.Limits.MaxBSTRCodeUnits},
		{key: "OPCDA_MAX_SUBSCRIPTIONS", target: &config.Runtime.Limits.MaxSubscriptions},
		{key: "OPCDA_MAX_SUBSCRIPTION_ITEMS", target: &config.Runtime.Limits.MaxSubscriptionItems},
		{key: "OPCDA_MAX_ITEM_PROPERTIES", target: &config.Runtime.Limits.MaxItemProperties},
	}
	for _, setting := range limitSettings {
		if *setting.target, err = intEnv(setting.key, *setting.target); err != nil {
			return Config{}, err
		}
	}

	if err := config.finalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) finalizeAndValidate() error {
	if config.Source.ProgID != "" && config.Source.CLSID != "" {
		return fmt.Errorf("set exactly one of OPCDA_SOURCE_PROG_ID and OPCDA_SOURCE_CLSID")
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxHTTPConnections <= 0 || config.MaxConcurrentRequests <= 0 ||
		config.MaxHTTPHeaderBytes <= 0 || config.MaxJSONDepth <= 0 || config.HTTPReadHeaderTimeout <= 0 || config.HTTPReadTimeout <= 0 ||
		config.HTTPWriteTimeout <= 0 || config.HTTPIdleTimeout <= 0 || config.RequestDeadline <= 0 {
		return fmt.Errorf("HTTP bounds and timeouts must be positive")
	}
	switch config.Frontend {
	case FrontendHTTP, FrontendGRPC, FrontendOPCUA:
	default:
		return fmt.Errorf("frontend must be http, grpc, or opcua")
	}
	if config.MaxGRPCReceiveBytes <= 0 || config.MaxGRPCSendBytes <= 0 || config.MaxGRPCConnections <= 0 ||
		config.MaxConcurrentGRPCRPCs <= 0 || config.MaxGRPCStreams == 0 || config.MaxGRPCMetadataBytes == 0 ||
		config.GRPCConnectionTimeout <= 0 || config.GRPCMaxConnectionIdle <= 0 || config.GRPCMaxConnectionAge <= 0 ||
		config.GRPCMaxConnectionGrace <= 0 || config.GRPCKeepaliveMinTime <= 0 {
		return fmt.Errorf("gRPC bounds must be positive")
	}
	if config.MaxGRPCReceiveBytes > 64<<20 || config.MaxGRPCSendBytes > 64<<20 || config.MaxGRPCConnections > 2048 ||
		config.MaxConcurrentGRPCRPCs > 1024 || config.MaxGRPCStreams > 4096 || config.MaxGRPCMetadataBytes > 1<<20 {
		return fmt.Errorf("gRPC bound exceeds the hard ceiling")
	}
	if config.GRPCConnectionTimeout > 24*time.Hour || config.GRPCMaxConnectionIdle > 24*time.Hour ||
		config.GRPCMaxConnectionAge > 24*time.Hour || config.GRPCMaxConnectionGrace > 24*time.Hour ||
		config.GRPCKeepaliveMinTime > 24*time.Hour || config.GRPCMaxConnectionGrace > config.GRPCMaxConnectionAge {
		return fmt.Errorf("gRPC timeout exceeds the hard ceiling or age grace exceeds age")
	}
	if uint64(config.MaxGRPCReceiveBytes)*uint64(config.MaxGRPCConnections)*uint64(config.MaxGRPCStreams) > maximumInflightGRPCReceiveBytes {
		return fmt.Errorf("configured concurrent gRPC receive budget exceeds the hard ceiling")
	}
	if uint64(config.MaxGRPCSendBytes)*uint64(config.MaxConcurrentGRPCRPCs) > maximumInflightGRPCSendBytes {
		return fmt.Errorf("configured concurrent gRPC send budget exceeds the hard ceiling")
	}
	if uint64(config.MaxGRPCMetadataBytes)*uint64(config.MaxGRPCConnections)*uint64(config.MaxGRPCStreams) > maximumInflightGRPCMetadata {
		return fmt.Errorf("configured concurrent gRPC metadata budget exceeds the hard ceiling")
	}
	if config.MaxHTTPBodyBytes > 64<<20 || config.MaxHTTPConnections > 2048 || config.MaxConcurrentRequests > 1024 ||
		config.MaxHTTPHeaderBytes > 1<<20 || config.MaxJSONDepth > 256 || config.HTTPReadHeaderTimeout > 24*time.Hour ||
		config.HTTPReadTimeout > 24*time.Hour || config.HTTPWriteTimeout > 24*time.Hour ||
		config.HTTPIdleTimeout > 24*time.Hour || config.RequestDeadline > 24*time.Hour {
		return fmt.Errorf("HTTP bound or timeout exceeds the v0 hard ceiling")
	}
	if uint64(config.MaxHTTPBodyBytes)*uint64(config.MaxConcurrentRequests) > maximumInflightHTTPBodyBytes {
		return fmt.Errorf("configured concurrent HTTP body budget exceeds the v0 hard ceiling")
	}
	if uint64(config.MaxHTTPHeaderBytes)*uint64(config.MaxHTTPConnections) > maximumInflightHeaderBytes {
		return fmt.Errorf("configured concurrent HTTP header budget exceeds the v0 hard ceiling")
	}
	listenAddress := config.HTTPListenAddress
	listenSetting := "OPCDA_HTTP_LISTEN"
	switch config.Frontend {
	case FrontendGRPC:
		listenAddress = config.GRPCListenAddress
		listenSetting = "gRPC listen address"
	case FrontendOPCUA:
		listenAddress = config.OPCUAListenAddress
		listenSetting = "OPC UA listen address"
		if err := config.OPCUA.validate(); err != nil {
			return err
		}
	}
	_, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", listenSetting, err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("%s port must be numeric and between 0 and 65535: %w", listenSetting, err)
	}
	if config.Runtime.ReconnectInitial <= 0 || config.Runtime.ReconnectMax <= 0 || config.Runtime.COMCallWatchdog <= 0 {
		return fmt.Errorf("reconnect and COM watchdog durations must be positive")
	}
	if config.Runtime.ReconnectInitial > config.Runtime.ReconnectMax {
		return fmt.Errorf("OPCDA_RECONNECT_INITIAL must not exceed OPCDA_RECONNECT_MAX")
	}
	if err := config.Runtime.Limits.ValidateForConfiguration(); err != nil {
		return err
	}

	config.Runtime.Source = config.Source
	config.Runtime.WriteEnabled = config.WriteEnabled
	if err := config.Runtime.ValidateForConfiguration(); err != nil {
		return err
	}
	return nil
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func uint32Env(key string, fallback uint32) (uint32, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return uint32(parsed), nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

// OPCUAFrontendConfig describes the single endpoint the OPC UA frontend
// publishes.
//
// SecurityPolicyURI and TransportProfileURI have no defaults. The known URIs
// are defined by OPC 10000-7, which this project has not transcribed, and a
// server that published a wrong one would be unusable by a real client. They
// are supplied by the operator rather than guessed.
type OPCUAFrontendConfig struct {
	EndpointURL         string
	ApplicationURI      string
	ProductURI          string
	ApplicationName     string
	SecurityPolicyURI   string
	TransportProfileURI string
	NamespaceURI        string
	SourceFolderName    string
	AnonymousPolicyID   string
	// SoftwareVersion and BuildNumber are what the standard Server BuildInfo
	// reports. They describe the adapter, so they come from the build rather
	// than from configuration, and stay empty on a build that does not set
	// them: an invented version is worse than no version.
	SoftwareVersion string
	BuildNumber     string
}

// validate checks what the operator must supply. Only SecurityMode None is
// implemented; ADR-0016 forbids describing that as production ready.
func (config OPCUAFrontendConfig) validate() error {
	if config.EndpointURL == "" {
		return fmt.Errorf("the OPC UA frontend requires an endpoint URL")
	}
	if config.ApplicationURI == "" {
		return fmt.Errorf("the OPC UA frontend requires an application URI")
	}
	if config.SecurityPolicyURI == "" {
		return fmt.Errorf(
			"the OPC UA frontend requires a security policy URI; the known URIs are defined by OPC 10000-7")
	}
	if config.TransportProfileURI == "" {
		return fmt.Errorf(
			"the OPC UA frontend requires a transport profile URI; the known URIs are defined by OPC 10000-7")
	}
	if config.NamespaceURI == "" {
		return fmt.Errorf("the OPC UA frontend requires a stable namespace URI")
	}
	if config.SourceFolderName == "" {
		return fmt.Errorf("the OPC UA frontend requires a source folder name")
	}
	return nil
}
