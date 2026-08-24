package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

type shutdownTrackingRuntime struct {
	lifecycleRuntime
	calls atomic.Int32
}

func (runtime *shutdownTrackingRuntime) Shutdown(context.Context) error {
	runtime.calls.Add(1)
	return nil
}

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

func TestServiceLifecycleServesGRPCDAStatus(t *testing.T) {
	config := DefaultConfig()
	config.Frontend = FrontendGRPC
	config.GRPCListenAddress = "127.0.0.1:0"
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

	connection, err := grpcgo.NewClient(service.Address(), grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	requestContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := opcdav1.NewOPCDAAccessClient(connection).Status(requestContext, &opcdav1.DAStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.RuntimeState != string(opcda.RuntimeStateNotConfigured) || response.Frontend == nil || !response.Frontend.Listening {
		t.Fatalf("gRPC status = %+v", response)
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

func TestServiceRejectsInvalidProgrammaticConfigBeforeRuntimeAllocation(t *testing.T) {
	config := DefaultConfig()
	config.MaxConcurrentRequests = 0
	if _, err := New(config, lifecycleRuntime{}); err == nil {
		t.Fatal("New accepted an unsafe programmatic configuration")
	}
}

func TestLoopbackServiceRejectsNonLoopbackHost(t *testing.T) {
	config := DefaultConfig()
	config.HTTPListenAddress = "127.0.0.1:0"
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Host = "rebind.attacker.example"
	response := httptest.NewRecorder()
	service.http.ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestExplicitExternalListenerDoesNotInferLoopbackHostPolicy(t *testing.T) {
	config := DefaultConfig()
	config.HTTPListenAddress = "0.0.0.0:0"
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Host = "adapter.example"
	response := httptest.NewRecorder()
	service.http.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
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

func TestStartFailureCleansRuntimeAndMakesServiceTerminal(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	runtime := &shutdownTrackingRuntime{}
	config := DefaultConfig()
	config.HTTPListenAddress = occupied.Addr().String()
	service, err := New(config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err == nil {
		t.Fatal("Start unexpectedly acquired an occupied listener")
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("runtime shutdown calls = %d", runtime.calls.Load())
	}
	if err := service.Start(); err == nil {
		t.Fatal("service restarted after terminal startup failure")
	}
}

func TestUnexpectedListenerFailureIsReported(t *testing.T) {
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
		_ = service.Shutdown(shutdownContext)
	})

	if err := service.listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-service.Errors():
		if err == nil {
			t.Fatal("nil listener failure")
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected listener failure was not reported")
	}
}

func TestGracefulShutdownDoesNotReportListenerFailure(t *testing.T) {
	config := DefaultConfig()
	config.HTTPListenAddress = "127.0.0.1:0"
	service, err := New(config, lifecycleRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-service.Errors():
		t.Fatalf("graceful shutdown reported listener failure: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
}
