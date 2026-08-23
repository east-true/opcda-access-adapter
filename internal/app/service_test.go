package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type lifecycleRuntime struct{}

func (lifecycleRuntime) Status(context.Context) opcda.RuntimeStatus {
	return opcda.RuntimeStatus{State: opcda.RuntimeStateNotConfigured, Capabilities: opcda.Capabilities{Browse: "unavailable"}}
}
func (lifecycleRuntime) Browse(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return opcda.BrowseResult{}, nil
}
func (lifecycleRuntime) ReadBatch(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
	return nil, nil
}
func (lifecycleRuntime) WriteBatch(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error) {
	return nil, nil
}
func (lifecycleRuntime) Shutdown(context.Context) error { return nil }

func TestServiceLifecycleServesStatus(t *testing.T) {
	config := DefaultConfig()
	config.HTTPListenAddress = "127.0.0.1:0"
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	response, err := http.Get("http://" + service.Address() + "/v1/status") // #nosec G107 -- test loopback listener.
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
}

func TestServiceAppliesHTTPTransportBounds(t *testing.T) {
	config := DefaultConfig()
	config.HTTPReadHeaderTimeout = 2 * time.Second
	config.HTTPReadTimeout = 3 * time.Second
	config.HTTPWriteTimeout = 4 * time.Second
	config.HTTPIdleTimeout = 5 * time.Second
	config.MaxHTTPHeaderBytes = 12345
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if service.server.ReadHeaderTimeout != 2*time.Second || service.server.ReadTimeout != 3*time.Second ||
		service.server.WriteTimeout != 4*time.Second || service.server.IdleTimeout != 5*time.Second ||
		service.server.MaxHeaderBytes != 12345 {
		t.Fatalf("HTTP server bounds not applied: %+v", service.server)
	}
}

func TestServiceBoundsIncompleteHeaderConnectionsAndRecovers(t *testing.T) {
	config := DefaultConfig()
	config.HTTPListenAddress = "127.0.0.1:0"
	config.MaxHTTPConnections = 1
	config.HTTPReadHeaderTimeout = 50 * time.Millisecond
	config.HTTPReadTimeout = 100 * time.Millisecond
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	connection, err := net.DialTimeout("tcp", service.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET /v1/status HTTP/1.1\r\nHost: test\r\nX-Incomplete: "); err != nil {
		t.Fatal(err)
	}

	blockedClient := &http.Client{Timeout: 200 * time.Millisecond}
	response, err := blockedClient.Get("http://" + service.Address() + "/v1/status") // #nosec G107 -- test loopback listener.
	if err == nil {
		response.Body.Close()
		t.Fatal("connection above the configured bound was accepted")
	}

	deadline := time.Now().Add(time.Second)
	for {
		response, err = blockedClient.Get("http://" + service.Address() + "/v1/status") // #nosec G107 -- test loopback listener.
		if err == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("recovered status = %d", response.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener did not recover after header timeout: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
