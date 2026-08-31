package grpcfrontend

import (
	"context"
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	"google.golang.org/protobuf/proto"
)

func FuzzDecodeDAWriteRequest(f *testing.F) {
	seed, err := proto.Marshal(&opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId:   "Exact.I2",
		DataType: &opcdav1.DAVarType{Raw: 2, Name: "VT_I2"},
		Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I2Value{I2Value: 42}},
	}}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0xff, 0x00, 0x80})

	f.Fuzz(func(t *testing.T, data []byte) {
		var request opcdav1.DAWriteRequest
		if proto.Unmarshal(data, &request) != nil || len(request.Items) > 100 {
			return
		}
		for _, item := range request.Items {
			if item == nil || item.DataType == nil || item.Value == nil {
				continue
			}
			varType, err := decodeWriteVarType(item.DataType)
			if err != nil {
				continue
			}
			_, _ = decodeWriteValue(varType, item.Value)
		}
	})
}

// The item property request surface added with the DA-native property calls.
// Every other untrusted request shape in this package is fuzzed, and this one
// arrived without a target; it has already produced two defects, so it is
// exactly the surface worth driving with arbitrary bytes.
//
// The handler is driven rather than a decoder, because what is new here is the
// validation and the result encoding, not the protobuf decode.
func FuzzItemPropertiesRequest(f *testing.F) {
	seed, err := proto.Marshal(&opcdav1.DAItemPropertiesRequest{
		ItemId: "Exact.I2", PropertyIds: []uint32{100, 102, 103},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0xff, 0x00, 0x80})
	empty, err := proto.Marshal(&opcdav1.DAItemPropertiesRequest{})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(empty)

	// A source that answers every identifier, so the encoder is driven with
	// whatever the fuzzer asks for rather than short-circuiting on a refusal.
	runtime := &testRuntime{propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{}}
	server := New(runtime, Config{MaxItemProperties: 16, MaxItemIDBytes: 128})

	f.Fuzz(func(t *testing.T, data []byte) {
		var request opcdav1.DAItemPropertiesRequest
		if proto.Unmarshal(data, &request) != nil {
			return
		}
		response, err := server.ItemProperties(context.Background(), &request)
		if err != nil {
			if response != nil {
				t.Fatal("a refused request returned a response as well as an error")
			}
			return
		}
		// An accepted request answers one result per requested identifier, in
		// order. Nothing the fuzzer supplies may change that.
		if len(response.Results) != len(request.PropertyIds) {
			t.Fatalf("results = %d, requested = %d", len(response.Results), len(request.PropertyIds))
		}
		for index, result := range response.Results {
			if result.PropertyId != request.PropertyIds[index] {
				t.Fatalf("result %d is property %d, requested %d", index, result.PropertyId, request.PropertyIds[index])
			}
			if result.ValuePresent != (result.Value != nil) {
				t.Fatalf("property %d contradicted its own value presence", result.PropertyId)
			}
			if !result.Ok && result.Value != nil {
				t.Fatalf("property %d carried a value behind a failure", result.PropertyId)
			}
		}
		if response.ItemId != request.ItemId {
			t.Fatal("the response named a different ItemID")
		}
	})
}

func FuzzAvailableItemPropertiesRequest(f *testing.F) {
	seed, err := proto.Marshal(&opcdav1.DAAvailableItemPropertiesRequest{ItemId: "Exact.I2"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0x0a, 0xff})

	runtime := &testRuntime{available: []opcda.AvailableProperty{
		{ID: opcda.PropertyEUUnits, Description: "EU Units", VarType: opcda.VTBSTR},
	}}
	server := New(runtime, Config{MaxItemProperties: 16, MaxItemIDBytes: 128})

	f.Fuzz(func(t *testing.T, data []byte) {
		var request opcdav1.DAAvailableItemPropertiesRequest
		if proto.Unmarshal(data, &request) != nil {
			return
		}
		response, err := server.AvailableItemProperties(context.Background(), &request)
		if err != nil {
			if response != nil {
				t.Fatal("a refused request returned a response as well as an error")
			}
			return
		}
		if response.ItemId != request.ItemId {
			t.Fatal("the response named a different ItemID")
		}
		for _, property := range response.Properties {
			// QueryAvailableProperties states a type for every property, so the
			// frontend reports one for every property.
			if property.DataType == nil {
				t.Fatalf("property %d was reported without a type", property.PropertyId)
			}
		}
	})
}
