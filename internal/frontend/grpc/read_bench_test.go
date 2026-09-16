package grpcfrontend

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The same two axes the HTTP benchmarks measure, on the frontend that encodes
// to protobuf instead of JSON. The runtime is a stub, so what is measured is
// the adapter's own share and nothing of the source's.
//
// Comparing the two frontends is the point of having both: a client choosing
// between them is choosing an encoding, and the cost of that choice is not
// something to guess at.

func benchmarkGRPCRuntime(items int) (*testRuntime, []*opcdav1.DAReadItem) {
	varType := opcda.VTR8
	requested := make([]*opcdav1.DAReadItem, items)
	results := make([]opcda.ReadResult, items)
	for index := range results {
		itemID := fmt.Sprintf("Channel1.Device1.Tag%04d", index)
		requested[index] = &opcdav1.DAReadItem{ItemId: itemID}
		results[index] = opcda.ReadResult{
			ItemID: opcda.DAItemID(itemID), VarType: &varType,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{
				ItemID: opcda.DAItemID(itemID), VarType: varType, Value: float64(index) + 0.5,
				QualityRaw: 0x00C0, HRESULT: opcda.SOK,
				Timestamp: time.Unix(1750000000, 0).UTC(), TimestampPresent: true,
			},
		}
	}
	runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
		return results, nil
	}}
	return runtime, requested
}

func benchmarkGRPCServer(runtime opcda.Runtime) *Server {
	return New(runtime, Config{
		MaxReadItems: 10000, MaxWriteItems: 10000, MaxItemIDBytes: 1024,
		MaxBrowseEntries: 1000, MaxBrowseDepth: 64, MaxItemProperties: 64,
		RequestDeadline: time.Second,
	})
}

// BenchmarkReadByBatchSizeOnTheWire measures the same work the HTTP benchmark
// does: bytes in, bytes out. A handler measured on its own leaves out the
// encoding, which is most of what a frontend is, and comparing that against a
// frontend measured through its transport would flatter this one for a reason
// that has nothing to do with either.
func BenchmarkReadByBatchSizeOnTheWire(b *testing.B) {
	for _, items := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			runtime, requested := benchmarkGRPCRuntime(items)
			server := benchmarkGRPCServer(runtime)
			encoded, err := proto.Marshal(&opcdav1.DAReadRequest{Items: requested})
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var request opcdav1.DAReadRequest
				if err := proto.Unmarshal(encoded, &request); err != nil {
					b.Fatal(err)
				}
				response, err := server.Read(ctx, &request)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := proto.Marshal(response); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReadByBatchSize measures the handler alone, without the encoding on
// either side. The difference between this and the wire benchmark above is what
// protobuf costs.
func BenchmarkReadByBatchSize(b *testing.B) {
	for _, items := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			runtime, requested := benchmarkGRPCRuntime(items)
			server := benchmarkGRPCServer(runtime)
			request := &opcdav1.DAReadRequest{Items: requested}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response, err := server.Read(ctx, request)
				if err != nil {
					b.Fatal(err)
				}
				if len(response.Results) != items {
					b.Fatalf("results = %d", len(response.Results))
				}
			}
		})
	}
}

func BenchmarkReadArrayByElementCount(b *testing.B) {
	for _, elements := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("elements=%d", elements), func(b *testing.B) {
			values := make([]any, elements)
			for index := range values {
				values[index] = float64(index) + 0.5
			}
			array := opcda.DAArray{
				ElementType: opcda.VTR8,
				Dimensions:  []opcda.DADimension{{Length: uint32(elements)}},
				Elements:    values,
			}
			varType := opcda.VTR8 | opcda.VTArray
			runtime := &testRuntime{read: func(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
				return []opcda.ReadResult{{
					ItemID: "Channel1.Device1.Array", VarType: &varType,
					HRESULT: opcda.SOK, HRESULTPresent: true,
					Value: &opcda.DAValue{
						ItemID: "Channel1.Device1.Array", VarType: varType, Value: array,
						QualityRaw: 0x00C0, HRESULT: opcda.SOK,
					},
				}}, nil
			}}
			server := benchmarkGRPCServer(runtime)
			request := &opcdav1.DAReadRequest{
				Items: []*opcdav1.DAReadItem{{ItemId: "Channel1.Device1.Array"}},
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := server.Read(ctx, request); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
