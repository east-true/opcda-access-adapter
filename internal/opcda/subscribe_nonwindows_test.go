//go:build !windows

package opcda

import (
	"context"
	"testing"
	"time"
)

func TestSubscribeRequiresWindows(t *testing.T) {
	runtime, err := New(Config{Source: SourceConfig{ProgID: "Fixture.DA"}, Limits: DefaultLimits()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	subscription, err := runtime.Subscribe(context.Background(), SubscribeRequest{
		Items:               []DAItemID{"Random.Int2"},
		RequestedUpdateRate: time.Second,
	})
	if subscription != nil {
		t.Fatal("non-Windows Subscribe returned a subscription")
	}
	adapterErr, ok := AsAdapterError(err)
	if !ok || adapterErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("Subscribe error = %v, want %s", err, CodeRuntimeUnavailable)
	}

	adapterErr, ok = AsAdapterError(runtime.Unsubscribe(context.Background(), "sub-1-1"))
	if !ok || adapterErr.Code != CodeRuntimeUnavailable {
		t.Fatalf("Unsubscribe error = %v, want %s", adapterErr, CodeRuntimeUnavailable)
	}

	if runtime.Status(context.Background()).Capabilities.Subscribe {
		t.Fatal("non-Windows runtime advertised the Subscribe capability")
	}
}
