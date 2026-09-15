package grpcfrontend

import (
	"net"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Serve is started in a goroutine, so a shutdown can arrive before it runs.
// gRPC reports that as ErrServerStopped, and a caller cannot tell it apart
// from a listener that failed -- so stopping an adapter immediately after
// starting it surfaced an error to whatever watches for one. Being stopped is
// not a failure to serve.
func TestServingAfterAStopIsNotAFailure(t *testing.T) {
	server := New(&testRuntime{status: opcda.RuntimeStatus{}}, Config{
		MaxWriteItems: 4, MaxItemIDBytes: 64,
	})
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = socket.Close() }()

	server.Stop()
	if err := server.Serve(socket); err != nil {
		t.Errorf("serving after a stop reported %v, want a clean stop", err)
	}
	if server.listening.Load() {
		t.Error("a server that never served reports itself listening")
	}
}
