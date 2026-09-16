package http

import (
	"bytes"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// What these measure is the adapter's own share: the runtime is a stub that
// answers immediately, so every nanosecond reported here is spent decoding a
// request, encoding a response, or in the machinery between the two. The DA
// server's own latency is not part of it and cannot be -- it is the source's,
// not the adapter's.
//
// Two axes are worth knowing. How the cost grows with the data a request
// carries says whether a batch of a hundred costs a hundred times a batch of
// one or rather more; allocations per operation say whether anything is
// retained that should not be, which is what turns a steady load into a
// growing process.

func benchmarkReadRuntime(items int) (*readRuntime, []string) {
	varType := opcda.VTR8
	itemIDs := make([]string, items)
	results := make([]opcda.ReadResult, items)
	for index := range results {
		itemID := fmt.Sprintf("Channel1.Device1.Tag%04d", index)
		itemIDs[index] = itemID
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
	return &readRuntime{results: results}, itemIDs
}

func benchmarkServer(runtime opcda.Runtime) *Server {
	return New(runtime, Config{
		MaxBodyBytes: 8 << 20, MaxConcurrent: 32, RequestDeadline: time.Second,
		MaxReadItems: 10000, MaxWriteItems: 10000, MaxItemIDBytes: 1024,
		MaxJSONDepth: 64, MaxBrowseEntries: 1000, MaxBrowseDepth: 64,
		MaxItemProperties: 64,
	})
}

func readBody(itemIDs []string) []byte {
	quoted := make([]string, len(itemIDs))
	for index, itemID := range itemIDs {
		quoted[index] = `{"itemId":"` + itemID + `"}`
	}
	return []byte(`{"source":"device","items":[` + strings.Join(quoted, ",") + `]}`)
}

// BenchmarkReadByBatchSize is the data-volume axis for a Read: the same work
// per item, asked for in batches of very different sizes.
func BenchmarkReadByBatchSize(b *testing.B) {
	for _, items := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			runtime, itemIDs := benchmarkReadRuntime(items)
			server := benchmarkServer(runtime)
			body := readBody(itemIDs)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
					bytes.NewReader(body)))
				if response.Code != stdhttp.StatusOK {
					b.Fatalf("status = %d: %s", response.Code, response.Body.String())
				}
			}
		})
	}
}

// BenchmarkReadArrayByElementCount is the same axis for one item whose value is
// an array. A batch spreads its cost over many small values; an array puts the
// same number of values inside one.
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
			runtime := &readRuntime{results: []opcda.ReadResult{{
				ItemID: "Channel1.Device1.Array", VarType: &varType,
				HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{
					ItemID: "Channel1.Device1.Array", VarType: varType, Value: array,
					QualityRaw: 0x00C0, HRESULT: opcda.SOK,
				},
			}}}
			server := benchmarkServer(runtime)
			body := readBody([]string{"Channel1.Device1.Array"})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/read",
					bytes.NewReader(body)))
				if response.Code != stdhttp.StatusOK {
					b.Fatalf("status = %d: %s", response.Code, response.Body.String())
				}
			}
		})
	}
}

// BenchmarkWriteByBatchSize is the write direction of the same axis, which
// costs differently: a Write decodes a typed value per item rather than
// encoding one.
func BenchmarkWriteByBatchSize(b *testing.B) {
	for _, items := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			results := make([]opcda.WriteResult, items)
			encoded := make([]string, items)
			for index := range results {
				itemID := fmt.Sprintf("Channel1.Device1.Tag%04d", index)
				results[index] = opcda.WriteResult{
					ItemID: opcda.DAItemID(itemID), HRESULT: opcda.SOK, HRESULTPresent: true,
				}
				encoded[index] = fmt.Sprintf(
					`{"itemId":%q,"dataType":"VT_R8","valueEncoding":"json","value":%d.5}`,
					itemID, index)
			}
			runtime := &writeRuntime{enabled: true, results: results}
			server := benchmarkServer(runtime)
			body := []byte(`{"items":[` + strings.Join(encoded, ",") + `]}`)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, newJSONRequest(stdhttp.MethodPost, "/v1/write",
					bytes.NewReader(body)))
				if response.Code != stdhttp.StatusOK {
					b.Fatalf("status = %d: %s", response.Code, response.Body.String())
				}
			}
		})
	}
}
