package opcua

import (
	"net"
	"testing"
)

// Serve is started in a goroutine, so a shutdown can arrive before it runs.
// The accept loop already treats a closed listener as a clean stop; the entry
// check did not, and reported net.ErrClosed instead. A caller cannot tell that
// apart from a listener that failed, so stopping an adapter immediately after
// starting it surfaced an error to whatever watches for one.
func TestServingAnAlreadyClosedListenerIsNotAFailure(t *testing.T) {
	listener, err := NewListener(testListenerConfig(), 1000, 2000)
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener failed: %v", err)
	}
	if err := listener.Serve(socket); err != nil {
		t.Errorf("serving after a close reported %v, want a clean stop", err)
	}
	_ = socket.Close()
}
