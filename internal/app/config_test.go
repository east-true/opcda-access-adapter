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
	if config.MaxHTTPBodyBytes <= 0 || config.MaxHTTPConnections <= 0 || config.MaxConcurrentRequests <= 0 ||
		config.MaxHTTPHeaderBytes <= 0 || config.HTTPReadHeaderTimeout <= 0 || config.HTTPReadTimeout <= 0 ||
		config.HTTPWriteTimeout <= 0 || config.HTTPIdleTimeout <= 0 || config.Runtime.Limits.CommandQueue <= 0 {
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
