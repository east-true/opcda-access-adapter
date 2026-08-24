package grpcfrontend

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCTransportPreservesReadAndEnforcesReceiveBound(t *testing.T) {
	actual := opcda.VTI4
	runtime := &testRuntime{read: func(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return []opcda.ReadResult{{
			ItemID: request.Items[0], VarType: &actual, HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{ItemID: request.Items[0], VarType: actual, Value: int32(17), QualityRaw: 0xC0, HRESULT: opcda.SOK},
		}}, nil
	}}
	server := New(runtime, Config{MaxReceiveBytes: 256})
	listener := bufconn.Listen(1 << 20)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := grpcgo.NewClient(
		"passthrough:///bufconn",
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithDefaultCallOptions(grpcgo.MaxCallSendMsgSize(8<<10)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := opcdav1.NewOPCDAAccessClient(connection)

	response, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Exact.I4"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || !response.Results[0].Ok || response.Results[0].QualityRaw != 0xC0 {
		t.Fatalf("Read response = %+v", response)
	}
	if value, ok := response.Results[0].Value.Value.(*opcdav1.DAScalarValue_I4Value); !ok || value.I4Value != 17 {
		t.Fatalf("Read value = %#v", response.Results[0].Value.Value)
	}

	_, err = client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: strings.Repeat("x", 1024)}}})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized request code = %s, err=%v", status.Code(err), err)
	}

	var unknown opcdav1.DAStatusResponse
	err = connection.Invoke(ctx, "/opcda.access.v1.OPCDAAccess/Subscribe", &opcdav1.DAStatusRequest{}, &unknown)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Subscribe code = %s, err=%v", status.Code(err), err)
	}
}
