package grpcfrontend

import (
	"context"
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A Write result has to say two things: which item it is about, and what
// happened. The second is an HRESULT or an adapter error code, and a result
// carrying neither says nothing at all -- there is no outcome to relay, so
// relaying it would hand a client a result it cannot read.
//
// The check that refuses that survived: with the comparison inverted, only a
// result that was at once missing its HRESULT and carrying an error code would
// have been caught, which no result is.
func TestAWriteResultMustSayWhatHappened(t *testing.T) {
	const failure = opcda.HRESULT(-1073479674) // OPC_E_BADRIGHTS

	for _, testCase := range []struct {
		name     string
		result   opcda.WriteResult
		accepted bool
	}{
		{
			name:     "an HRESULT and nothing else",
			result:   opcda.WriteResult{ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true},
			accepted: true,
		},
		{
			name: "an error code and nothing else",
			result: opcda.WriteResult{
				ItemID: "Test/Int32", ErrorCode: string(opcda.CodeUnsupportedVarType),
			},
			accepted: true,
		},
		{
			name: "both of them",
			result: opcda.WriteResult{
				ItemID: "Test/Int32", HRESULT: failure, HRESULTPresent: true,
				ErrorCode: string(opcda.CodeUnsupportedVarType),
			},
			accepted: true,
		},
		{
			name:     "neither of them",
			result:   opcda.WriteResult{ItemID: "Test/Int32"},
			accepted: false,
		},
		// The other half of the same check: a result about an item that was
		// not asked about is not this request's result.
		{
			name:     "an answer about a different item",
			result:   opcda.WriteResult{ItemID: "Test/Other", HRESULT: opcda.SOK, HRESULTPresent: true},
			accepted: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &testRuntime{
				status: opcda.RuntimeStatus{WriteEnabled: true},
				write: func(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error) {
					return []opcda.WriteResult{testCase.result}, nil
				},
			}
			server := New(runtime, Config{MaxWriteItems: 4, MaxItemIDBytes: 64})
			_, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{
				Items: []*opcdav1.DAWriteItem{{
					ItemId:   "Test/Int32",
					DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI4)},
					Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}},
				}},
			})
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}
}

// A client may ask for as many item properties as the configured bound names,
// and one more than that is refused. The bound survived being tightened by
// one, which would have made the documented limit one property smaller.
func TestAnItemPropertiesRequestMayFillItsBound(t *testing.T) {
	const limit = 3
	runtime := &testRuntime{
		propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{},
	}
	server := New(runtime, Config{MaxItemProperties: limit, MaxItemIDBytes: 64})

	identifiers := func(n int) []uint32 {
		ids := make([]uint32, n)
		for index := range ids {
			ids[index] = uint32(index + 1)
		}
		return ids
	}

	if _, err := server.ItemProperties(context.Background(), &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float", PropertyIds: identifiers(limit),
	}); err != nil {
		t.Errorf("exactly %d properties were refused: %v", limit, err)
	}
	if _, err := server.ItemProperties(context.Background(), &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float", PropertyIds: identifiers(limit + 1),
	}); err == nil {
		t.Errorf("%d properties were accepted", limit+1)
	}
	// And none at all is a request that names nothing to read.
	if _, err := server.ItemProperties(context.Background(), &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float",
	}); err == nil {
		t.Error("a request naming no properties was accepted")
	}
}
