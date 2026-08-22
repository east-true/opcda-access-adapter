package app

import (
	"context"
	"io"
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
