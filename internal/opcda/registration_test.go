package opcda

import "testing"

func TestRegistrationCacheRejectsStaleGeneration(t *testing.T) {
	cache := newRegistrationCache(2, 4)
	current := itemRegistration{ItemID: "Exact.ID", ServerHandle: 11, Generation: 4}
	if !cache.put(current) {
		t.Fatal("current registration was rejected")
	}
	if got, ok := cache.get(current.ItemID); !ok || got.ServerHandle != 11 {
		t.Fatalf("get current = %+v, %v", got, ok)
	}

	cache.reset(5)
	if _, ok := cache.get(current.ItemID); ok {
		t.Fatal("stale registration survived generation reset")
	}
	if cache.put(current) {
		t.Fatal("stale registration was accepted into new generation")
	}
}

func TestRegistrationCacheIsBounded(t *testing.T) {
	cache := newRegistrationCache(1, 1)
	if !cache.put(itemRegistration{ItemID: "A", Generation: 1}) {
		t.Fatal("first registration was rejected")
	}
	if cache.put(itemRegistration{ItemID: "B", Generation: 1}) {
		t.Fatal("registration beyond bound was accepted")
	}
	if got := cache.remaining(); got != 0 {
		t.Fatalf("remaining = %d, want 0", got)
	}
}
