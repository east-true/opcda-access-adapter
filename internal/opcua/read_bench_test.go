package opcua

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The same two axes the frontend benchmarks measure, on the OPC UA server. The
// runtime is a stub that answers immediately, so what is measured is the
// adapter's own share: resolving nodes, reading the source in one batch, and
// encoding the response.
//
// The encoded length is measured alongside the service call, because a UA
// client pays for both and measuring the handler alone would leave out the
// part that grows fastest with a batch.

func benchmarkUAService(b *testing.B, items int) (*DataAccessService, []ReadValueID) {
	b.Helper()
	space, err := NewAddressSpace(AddressSpaceConfig{
		NamespaceURI:     "urn:example:opcda-access-adapter",
		ApplicationURI:   "urn:example:opcda-access-adapter:server",
		SourceFolderName: "Source",
	})
	if err != nil {
		b.Fatal(err)
	}
	rights := &opcda.DAAccessRights{Raw: 3, Read: true, Write: true}
	canonical := opcda.VTR8
	entries := make([]opcda.BrowseEntry, items)
	results := make([]opcda.ReadResult, items)
	values := make([]ReadValueID, items)
	for index := range entries {
		id := fmt.Sprintf("Channel1.Device1.Tag%04d", index)
		name := fmt.Sprintf("Tag%04d", index)
		itemID := opcda.DAItemID(id)
		entries[index] = opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: &itemID,
			CanonicalType: &canonical, AccessRights: rights,
		}
		results[index] = opcda.ReadResult{
			ItemID: itemID, VarType: &canonical, CanonicalType: &canonical,
			HRESULT: opcda.SOK, HRESULTPresent: true,
			Value: &opcda.DAValue{
				ItemID: itemID, VarType: canonical, Value: float64(index) + 0.5,
				QualityRaw: QualityGood, HRESULT: opcda.SOK,
				Timestamp: time.Unix(1750000000, 0).UTC(), TimestampPresent: true,
			},
		}
		values[index] = ReadValueID{NodeID: ItemNodeID(itemID), AttributeID: AttributeValue}
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		b.Fatal(err)
	}
	limits := DefaultDataAccessLimits()
	limits.MaxNodesPerRead = 10000
	service, err := NewDataAccessService(space, &stubRuntime{readResults: results}, limits)
	if err != nil {
		b.Fatal(err)
	}
	return service, values
}

func BenchmarkReadByBatchSize(b *testing.B) {
	for _, items := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			service, values := benchmarkUAService(b, items)
			request := ReadRequest{
				Header:             RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
				TimestampsToReturn: TimestampsBoth,
				NodesToRead:        values,
			}
			ctx := context.Background()
			now := time.Unix(1750000000, 0).UTC()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response, err := service.Read(ctx, request, now)
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

// BenchmarkReadByBatchSizeEncoded adds what a client actually receives. The
// difference between this and the service benchmark is what the binary
// encoding costs.
func BenchmarkReadByBatchSizeEncoded(b *testing.B) {
	for _, items := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			service, values := benchmarkUAService(b, items)
			request := ReadRequest{
				Header:             RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
				TimestampsToReturn: TimestampsBoth,
				NodesToRead:        values,
			}
			ctx := context.Background()
			now := time.Unix(1750000000, 0).UTC()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response, err := service.Read(ctx, request, now)
				if err != nil {
					b.Fatal(err)
				}
				encoder, err := NewEncoder(DefaultBinaryLimits())
				if err != nil {
					b.Fatal(err)
				}
				encoder.WriteReadResponse(response)
				encoded, err := encoder.Bytes()
				if err != nil {
					b.Fatal(err)
				}
				if i == 0 {
					b.SetBytes(int64(len(encoded)))
				}
			}
		})
	}
}

// BenchmarkReadArrayByElementCount puts the same number of values inside one
// item instead of spreading them over a batch. For OPC UA this is the cheaper
// shape by construction: one node to resolve, one status, one timestamp pair.
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
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				variant, status := variantForDAValue(opcda.DAValue{Value: array})
				if status != StatusGood {
					b.Fatal(status.Hex())
				}
				encoder, err := NewEncoder(DefaultBinaryLimits())
				if err != nil {
					b.Fatal(err)
				}
				encoder.WriteVariant(variant)
				if _, err := encoder.Bytes(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
