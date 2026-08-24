package app

import (
	"testing"
	"time"
)

func TestDefaultConfigIsBoundedAndLoopback(t *testing.T) {
	config := DefaultConfig()
	if config.HTTPListenAddress != "127.0.0.1:8080" {
		t.Fatalf("listen address = %q", config.HTTPListenAddress)
	}
	if config.Frontend != FrontendHTTP || config.GRPCListenAddress != "127.0.0.1:50051" {
		t.Fatalf("frontend defaults = %q/%q", config.Frontend, config.GRPCListenAddress)
	}
	if config.MaxGRPCConnections != 16 || config.MaxGRPCStreams != 16 || config.MaxConcurrentGRPCRPCs != 32 ||
		config.GRPCConnectionTimeout != 5*time.Second || config.GRPCMaxConnectionIdle != 2*time.Minute ||
		config.GRPCMaxConnectionAge != 30*time.Minute || config.GRPCMaxConnectionGrace != 30*time.Second ||
		config.GRPCKeepaliveMinTime != 30*time.Second {
		t.Fatalf("gRPC defaults = %+v", config)
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxHTTPConnections <= 0 || config.MaxConcurrentRequests <= 0 ||
		config.MaxHTTPHeaderBytes <= 0 || config.HTTPReadHeaderTimeout <= 0 || config.HTTPReadTimeout <= 0 ||
		config.HTTPWriteTimeout <= 0 || config.HTTPIdleTimeout <= 0 || config.MaxJSONDepth != 64 || config.Runtime.Limits.CommandQueue <= 0 ||
		config.MaxGRPCReceiveBytes <= 0 || config.MaxGRPCSendBytes <= 0 || config.MaxGRPCConnections <= 0 ||
		config.MaxConcurrentGRPCRPCs <= 0 || config.MaxGRPCStreams == 0 || config.MaxGRPCMetadataBytes == 0 {
		t.Fatal("expected positive bounded defaults")
	}
	if config.WriteEnabled {
		t.Fatal("write must default disabled")
	}
	if config.Runtime.ReconnectInitial != time.Second || config.Runtime.ReconnectMax != 30*time.Second {
		t.Fatalf("reconnect defaults = %s/%s", config.Runtime.ReconnectInitial, config.Runtime.ReconnectMax)
	}
	if config.Runtime.COMCallWatchdog != 30*time.Second {
		t.Fatalf("COM watchdog default = %s", config.Runtime.COMCallWatchdog)
	}
}

func TestGRPCConfigurationRejectsUnsafeBoundsAndAddress(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "receive", mutate: func(config *Config) { config.MaxGRPCConnections = 17 }},
		{name: "send", mutate: func(config *Config) { config.MaxGRPCSendBytes = 16 << 20; config.MaxConcurrentGRPCRPCs = 17 }},
		{name: "metadata", mutate: func(config *Config) { config.MaxGRPCMetadataBytes = 1 << 20; config.MaxGRPCConnections = 5 }},
		{name: "connections", mutate: func(config *Config) { config.MaxGRPCConnections = 2049 }},
		{name: "streams", mutate: func(config *Config) { config.MaxGRPCStreams = 4097 }},
		{name: "connection timeout", mutate: func(config *Config) { config.GRPCConnectionTimeout = 0 }},
		{name: "age grace", mutate: func(config *Config) { config.GRPCMaxConnectionGrace = config.GRPCMaxConnectionAge + time.Second }},
		{name: "listen", mutate: func(config *Config) { config.GRPCListenAddress = "127.0.0.1:not-a-port" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.Frontend = FrontendGRPC
			test.mutate(&config)
			if err := config.finalizeAndValidate(); err == nil {
				t.Fatal("unsafe gRPC configuration was accepted")
			}
		})
	}
}

func TestLoadConfigAppliesExplicitGRPCFrontendAndBounds(t *testing.T) {
	t.Setenv("OPCDA_FRONTEND", "grpc")
	t.Setenv("OPCDA_GRPC_LISTEN", "127.0.0.1:55051")
	t.Setenv("OPCDA_MAX_GRPC_RECEIVE_BYTES", "262144")
	t.Setenv("OPCDA_MAX_GRPC_SEND_BYTES", "524288")
	t.Setenv("OPCDA_MAX_GRPC_CONNECTIONS", "17")
	t.Setenv("OPCDA_MAX_CONCURRENT_GRPC_RPCS", "9")
	t.Setenv("OPCDA_MAX_GRPC_STREAMS", "11")
	t.Setenv("OPCDA_MAX_GRPC_METADATA_BYTES", "8192")
	t.Setenv("OPCDA_GRPC_CONNECTION_TIMEOUT", "3s")
	t.Setenv("OPCDA_GRPC_MAX_CONNECTION_IDLE", "4m")
	t.Setenv("OPCDA_GRPC_MAX_CONNECTION_AGE", "20m")
	t.Setenv("OPCDA_GRPC_MAX_CONNECTION_AGE_GRACE", "15s")
	t.Setenv("OPCDA_GRPC_KEEPALIVE_MIN_TIME", "45s")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Frontend != FrontendGRPC || config.GRPCListenAddress != "127.0.0.1:55051" ||
		config.MaxGRPCReceiveBytes != 262144 || config.MaxGRPCSendBytes != 524288 || config.MaxGRPCConnections != 17 ||
		config.MaxConcurrentGRPCRPCs != 9 || config.MaxGRPCStreams != 11 || config.MaxGRPCMetadataBytes != 8192 ||
		config.GRPCConnectionTimeout != 3*time.Second || config.GRPCMaxConnectionIdle != 4*time.Minute ||
		config.GRPCMaxConnectionAge != 20*time.Minute || config.GRPCMaxConnectionGrace != 15*time.Second ||
		config.GRPCKeepaliveMinTime != 45*time.Second {
		t.Fatalf("gRPC environment config = %+v", config)
	}
}

func TestLoadConfigRejectsInvalidReconnectBounds(t *testing.T) {
	t.Setenv("OPCDA_RECONNECT_INITIAL", "31s")
	t.Setenv("OPCDA_RECONNECT_MAX", "30s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig accepted reconnect initial greater than maximum")
	}
}

func TestLoadConfigAppliesExplicitRuntimeBounds(t *testing.T) {
	t.Setenv("OPCDA_COMMAND_QUEUE", "7")
	t.Setenv("OPCDA_MAX_READ_ITEMS", "8")
	t.Setenv("OPCDA_MAX_BSTR_CODE_UNITS", "9")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime.Limits.CommandQueue != 7 || config.Runtime.Limits.MaxReadItems != 8 || config.Runtime.Limits.MaxBSTRCodeUnits != 9 {
		t.Fatalf("runtime limits not applied: %+v", config.Runtime.Limits)
	}
}

func TestLoadConfigRejectsUnsafeHTTPCeiling(t *testing.T) {
	t.Setenv("OPCDA_MAX_CONCURRENT_REQUESTS", "1025")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("HTTP concurrency above hard ceiling was accepted")
	}
}

func TestLoadConfigAppliesAndBoundsJSONDepth(t *testing.T) {
	t.Setenv("OPCDA_MAX_JSON_DEPTH", "17")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxJSONDepth != 17 {
		t.Fatalf("JSON depth = %d", config.MaxJSONDepth)
	}

	t.Setenv("OPCDA_MAX_JSON_DEPTH", "257")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("JSON depth above hard ceiling was accepted")
	}
}

func TestLoadConfigAppliesHTTPTransportBounds(t *testing.T) {
	t.Setenv("OPCDA_MAX_HTTP_CONNECTIONS", "17")
	t.Setenv("OPCDA_MAX_HTTP_HEADER_BYTES", "16384")
	t.Setenv("OPCDA_HTTP_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("OPCDA_HTTP_READ_TIMEOUT", "3s")
	t.Setenv("OPCDA_HTTP_WRITE_TIMEOUT", "4s")
	t.Setenv("OPCDA_HTTP_IDLE_TIMEOUT", "5s")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.MaxHTTPConnections != 17 || config.MaxHTTPHeaderBytes != 16384 ||
		config.HTTPReadHeaderTimeout != 2*time.Second || config.HTTPReadTimeout != 3*time.Second ||
		config.HTTPWriteTimeout != 4*time.Second || config.HTTPIdleTimeout != 5*time.Second {
		t.Fatalf("HTTP transport bounds not applied: %+v", config)
	}
}

func TestLoadConfigRejectsUnsafeHTTPConnectionCeiling(t *testing.T) {
	t.Setenv("OPCDA_MAX_HTTP_CONNECTIONS", "2049")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("HTTP connections above hard ceiling were accepted")
	}
}

func TestLoadConfigRejectsUnsafeAggregateHTTPBudgets(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "bodies",
			env: map[string]string{
				"OPCDA_MAX_HTTP_BODY_BYTES":     "67108864",
				"OPCDA_MAX_CONCURRENT_REQUESTS": "5",
			},
		},
		{
			name: "headers",
			env: map[string]string{
				"OPCDA_MAX_HTTP_CONNECTIONS":  "65",
				"OPCDA_MAX_HTTP_HEADER_BYTES": "1048576",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if _, err := LoadConfig(); err == nil {
				t.Fatal("aggregate HTTP budget above hard ceiling was accepted")
			}
		})
	}
}

func TestLoadConfigRejectsMalformedListenAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "127.0.0.1:not-a-port", "127.0.0.1:65536"} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("OPCDA_HTTP_LISTEN", address)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("malformed listen address %q was accepted", address)
			}
		})
	}
}
