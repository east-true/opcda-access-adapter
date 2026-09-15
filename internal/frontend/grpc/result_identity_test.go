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

// An active subscription item carries the DA metadata that makes it usable:
// the canonical VARTYPE the source keeps it in, and the access rights it
// grants. An item reported active without them is the source and the adapter
// disagreeing, and relaying it would give a client a subscription entry it
// cannot type or write. Both halves are checked separately, because either
// alone would let the other through.
func TestAnActiveSubscriptionItemCarriesItsMetadata(t *testing.T) {
	canonical := opcda.VTI4
	rights := opcda.DAAccessRights{Raw: 3, Read: true, Write: true}

	for _, testCase := range []struct {
		name     string
		item     opcda.SubscriptionItemStatus
		accepted bool
	}{
		{
			name: "an active item with both",
			item: opcda.SubscriptionItemStatus{
				ItemID: "Test/A", Active: true,
				CanonicalType: &canonical, AccessRights: &rights,
			},
			accepted: true,
		},
		{
			name: "an active item with no canonical type",
			item: opcda.SubscriptionItemStatus{
				ItemID: "Test/A", Active: true, AccessRights: &rights,
			},
		},
		{
			name: "an active item with no access rights",
			item: opcda.SubscriptionItemStatus{
				ItemID: "Test/A", Active: true, CanonicalType: &canonical,
			},
		},
		{
			name: "an active item with neither",
			item: opcda.SubscriptionItemStatus{ItemID: "Test/A", Active: true},
		},
		// An item that is not active was never established, so it has no
		// metadata to carry and is not held to any.
		{
			name:     "an inactive item with neither",
			item:     opcda.SubscriptionItemStatus{ItemID: "Test/A"},
			accepted: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			info := opcda.SubscriptionInfo{
				ID:    "s1",
				Items: []opcda.SubscriptionItemStatus{testCase.item},
			}
			if testCase.item.Active {
				info.ActiveItemCount = 1
			}
			_, err := encodeSubscriptionCreated(info, 64)
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}
}

// VT_EMPTY and VT_NULL carry no value, and this is the third place that rule
// is written -- the DA core validates a Write of one, the HTTP frontend
// encodes a Read of one, and this encodes it for gRPC. Inverting the check
// makes the rule its own opposite here too: an empty value carrying a number
// would be encoded and one carrying nothing refused.
func TestAnEmptyScalarOverGRPCIsTheAbsenceOfAValue(t *testing.T) {
	for _, varType := range []opcda.DAVarType{opcda.VTEmpty, opcda.VTNull} {
		t.Run(varType.String(), func(t *testing.T) {
			encoded, err := encodeScalar(varType, nil)
			if err != nil {
				t.Fatalf("a %s value carrying nothing was refused: %v", varType, err)
			}
			if encoded.GetEmptyOrNull() != true {
				t.Errorf("a %s value was encoded as %#v", varType, encoded.Value)
			}
			if _, err := encodeScalar(varType, int32(0)); err == nil {
				t.Errorf("a %s value carrying a number was encoded", varType)
			}
		})
	}
	// The control: a type that does carry a value still requires the right one.
	if _, err := encodeScalar(opcda.VTI4, int32(1)); err != nil {
		t.Errorf("a VT_I4 value carrying an int32 was refused: %v", err)
	}
	if _, err := encodeScalar(opcda.VTI4, nil); err == nil {
		t.Error("a VT_I4 value carrying nothing was encoded")
	}
}
