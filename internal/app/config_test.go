package app

import "testing"

func TestDefaultConfigIsBoundedAndLoopback(t *testing.T) {
	config := DefaultConfig()
	if config.HTTPListenAddress != "127.0.0.1:8080" {
		t.Fatalf("listen address = %q", config.HTTPListenAddress)
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxConcurrentRequests <= 0 || config.Runtime.Limits.CommandQueue <= 0 {
		t.Fatal("expected positive bounded defaults")
	}
	if config.WriteEnabled {
		t.Fatal("write must default disabled")
	}
}
