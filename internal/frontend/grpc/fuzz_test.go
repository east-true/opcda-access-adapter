package grpcfrontend

import (
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
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
