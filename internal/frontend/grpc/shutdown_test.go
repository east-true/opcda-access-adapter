package grpcfrontend

import (
	"context"
	"net"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// GracefulStop is the adapter's promise that shutting down neither cuts a call
// off nor hangs on one. Neither half had ever run.

func TestGracefulStopReturnsWhenCallsHaveFinished(t *testing.T) {
	server, listener, serveDone := serveForShutdown(t, &testRuntime{})
	client := shutdownClient(t, listener)

	if _, err := client.Status(context.Background(), &opcdav1.DAStatusRequest{}); err != nil {
		t.Fatalf("Status before shutdown: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.GracefulStop(ctx); err != nil {
		t.Fatalf("GracefulStop with nothing in flight: %v", err)
	}
	if server.listening.Load() {
		t.Fatal("the server still reports itself as listening after shutdown")
	}
	_ = listener.Close()
	<-serveDone
}

// A call that will not finish must not hold shutdown open forever. The context
// is the bound: when it expires the server is stopped hard and the caller is
// told why, rather than GracefulStop blocking on a request that never returns.
func TestGracefulStopStopsHardWhenItsContextExpires(t *testing.T) {
	blocked := make(chan struct{})
	released := make(chan struct{})
	// The handler waits, but honours cancellation. That is the assumption
	// GracefulStop's bound rests on and the adapter's own handlers keep: the
	// hard Stop cancels in-flight RPCs, and a handler that ignored that would
	// hold shutdown open no matter what context GracefulStop was given.
	runtime := &testRuntime{read: func(ctx context.Context, _ opcda.ReadRequest) ([]opcda.ReadResult, error) {
		close(blocked)
		select {
		case <-released:
		case <-ctx.Done():
		}
		return nil, opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "released")
	}}
	server, listener, serveDone := serveForShutdown(t, runtime)
	client := shutdownClient(t, listener)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = client.Read(context.Background(), &opcdav1.DAReadRequest{
			Source: opcdav1.DADataSource_DA_DATA_SOURCE_DEVICE,
			Items:  []*opcdav1.DAReadItem{{ItemId: "Blocked"}},
		})
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocking Read never reached the runtime")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := server.GracefulStop(ctx)
	if err == nil {
		t.Fatal("GracefulStop reported success while a call was still in flight")
	}
	if !errorsIsDeadline(err) {
		t.Fatalf("GracefulStop returned %v, want its context's error", err)
	}
	// It must have given up on its own bound rather than waited for the call.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("GracefulStop waited %s for a call that never finished", elapsed)
	}
	close(released)
	<-readDone
	_ = listener.Close()
	<-serveDone
}

func errorsIsDeadline(err error) bool {
	return err == context.DeadlineExceeded
}

func serveForShutdown(t *testing.T, runtime *testRuntime) (*Server, *bufconn.Listener, chan error) {
	t.Helper()
	server := New(runtime, Config{})
	listener := bufconn.Listen(1 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	return server, listener, serveDone
}

func shutdownClient(t *testing.T, listener *bufconn.Listener) opcdav1.OPCDAAccessClient {
	t.Helper()
	connection, err := grpcgo.NewClient(
		"passthrough:///bufconn",
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return opcdav1.NewOPCDAAccessClient(connection)
}

// A runtime that answers a different number of results, or the same number in a
// different order, is broken. The frontend refuses to pass that on, because a
// client matching results to requests by position would silently read one
// item's value as another's.
func TestAResultThatDoesNotMatchTheRequestIsRefused(t *testing.T) {
	actual := opcda.VTI4
	result := func(itemID opcda.DAItemID) opcda.ReadResult {
		return opcda.ReadResult{
			ItemID: itemID, VarType: &actual, HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{ItemID: itemID, VarType: actual, Value: int32(1), QualityRaw: 0xC0, HRESULT: opcda.SOK},
		}
	}
	for _, testCase := range []struct {
		name    string
		results []opcda.ReadResult
	}{
		{"too few", []opcda.ReadResult{result("A")}},
		{"too many", []opcda.ReadResult{result("A"), result("B"), result("C")}},
		{"out of order", []opcda.ReadResult{result("B"), result("A")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
				return testCase.results, nil
			}}
			_, err := New(runtime, Config{}).Read(context.Background(), &opcdav1.DAReadRequest{
				Source: opcdav1.DADataSource_DA_DATA_SOURCE_DEVICE,
				Items:  []*opcdav1.DAReadItem{{ItemId: "A"}, {ItemId: "B"}},
			})
			assertGRPCDetail(t, err, codes.Internal, string(opcda.CodeInternalResultMismatch))
		})
	}
}
