package app

import (
	"context"
	"testing"
	"time"
)

// A listener that stops because the service was asked to stop has not failed,
// and the error channel is what an operator's supervisor watches. Reporting a
// graceful stop there would make every ordinary shutdown look like a crash and
// could restart an adapter that was deliberately taken down.
//
// Only the HTTP arm said so. The gRPC and OPC UA arms have their own goroutine
// and their own nil check, and the sweep found both removable: inverting either
// left every case passing, because no case had ever shut one of those two down
// and then looked at the channel.
func TestNoFrontendReportsAFailureWhenItIsAskedToStop(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		configure func(*Config)
	}{
		{
			name: "HTTP",
			configure: func(config *Config) {
				config.Frontend = FrontendHTTP
				config.HTTPListenAddress = "127.0.0.1:0"
			},
		},
		{
			name: "gRPC",
			configure: func(config *Config) {
				config.Frontend = FrontendGRPC
				config.GRPCListenAddress = "127.0.0.1:0"
			},
		},
		{
			name: "OPC UA",
			configure: func(config *Config) {
				config.Frontend = FrontendOPCUA
				config.OPCUAListenAddress = "127.0.0.1:0"
				config.OPCUA = testOPCUAEndpoint()
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := DefaultConfig()
			testCase.configure(&config)
			service, err := New(config, lifecycleRuntime{})
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Start(); err != nil {
				t.Fatal(err)
			}
			// The listener is serving before the shutdown, or the case would
			// pass against a frontend that never started.
			if address := service.Address(); address == "" {
				t.Fatal("the service reports no listen address, so it never served")
			}

			shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := service.Shutdown(shutdownContext); err != nil {
				t.Fatalf("the shutdown itself failed: %v", err)
			}
			select {
			case err := <-service.Errors():
				t.Errorf("a graceful shutdown was reported as a listener failure: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}
